// Package member is a fleet member of a sprint: the loop a fleet machine runs
// against the sprint's fleet table (the machine's queue) and the sprint's
// verbs, with each card run as one child through a runner. The fleet table
// is the dispatcher; the member only replays, for real, the sequence the
// world driver plays in simulation: beat, queue, finish what ended, take to
// its width, start each card taken as a child. A reader runs the same loop
// against the readers table: begin what was asked, report what ended.
//
// The member speaks to the sprint only through its command line and JSON
// (Sprint), and runs a card only through a Runner, so the harness underneath
// (a Claude Code child, an OpenCode child, a test double) is replaceable and
// the loop is testable without a store or a process.
package member

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
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

// Result is how a child ended. Ran is whether the child ran to its end (its
// harness answered); OK is its verdict: a work card's, that the work is done,
// a read's, the reader's `verdict: ok` in RESULT.md. A read whose child did
// not run or gave no verdict has no finding to report: the read is left as it
// is for the sprint's lateness rule, never filed as broken against the work.
type Result struct {
	Ran     bool
	OK      bool
	Verdict string // a read's: "ok", "broken", or "" when the reader gave none
	Head    string
	Report  string
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
	Notes      []string `json:"notes"`
	Branch     string   `json:"branch,omitempty"`
	Base       string   `json:"base,omitempty"`
	Worker     string   `json:"worker,omitempty"`
	Head       string   `json:"head,omitempty"`
	WorkBranch string   `json:"work_branch,omitempty"`
	WorkBase   string   `json:"work_base,omitempty"`
	Report     string   `json:"report,omitempty"`
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
	Cards []queueCard `json:"cards"`
}

type takeOut struct {
	Packets []Packet `json:"packets"`
}

// Config is one member's or reader's standing.
type Config struct {
	As     string // the member's (reader's) name in the fleet (readers) table
	Width  int    // the most cards it runs at once
	Reader bool   // run the readers-table loop instead of the fleet's
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
	spent   bool // a read whose child ended with no verdict: not ours to report, not run again until the sprint moves the card
}

// Member is the loop's state: the children running, by card id.
type Member struct {
	cfg     Config
	sprint  Sprint
	runner  Runner
	out     io.Writer
	running map[string]launch // by card id (a work card's id, a read card's id)
	epoch   uint64
}

// New is a member with nothing running.
func New(cfg Config, s Sprint, r Runner, out io.Writer) *Member {
	return &Member{cfg: cfg, sprint: s, runner: r, out: out, running: map[string]launch{}}
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
	if m.cfg.Width > 0 {
		load = m.Running() * 100 / m.cfg.Width
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
	held := []string{"--epoch", strconv.FormatUint(q.Epoch, 10)}
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
		if c.Packet != nil && (l.epoch != c.Packet.Epoch || (!m.cfg.Reader && l.gen != c.Packet.Gen) || (m.cfg.Reader && l.attempt != c.Packet.Attempt)) {
			// the claim moved under the child (a clear, a redeal): its result
			// is nobody's; it is reaped when it ends and the new claim is run
			if !l.child.Done() {
				continue
			}
			fmt.Fprintf(m.out, "reap %s: the claim moved (epoch %d gen %d attempt %d, now epoch %d gen %d attempt %d)\n", id, l.epoch, l.gen, l.attempt, c.Packet.Epoch, c.Packet.Gen, c.Packet.Attempt)
			delete(m.running, id)
			claimMoved[id] = true
			continue
		}
		if l.spent || !l.child.Done() {
			continue
		}
		r := l.child.Result()
		launched := []string{"--epoch", strconv.FormatUint(l.epoch, 10)}
		var args []string
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
			args = []string{"finish", "--as", m.cfg.As, id + "@" + strconv.Itoa(l.gen), "--report", oneLine(r.Report)}
			if r.Head != "" {
				args = append(args, "--head", r.Head)
			}
			if l.branch != "" {
				args = append(args, "--branch", l.branch)
			}
			if !r.OK {
				args = append(args, "--failed")
			}
			args = append(args, launched...)
		}
		code, out := m.sprint.Run(args...)
		fmt.Fprintf(m.out, "%s %s ok=%t exit=%d\n", args[0], id, r.OK, code)
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
			fmt.Fprintf(m.out, "drop %s: no longer in the queue\n", id)
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
	room := m.cfg.Width - m.Running()
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
// have no child of ours, up to member width.
func (m *Member) recoverWorking(ids []string, byID map[string]queueCard, wasOurs, claimMoved map[string]bool) int {
	acted := 0
	for _, id := range ids {
		if m.Running() >= m.cfg.Width {
			break
		}
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
	if m.Running() >= m.cfg.Width {
		fmt.Fprintf(m.out, "start %s: width %d full\n", p.Card, m.cfg.Width)
		return false
	}
	ch, err := m.runner.Start(p)
	if err != nil {
		fmt.Fprintf(m.out, "start %s: %v\n", p.Card, err)
		return false
	}
	m.running[p.Card] = launch{child: ch, gen: p.Gen, attempt: p.Attempt, epoch: p.Epoch, branch: p.Branch}
	fmt.Fprintf(m.out, "start %s attempt=%d gen=%d running=%d/%d\n", p.Card, p.Attempt, p.Gen, m.Running(), m.cfg.Width)
	return true
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
		if p.Base != "" {
			fmt.Fprintf(&b, " The work is branch %s from %s; commit there and put the head you finished at in RESULT.md's Head as `rev: <sha>`.", p.Branch, p.Base)
		} else {
			b.WriteString(" Work in the directory you start in and nowhere else.")
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
		b.WriteString("Your verdict goes in RESULT.md's Head as one line, `verdict: ok` or `verdict: broken` (broken means the work is wrong for the card, with the finding in your One line; a problem of your own run is not a verdict, leave the line out). ")
	}
	b.WriteString("Your RESULT.md's One line is what the sprint records as your report; the member reports it for you as:\n\n")
	if p.Kind == "read" {
		fmt.Fprintf(&b, "    %s read --as %s (--ok | --broken) %s --epoch %d --finding '<one line>'\n", sprintBin, p.As, p.Card, p.Epoch)
	} else {
		fmt.Fprintf(&b, "    %s finish --as %s %s@%d --epoch %d --branch %s --head <sha> --report '<one line>' [--failed]\n", sprintBin, p.As, p.Card, p.Gen, p.Epoch, p.Branch)
	}
	return b.String()
}
