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
	"strings"
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
      line was written just before a crash may be printed again. Runs until killed.
      Run one hook per name; two hooks on the same name split its messages.

Environment: NOVA_BUS_REDIS=host:port (required), NOVA_BUS_PASSWORD (optional).
`

// Group is the consumer group, and consumer, every hook reads its inbox with.
const group = "hook"

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
	// pending, else blocks a while for new ones (none on a quiet timeout).
	Read(ctx context.Context, stream string, pending bool) ([]Msg, error)
	Ack(ctx context.Context, stream, id string) error
}

type writeError struct{ error }

// main has no signal handling: a kill at any point is safe, because a message
// is acked only after its line is written (tla/Bus.tla).
func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, dialRedis))
}

func run(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer, dial func(addr, password string) Store) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	fs := flag.NewFlagSet("nova-bus "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	as := fs.String("as", "", "")
	to := fs.String("to", "", "")
	re := fs.String("re", "", "")
	if args[0] != "send" && args[0] != "hook" {
		return fail(stderr, "unknown verb %q (send, hook, help)", args[0])
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if env("NOVA_BUS_REDIS") == "" {
		return fail(stderr, "NOVA_BUS_REDIS is not set: give the Redis address as host:port")
	}
	if strings.TrimSpace(*as) == "" {
		return fail(stderr, "no name: give --as <name>")
	}
	if *re == "-" {
		return fail(stderr, "--re wants a message id (as hook prints it), not -")
	}
	store := dial(env("NOVA_BUS_REDIS"), env("NOVA_BUS_PASSWORD"))
	if args[0] == "hook" {
		if fs.NArg() != 0 || *to != "" || *re != "" {
			return fail(stderr, "hook takes only --as <name>")
		}
		return runHook(ctx, store, *as, stdout, stderr)
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

func runHook(ctx context.Context, store Store, name string, stdout, stderr io.Writer) int {
	for {
		err := hook(ctx, store, "bus:"+name, stdout)
		var we writeError
		if errors.As(err, &we) {
			return fail(stderr, "writing stdout: %v", err)
		}
		fmt.Fprintf(stderr, "nova-bus: %v; reconnecting\n", err)
		time.Sleep(time.Second)
	}
}

// hook prints and acks the pending entries, then the new ones, until an error.
func hook(ctx context.Context, store Store, stream string, out io.Writer) error {
	if err := store.Group(ctx, stream); err != nil {
		return err
	}
	pending := true
	for {
		msgs, err := store.Read(ctx, stream, pending)
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
