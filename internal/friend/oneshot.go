package friend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// OneShotHarness is a Deliverer whose lanes run each card as one fresh headless run of
// the harness: no session is opened or kept between cards, so every card starts from a
// fresh context, and nothing is ever handed to her main session but bus messages
// (docs/SPEC-FRIEND.md, "Every lane refreshes independently"; tla/FriendLanes.tla). The
// owner, 2026-10-10, on the batch turn: "this is a bad design. each lane should refresh
// independently." dsh, opencode, codex, gemini and grok are one; claude runs its cards
// the same way as a CardRunner.
type OneShotHarness interface {
	Deliverer
	// RunOneShot runs text as one fresh headless run in the lane's job directory (the
	// one its context carries, LaneDirOf) and blocks until it ends: its exit, a refused
	// permission and the first error line read from its output; a rate limit, out of
	// funds or a usage limit as a lane's turn reads them (laneLimit); a provider's
	// refusal as Deliver answers it. The run is a lane's child, so under a lane's
	// context it runs inside the lane's wall (Wall.Exec).
	RunOneShot(ctx context.Context, text string) (LaneTurn, error)
}

// RunsOneShot says harness runs each card of a lane as one fresh headless run
// (a OneShotHarness): no row of hers is delivered in batch.
func RunsOneShot(harness string) bool {
	switch harness {
	case "dsh", "opencode", "codex", "gemini", "grok":
		return true
	}
	return false
}

// OneShotLabel is how a one-shot run names its session in the record and in a
// provider's refusal: it has none of its own until the harness makes one.
const OneShotLabel = "(one-shot)"

// OneShotText is the whole prompt of one lane's run: who the friend is, from her own
// files (a fresh run has no seed turn before it), then the card's turn (CardText) with
// nothing riding along: the pong line, the word about the coordinator and the bus
// messages go to her main session, never into a lane.
func OneShotText(friend string, job LaneJob, n, width int, sendLine, agents, memory string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s: one of %d lanes of %s, this is lane %d, a fresh run that ends with this one card.\n", friend, width, friend, n)
	switch {
	case agents != "" && memory != "":
		fmt.Fprintf(&b, "Read %s and every file under %s/ first: they are who you are.\n", agents, memory)
	case agents != "":
		fmt.Fprintf(&b, "Read %s first: it is who you are.\n", agents)
	}
	b.WriteString("\n")
	b.WriteString(CardText(job, n, width, sendLine, "", "", "", nil))
	return b.String()
}

