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

// Poll runs one bus read and classifies every line of it.
func (b *Bus) Poll(ctx context.Context, now time.Time) (Result, error) {
	var res Result
	first := !b.firstPoll
	b.firstPoll = true

	// Under --refresh the carried list is read WHOLE once, on the first poll,
	// so a cold watcher lists what it is owed before what is new.
	if b.Refresh && first {
		if out, _, err := b.run(ctx, b.inboxArgs()...); err == nil {
			if n := carrying(out); n > 0 {
				open, _, err := b.run(ctx, append(b.inboxArgs(), "--open", "--open-max", strconv.Itoa(n))...)
				if err == nil {
					b.classify(open, &res)
				}
			}
		}
	}

	args := b.inboxArgs()
	if b.Refresh {
		args = b.waitArgs(b.Timeout)
	}
	out, code, err := b.run(ctx, args...)
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
func (b *Bus) run(ctx context.Context, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, b.Timeout+15*time.Second)
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
	return f[0], f[1]
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
