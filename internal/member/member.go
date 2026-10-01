// Package member is a fleet member of a sprint: the loop a fleet machine runs
// against the sprint's fleet table (the machine's queue) and the sprint's
// verbs, with each card run as one child through a runner. The fleet table
// is the dispatcher; the member only replays, for real, the sequence the
// world driver plays in simulation: beat, queue, push and finish what ended, take to
// its width, start each card taken as a child. A reader runs the same loop
// against the readers table: begin what was asked, report what ended.
//
// The member speaks to the sprint only through its command line and JSON
// (Sprint), and runs a card only through a Runner, so the harness underneath
// (a Claude Code child, an OpenCode child, a test double) is replaceable and
// the loop is testable without a store or a process.
//
// A work card ends at a local commit inside the wall, which holds no
// credential; the member, outside the wall, pushes that commit to origin's
// branch the sprint named (Pusher) before it reports the finish, so the merge
// finds the work on origin from any machine.
package member

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// Sprint runs one sprint verb and returns its exit code and stdout.
type Sprint interface {
	Run(args ...string) (code int, out []byte)
}

// Runner starts a card as a child and reports how it ended.
type Runner interface {
	// Start runs the packet as one child and returns a handle for it.
	Start(p Packet) (Child, error)
}

// Child is one running card.
type Child interface {
	// Done says whether the child has ended.
	Done() bool
	// Result is how it ended: ok, the head it finished at (a work card;
	// "" when unknown), and the report (one paragraph, the child's own words).
	Result() Result
}

// Pusher puts a work card's commit on origin, outside the wall: the commit
// the child's result names (Result.Head) pushed to the branch the packet
// names (Packet.Branch), never forced. It is asked once per ended launch.
type Pusher interface {
	Push(p Packet, r Result) Push
}

// Push is how a push ended: exactly one of Sha, None and Refused is set. A push that
// landed may also have opened the card's pull request (PR) or said why not (PRNote).
type Push struct {
	Sha     string // pushed: the full sha origin's branch now holds
	None    string // not pushed: why (the child committed nothing); the finish is failed, no commit
	Refused string // the push failed: git's own line; the finish is a failure
	PR      string // the pull request the member opened for the pushed head, "" when none
	PRNote  string // why a pull request the result asked for was not opened, "" when none was asked or it opened
}

// pushWidth is the most pushes one tick runs at once: each is one git to
// origin, and a tick whose children ended together pushes them together.
const pushWidth = 8

// Result is how a child ended. Ran is whether the child ran to its end (its
// harness answered); OK is its verdict: a work card's, that the work is done,
// a read's, the reader's `verdict: ok` in RESULT.md. A read whose child did
// not run or gave no verdict has no finding to report: the read is left as it
// is for the sprint's lateness rule, never filed as broken against the work.
// Shaped is whether RESULT.md has the contract's shape (docs/SPEC-CARD-CONTRACT.md
// section 3); a work card's finish is ok only with it (Judge).
type Result struct {
	Ran     bool
	OK      bool
	Shaped  bool
	Verdict string // a work card's "ok" or "not-done"; a read's "ok", "broken", or "" when the reader gave none
	Head    string
	Report  string
	Title   string // the pull request the child asked for (gh pr create), "" when none
	Body    string
	// End is how a run that did not finish ended, as the child's harness said it:
	// EndProvider, EndBudget, EndDeadline, or "" (Judge puts it first in a failed
	// finish's reason). Usage is what it spent, one line (its budget and wall),
	// reported with the finish onto the attempt's record.
	End   string
	Usage string
}

// The ends Judge names first in a failed finish.
const (
	EndProvider = cardhdr.EndProvider // the sprint's route stats count it apart
	EndBudget   = "budget"
	EndDeadline = "deadline"
)

// Finish is how a work launch ended, as the member judges it (tla/CardContract.tla).
type Finish string