// oneShotEnd is a one-shot run's output read as a lane turn's is (OpenCode.DeliverTo):
// kept on the record, its tail read for a limit whatever the exit, then for a
// provider's refusal of the run.
func oneShotEnd(rec io.Writer, out string, exit int, err error) (LaneTurn, error) {
	if rec != nil && out != "" {
		fmt.Fprintln(rec, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	lt := LaneTurn{Exit: exit, Rejected: PermissionRejection(out), FirstError: HarnessFirstError(out)}
	if err == nil {
		if limit := laneLimit(OneShotLabel, out); limit != nil {
			return lt, limit
		}
	}
	lt.Exit, err = refused(OneShotLabel, out, exit, err)
	return lt, err
}

// DSHOneShotArgs is a dsh lane's run: `dsh headless -`, no --session-id, so the
// headless profile answers the one task in a fresh session of its own and exits
// (dsh --profile headless --help: "Answer one task and exit"); the text on stdin.
func DSHOneShotArgs() []string { return []string{"headless", "-"} }

// RunOneShot is one card of a dsh lane: `dsh headless -` in the card's job directory,
// the text on stdin. A run the headless runner refuses outright (MISSING_CREDENTIAL)
// is a SessionRefused whose output, which may name a credential, is never kept.
func (d *DSH) RunOneShot(ctx context.Context, text string) (LaneTurn, error) {
	program := d.Program
	if program == "" {
		program = DSHProgram
	}
	out, exit, err := d.Run(ctx, LaneDirOf(ctx, d.Dir), program, DSHOneShotArgs(), text)
	if err == nil {
		if r, ok := DSHRefusal(OneShotLabel, LaneDirOf(ctx, d.Dir), out); ok {
			return LaneTurn{Exit: exit}, r
		}
	}
	return oneShotEnd(d.Out, out, exit, err)
}

// RunOneShot is one card of an OpenCode lane: `opencode run [--standalone] <text>` in
// the card's job directory with no --session, so the run opens a fresh session of its
// own, and no session listing is read (the listing is what the lane wall broke on
// 2026-10-09, nova-tools #5537). The friend's directories are allowed in her project
// config first, as for every turn.
func (o *OpenCode) RunOneShot(ctx context.Context, text string) (LaneTurn, error) {
	lt, _, err := o.runOneShot(ctx, text)
	return lt, err
}

func (o *OpenCode) runOneShot(ctx context.Context, text string) (LaneTurn, string, error) {
	o.allow()
	out, exit, err := o.Run(ctx, LaneDirOf(ctx, o.Dir), o.program(), o.runVerb(text), "")
	lt, err := oneShotEnd(o.Out, out, exit, err)
	return lt, out, err
}

// openCodeSession is a session id as opencode prints it.
var openCodeSession = regexp.MustCompile(`\bses_[0-9A-Za-z]{8,}\b`)

// RunOneShot is OpenCode's one-shot run, then priced from the session it made when its
// output names it (`opencode export <session>`); a run whose output names none is said
// on the record with its cost owed, and the run stands.
func (p *OpenCodePriced) RunOneShot(ctx context.Context, text string) (LaneTurn, error) {
	lt, out, err := p.runOneShot(ctx, text)
	var limited UsageLimited
	p.mu.Lock()
	p.until = time.Time{}
	if errors.As(err, &limited) {
		p.until = limited.Until
	}
	p.mu.Unlock()
	if id := openCodeSession.FindString(out); id != "" {
		p.price(ctx, id)
	} else if p.Out != nil {
		fmt.Fprintf(p.Out, "opencode: session=%s cost=- (the run printed no session id, so its record was not read)\n", OneShotLabel)
	}
	return lt, err
}

// CodexOneShotArgs is a codex lane's run: `codex exec` with no resume, a fresh
// session, the prompt on stdin ("-"). The lane's wall is the sandbox: codex's own
// seatbelt cannot be applied inside the wall's, and a headless run has no one to
// answer an approval, so both are off inside the wall, never outside it (a lane's
// child never runs unwalled, Wall.Exec).
func CodexOneShotArgs() []string {
	return []string{"exec", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "-"}
}

// RunOneShot is one card of a codex lane in the card's job directory.
func (c *Codex) RunOneShot(ctx context.Context, text string) (LaneTurn, error) {
	out, exit, err := c.Run(ctx, LaneDirOf(ctx, c.Dir), c.program(), CodexOneShotArgs(), text)
	return oneShotEnd(c.Out, out, exit, err)
}

// GeminiOneShotArgs is a gemini lane's run: headless (--prompt) with no --resume, a
// fresh session; the lane's job directory trusted for this process, and its tools
// approved inside the lane's wall (a headless run has no one to answer).
func GeminiOneShotArgs(text string) []string {
	return []string{"--skip-trust", "--approval-mode", "yolo", "--prompt=" + text}
}

// RunOneShot is one card of a gemini lane in the card's job directory.
func (g *Gemini) RunOneShot(ctx context.Context, text string) (LaneTurn, error) {
	program := g.Program
	if program == "" {
		program = "gemini"
	}
	out, exit, err := g.Run(ctx, LaneDirOf(ctx, g.Dir), program, GeminiOneShotArgs(text), "")
	return oneShotEnd(g.Out, out, exit, err)
}

// GrokOneShotArgs is a grok lane's run: a single-turn prompt (--single) in dir, a
// fresh session that prints its answer and exits, its tools approved inside the
// lane's wall.
func GrokOneShotArgs(dir, text string) []string {
	return []string{"--cwd", dir, "--always-approve", "--single", text}
}

// RunOneShot is one card of a grok lane in the card's job directory: a process of its
// own, never a line in the open window's wake file.
func (g *Grok) RunOneShot(ctx context.Context, text string) (LaneTurn, error) {
	program := g.Program
	if program == "" {
		program = "grok"
	}
	dir := LaneDirOf(ctx, g.Dir)
	out, exit, err := g.Run(ctx, dir, program, GrokOneShotArgs(dir, text), "")
	return oneShotEnd(g.Out, out, exit, err)
}
