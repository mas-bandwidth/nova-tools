// Command nova-bus sends messages to named inboxes in Redis and hooks a
// process in to one inbox: every message is one line on its stdout.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

const usage = `nova-bus: messages between named inboxes, kept in Redis.

  nova-bus send --as <me> --to <name>[,<name>] [--re <id>] <subject> <body | ->
      Append one message to each recipient's inbox; prints "<name> <id>" for each.
      A body of - is read from stdin. A failed send prints SEND FAILED and exits 1;
      it is never retried.
  nova-bus hook --as <name>
      Print every waiting message for <name>, then each new one as it arrives,
      one line each: id<TAB>from<TAB>re<TAB>subject<TAB>body (re is - when none;
      \n, \r, \t and \\ escaped). A message is marked handled only after its line
      is written, so a restarted hook resumes where it stopped; a message whose
      line was written just before a crash may be printed again. While it runs,
      <name> is present (see who). Runs until killed; exits 0 on SIGINT/SIGTERM.
      One hook per name: a second is refused while the first is present.
  nova-bus who
      Print each name whose hook is running and how long it has been up, one
      per line, name<TAB>uptime, sorted by name. A crashed hook drops out
      within 30 seconds.
  nova-bus pending --as <name> [--list]
      Print how many messages wait for <name> that no hook has handled; with
      --list, then id<TAB>subject for each, oldest first. Nothing is consumed.
  nova-bus help | -h | --help
      Print this.

Names may not contain ':'. Exit 0 ok, 1 refused or failed, 2 usage error.
Environment: NOVA_BUS_REDIS=host:port (required), NOVA_BUS_PASSWORD (optional).
`

// verbs maps each verb to the flags it takes.
var verbs = map[string][]string{"send": {"as", "to", "re"}, "hook": {"as"}, "who": {}, "pending": {"as", "list"}}

// Group is the consumer group, and consumer, every hook reads its inbox with.
const group = "hook"

// A hook holds its presence key for presenceTTL and sets it again every
// presenceRefresh, so a live hook never lapses and a crashed one lapses
// within presenceTTL (tla/Presence.tla: TTL, R). A read waits at most
// readBlock, because a blocked read is not interrupted by a signal: it bounds
// how long a clean exit takes. An ack or a release outlives the signal by at
// most cleanupTimeout.
const (
	presenceTTL     = 30 * time.Second
	presenceRefresh = 10 * time.Second
	readBlock       = time.Second
	cleanupTimeout  = 2 * time.Second
)

// Msg is one inbox entry: its stream id and its fields.
type Msg struct {
	ID     string
	Fields map[string]string
}

// Store is the part of Redis nova-bus uses.
type Store interface {
	Add(ctx context.Context, stream string, fields map[string]string) (string, error)
	Group(ctx context.Context, stream string) error
	// Read returns this consumer's delivered-but-unacked entries when
	// pending, else waits up to block for new ones (none on a quiet timeout).
	Read(ctx context.Context, stream string, pending bool, block time.Duration) ([]Msg, error)
	Ack(ctx context.Context, stream, id string) error
	// Count is the number of entries no hook has acked; Waiting returns
	// them, oldest first. Neither consumes anything.
	Count(ctx context.Context, stream string) (int64, error)
	Waiting(ctx context.Context, stream string) ([]Msg, error)
	// Claim sets name's presence to value for ttl unless another value is
	// present (false); Release deletes it only if it still holds value.
	Claim(ctx context.Context, name, value string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, name, value string) error
	// Presence returns every present name and its value ("" if unreadable).
	Presence(ctx context.Context) (map[string]string, error)
}

// Clock is time as nova-bus sees it; the tests pass a fake.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type writeError struct{ error }

var errHooked = errors.New("name already hooked")