const (
	FinishOK     Finish = "ok"     // reported: the result's head, pushed
	FinishFailed Finish = "failed" // reported --failed, with the reason
	FinishReaped Finish = "reaped" // never reported: the claim moved, or the card left the queue
)

// Judge is a work card's finish from its result and its push, in one place
// (docs/SPEC-CARD-CONTRACT.md section 4; tla/CardContract.tla, Judge): ok only
// when the result has the shape, its verdict is ok, and the member pushed a
// commit the child made; otherwise failed, with the reason. Every work card
// ends with a commit: a child with nothing to do says `verdict: nothing`, and
// that is a failed finish, nothing to do, for the coordinator to judge.
func Judge(r Result, pu Push) (fin Finish, why string) {
	defer func() {
		if fin == FinishFailed && r.End != "" {
			why = r.End + ": " + why
		}
	}()
	switch {
	case pu.Refused != "":
		return FinishFailed, "push refused: " + pu.Refused
	case !r.Shaped:
		return FinishFailed, "no RESULT.md shape"
	case r.Verdict == "nothing":
		why := strings.TrimSpace(r.Report)
		if len(why) >= len("nothing:") && strings.EqualFold(why[:len("nothing:")], "nothing:") {
			why = strings.TrimSpace(why[len("nothing:"):])
		}
		return FinishFailed, "nothing to do: " + why
	case r.Verdict != "ok":
		return FinishFailed, "verdict " + r.Verdict
	case pu.Sha == "":
		return FinishFailed, "no commit: " + pu.None
	}
	return FinishOK, ""
}

// Packet is what a card hands the member, as `nova-sprint queue --json` and
// `take --json` print it.
type Packet struct {
	Card       string   `json:"card"`
	Kind       string   `json:"kind"` // work or read
	As         string   `json:"as"`
	Primary    string   `json:"primary"`
	Stream     string   `json:"stream"`
	Attempt    int      `json:"attempt"`
	Gen        int      `json:"gen,omitempty"`
	Epoch      uint64   `json:"epoch"`
	Brief      string   `json:"brief,omitempty"`
	Fix        string   `json:"fix,omitempty"`
	Finding    string   `json:"finding,omitempty"` // a rework's: the readers' words that found the attempt before broken
	Why        string   `json:"why,omitempty"`     // a rework's: how the attempt before ended
	Notes      []string `json:"notes"`
	Branch     string   `json:"branch,omitempty"`
	Base       string   `json:"base,omitempty"`
	BaseHead   string   `json:"base_head,omitempty"`
	Worker     string   `json:"worker,omitempty"`
	Head       string   `json:"head,omitempty"`
	WorkBranch string   `json:"work_branch,omitempty"`
	WorkBase   string   `json:"work_base,omitempty"`
	Report     string   `json:"report,omitempty"`
	// A work card's route, as the deal drew it (docs/SPEC-SPRINT.md, the deal's
	// route): the model the child runs on, its budget and deadline (seconds);
	// empty when the store has no route, and the member's own run.
	Route    string `json:"route,omitempty"`
	Model    string `json:"model,omitempty"`
	Tokens   string `json:"tokens,omitempty"`
	Deadline int    `json:"deadline,omitempty"`
}

// queueCard is one card of `nova-sprint queue --as <me> --json`.
type queueCard struct {
	ID     string  `json:"id"`
	Table  string  `json:"table"`
	Row    string  `json:"row"`
	Col    string  `json:"col"`
	Gen    int     `json:"gen"`
	Packet *Packet `json:"packet"`
}

type queueOut struct {
	As    string      `json:"as"`
	Epoch uint64      `json:"epoch"`
	Width int         `json:"width"` // the member's width, from its fleet row (0 for a reader)
	Cards []queueCard `json:"cards"`
}

type takeOut struct {
	Packets []Packet `json:"packets"`
}

