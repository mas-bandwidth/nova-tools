package wake

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PinnedBusVersion is the nova-bus this tool is written against.
//
// The pin is here because the two-poll freshness promise is a property of
// nova-bus's PUSH and not of its read: new mail reaches the checkout through
// the fetch inside `inbox --advance`'s push, and a nova-bus that stopped
// fetching there would leave a watcher that looks perfectly healthy and is
// blind. Moving it is a rule change under CONTRIBUTING, made after the
// measurement in SPEC-WAKE's "How the checkout receives mail" is repeated
// against the new version.
const PinnedBusVersion = "v0.10.3"

// suppressed names the bookkeeping tokens: the lines that say the same thing
// every poll. They are COUNTED and not printed.
//
// THE ALLOW-LIST DECIDES WHAT IS SUPPRESSED, NEVER WHAT IS SHOWN. A watcher
// that kept only the tokens it knew about once dropped an INBOX REFUSED line
// and gave the window thirty minutes of confident quiet over a bus that was
// refusing every read. So a line this tool cannot classify is a line this tool
// PRINTS -- including INBOX FAIL, INBOX UNREADABLE, INBOX UNADDRESSED, INBOX
// SWITCH, a git transcript, a line from a future version of nova-bus this tool
// has never heard of, and anything on stderr at all.
var suppressed = map[string]bool{
	"INBOX OPEN":   true,
	"INBOX OK":     true,
	"INBOX CURSOR": true,
	"INBOX SCOPE":  true,
	"INBOX LEGACY": true,
}

// Bus is the bus-inbox source.
type Bus struct {
	Dir             string
	As              string
	ReceiptMaxWords int
	Every_          time.Duration
	Timeout         time.Duration
	// Refresh runs `nova-bus wait` instead of `inbox`, with no --advance:
	// wait takes the checkout lock, fetches, fast-forwards and returns the
	// moment the inbox would list something new or at its timeout. So new mail
	// reaches a non-advancing watcher within one poll, no cursor moves and
	// nothing is consumed.
	Refresh bool
	Remote  string
	Branch  string

	// Seen answers whether this tool has seen an unrecognised line before. It
	// is the state's sighting memory, handed in rather than reached for, so
	// that the classifier is a pure function of its inputs.
	Seen func(line string) bool

	// Printed answers whether a note id has already been printed.
	Printed func(id string) bool

	read, suppress, relay, standing int
	firstPoll                       bool
	budget                          time.Duration
	budgetSet                       bool
}

func (b *Bus) Name() string         { return "bus" }
func (b *Bus) Every() time.Duration { return b.Every_ }

// Counts are rule 7's four numbers, printed once per run before the verdict.
// They add up: read equals the sum of the other three. A bus that printed
// nothing and a bus that printed twelve bookkeeping lines must not look the
// same.
func (b *Bus) Counts() (read, suppress, relay, standing int) {
	return b.read, b.suppress, b.relay, b.standing
}

// Budget is the time this poll may block for: the time to the earliest due
// source, AT MOST --interval (docs/SPEC-WAKE.md, "The bus inbox", --refresh).
// It is set by the loop before each bus poll, because the earliest due source
// is a fact about the whole watch and not about this source. --gh-timeout is
// the budget for a forge call and was never this one: at the documented
// defaults it would block about nine intervals per poll.
func (b *Bus) Budget(d time.Duration) {
	b.budget = d
	b.budgetSet = true
}

func (b *Bus) waitBudget() time.Duration {
	if b.budgetSet && b.budget > 0 && b.budget < b.Every_ {
		return b.budget
	}
	if b.Every_ > 0 {
		return b.Every_
	}
	return b.Timeout
}

