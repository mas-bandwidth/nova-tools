// nova-bus2 is the message bus between AIs over Redis streams
// (docs/SPEC-BUS2.md; the delivery machine is tla/Bus2.tla). A message goes
// to every recipient's stream and to the log in one transaction; a recipient
// receives through its consumer group, so a message is pending until it is
// acked and a reader that died before acking is handed it again. The verbs
// are send, peek, recv, ack, log and names; the dispatch, the banner, the
// help, the version verb, the refusals and the output envelope are
// internal/tool's, and the rules are internal/bus2's.
package main

import (
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
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

var version string

// RedisEnv names the store when --redis does not (SPEC-BUS2.md, the config).
const RedisEnv = "NOVA_BUS_REDIS"

// ExecBudget bounds one run of --exec's command: a delivery into a harness
// is a write of a few lines; one that takes longer is stuck. It is also how
// long a reader keeps a message before another may claim it (bus2.ClaimAfter).
const ExecBudget = bus2.ClaimAfter

// ForeverBlock is how long one read of the loop waits before it looks again
// (so a signal is seen within it). It is --block's default; the wait's own
// deadline is the block plus bus2.BlockMargin (SPEC-BUS2.md, the deadlines).
const ForeverBlock = 30 * time.Second

// timeoutFlag declares --timeout on a verb: the deadline one store call runs
// under (SPEC-BUS2.md, the deadlines), bus2.CallTimeout by default. A store
// past it is refused as not answering, so a host under load gets more.
func timeoutFlag(f *tool.Flags) {
	f.Duration("timeout", bus2.CallTimeout, "how long one store call may take, a Go duration above zero; a store past it is refused as not answering (the host may be overloaded), so try again or give a loaded host more")
	f.Check(func(c *tool.Call) {
		if c.Dur("timeout") <= 0 {
			c.Problem("--timeout wants a Go duration above zero, like 5s")
		}
	})
}

// world is what the tool reaches outside itself: the environment, the store
// it opens for an address, the command --exec runs, and the signals a loop
// stops on. main passes the real one; a test passes its own over
// internal/bus2's Fake, so no test opens a socket.
type world struct {
	getenv func(string) string
	// open dials the store and says which user it logged in as ("" when the
	// store has no login: the default user), the identity every verb acts as.
	open    func(ctx context.Context, addr string) (st bus2.Store, login string, closeStore func(), err error)
	run     func(ctx context.Context, command, stdin string, stdout, stderr io.Writer) (exit int, err error)
	signals func(ctx context.Context) (context.Context, context.CancelFunc)
}

func realWorld() world {
	w := world{getenv: os.Getenv, run: runShell,
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		}}
	w.open = w.openRedis
	return w
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, realWorld())) }

// run is the entry point apart from the process.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, w world) int {
	return busTool(w).Run(args, stdin, stdout, stderr)
}

// openRedis dials the store through redisconn, the one way a nova tool opens
// Redis, with the fleet's login: NOVA_SPRINT_REDIS_USER names the user and
// NOVA_SPRINT_REDIS_PASSWORD_ENV the variable that holds its password
// (NOVA_REDIS_BENCH_PASSWORD when it names none); no user is the default
// user with no password. The password is never on the line and never
// printed (internal/redisconn).
func (w world) openRedis(ctx context.Context, addr string) (bus2.Store, string, func(), error) {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if w.getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if w.getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	resolved, err := redisconn.Resolve(o, w.getenv)
	if err != nil {
		return nil, "", nil, err
	}
	conn, err := redisconn.Open(ctx, o, w.getenv)
	if err != nil {
		return nil, "", nil, err
	}
	return bus2.Redis{C: conn.Client()}, resolved.User, func() { conn.Close() }, nil
}

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