// main stops a hook cleanly on SIGINT or SIGTERM, releasing its presence, and
// ignores SIGPIPE so a closed stdout is a write error (EPIPE) and the same
// clean exit. A kill at any other point is safe too: a message is acked only
// after its line is written (tla/Bus.tla), and presence lapses by its TTL
// (tla/Presence.tla).
func main() {
	signal.Ignore(syscall.SIGPIPE)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, dialRedis, realClock{})
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer, dial func(addr, password string) Store, clk Clock) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	verb := args[0]
	takes, ok := verbs[verb]
	if !ok {
		return misuse(stderr, "unknown verb %q (send, hook, who, pending, help)", verb)
	}
	fs := flag.NewFlagSet("nova-bus "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	as := fs.String("as", "", "")
	to := fs.String("to", "", "")
	re := fs.String("re", "", "")
	list := fs.Bool("list", false, "")
	if err := fs.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, usage)
		return 0
	} else if err != nil {
		return 2
	}
	var bad []string
	fs.Visit(func(f *flag.Flag) {
		if !slices.Contains(takes, f.Name) {
			bad = append(bad, "--"+f.Name)
		}
	})
	names := strings.Split(*to, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	switch {
	case len(bad) > 0:
		return misuse(stderr, "%s does not take %s", verb, strings.Join(bad, ", "))
	case verb != "who" && strings.TrimSpace(*as) == "":
		return misuse(stderr, "no name: give --as <name>")
	case strings.Contains(*as+*to, ":"):
		return misuse(stderr, "a name may not contain ':'")
	case *re == "-":
		return misuse(stderr, "--re wants a message id (as hook prints it), not -")
	case verb != "send" && fs.NArg() != 0:
		return misuse(stderr, "%s takes no arguments", verb)
	case verb == "send" && (fs.NArg() != 2 || *to == ""):
		return misuse(stderr, "usage: nova-bus send --as <me> --to <name>[,<name>] [--re <id>] <subject> <body | ->")
	case verb == "send" && slices.Contains(names, ""):
		return misuse(stderr, "empty name in --to %q", *to)
	case env("NOVA_BUS_REDIS") == "":
		return fail(stderr, "NOVA_BUS_REDIS is not set: give the Redis address as host:port")
	}
	store := dial(env("NOVA_BUS_REDIS"), env("NOVA_BUS_PASSWORD"))
	switch verb {
	case "hook":
		return runHook(ctx, store, *as, clk, stdout, stderr)
	case "who":
		return who(ctx, store, clk.Now(), stdout, stderr)
	case "pending":
		return pending(ctx, store, *as, *list, stdout, stderr)
	}
	body := fs.Arg(1)
	if body == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return fail(stderr, "reading body from stdin: %v", err)
		}
		body = string(b)
	}
	msg := map[string]string{"from": *as, "to": *to, "subject": fs.Arg(0), "body": body, "re": *re}
	for _, name := range names {
		id, err := store.Add(ctx, "bus:"+name, msg)
		if err != nil {
			return fail(stderr, "SEND FAILED to %s: %v", name, err)
		}
		fmt.Fprintf(stdout, "%s %s\n", name, id)
	}
	return 0
}

// runHook runs hook, reconnecting after an error, until ctx ends (exit 0) or
// stdout fails (exit 1), releasing presence either way; or until the name is
// found hooked by another process (exit 1, its presence left alone)
// (tla/Presence.tla Exit, Claim: OnlyOwnerDeletes, GoneAfterExit).
func runHook(ctx context.Context, store Store, name string, clk Clock, stdout, stderr io.Writer) int {
	value := clk.Now().UTC().Format(time.RFC3339) + "\t" + rand.Text()
	for {
		err := hook(ctx, store, name, value, clk, stdout)
		var we writeError
		if errors.Is(err, errHooked) {
			return fail(stderr, "name already hooked: %s", name)
		}
		if ctx.Err() == nil && !errors.As(err, &we) {
			fmt.Fprintf(stderr, "nova-bus: %v; reconnecting\n", err)
			select {
			case <-ctx.Done():
			case <-clk.After(time.Second):
			}
			continue
		}
		rel, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		if rerr := store.Release(rel, name, value); rerr != nil {
			fmt.Fprintf(stderr, "nova-bus: releasing presence: %v\n", rerr)
		}
		cancel()
		if ctx.Err() != nil {
			return 0
		}
		return fail(stderr, "writing stdout: %v", err)
	}
}

