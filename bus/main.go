// Command nova-bus sends messages to named inboxes in Redis and hooks a
// process in to one inbox: every message is one line on its stdout.
package main

import (
	"context"
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
      A body of - is read from stdin.
  nova-bus hook --as <name>
      Print every waiting message for <name>, then each new one as it arrives,
      one line each: id<TAB>from<TAB>re<TAB>subject<TAB>body (re is - when none;
      \n, \r, \t and \\ escaped). A message is marked handled only after its line
      is written, so a restarted hook resumes where it stopped; a message whose
      line was written just before a crash may be printed again. While it runs,
      <name> is present (see who). Runs until killed or interrupted.
      Run one hook per name; two hooks on the same name split its messages.
  nova-bus who
      Print each name whose hook is running and how long it has been up, one
      per line, name<TAB>uptime, sorted by name. A crashed hook drops out
      within 30 seconds.
  nova-bus pending --as <name> [--list]
      Print how many messages wait for <name> that no hook has handled; with
      --list, then id<TAB>subject for each, oldest first. Nothing is consumed.
  nova-bus help
      Print this.

Names may not contain ':'.
Environment: NOVA_BUS_REDIS=host:port (required), NOVA_BUS_PASSWORD (optional).
`

// verbs maps each verb to the flags it takes.
var verbs = map[string][]string{"send": {"as", "to", "re"}, "hook": {"as"}, "who": {}, "pending": {"as", "list"}}

// Group is the consumer group, and consumer, every hook reads its inbox with.
const group = "hook"

// A hook holds its presence key for presenceTTL and sets it again every
// presenceRefresh, so a live hook never lapses and a crashed one lapses
// within presenceTTL (tla/Presence.tla: TTL, R).
const (
	presenceTTL     = 30 * time.Second
	presenceRefresh = 10 * time.Second
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
	// Waiting returns the entries no hook has acked, oldest first, and
	// consumes nothing.
	Waiting(ctx context.Context, stream string) ([]Msg, error)
	SetPresence(ctx context.Context, name, value string, ttl time.Duration) error
	DelPresence(ctx context.Context, name string) error
	// Presence returns every present name and its value.
	Presence(ctx context.Context) (map[string]string, error)
}

type writeError struct{ error }

// main stops a hook cleanly on SIGINT or SIGTERM, deleting its presence. A kill
// at any other point is safe too: a message is acked only after its line is
// written (tla/Bus.tla), and presence lapses by its TTL (tla/Presence.tla).
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, dialRedis, time.Now)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer, dial func(addr, password string) Store, now func() time.Time) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	verb := args[0]
	takes, ok := verbs[verb]
	if !ok {
		return fail(stderr, "unknown verb %q (send, hook, who, pending, help)", verb)
	}
	fs := flag.NewFlagSet("nova-bus "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	as := fs.String("as", "", "")
	to := fs.String("to", "", "")
	re := fs.String("re", "", "")
	list := fs.Bool("list", false, "")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var bad []string
	fs.Visit(func(f *flag.Flag) {
		if !slices.Contains(takes, f.Name) {
			bad = append(bad, "--"+f.Name)
		}
	})
	if len(bad) > 0 {
		return fail(stderr, "%s does not take %s", verb, strings.Join(bad, ", "))
	}
	if env("NOVA_BUS_REDIS") == "" {
		return fail(stderr, "NOVA_BUS_REDIS is not set: give the Redis address as host:port")
	}
	if verb != "who" && strings.TrimSpace(*as) == "" {
		return fail(stderr, "no name: give --as <name>")
	}
	if strings.Contains(*as+*to, ":") {
		return fail(stderr, "a name may not contain ':'")
	}
	if *re == "-" {
		return fail(stderr, "--re wants a message id (as hook prints it), not -")
	}
	if verb != "send" && fs.NArg() != 0 {
		return fail(stderr, "%s takes no arguments", verb)
	}
	store := dial(env("NOVA_BUS_REDIS"), env("NOVA_BUS_PASSWORD"))
	switch verb {
	case "hook":
		return runHook(ctx, store, *as, now, stdout, stderr)
	case "who":
		return who(ctx, store, now(), stdout, stderr)
	case "pending":
		return pending(ctx, store, *as, *list, stdout, stderr)
	}
	if fs.NArg() != 2 || *to == "" {
		return fail(stderr, "usage: nova-bus send --as <me> --to <name>[,<name>] [--re <id>] <subject> <body | ->")
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
	names := strings.Split(*to, ",")
	for i := range names {
		if names[i] = strings.TrimSpace(names[i]); names[i] == "" {
			return fail(stderr, "empty name in --to %q", *to)
		}
	}
	for _, name := range names {
		id, err := store.Add(ctx, "bus:"+name, msg)
		if err != nil {
			return fail(stderr, "send to %s: %v", name, err)
		}
		fmt.Fprintf(stdout, "%s %s\n", name, id)
	}
	return 0
}

// runHook runs hook, reconnecting after an error, until ctx ends or stdout
// fails; either is a clean exit and deletes the presence key.
func runHook(ctx context.Context, store Store, name string, now func() time.Time, stdout, stderr io.Writer) int {
	start := now()
	for {
		err := hook(ctx, store, name, start, now, stdout)
		var we writeError
		if ctx.Err() == nil && !errors.As(err, &we) {
			fmt.Fprintf(stderr, "nova-bus: %v; reconnecting\n", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		del, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		derr := store.DelPresence(del, name)
		cancel()
		if derr != nil {
			fmt.Fprintf(stderr, "nova-bus: deleting presence: %v\n", derr)
		}
		if ctx.Err() != nil {
			return 0
		}
		return fail(stderr, "writing stdout: %v", err)
	}
}

// hook prints and acks the pending entries, then the new ones, until an
// error. It sets name's presence first and again whenever presenceRefresh has
// passed; a read waits no longer than the next refresh is due (tla/Presence.tla
// Start, Refresh).
func hook(ctx context.Context, store Store, name string, start time.Time, now func() time.Time, out io.Writer) error {
	stream := "bus:" + name
	var due time.Time
	refresh := func() error {
		t := now()
		if t.Before(due) {
			return nil
		}
		due = t.Add(presenceRefresh)
		return store.SetPresence(ctx, name, start.UTC().Format(time.RFC3339), presenceTTL)
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
		msgs, err := store.Read(ctx, stream, pending, max(due.Sub(now()), time.Millisecond))
		if err != nil {
			return err
		}
		if pending && len(msgs) == 0 {
			pending = false
		}
		for _, m := range msgs {
			if _, err := io.WriteString(out, line(m)); err != nil {
				return writeError{err}
			}
			if err := store.Ack(ctx, stream, m.ID); err != nil {
				return err
			}
		}
	}
}

// who prints name<TAB>uptime for every present name, sorted by name.
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
		if t, err := time.Parse(time.RFC3339, present[n]); err == nil {
			up = max(now.Sub(t), 0).Truncate(time.Second).String()
		}
		fmt.Fprintf(stdout, "%s\t%s\n", n, up)
	}
	return 0
}

// pending prints the count of entries no hook has acked, then with list each
// id<TAB>subject.
func pending(ctx context.Context, store Store, name string, list bool, stdout, stderr io.Writer) int {
	msgs, err := store.Waiting(ctx, "bus:"+name)
	if err != nil {
		return fail(stderr, "pending: %v", err)
	}
	fmt.Fprintln(stdout, len(msgs))
	for i := 0; list && i < len(msgs); i++ {
		fmt.Fprintf(stdout, "%s\t%s\n", msgs[i].ID, escaper.Replace(msgs[i].Fields["subject"]))
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
