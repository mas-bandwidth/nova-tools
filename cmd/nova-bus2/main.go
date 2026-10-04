// nova-bus2 is the message bus between AIs over Redis streams
// (docs/SPEC-BUS2.md; the delivery machine is tla/Bus2.tla). A message goes
// to every recipient's stream and to the log in one transaction; a recipient
// receives through its consumer group, so a message is pending until it is
// acked and a reader that died before acking is handed it again. The verbs
// are send, recv, ack, peek, log and names; the dispatch, the banner, the
// help, the version verb, the refusals and the output envelope are
// internal/tool's, and the rules are internal/bus2's.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

// RedisEnv names the bus's own address variable; the sprint's and the
// general one are read after it, then the seat's address (SPEC-BUS2.md, the
// config).
const RedisEnv = "NOVA_BUS_REDIS"

// world is what the tool reaches outside itself: the environment, the store
// it opens for an address, the host's name (the default consumer), the
// command --exec runs, and the signals a loop stops on. main passes the real
// one; a test passes its own over internal/bus2's Fake, so no test opens a
// socket.
type world struct {
	getenv   func(string) string
	hostname func() string
	open     func(ctx context.Context, addr string) (bus2.Store, func(), error)
	run      func(ctx context.Context, command, stdin string, stdout, stderr io.Writer) (exit int, err error)
	signals  func(ctx context.Context) (context.Context, context.CancelFunc)
	seat     *seatcred.Selection
}

func realWorld() world {
	w := world{getenv: os.Getenv, seat: new(seatcred.Selection), run: runShell,
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		}}
	w.hostname = func() string {
		h, err := os.Hostname()
		if err != nil {
			return "unknown-host" // the consumer's name is a label; --consumer names a better one
		}
		return h
	}
	w.open = w.openRedis
	return w
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, realWorld())) }

// run is the entry point apart from the process: --seat is taken off the
// line first (as nova-table does), then the tool dispatches.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, w world) int {
	if w.seat != nil {
		rest, err := w.seat.FromArgs(args, w.getenv)
		if err != nil {
			fmt.Fprintf(stderr, "BUS2 REFUSED: %s; run: nova-bus2 help\n", err)
			return 2
		}
		args = rest
	}
	return busTool(w).Run(args, stdin, stdout, stderr)
}

// redisDefault is the --redis default: NOVA_BUS_REDIS, then NOVA_SPRINT_REDIS,
// then NOVA_REDIS_ADDR, then the seat's address, as nova-sprint and
// nova-table read theirs.
func (w world) redisDefault() string {
	for _, k := range []string{RedisEnv, "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR"} {
		if v := w.getenv(k); v != "" {
			return v
		}
	}
	if w.seat != nil {
		return w.seat.Addr()
	}
	return ""
}

// openRedis dials the store through redisconn, the one way a nova tool opens
// Redis, as the seat (--seat, NOVA_SEAT) when one is selected, else as
// NOVA_SPRINT_REDIS_USER with the password in the variable
// NOVA_SPRINT_REDIS_PASSWORD_ENV names (NOVA_REDIS_BENCH_PASSWORD when it
// names none); no user is the default user with no password. The password
// is never on the line and never printed (internal/redisconn).
func (w world) openRedis(ctx context.Context, addr string) (bus2.Store, func(), error) {
	o := redisconn.Options{Addr: addr}
	getenv := w.getenv
	if c, ok, err := w.seat.Active(); ok {
		if err != nil {
			return nil, nil, err
		}
		var password string
		// ignored: Use fails only when its function is nil or fails, and this one does neither
		_ = c.Password.Use(func(pw string) error { password = pw; return nil })
		o.User, o.PasswordEnv = c.User, c.Key
		getenv = func(k string) string {
			if k == c.Key {
				return password
			}
			return w.getenv(k)
		}
	} else {
		o.Env = redisconn.Env{User: redisauth.UserEnv}
		if w.getenv(redisauth.UserEnv) != "" {
			o.Env.PasswordEnv = redisauth.PasswordEnvEnv
			if w.getenv(redisauth.PasswordEnvEnv) == "" {
				o.PasswordEnv = redisauth.DefaultPasswordEnv
			}
		}
	}
	conn, err := redisconn.Open(ctx, o, getenv)
	if err != nil {
		return nil, nil, err
	}
	return bus2.Redis{C: conn.Client()}, func() { conn.Close() }, nil
}