// Config is one member's or reader's standing.
type Config struct {
	As string // the member's (reader's) name in the fleet (readers) table
	// Width is an override of the most cards it runs at once: a reader's
	// width, or a twin's. A member with none runs the width its fleet row
	// names, read with its queue every tick (the fleet row is the truth).
	Width  int
	Reader bool // run the readers-table loop instead of the fleet's
}

// launch is one child and the claim it was started for: the card at the
// generation (a read: the attempt) and the epoch of the packet it was handed,
// so its result settles that claim and no other (a card cleared and dealt
// again is a new claim, and an old child's result is reaped, never reported).
type launch struct {
	child   Child
	gen     int
	attempt int
	epoch   uint64
	branch  string
	packet  Packet
	push    *Push // the push at its end, once made (a finish the store did not answer is reported again, never pushed again)
	spent   bool  // a read whose child ended with no verdict: not ours to report, not run again until the sprint moves the card
}

// Member is the loop's state: the children running, by card id.
type Member struct {
	cfg     Config
	sprint  Sprint
	runner  Runner
	pusher  Pusher
	out     io.Writer
	running map[string]launch // by card id (a work card's id, a read card's id)
	epoch   uint64
	width   int // the width this tick runs to: the override, else the fleet row's
}

// New is a member with nothing running. A reader pushes nothing, and its
// pusher may be nil; a work member's pusher pushes every work card's commit.
func New(cfg Config, s Sprint, r Runner, pu Pusher, out io.Writer) *Member {
	return &Member{cfg: cfg, sprint: s, runner: r, pusher: pu, out: out, running: map[string]launch{}, width: cfg.Width}
}

// Running is how many children are running (a spent launch holds no place).
func (m *Member) Running() int {
	n := 0
	for _, l := range m.running {
		if !l.spent {
			n++
		}
	}
	return n
}

