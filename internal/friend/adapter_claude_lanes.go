package friend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CardRunner is a Deliverer whose one-shot lane runs each card as a process
// of its own: no session to open or keep, the card's brief the whole prompt,
// no bus message riding along, and the card's result read from its outbox,
// never from the process's output (docs/SPEC-FRIEND.md, one-shot lanes; the
// owner, 2026-10-04: four Claude accounts as heavy-tier friends). Claude is
// one.
type CardRunner interface {
	Deliverer
	// RunCard runs card c to its end: its exit, and an error naming what the
	// outbox lacks when the run wrote no REPORT.md or RESULT.md there.
	RunCard(ctx context.Context, c Card) (LaneTurn, error)
	// Refusal is why no card can run now, with its remedy; empty when one can.
	Refusal() string
}

// RunsCards says harness runs each card as a process of its own (a
// CardRunner) when its row is one-shot: it has no session to push a turn
// into, so install and run owe it no deliver-command refusal; its session
// check goes in by the folder (FolderCheck) for a live session to answer.
// NewDeliverer answers its batch adapter, the wake file (ClaudeWake);
// NewClaude is the one that runs cards.
func RunsCards(harness string) bool { return harness == "claude" }

// NewClaude is the claude harness's card runner over run, in the friend's
// directory dir, its runs' output going to out when set.
func NewClaude(friend, dir string, run Exec, out io.Writer) *Claude {
	return &Claude{Stub: Stub{Harness: "claude"}, Friend: friend, Dir: dir, Run: run, Out: out}
}

// Claude is the claude harness: no deliver command in batch (Stub, passive:
// the daemon reads nothing for it), and in one-shot mode each card run as
// `env CLAUDE_CONFIG_DIR=<dir> claude -p <brief> --output-format stream-json
// --verbose` and the trim (ClaudeTrim) in Dir with stdin from /dev/null,
// priced and its limit read from its stream-json (adapter_claude.go). The config directory is the
// friend row's config_dir, so each friend is its own account's login and
// settings (its permission mode among them); stream-json prints as the run
// works, so the daemon's silence watch sees a working run.
type Claude struct {
	Stub
	Friend, Dir string
	// ConfigDir is the friend row's config_dir as the daemon last read it;
	// nil or empty refuses every card.
	ConfigDir func() string
	Run       Exec
	Program   string           // "claude" when empty
	Out       io.Writer        // where the run's output goes, when set: the daemon's record
	Now       func() time.Time // time.Now when nil: the clock a limit's reset is read against
	// TokenCap is the friend row's per-card token cap as the daemon last read
	// it (TokenCapOf; 0 none); nil is DefaultTokenCap (tokencap.go).
	TokenCap func() int64

	mu    sync.Mutex
	runs  int      // the runs priced so far
	cost  float64  // every run's cost so far, in US dollars
	usage Usage    // the last usage a run measured
	cards cardRuns // each card's tokens over its finished runs
}

func (c *Claude) program() string {
	if c.Program == "" {
		return "claude"
	}
	return c.Program
}

func (c *Claude) configDir() string {
	if c.ConfigDir == nil {
		return ""
	}
	return c.ConfigDir()
}

// Refusal names the missing config_dir and its remedy: a claude friend in
// one-shot mode runs only as her own account.
func (c *Claude) Refusal() string {
	if c.configDir() != "" {
		return ""
	}
	return fmt.Sprintf("friend %s is a claude friend in one-shot mode with no config_dir, and a lane runs only as her own account (CLAUDE_CONFIG_DIR); run: nova-config friend set %s --config_dir <her account's absolute config directory>, or nova-friend run --config-dir <dir>", c.Friend, c.Friend)
}

func (c *Claude) RunCard(ctx context.Context, card Card) (LaneTurn, error) {
	if why := c.Refusal(); why != "" {
		return LaneTurn{}, errors.New(why)
	}
	brief, err := os.ReadFile(card.Brief)
	if err != nil {
		return LaneTurn{}, fmt.Errorf("the card's brief: %w", err)
	}
	args := append([]string{"CLAUDE_CONFIG_DIR=" + c.configDir(), c.program(), "-p", LanePrompt(card, string(brief)), "--add-dir", c.Dir, "--output-format", "stream-json", "--verbose"}, ClaudeTrim...)
	// the run's stream-json usage counted as it prints, and the run stopped at the card's cap (tokencap.go)
	limit := tokenCap(c.TokenCap)
	run, watch, stop := watchClaude(ctx, limit, c.cards.prior(card.Outbox))
	defer stop()
	out, exit, err := c.Run(run, LaneDirOf(ctx, c.Dir), "env", args, "") // in the card's job directory when its lane names one
	if c.Out != nil && out != "" {
		fmt.Fprintln(c.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	capped, at := watch.done()
	c.cards.set(card.Outbox, at)
	if capped {
		_ = c.account(card.ID, out) // ignored: a limit in a run the cap stopped is the cap's end, not the provider's; the cost is on the record
		e, werr := writeCapReport(c.Friend, card.Outbox, TokenCapped{Cap: limit, At: at, Card: card.ID})
		if c.Out != nil {
			fmt.Fprintf(c.Out, "claude: run=%s %s; its run is stopped, usage %s%s\n", card.ID, e.Error(), at, reportWords(e, werr))
		}
		return LaneTurn{Exit: exit}, e
	}
	if err == nil {
		if limited := c.account(card.ID, out); limited != nil {
			return LaneTurn{Exit: exit}, limited
		}
	}
	if exit, err = refused("claude -p", out, exit, err); err != nil {
		return LaneTurn{Exit: exit}, err
	}
	var missing []string
	for _, f := range []string{"REPORT.md", "RESULT.md"} {
		if !exists(filepath.Join(card.Outbox, f)) {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return LaneTurn{Exit: exit, FirstError: HarnessFirstError(out)}, NoReport{Run: "claude -p", Exit: exit, Outbox: card.Outbox, Lacks: missing}
	}
	return LaneTurn{Exit: exit}, nil
}