// ExecBudget bounds one run of --exec's command: a delivery into a harness
// is a write of a few lines; one that takes longer is stuck.
const ExecBudget = 60 * time.Second

// runShell runs --exec's command through the shell with the message on its
// stdin, under ExecBudget (internal/subproc: WaitDelay and the bound).
func runShell(ctx context.Context, command, stdin string, stdout, stderr io.Writer) (int, error) {
	cmd, cancel := subproc.CommandFor(ctx, ExecBudget, "/bin/sh", "-c", command)
	defer cancel()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(stdin), stdout, stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

// storeDetail is what every store verb's help says about the store and the
// login (SPEC-BUS2.md, the config).
const storeDetail = `The store is --redis <host:port>, else ` + RedisEnv + `, NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, then the
seat's address; the login is the seat's (--seat, NOVA_SEAT), else NOVA_SPRINT_REDIS_USER with the
password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV names (never a password on the line).`

func busTool(w world) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-bus2",
		What:  "messages between AIs over Redis streams: sent once, delivered until acked",
		Stamp: version,
		How: `the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand after a plain recv; names: nova-config friend and machine rows.
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS, NOVA_REDIS_ADDR, a seat).`,
		ExitTable: "0 done, 1 the verb ran and said no (recv: nothing within --block; recv --exec: the command failed), 2 could not run (a flag, an input, a store that did not answer).",
		Words:     []string{"NONE"},
		Verbs: []tool.Verb{
			{
				Name:    "send",
				Usage:   "send --as <me> --to <a,b> [--cc <c>] --subject <s> (--body <text> | --file <path> | --stdin) [--re <id>] [--redis <addr>]",
				Example: `send --as ada --to bob --subject hello --body "are you there?"`,
				Effect:  tool.Delivery + ": one entry on every recipient's stream and the log, in one transaction",
				Detail: `Prints SEND OK id=<ulid> to=<names> cc=<names> at=<RFC3339>. The id is the message's for
ever: ack takes it, --re names it, log --re finds the answers. A name is lowercase letters,
digits and hyphens (at most 64 bytes) and must be a nova-config friend or machine row.
` + storeDetail,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the sender")
					f.Required("to", "the recipients, comma-separated names")
					f.String("cc", "", "more recipients, comma-separated names; each gets the message as well")
					f.Required("subject", "one line saying what the message is")
					f.String("body", "", "the message's text (or --file, or --stdin)")
					f.String("file", "", "a file holding the message's text")
					f.Bool("stdin", false, "read the message's text from stdin")
					f.String("re", "", "the id of the message this one answers")
					f.Redis(w.redisDefault())
					f.Check(func(c *tool.Call) {
						n := 0
						for _, k := range []string{"body", "file", "stdin"} {
							if c.Given(k) {
								n++
							}
						}
						if n != 1 {
							c.Problem("the body comes from exactly one of --body <text>, --file <path> or --stdin")
						}
					})
				},
				Run: w.send,
			},
			{
				Name:    "peek",
				Usage:   "peek --as <me> [--max <n>] [--redis <addr>]",
				Example: "peek --as bob",
				Effect:  tool.Inspection,
				Detail: `Prints PEEK OK pending=<n> new=<n>, then one PEEK MESSAGE line per message (state, id,
from, subject, at): pending is delivered and not acked, new is never delivered. Moves nothing.
` + storeDetail,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the recipient")
					f.Max()
					f.Redis(w.redisDefault())
				},
				Run: w.peek,
			},
			{
				Name:    "recv",
				Usage:   "recv --as <me> [--block <duration>] [--forever --exec <command>] [--exec <command>] [--consumer <name>] [--redis <addr>]",
				Example: "recv --as bob --block 2s\nrecv --as bob --exec true",
				Effect:  tool.Delivery + ": moves one message to pending; with --exec it runs the command and acks on exit 0",
				Detail: `Prints one message, oldest pending first (one handed out and not acked, by any consumer),
else the oldest new one: a RECV OK header line (id, from, to, cc, subject, re, at, entry) then a
blank line and the body; RECV NONE at exit 1 when --block runs out with none. The message stays
pending until ack. --exec '<command>' runs the command with that same text on its stdin and acks
the message when it exits 0; a non-zero exit leaves the message pending and is RECV FAIL at exit 1.
--forever loops over every message and needs --exec (a loop that acks nothing would hand out the
same message for ever); it stops on SIGINT or SIGTERM, or at the first command that fails.
` + storeDetail,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the recipient")
					f.Duration("block", 0, "how long to wait for a message when none is there (0: answer at once)")
					f.Bool("forever", false, "loop over every message, delivering each with --exec, until a signal")
					f.String("exec", "", "a shell command run with each message on its stdin; exit 0 acks the message")
					f.String("consumer", "", "this reader's name in the group (default: the host's name)")
					f.Redis(w.redisDefault())
					f.Check(func(c *tool.Call) {
						if c.Bool("forever") && c.Str("exec") == "" {
							c.Problem("--forever wants --exec <command>: a loop that acks nothing would hand out the same message for ever")
						}
						if c.Dur("block") < 0 {
							c.Problem("--block must be zero or more")
						}
					})
				},
				Run: w.recv,
			},
			{
				Name:    "ack",
				Usage:   "ack --as <me> --id <id,...> [--redis <addr>]",
				Example: "ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV",
				Effect:  tool.Delivery + ": acks the messages on your stream",
				Detail: `Prints ACK OK id=<id> acked=true|false per id: false when the id is not pending for you
(acked already, never delivered, or not yours), so acking twice is safe and exits 0.
` + storeDetail,
				Flags: func(f *tool.Flags) {
					f.Required("as", "your name, the recipient")
					f.Required("id", "the message ids, comma-separated, as recv printed them")
					f.Redis(w.redisDefault())
				},
				Run: w.ack,
			},
			{
				Name:    "log",
				Usage:   "log [--since <RFC3339>] [--from <name>] [--to <name>] [--re <id>] [--bodies] [--max <n>] [--redis <addr>]",
				Example: "log --from ada --max 5",
				Effect:  tool.Inspection,
				Detail: `Prints LOG OK shown=<n> and one LOG MESSAGE line per message of the log, oldest first
(id, from, to, cc, re, at, subject; the body too with --bodies). --to matches to and cc.
` + storeDetail,
				Flags: func(f *tool.Flags) {
					f.String("since", "", "keep messages at or after this instant, RFC 3339 (2026-10-03T12:00:00Z)")
					f.String("from", "", "keep messages from this name")
					f.String("to", "", "keep messages to or cc this name")
					f.String("re", "", "keep messages answering this id")
					f.Bool("bodies", false, "print each message's body as well")
					f.Max()
					f.Redis(w.redisDefault())
					f.Check(func(c *tool.Call) {
						if s := c.Str("since"); s != "" {
							if _, err := time.Parse(time.RFC3339, s); err != nil {
								c.Problem(fmt.Sprintf("--since %q is no instant; it wants RFC 3339 (2026-10-03T12:00:00Z)", s))
							}
						}
					})
				},
				Run: w.log,
			},
			{
				Name:    "names",
				Usage:   "names [--redis <addr>]",
				Example: "names",
				Effect:  tool.Inspection,
				Detail: `Prints NAMES OK count=<n> and one NAMES NAME name=<name> line per known name:
nova-config's friend and machine rows, as applied into the store.
` + storeDetail,
				Flags: func(f *tool.Flags) { f.Redis(w.redisDefault()) },
				Run:   w.names,
			},
		},
	}
}