// Tick is one pass of the loop: beat, read the queue, report every child
// that ended, take (begin) up to the width, start each card taken. It returns
// the number of cards it acted on (reports plus starts), and the first error
// that stopped a step; a refused verb is not an error here (it is printed and
// the card is left for the next pass), a store that does not answer is.
func (m *Member) Tick(now time.Time) (acted int, err error) {
	load := 0
	if m.width > 0 {
		load = m.Running() * 100 / m.width
	}
	if !m.cfg.Reader {
		if code, out := m.sprint.Run("fleet", "beat", m.cfg.As, "--load", strconv.Itoa(load)); code == 2 {
			return 0, fmt.Errorf("beat: the store did not answer: %s", strings.TrimSpace(string(out)))
		}
	}
	code, out := m.sprint.Run("queue", "--as", m.cfg.As, "--json")
	if code != 0 {
		return 0, fmt.Errorf("queue: exit %d: %s", code, strings.TrimSpace(string(out)))
	}
	var q queueOut
	if err := json.Unmarshal(out, &q); err != nil {
		return 0, fmt.Errorf("queue: not JSON: %w", err)
	}
	m.epoch = q.Epoch
	if m.cfg.Width == 0 && q.Width != m.width {
		// the fleet row changed (fleet up --width, fleet sync): said once, run from now
		fmt.Fprintf(m.out, "width %d -> %d (the fleet row)\n", m.width, q.Width)
		m.width = q.Width
	}
	held := []string{"--epoch", strconv.FormatUint(q.Epoch, 10)}
	// 1. Report every child that ended, one verb per card (each report is its
	// own words).
	wasOurs := map[string]bool{}
	for id := range m.running {
		wasOurs[id] = true
	}
	claimMoved := map[string]bool{}
	ids := make([]string, 0, len(q.Cards))
	byID := map[string]queueCard{}
	for _, c := range q.Cards {
		ids = append(ids, c.ID)
		byID[c.ID] = c
	}
	sort.Strings(ids)
	m.pushEnded(ids, byID)
	for _, id := range ids {
		c := byID[id]
		inFlight := c.Col == "working" || c.Col == "reading"
		if !inFlight {
			continue
		}
		l, ours := m.running[id]
		if !ours {
			continue
		}
		if m.moved(l, c) {
			// the claim moved under the child (a clear, a redeal): its result
			// is nobody's; it is reaped when it ends and the new claim is run
			if !l.child.Done() {
				continue
			}
			fmt.Fprintf(m.out, "%s %s: the claim moved (epoch %d gen %d attempt %d, now epoch %d gen %d attempt %d)\n", FinishReaped, id, l.epoch, l.gen, l.attempt, c.Packet.Epoch, c.Packet.Gen, c.Packet.Attempt)
			delete(m.running, id)
			claimMoved[id] = true
			continue
		}
		if l.spent || !l.child.Done() || (!m.cfg.Reader && l.push == nil) {
			// a work child that ended after this tick's pushes is pushed and
			// reported next tick
			continue
		}
		r := l.child.Result()
		launched := []string{"--epoch", strconv.FormatUint(l.epoch, 10)}
		var args []string
		ok := r.OK // as reported: a work card whose push was refused is reported failed
		if m.cfg.Reader {
			if !r.Ran || (r.Verdict != "ok" && r.Verdict != "broken") {
				// no verdict is no finding: the read stays reading for the
				// sprint's lateness rule to re-ask; the child is let go
				fmt.Fprintf(m.out, "read %s: no verdict (ran=%t verdict=%q); left for the sprint to re-ask\n", id, r.Ran, r.Verdict)
				l.spent = true
				m.running[id] = l
				continue
			}
			word := "--ok"
			if r.Verdict == "broken" {
				word = "--broken"
			}
			args = append([]string{"read", "--as", m.cfg.As, word, id, "--finding", oneLine(r.Report)}, launched...)
		} else {
			pu := *l.push
			fin, why := Judge(r, pu)
			report := oneLine(r.Report)
			switch {
			case pu.Sha != "":
				// the report carries the push first, so the 500-byte cut never takes it
				said := "pushed=" + pu.Sha + " to " + l.branch
				if pu.PR != "" {
					said += " pr=" + pu.PR
				}
				report = cut(said + ": " + report)
				fmt.Fprintf(m.out, "push %s pushed=%s branch=%s\n", id, pu.Sha, l.branch)
				if pu.PRNote != "" {
					fmt.Fprintf(m.out, "NOTE pr %s not opened: %s\n", id, oneLine(pu.PRNote))
				}
			case pu.Refused != "":
				fmt.Fprintf(m.out, "NOTE push %s refused: %s\n", id, pu.Refused)
			default:
				fmt.Fprintf(m.out, "push %s: not pushed: %s\n", id, pu.None)
			}
			if fin != FinishOK {
				report = cut(why + "; " + report)
				fmt.Fprintf(m.out, "NOTE finish %s failed: %s\n", id, why)
			}
			args = []string{"finish", "--as", m.cfg.As, id + "@" + strconv.Itoa(l.gen), "--report", report}
			// the head and the branch are the push's: a finish names only what origin holds
			if pu.Sha != "" {
				args = append(args, "--head", pu.Sha)
				if l.branch != "" {
					args = append(args, "--branch", l.branch)
				}
			}
			if fin != FinishOK {
				args = append(args, "--failed")
			}
			if r.Usage != "" {
				args = append(args, "--usage", r.Usage)
			}
			ok = fin == FinishOK
			args = append(args, launched...)
		}
		code, out := m.sprint.Run(args...)
		fmt.Fprintf(m.out, "%s %s ok=%t exit=%d%s\n", args[0], id, ok, code, routeWords(l.packet))
		if code == 2 {
			return acted, fmt.Errorf("%s %s: the store did not answer: %s", args[0], id, strings.TrimSpace(string(out)))
		}
		delete(m.running, id) // refused (1) too: the card is no longer ours to report
		acted++
	}
	// A child whose card the queue no longer lists (the sprint was cleared, the
	// card was dealt elsewhere) has nothing left to report to: once it has
	// ended it is forgotten, so it does not hold a place of the width for ever.
	for id, l := range m.running {
		if _, listed := byID[id]; !listed && l.child.Done() {
			delete(m.running, id)
			fmt.Fprintf(m.out, "%s %s: no longer in the queue (dropped or returned)\n", FinishReaped, id)
		}
	}
	// 2. Recover in-flight (working/reading) cards that have no child of ours,
	// clamped to width. A card in the queue as working (reading) with no child
	// of ours is a card from before this process started (or one whose moved
	// claim just reaped): it is run again from its packet, at the same
	// generation, so a member restart loses nothing. Clamped to width so
	// recovery never overflows capacity; excess cards remain in the queue for
	// subsequent passes.
	acted += m.recoverWorking(ids, byID, wasOurs, claimMoved)
	// 3. Take (begin) up to the width, in one verb, and start each.
	room := m.width - m.Running()
	if room <= 0 {
		return acted, nil
	}
	ready := 0
	for _, c := range q.Cards {
		if c.Col == "ready" || c.Col == "asked" {
			ready++
		}
	}
	if ready == 0 {
		return acted, nil
	}
	var packets []Packet
	if m.cfg.Reader {
		// the reads begun are named: the first n asked in queue order, so what
		// is started is exactly what was claimed
		var ids []string
		for _, c := range q.Cards {
			if c.Col == "asked" && c.Packet != nil && len(ids) < room {
				ids = append(ids, c.ID)
				packets = append(packets, *c.Packet)
			}
		}
		args := append(append([]string{"read", "--as", m.cfg.As, "--begin"}, ids...), held...)
		code, out := m.sprint.Run(args...)
		if code == 2 {
			return acted, fmt.Errorf("read --begin: the store did not answer: %s", strings.TrimSpace(string(out)))
		}
		if code != 0 {
			fmt.Fprintf(m.out, "read --begin refused: %s\n", strings.TrimSpace(string(out)))
			return acted, nil
		}
	} else {
		args := append([]string{"take", "--as", m.cfg.As, "--limit", strconv.Itoa(room), "--json"}, held...)
		code, out := m.sprint.Run(args...)
		if code == 2 {
			return acted, fmt.Errorf("take: the store did not answer: %s", strings.TrimSpace(string(out)))
		}
		if code != 0 {
			fmt.Fprintf(m.out, "take refused: %s\n", strings.TrimSpace(string(out)))
			return acted, nil
		}
		var t takeOut
		if err := json.Unmarshal(out, &t); err != nil {
			return acted, fmt.Errorf("take: not JSON: %w", err)
		}
		packets = t.Packets
	}
	for _, p := range packets {
		if m.start(p) {
			acted++
		}
	}
	return acted, nil
}