// Poll runs one bus read and classifies every line of it.
func (b *Bus) Poll(ctx context.Context, now time.Time) (Result, error) {
	var res Result
	// The carried list is read once per RUN, on the first poll that could be
	// read: a first poll whose remote was unreachable has not read it, and a
	// run that counted that as the first would owe the window a list it never
	// printed.
	first := !b.firstPoll

	args := b.inboxArgs()
	if b.Refresh {
		args = b.waitArgs(b.waitBudget())
	}
	out, code, err := b.runWithin(ctx, b.processBudget(), args...)
	if err == nil && code == 0 {
		b.firstPoll = true
	}

	// Under --refresh the carried list is read WHOLE once, on the first poll,
	// so a cold watcher lists what it is owed before what is new -- and the
	// carrying= count comes from the poll's OWN INBOX OPEN line rather than
	// from an extra plain inbox, which is neither one of the enumerated
	// program shapes nor a read whose lines anything counted.
	if b.Refresh && first {
		if n := carrying(out); n > 0 {
			open, _, oerr := b.run(ctx, append(b.inboxArgs(), "--open", "--open-max", strconv.Itoa(n))...)
			// Classified BEFORE the poll's own lines: what the window is owed
			// prints before what is new.
			b.classify(open, &res)
			if oerr != nil {
				b.classify(out, &res)
				return res, oerr
			}
		}
	}

	b.classify(out, &res)
	if err != nil {
		return res, err
	}
	if code != 0 {
		return res, fmt.Errorf("nova-bus exit=%d", code)
	}
	return res, nil
}

func (b *Bus) inboxArgs() []string {
	return []string{"inbox", "--bus", b.Dir, "--as", b.As,
		"--receipt-max-words", strconv.Itoa(b.ReceiptMaxWords)}
}

// waitArgs is `wait` WITHOUT --advance: it fetches and moves no cursor. One
// fetch per poll, never two, which is why --refresh and --advance-cursor
// together are refused.
func (b *Bus) waitArgs(t time.Duration) []string {
	return []string{"wait", "--bus", b.Dir, "--as", b.As,
		"--receipt-max-words", strconv.Itoa(b.ReceiptMaxWords),
		"--timeout", Dur(t), "--remote", b.Remote, "--branch", b.Branch}
}

// run starts nova-bus under the timeout and reads its stdout and stderr
// TOGETHER, line by line: rule 7 is about every line the source produced, and a
// tool that read only stdout would be the grep that dropped the REFUSED line.
// processBudget is how long the PROCESS may take, as against how long the wait
// inside it may block. Under --refresh the poll is told to block for the time to
// the earliest due source, and killing the process at --gh-timeout would kill
// the fetch this tool asked for: a legal `--refresh --interval 2m` was SIGKILLed
// at 60s on every poll, and three of those BROKEN a perfectly healthy watch.
// --gh-timeout is the budget for a forge call and is the slack on top here.
func (b *Bus) processBudget() time.Duration {
	budget := b.Timeout + 15*time.Second
	if b.Refresh {
		if wait := b.waitBudget(); wait+b.Timeout > budget {
			budget = wait + b.Timeout
		}
	}
	return budget
}

func (b *Bus) run(ctx context.Context, args ...string) (string, int, error) {
	return b.runWithin(ctx, b.Timeout+15*time.Second, args...)
}

func (b *Bus) runWithin(ctx context.Context, budget time.Duration, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-bus", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), 0, fmt.Errorf("nova-bus timed out")
	}
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			return out.String(), ee.ExitCode(), nil
		}
		return out.String(), 0, fmt.Errorf("nova-bus could not be run: %s", oneLineOf(err.Error()))
	}
	return out.String(), 0, nil
}

// classify is rule 7: every line is suppressed, relayed or standing, every line
// is counted, and nothing is dropped silently.
func (b *Bus) classify(out string, res *Result) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.read++
		toks := strings.Fields(line)
		token := ""
		if len(toks) >= 2 {
			token = toks[0] + " " + toks[1]
		}
		if suppressed[token] {
			b.suppress++
			continue
		}
		if token == "INBOX NOTE" {
			id, value := parseNote(line)
			if id != "" && b.Printed != nil && b.Printed(id) {
				// The window has been shown it. This is the ONE suppression
				// this tool's own state decides, and the mark behind it is
				// written only after the line was printed.
				b.suppress++
				continue
			}
			b.relay++
			res.Items = append(res.Items, Item{Kind: KindBus, Key: "bus:note:" + id, Value: value, ID: id})
			continue
		}
		// The default case PRINTS.
		if b.Seen != nil && b.Seen(line) {
			// Shown every time, woken on once: a bus that has been refusing for
			// an hour must not wake the window every interval with one sentence
			// it has already acted on, and dropping the line instead would be
			// the false-quiet failure.
			b.standing++
			res.Standing = append(res.Standing, "WAKE BUS STANDING "+escapeTail(line))
			continue
		}
		b.relay++
		res.Items = append(res.Items, Item{Kind: KindBusLine, Key: "bus:line:" + line, Value: "seen"})
	}
}