// bus opens the store named by --redis for a verb, or says why not: an
// empty address is a usage refusal, a store that did not answer is one too
// (exit 2, the banner's table), in redisconn's one line.
func (w world) bus(c *tool.Call) (*bus2.Bus, func(), *tool.Out) {
	addr := c.Want("redis", "the Redis address, host:port (or "+RedisEnv+", NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat)")
	if o := c.Refused(); o != nil {
		return nil, nil, o
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	defer cancel()
	st, closeStore, err := w.open(ctx, addr)
	if err != nil {
		return nil, nil, tool.Refuse(err.Error())
	}
	return &bus2.Bus{Store: st}, closeStore, nil
}

// answer renders an error of the bus: a Refusal names the input (exit 2),
// anything else is the store (exit 2, with redisconn's words).
func answer(err error) *tool.Out {
	var r *bus2.Refusal
	if errors.As(err, &r) {
		return tool.Refuse(r.Problems...)
	}
	return tool.Refuse("the store did not answer: " + err.Error())
}

func names(csv string) []string {
	var out []string
	for _, w := range strings.Split(csv, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func (w world) send(c *tool.Call) *tool.Out {
	body := c.Str("body")
	switch {
	case c.Given("file"):
		raw, err := os.ReadFile(c.Str("file"))
		if err != nil {
			return tool.Refuse("--file: " + err.Error())
		}
		body = string(raw)
	case c.Bool("stdin"):
		raw, err := io.ReadAll(io.LimitReader(c.Stdin, bus2.MaxBody+1))
		if err != nil {
			return tool.Refuse("--stdin: " + err.Error())
		}
		body = string(raw)
	}
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	m, err := b.Send(context.Background(), bus2.Message{
		From: c.Str("as"), To: names(c.Str("to")), CC: names(c.Str("cc")),
		Subject: c.Str("subject"), Re: c.Str("re"), Body: body,
	})
	if err != nil {
		return answer(err)
	}
	return tool.Done().Fact("id", m.ID).Fact("to", strings.Join(m.To, ",")).Fact("cc", strings.Join(m.CC, ",")).Fact("at", m.At.Format(time.RFC3339))
}

// message is a received message as one Out: the header line's facts and the
// body as the payload, so the text form is the header, a blank line and the
// body, and --json carries the same under facts and payload.
func message(e bus2.Entry) *tool.Out {
	m := e.Message()
	o := tool.Done()
	o.Verb = "recv" // the token of the line, also when text renders it for --exec before the skeleton has
	return o.Fact("id", m.ID).Fact("from", m.From).Fact("to", strings.Join(m.To, ",")).Fact("cc", strings.Join(m.CC, ",")).
		Fact("subject", tool.Text(m.Subject)).Fact("re", m.Re).Fact("at", m.At.Format(time.RFC3339)).Fact("entry", e.Entry)
}

// text is the message as recv prints it and as --exec's command reads it.
func text(o *tool.Out, body string) string {
	var b strings.Builder
	o.Render(&b, false)
	return b.String() + "\n" + body
}

func (w world) recv(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, consumer, block, command := c.Str("as"), cmp.Or(c.Str("consumer"), w.hostname()), c.Dur("block"), c.Str("exec")
	ctx, stop := w.signals(context.Background())
	defer stop()
	one := func(block time.Duration) (*tool.Out, bool) {
		e, ok, err := b.Recv(ctx, as, consumer, block)
		if err != nil {
			if ctx.Err() != nil {
				return tool.Done().Note("stopped by a signal"), false
			}
			return answer(err), false
		}
		if !ok {
			return tool.Fail("nothing for " + as + " within --block " + block.String()).As("NONE"), false
		}
		o := message(e)
		m := e.Message()
		if command == "" {
			o.Payload = "\n" + m.Body
			return o, true
		}
		exit, err := w.run(ctx, command, text(o, m.Body), c.Stderr, c.Stderr)
		if err != nil {
			return tool.Fail("--exec could not run: "+err.Error()).Fact("id", m.ID), false
		}
		if exit != 0 {
			return tool.Fail(fmt.Sprintf("--exec exited %d, so the message stays pending", exit)).Fact("id", m.ID).Fact("exec_exit", exit), false
		}
		acked, err := b.AckEntry(ctx, as, e.Entry)
		if err != nil {
			return answer(err), false
		}
		return o.Fact("acked", acked).Fact("exec_exit", 0), true
	}
	if !c.Bool("forever") {
		o, _ := one(block)
		return o
	}
	// the loop: every message in turn, each a line of its own, until a signal
	// or a command that fails; a NONE is a wait that ran out, not a line
	o := tool.Exit(0)
	delivered := 0
	for ctx.Err() == nil {
		res, ok := one(cmp.Or(block, 30*time.Second))
		if ok {
			delivered++
			res.Payload = ""
			res.Render(c.Stdout, c.Bool("json"))
			continue
		}
		if res.Word == "NONE" {
			continue
		}
		if res.Status != tool.OK {
			res.Render(c.Stderr, c.Bool("json"))
			return tool.Exit(res.Exit)
		}
		break
	}
	fmt.Fprintf(c.Stdout, "RECV OK delivered=%d stopped=signal\n", delivered)
	return o
}

func (w world) ack(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	ids := names(c.Str("id"))
	acked, err := b.Ack(context.Background(), c.Str("as"), ids)
	if err != nil {
		return answer(err)
	}
	o := tool.Done().Fact("acked", count(acked)).Fact("asked", len(ids))
	for _, id := range ids {
		o.Item("id", "id", id, "acked", acked[id])
	}
	return o
}

func count(m map[string]bool) int {
	n := 0
	for _, v := range m {
		if v {
			n++
		}
	}
	return n
}

func (w world) peek(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	pending, fresh, err := b.Peek(context.Background(), c.Str("as"), 0)
	if err != nil {
		return answer(err)
	}
	o := tool.Done().Fact("pending", len(pending)).Fact("new", len(fresh))
	for _, state := range []struct {
		name string
		es   []bus2.Entry
	}{{"pending", pending}, {"new", fresh}} {
		for _, e := range state.es {
			m := e.Message()
			o.Item("message", "state", state.name, "id", m.ID, "from", m.From, "at", m.At.Format(time.RFC3339), "subject", tool.Text(m.Subject))
		}
	}
	return o
}

func (w world) log(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	f := bus2.Filter{From: c.Str("from"), To: c.Str("to"), Re: c.Str("re")}
	if s := c.Str("since"); s != "" {
		f.Since, _ = time.Parse(time.RFC3339, s) // ignored: checked by the verb's flag rule
	}
	got, err := b.Log(context.Background(), f)
	if err != nil {
		return answer(err)
	}
	o := tool.Done().Fact("total", len(got))
	for _, e := range got {
		m := e.Message()
		kv := []any{"id", m.ID, "from", m.From, "to", strings.Join(m.To, ","), "cc", strings.Join(m.CC, ","), "re", m.Re, "at", m.At.Format(time.RFC3339), "subject", tool.Text(m.Subject)}
		if c.Bool("bodies") {
			kv = append(kv, "body", tool.Text(m.Body))
		}
		o.Item("message", kv...)
	}
	return o
}

func (w world) names(c *tool.Call) *tool.Out {
	b, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	got, err := b.Names(context.Background())
	if err != nil {
		return answer(err)
	}
	o := tool.Done().Fact("count", len(got))
	for _, n := range got {
		o.Item("name", "name", n)
	}
	return o
}