// recoverWorking starts children for in-flight (working/reading) cards that
// have no child of ours, up to member width. Each card it leaves for want of
// room is said, one line, and stays in the queue for a later pass.
func (m *Member) recoverWorking(ids []string, byID map[string]queueCard, wasOurs, claimMoved map[string]bool) int {
	acted := 0
	for _, id := range ids {
		c := byID[id]
		inFlight := c.Col == "working" || c.Col == "reading"
		if !inFlight {
			continue
		}
		if _, ours := m.running[id]; ours {
			continue
		}
		if wasOurs[id] && !claimMoved[id] {
			continue
		}
		if c.Packet == nil {
			continue
		}
		if m.Running() >= m.width {
			fmt.Fprintf(m.out, "recover %s deferred: width %d full\n", id, m.width)
			continue
		}
		if m.start(*c.Packet) {
			acted++
		}
	}
	return acted
}

// start runs a packet as a child, unless one is already running for it or width is full.
func (m *Member) start(p Packet) bool {
	if _, ok := m.running[p.Card]; ok {
		fmt.Fprintf(m.out, "start %s: already running\n", p.Card)
		return false
	}
	if m.Running() >= m.width {
		fmt.Fprintf(m.out, "start %s: width %d full\n", p.Card, m.width)
		return false
	}
	ch, err := m.runner.Start(p)
	if err != nil {
		fmt.Fprintf(m.out, "start %s: %v\n", p.Card, err)
		if p.Kind != "read" {
			m.failLaunch(p, err)
		}
		return false
	}
	m.running[p.Card] = launch{child: ch, gen: p.Gen, attempt: p.Attempt, epoch: p.Epoch, branch: p.Branch, packet: p}
	fmt.Fprintf(m.out, "start %s attempt=%d gen=%d running=%d/%d%s\n", p.Card, p.Attempt, p.Gen, m.Running(), m.width, routeWords(p))
	return true
}