// parseNote reads one INBOX NOTE line into a note's id and the state value
// behind its WAKE BUS line.
//
// commit= is the checkout head this tool is reading, and the honest answer is
// that nova-bus's listing does not carry a per-note commit: inbox prints
// id=, from=, addr=, at= and path=, and asking git for a commit per note would
// be a git call per note, which the "only programs it starts" rule forbids. It
// is filled in by the caller from the head it already read, or left at -.
func parseNote(line string) (id, value string) {
	head, subject, _ := strings.Cut(line, ": ")
	var from, addr, at, path string
	for _, tok := range strings.Fields(head) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch k {
		case "id":
			id = v
		case "from":
			from = v
		case "addr":
			addr = v
		case "at":
			at = v
		case "path":
			path = v
		}
	}
	if id == "" || id == "-" {
		// A note with no id is still a note. Its key is its path, which is the
		// only other thing that names it, and the delivery id falls back to the
		// hash over the key and the value like every other kind.
		id = "-" + path
	}
	return id, Compose(from, addr, at, "", path, subject)
}

// WithCommit fills a bus note's empty commit field with the checkout head the
// poll read, so that what woke the window is named by a sha as well as an id
// (rule 6).
func WithCommit(value, head string) string {
	p := fields(Decompose(value), 6)
	if p[3] == "" {
		p[3] = head
	}
	return Compose(p...)
}

// carrying reads the INBOX OPEN line's carrying=<n>.
func carrying(out string) int {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "INBOX OPEN ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if v, ok := strings.CutPrefix(tok, "carrying="); ok {
				n, err := strconv.Atoi(v)
				if err == nil {
					return n
				}
			}
		}
	}
	return 0
}

// BusVersion runs `nova-bus version` and reads the second token of its first
// line. It is run ONCE, before the opening line.
func BusVersion(ctx context.Context, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "nova-bus", "version")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("nova-bus version: %s", oneLineOf(err.Error()))
	}
	first, _, _ := strings.Cut(out.String(), "\n")
	toks := strings.Fields(first)
	if len(toks) < 2 {
		return "", fmt.Errorf("nova-bus version printed %q, which has no version token in it", oneLineOf(first))
	}
	return toks[1], nil
}

// Head reads the bus checkout's newest commit and its commit stamp, read-only,
// under the timeout. It is the freshness the WAKE SOURCE bus line shows, so a
// reader of the transcript can see a checkout stand still.
func Head(ctx context.Context, dir string, timeout time.Duration) (sha, at string) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "log", "-1", "--format=%H%x1f%cI")
	raw, err := cmd.Output()
	if err != nil {
		return "-", "-"
	}
	f := strings.Split(strings.TrimSpace(string(raw)), "\x1f")
	if len(f) != 2 {
		return "-", "-"
	}
	return f[0], StampOf(f[1])
}

// Classify is rule 7 over a transcript the caller has already read: every line
// suppressed, relayed or standing, every line counted, and the DEFAULT CASE
// PRINTS. It is exported so that `serve` runs this classifier and not a second
// one -- the 2026-09-10 hurt was a reader that kept only the tokens it knew
// about, and one classifier is one place for that to be right.
func (b *Bus) Classify(out string) Result {
	var res Result
	b.classify(out, &res)
	return res
}

// BusNoteIDs is every INBOX NOTE id in a transcript, in the order the bus
// listed them. It is the BUS ORDER a caller needs for notes the classifier
// suppresses -- a note already handed to the receiver is suppressed and still
// has a place in the order the queue is dispatched in.
func BusNoteIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		toks := strings.Fields(line)
		if len(toks) < 2 || toks[0]+" "+toks[1] != "INBOX NOTE" {
			continue
		}
		if id, _ := parseNote(line); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// StampOf is an RFC 3339 stamp from a git-formatted one, put into UTC the way
// every other stamp this tool prints is. git's %cI carries the committer's own
// offset, and a freshness field a reader compares to at= must not read four
// hours stale at a glance (rule 9: one clock, one spelling).
func StampOf(s string) string {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return s
	}
	return Stamp(t)
}