// hook claims name's presence, then prints and acks the pending entries, then
// the new ones, until an error. Each call starts from the pending list, so a
// batch handed by a read whose reply was lost is printed after the reconnect.
// It claims again whenever presenceRefresh has passed, and a read waits no
// longer than that is due (tla/Presence.tla Start, Refresh: StartOnlyWhenFree,
// LiveIsPresent). An ack is not cut short by a signal: a printed line is acked.
func hook(ctx context.Context, store Store, name, value string, clk Clock, out io.Writer) error {
	stream := "bus:" + name
	var due time.Time
	refresh := func() error {
		t := clk.Now()
		if t.Before(due) {
			return nil
		}
		due = t.Add(presenceRefresh)
		ok, err := store.Claim(ctx, name, value, presenceTTL)
		if err == nil && !ok {
			err = errHooked
		}
		return err
	}
	if err := refresh(); err != nil {
		return err
	}
	if err := store.Group(ctx, stream); err != nil {
		return err
	}
	pending := true
	for {
		if err := refresh(); err != nil {
			return err
		}
		msgs, err := store.Read(ctx, stream, pending, min(max(due.Sub(clk.Now()), time.Millisecond), readBlock))
		if err != nil {
			return err
		}
		if pending && len(msgs) == 0 {
			pending = false
		}
		for _, m := range msgs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := io.WriteString(out, line(m)); err != nil {
				return writeError{err}
			}
			ack, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			err := store.Ack(ack, stream, m.ID)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

// who prints name<TAB>uptime for every present name, sorted by name; ? when
// the value is not a start time.
func who(ctx context.Context, store Store, now time.Time, stdout, stderr io.Writer) int {
	present, err := store.Presence(ctx)
	if err != nil {
		return fail(stderr, "who: %v", err)
	}
	names := make([]string, 0, len(present))
	for n := range present {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		up := "?"
		start, _, _ := strings.Cut(present[n], "\t")
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			up = max(now.Sub(t), 0).Truncate(time.Second).String()
		}
		fmt.Fprintf(stdout, "%s\t%s\n", n, up)
	}
	return 0
}

// pending prints the count of entries no hook has acked, then with list each
// id<TAB>subject.
func pending(ctx context.Context, store Store, name string, list bool, stdout, stderr io.Writer) int {
	n, err := store.Count(ctx, "bus:"+name)
	if err != nil {
		return fail(stderr, "pending: %v", err)
	}
	fmt.Fprintln(stdout, n)
	if !list {
		return 0
	}
	msgs, err := store.Waiting(ctx, "bus:"+name)
	if err != nil {
		return fail(stderr, "pending: %v", err)
	}
	for _, m := range msgs {
		fmt.Fprintf(stdout, "%s\t%s\n", m.ID, escaper.Replace(m.Fields["subject"]))
	}
	return 0
}

var escaper = strings.NewReplacer("\\", `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

func line(m Msg) string {
	f := m.Fields
	re := f["re"]
	if re == "" {
		re = "-"
	}
	parts := []string{m.ID, f["from"], re, f["subject"], f["body"]}
	for i := range parts {
		parts[i] = escaper.Replace(parts[i])
	}
	return strings.Join(parts, "\t") + "\n"
}

func fail(stderr io.Writer, format string, a ...any) int {
	fmt.Fprintf(stderr, "nova-bus: "+format+"\n", a...)
	return 1
}

func misuse(stderr io.Writer, format string, a ...any) int {
	fail(stderr, format, a...)
	return 2
}