// failLaunch reports a taken work card this member cannot launch (no model, a
// slot it cannot make) as a failed finish with the reason, so the store sees it
// at once and opens the failed-work judgment; a card left working would be
// started again every tick, the refusal only in this log, until judged late.
func (m *Member) failLaunch(p Packet, why error) {
	args := []string{"finish", "--as", m.cfg.As, p.Card + "@" + strconv.Itoa(p.Gen), "--failed",
		"--report", cut("launch refused: " + oneLine(why.Error())), "--epoch", strconv.FormatUint(p.Epoch, 10)}
	code, out := m.sprint.Run(args...)
	fmt.Fprintf(m.out, "finish %s ok=false exit=%d launch refused%s\n", p.Card, code, routeWords(p))
	if code != 0 {
		fmt.Fprintf(m.out, "NOTE finish %s refused: %s\n", p.Card, strings.TrimSpace(string(out)))
	}
}

// moved says the claim moved under a launch: the queue's card is at another
// epoch, generation (a read: attempt) than the one the child was started for.
func (m *Member) moved(l launch, c queueCard) bool {
	return c.Packet != nil && (l.epoch != c.Packet.Epoch || (!m.cfg.Reader && l.gen != c.Packet.Gen) || (m.cfg.Reader && l.attempt != c.Packet.Attempt))
}

// pushEnded pushes, before any finish is reported, the commit of every work
// launch whose child ended and whose claim the queue still holds, pushWidth
// at a time; each launch keeps its push, so the finish (and a finish the
// store did not answer, reported again next tick) reads it and never pushes
// twice. The rule is docs/SPEC-SWARM.md's `member` (the push at a work card's
// finish) and docs/SPEC-SPRINT.md's finish row (the head the merge reads).
func (m *Member) pushEnded(ids []string, byID map[string]queueCard) {
	if m.cfg.Reader {
		return
	}
	var due []string
	for _, id := range ids {
		c := byID[id]
		l, ours := m.running[id]
		if !ours || l.push != nil || c.Col != "working" || m.moved(l, c) || !l.child.Done() {
			continue
		}
		due = append(due, id)
	}
	pushes := make([]Push, len(due))
	var wg sync.WaitGroup
	gate := make(chan struct{}, pushWidth)
	for i, id := range due {
		l := m.running[id]
		r := l.child.Result()
		switch {
		case r.Head == "":
			pushes[i] = Push{None: "the child's result names no commit (no head: line, no push recorded)"}
		case l.branch == "":
			pushes[i] = Push{None: "the packet names no branch to push to"}
		case m.pusher == nil:
			pushes[i] = Push{None: "this member has no pusher"}
		default:
			wg.Add(1)
			go func(i int, p Packet, r Result) {
				defer wg.Done()
				gate <- struct{}{}
				defer func() { <-gate }()
				pushes[i] = m.pusher.Push(p, r)
			}(i, l.packet, r)
		}
	}
	wg.Wait()
	for i, id := range due {
		l := m.running[id]
		pu := pushes[i]
		if pu.Sha == "" && pu.Refused == "" && pu.None == "" {
			pu.Refused = "the pusher said nothing"
		}
		l.push = &pu
		m.running[id] = l
	}
}