func busTool(w world) *tool.Tool {
	return &tool.Tool{
		Name:  "nova-bus2",
		What:  "messages between AIs over Redis streams: sent once, delivered until acked",
		Stamp: version,
		How: `the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand after a plain recv; names: nova-config friend and machine rows.
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else ` + RedisEnv + `); user NOVA_SPRINT_REDIS_USER.`,
		ExitTable: "0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed), 2 could not run (a flag, an input, a store that did not answer).",
		Words:     []string{"NONE"},
		Verbs: []tool.Verb{
			{
				Name:    "send",
				Usage:   "send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--redis <addr>] [--timeout <d>]",
				Example: `send --as ada --to bob --subject hello --body "are you there?"`,
				Effect:  tool.Delivery + ": one entry on every recipient's stream and the log, in one transaction",
				Detail: `Prints SEND OK id=<ulid> to=<names> cc=<names> at=<RFC3339>; the id is the message's for ever.
You are the user the connection logged in as (NOVA_SPRINT_REDIS_USER): --as may name it or be left
out, and another name is refused. With no login (a store with no users) --as is your word for who you
are, and the line says login=none.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the sender: the login user when there is one (then it may be left out)")
					f.Required("to", "the recipients, comma-separated names")
					f.String("cc", "", "more recipients, comma-separated names; each gets the message as well")
					f.Required("subject", "one line saying what the message is")
					f.String("body", "", "the message's text (or --stdin; at most 1 MiB)")
					f.Bool("stdin", false, "read the message's text from stdin")
					f.String("re", "", "the id of the message this one answers")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+")")
					timeoutFlag(f)
					f.Check(func(c *tool.Call) {
						if c.Given("body") == c.Given("stdin") {
							c.Problem("the body comes from exactly one of --body <text> or --stdin")
						}
					})
				},
				Run: w.send,
			},
			{
				Name:    "peek",
				Usage:   "peek [--as <me>] [--redis <addr>] [--timeout <d>]",
				Example: "peek --as bob",
				Effect:  tool.Inspection,
				Detail: `Prints PEEK OK pending=<n> new=<n>, then one PEEK MESSAGE state=<pending|new> id=<id> from=<name>
at=<RFC3339> subject=<s> line per message: pending is delivered and not acked, new is never delivered.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+")")
					timeoutFlag(f)
				},
				Run: w.peek,
			},
			{
				Name:    "recv",
				Usage:   "recv [--as <me>] [--forever --exec <command>] [--exec <command>] [--block <d>] [--redis <addr>] [--timeout <d>]",
				Example: "recv --as bob --exec true",
				Effect:  tool.Delivery + ": moves one message to pending; with --exec it runs the command and acks on exit 0",
				Detail: `Prints one message: a line RECV OK id=<id> from=<name> to=<names> cc=<names> re=<id> at=<RFC3339>
subject=<s> (login=none when the connection has no login user), a blank line, the body; or RECV
NONE at exit 1 when nothing waits. You are the login user, as in send. The oldest message a
reader lost (delivered, not acked, idle a minute) comes first, else the oldest new one; the reader
keeps it for a minute. --exec '<command>' runs the command with that same text on its stdin and
acks the message when it exits 0 (the line adds acked=true exec_exit=0); a non-zero exit leaves
it pending and is RECV FAILED at exit 1. --forever loops, waiting for messages, and needs --exec; it
stops on SIGINT or SIGTERM, or at the first command that fails. --block <duration> names how long one
wait of the loop looks before it looks again (30 s by default); the wait's own deadline is it plus
ten seconds, never --timeout (SPEC-BUS2.md, the deadlines).`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.Bool("forever", false, "loop over every message, delivering each with --exec, until a signal")
					f.String("exec", "", "a shell command run with each message on its stdin; exit 0 acks the message")
					f.Duration("block", ForeverBlock, "with --forever, how long one wait of the loop looks before it looks again, a Go duration above zero; the wait's deadline is this plus ten seconds")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+")")
					timeoutFlag(f)
					f.Check(func(c *tool.Call) {
						if c.Bool("forever") && c.Str("exec") == "" {
							c.Problem("--forever wants --exec <command>: a loop that acks nothing would hand out the same message for ever")
						}
						if c.Bool("forever") && c.Dur("block") <= 0 {
							c.Problem("--block wants a Go duration above zero when --forever waits, like 30s")
						}
					})
				},
				Run: w.recv,
			},
			{
				Name:    "ack",
				Usage:   "ack [--as <me>] --id <id,...> [--redis <addr>] [--timeout <d>]",
				Example: "ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV",
				Effect:  tool.Delivery + ": acks the messages on your stream",
				Detail: `Prints ACK OK acked=<n> asked=<n> (login=none when the connection has no login user), then one
ACK ID id=<id> acked=<true|false> line per id: false when the id is not pending for you (acked
already, never delivered, or not yours), so acking twice is safe and exits 0. You are the login
user, as in send.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.Required("id", "the message ids, comma-separated, as recv printed them")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+")")
					timeoutFlag(f)
				},
				Run: w.ack,
			},
			{
				Name:    "log",
				Usage:   "log [--bodies] [--max <n>] [--redis <addr>] [--timeout <d>]",
				Example: "log --max 5",
				Effect:  tool.Inspection,
				Detail: `Prints LOG OK total=<n>, then one LOG MESSAGE id=<id> from=<name> to=<names> cc=<names> re=<id>
at=<RFC3339> subject=<s> line per message of the log, oldest first, with body=<text> too under --bodies.`,
				Flags: func(f *tool.Flags) {
					f.Bool("bodies", false, "print each message's body as well")
					f.Max()
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+")")
					timeoutFlag(f)
				},
				Run: w.log,
			},
			{
				Name:    "names",
				Usage:   "names [--redis <addr>] [--timeout <d>]",
				Example: "names",
				Effect:  tool.Inspection,
				Detail:  "Prints NAMES OK count=<n>, then one NAMES NAME name=<name> line per known name: nova-config's friend and machine rows.",
				Flags: func(f *tool.Flags) {
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+")")
					timeoutFlag(f)
				},
				Run: w.names,
			},
		},
	}
}

// bus opens the store named by --redis for a verb, or says why not: an
// empty address is a usage refusal, a store that did not answer is one too
// (exit 2, the banner's table), in redisconn's one line.
func (w world) bus(c *tool.Call) (*bus2.Bus, string, func(), *tool.Out) {
	addr := c.Want("redis", "the Redis address, host:port (or "+RedisEnv+")")
	if o := c.Refused(); o != nil {
		return nil, "", nil, o
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	defer cancel()
	st, login, closeStore, err := w.open(ctx, addr)
	if err != nil {
		return nil, "", nil, tool.Refuse(err.Error())
	}
	// every call the verb makes on the store runs under its --timeout
	// (SPEC-BUS2.md, the deadlines); a store another hand made (a test's
	// fake) answers under its own.
	if rs, ok := st.(bus2.Redis); ok {
		rs.Timeout = c.Dur("timeout")
		st = rs
	}
	return &bus2.Bus{Store: st}, login, closeStore, nil
}

// identity is who the verb acts as: the user the connection logged in as,
// which --as may repeat and never contradict (the store's login is the
// identity, not a word on the line; SPEC-BUS2.md, the identity); with no
// login user, --as alone, and the result says login=none.
func identity(c *tool.Call, login string) (string, *tool.Out) {
	as := c.Str("as")
	switch {
	case login == "" && strings.TrimSpace(as) == "":
		return "", tool.Refuse("--as is required: this connection has no login user (" + redisauth.UserEnv + " is unset), so it wants your name; refusing to guess")
	case login != "" && as != "" && as != login:
		return "", tool.Refuse(fmt.Sprintf("--as %s is not the login user %s: this connection acts as %s; drop --as, or log in as %s (%s=%s with its password)", as, login, login, as, redisauth.UserEnv, as))
	case login != "":
		return login, nil
	}
	return as, nil
}

// loginFact marks a result made with no login user, so the weakness (any
// name on the line is believed) is visible, never silent.
func loginFact(o *tool.Out, login string) *tool.Out {
	if login == "" {
		o.Fact("login", "none")
	}
	return o
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
	if c.Bool("stdin") {
		raw, err := io.ReadAll(io.LimitReader(c.Stdin, bus2.MaxBody+1))
		if err != nil {
			return tool.Refuse("--stdin: " + err.Error())
		}
		if len(raw) > bus2.MaxBody {
			return tool.Refuse(fmt.Sprintf("the body on stdin is over 1 MiB; at most %d bytes", bus2.MaxBody))
		}
		body = string(raw)
	}
	b, login, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	m, err := b.Send(context.Background(), bus2.Message{
		From: as, To: names(c.Str("to")), CC: names(c.Str("cc")),
		Subject: c.Str("subject"), Re: c.Str("re"), Body: body,
	})
	if err != nil {
		return answer(err)
	}
	return loginFact(tool.Done().Fact("id", m.ID).Fact("to", strings.Join(m.To, ",")).Fact("cc", strings.Join(m.CC, ",")).Fact("at", m.At.Format(time.RFC3339)), login)
}

// message is a received message as one Out: the header line's facts and the
// body as the payload, so the text form is the header, a blank line and the
// body, and --json carries the same under facts and payload.
func message(m bus2.Message, login string) *tool.Out {
	o := tool.Done()
	o.Verb = "recv" // the token of the line, also when text renders it for --exec before the skeleton has
	o.Fact("id", m.ID).Fact("from", m.From).Fact("to", strings.Join(m.To, ",")).Fact("cc", strings.Join(m.CC, ",")).
		Fact("re", m.Re).Fact("at", m.At.Format(time.RFC3339))
	return loginFact(o, login).Fact("subject", tool.Text(m.Subject))
}

// text is the message as recv prints it and as --exec's command reads it:
// the header line, a blank line, the body ending in a newline.
func text(m bus2.Message, login string) string {
	var b strings.Builder
	message(m, login).Render(&b, false)
	body := m.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return b.String() + "\n" + body
}

func (w world) recv(c *tool.Call) *tool.Out {
	b, login, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	command := c.Str("exec")
	ctx, stop := w.signals(context.Background())
	defer stop()
	stopped := tool.Done().Note("stopped by a signal; a message being delivered stays pending")
	// one is one recv: the result, and whether a message was delivered
	one := func(block time.Duration) (*tool.Out, bool) {
		e, ok, err := b.Recv(ctx, as, block)
		if ctx.Err() != nil {
			return stopped, false
		}
		if err != nil {
			return answer(err), false
		}
		if !ok {
			return tool.Fail("nothing for " + as).As("NONE"), false
		}
		m := e.Message()
		o := message(m, login)
		if command == "" {
			o.Payload = "\n" + m.Body
			return o, true
		}
		exit, err := w.run(ctx, command, text(m, login), c.Stderr, c.Stderr)
		if ctx.Err() != nil {
			return stopped, false
		}
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
		o, _ := one(0)
		return o
	}
	// the loop: every message in turn, each a line of its own, until a signal
	// or a command that fails; a NONE is a wait that ran out, not a line
	for {
		res, ok := one(c.Dur("block"))
		switch {
		case ok:
			res.Render(c.Stdout, c.Bool("json"))
		case res.Word == "NONE":
		case res == stopped:
			return tool.Exit(0)
		default:
			res.Render(c.Stderr, c.Bool("json"))
			return tool.Exit(res.Exit)
		}
	}
}

func (w world) ack(c *tool.Call) *tool.Out {
	b, login, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	ids := names(c.Str("id"))
	acked, err := b.Ack(context.Background(), as, ids)
	if err != nil {
		return answer(err)
	}
	n := 0
	for _, v := range acked {
		if v {
			n++
		}
	}
	o := loginFact(tool.Done().Fact("acked", n).Fact("asked", len(ids)), login)
	for _, id := range ids {
		o.Item("id", "id", id, "acked", acked[id])
	}
	return o
}

func (w world) peek(c *tool.Call) *tool.Out {
	b, login, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	pending, fresh, err := b.Peek(context.Background(), as)
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
	b, _, closeStore, refused := w.bus(c)
	if refused != nil {
		return refused
	}
	defer closeStore()
	got, err := b.Log(context.Background())
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
	b, _, closeStore, refused := w.bus(c)
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