// cut is a report cut at 500 bytes, the bound oneLine keeps.
func cut(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// oneLine is a report as one line for a verb's flag: the first non-empty
// line, cut at 500 bytes; "" when there is none.
func oneLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if len(l) > 500 {
			l = l[:500]
		}
		return l
	}
	return ""
}

// CardText is the card file a child is given: the brief VERBATIM first (a
// card's brief is a whole child brief in the card grammar `nova-swarm lint
// --card` checks, whose line 1 is the contract line), then, appended, the
// mechanics the sprint adds: the attempt, the branch and base when a base
// names a repository, the fix of this attempt, the notes, and the exact
// command that reports it. Nothing is put in front of the brief.
func CardText(p Packet, sprintBin string) string {
	var b strings.Builder
	brief := strings.TrimRight(p.Brief, "\n")
	if brief != "" {
		b.WriteString(brief)
		b.WriteString("\n\n")
	}
	b.WriteString("## From the sprint\n\n")
	if p.Kind == "read" {
		fmt.Fprintf(&b, "This is read %s: attempt %d of %s, worked by %s, at head %s on branch %s", p.Card, p.Attempt, p.Primary, p.Worker, p.Head, p.WorkBranch)
		if p.WorkBase != "" {
			fmt.Fprintf(&b, " (base %s)", p.WorkBase)
		}
		b.WriteString(".\n\n")
		if strings.TrimSpace(p.Report) != "" {
			fmt.Fprintf(&b, "The worker's report:\n\n%s\n\n", strings.TrimSpace(p.Report))
		}
	} else {
		fmt.Fprintf(&b, "This is %s: attempt %d of %s (stream %s).", p.Card, p.Attempt, p.Primary, p.Stream)
		if p.Branch != "" {
			fmt.Fprintf(&b, " The checkout is on branch %s; JOB.md, which the prompt names first, says where it is and how this card ends. When you end, the member pushes your commit to origin's branch %s from outside the wall.", p.Branch, p.Branch)
		}
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(p.Fix) != "" {
		fmt.Fprintf(&b, "Fix, this attempt:\n\n%s\n\n", strings.TrimSpace(p.Fix))
	}
	for _, n := range p.Notes {
		if strings.TrimSpace(n) != "" {
			fmt.Fprintf(&b, "Note:\n\n%s\n\n", strings.TrimSpace(n))
		}
	}
	if p.Kind == "read" {
		b.WriteString("Your verdict is RESULT.md's `verdict: ok` or `verdict: broken` (broken means the work is wrong for the card, with the finding as your report; a problem of your own run is not a verdict, leave the line out). ")
	}
	b.WriteString("Your RESULT.md's `report:` line is what the sprint records as your report; the member reports it for you as:\n\n")
	if p.Kind == "read" {
		fmt.Fprintf(&b, "    %s read --as %s (--ok | --broken) %s --epoch %d --finding '<one line>'\n", sprintBin, p.As, p.Card, p.Epoch)
	} else {
		fmt.Fprintf(&b, "    %s finish --as %s %s@%d --epoch %d --branch %s --head <sha> --report '<one line>' [--failed]\n", sprintBin, p.As, p.Card, p.Gen, p.Epoch, p.Branch)
	}
	return b.String()
}

// routeWords is a work card's route as the member's start and finish lines name
// it: " route=<r> model=<m>", "" when the packet names none (the member's
// override, said on its MEMBER line) and for a read.
func routeWords(p Packet) string {
	switch {
	case p.Kind == "read":
		return ""
	case p.Route == "":
		return "" // the member's override, said once on its MEMBER line
	}
	return " route=" + p.Route + " model=" + p.Model
}
