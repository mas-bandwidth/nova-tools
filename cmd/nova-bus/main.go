// nova-bus is the message bus between AIs over Redis streams
// (docs/SPEC-BUS.md; the delivery machine is tla/Bus2.tla). A message goes
// to every recipient's stream and to the log in one transaction; a recipient
// receives through its consumer group, so a message is pending until it is
// acked and a reader that died before acking is handed it again. The verbs
// are wait, send, peek, recv, ack, receipts, overdue, log and names; the dispatch, the banner,
// the help, the version verb, the refusals and the output envelope are
// internal/tool's, and the rules are internal/bus's.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/redis/go-redis/v9"
)

var version string

// RedisEnv names the store when --redis does not; with neither, the store is
// the fleet row's bus field as nova-config apply wrote it (FleetBusKey) into
// the sprint store at SprintRedisEnv, so no friend types the address
// (SPEC-BUS.md, the config). The key is spelled here, as the roster's are in
// internal/bus, so this command depends on no config code.
const (
	RedisEnv       = "NOVA_BUS_REDIS"
	SprintRedisEnv = "NOVA_SPRINT_REDIS"
	FleetBusKey    = "fleet:bus"
)

// RequirePushEnv set to 1 makes the push gate refuse, as --require-push does. Without
// it the proof is advice, never a gate (issue #5450: a claude friend and a machine
// with no daemon could never be written to or read as): send and recv print
// NOTE push=<state> for <name> for each name not heard, and go on.
const RequirePushEnv = "NOVA_BUS_REQUIRE_PUSH"

// ExecBudget bounds one run of --exec's command: a delivery into a harness
// is a write of a few lines; one that takes longer is stuck. It is also how
// long a reader keeps a message before another may claim it (bus.ClaimAfter).
const ExecBudget = bus.ClaimAfter

// ForeverBlock is how long one read of the loop waits before it looks again
// (so a signal is seen within it).
const ForeverBlock = 30 * time.Second

// world is what the tool reaches outside itself: the environment, the store
// it opens for an address, the command --exec runs, the signals a loop
// stops on, the clock a wait reads and the wake file it watches. main passes
// the real one; a test passes its own over internal/bus's Fake, so no test
// opens a socket or waits real time.
type world struct {
	getenv func(string) string
	// open dials the store and says which user it logged in as ("" when the
	// store has no login: the default user), the identity every verb acts as.
	open    func(ctx context.Context, addr string) (st bus.Store, login string, closeStore func(), err error)
	run     func(ctx context.Context, command, stdin string, stdout, stderr io.Writer) (exit int, err error)
	signals func(ctx context.Context) (context.Context, context.CancelFunc)
	lookup  bus.Lookup // a store named by a host name is judged by every address it resolves to
	// fleetBus reads the applied fleet row's bus address from the sprint store
	// at addr ("" when the row has none).
	fleetBus func(ctx context.Context, addr string) (string, error)
	// now is the clock a wait reads: the real one, or a test's.
	now func() time.Time
	// wakeArm binds a caller-owned cursor; wakeLine validates it and reads one record.
	wakeArm  func(path, token string) (wakeCursor, error)
	wakeLine func(path string, cursor wakeCursor) (string, wakeCursor, error)
}

func realWorld() world {
	w := world{getenv: os.Getenv, run: runShell, now: time.Now,
		wakeArm: realWakeArm, wakeLine: realWakeLine,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		}}
	w.open = w.openRedis
	w.fleetBus = w.readFleetBus
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
// sprintOptions is the fleet's login for a store at addr (the convention above).
func (w world) sprintOptions(addr string) redisconn.Options {
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: redisauth.UserEnv}}
	if w.getenv(redisauth.UserEnv) != "" {
		o.Env.PasswordEnv = redisauth.PasswordEnvEnv
		if w.getenv(redisauth.PasswordEnvEnv) == "" {
			o.PasswordEnv = redisauth.DefaultPasswordEnv
		}
	}
	return o
}

// readFleetBus is one GET of FleetBusKey on the sprint store.
func (w world) readFleetBus(ctx context.Context, addr string) (string, error) {
	conn, err := redisconn.Open(ctx, w.sprintOptions(addr), w.getenv)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	v, err := conn.Client().Get(ctx, FleetBusKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

func (w world) openRedis(ctx context.Context, addr string) (bus.Store, string, func(), error) {
	o := w.sprintOptions(addr)
	resolved, err := redisconn.Resolve(o, w.getenv)
	if err != nil {
		return nil, "", nil, err
	}
	conn, err := redisconn.Open(ctx, o, w.getenv)
	if err != nil {
		return nil, "", nil, err
	}
	return bus.Redis{C: conn.Client()}, resolved.User, func() { conn.Close() }, nil
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
		Name:  "nova-bus",
		What:  "messages between AIs over Redis streams: sent once, delivered until acked",
		Stamp: version,
		How: `the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks; an unproven push is a NOTE, not a refusal; --require-push refuses.
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis at --redis (else NOVA_BUS_REDIS, else fleet:bus); loopback/tailnet only.`,
		ExitTable: "0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).",
		Words:     []string{"NONE", "WAKE", "ARMED", "MESSAGE", "OVERDUE"},
		Verbs: []tool.Verb{
			{
				Name:    "wait",
				Usage:   "wait [--as <me>] [--after <id>] [--timeout <duration>] [--skip-subject <prefix,...>] [--wake-file <path>] [--wake-after <cursor>] [--redis <addr>]",
				Example: "wait --as bob --timeout 1s",
				Effect:  tool.Inspection,
				ExitTable: "0 the wait ended: WAIT OK, entries that counted, or WAIT WAKE, a line on the wake file; 1 WAIT NONE, the timeout ran out; " +
					"2 could not run (a flag, an input, a store that did not answer).",
				Detail: `Prints WAIT ARMED after=<id> first: the cursor the wait starts past, --after <id> when given (a stream
entry id, <ms>-<seq>), else the stream's last id read once at start, 0-0 when the stream is empty.
Re-arm the next run with the id WAIT OK or WAIT NONE printed, and nothing between two runs is missed.
The wait takes nothing: it reads your stream past the cursor with XREAD, never the consumer group,
so a later recv still delivers and acks what it saw. It ends on the first entries past the cursor
that are not from you and whose subject starts with none of --skip-subject's prefixes (matched
without case; default PING,PONG): one WAIT MESSAGE id=<id> from=<name> subject=<s> bytes=<n> line
each, at most 5, then WAIT OK after=<last id seen> at exit 0. Skipped entries move the cursor and
are not printed. --wake-file <path> also ends the wait when a line is appended to the file after the
start (a harness's deliver adapter appends one per message). Save the returned wake-after cursor
and re-arm with --wake-after <cursor> as well as --after <id>; wake-offset is the ending byte offset.
The cursor binds file identity and consumed prefix; replacement, truncation or rewriting refuses
with a reconciliation remedy. For initial migration, --wake-after 0 explicitly replays from byte zero. Retain the returned payload
and deduplicate by its durable identity before saving the returned cursor. No replay is an LLM receipt.
Without --wake-after the file starts at its current end. A missing
new file binds when it first appears; pipes and other nonregular files are refused: WAIT WAKE file=<path> line=<first line>
at exit 0. Past --timeout <duration> (a Go duration; 0, the default, is for ever) it is WAIT NONE
after=<cursor> waited=<duration> on standard error at exit 1. --json prints one object when the
wait ends: {"status":"ok","word":"OK|NONE|WAKE","after":<id>,"messages":[{"id":<id>,"from":<name>,
"subject":<s>,"bytes":<n>}],"wake":{"file":<path>,"line":<text>},"wake_after":<cursor>,"wake_offset":<bytes>} (messages is empty and wake left
out when they hold nothing; the ARMED line is the text form's). Exit 2 when a flag is wrong, the
name is not on the roster, or the store does not answer.
example: nova-bus wait --as bob --timeout 1s`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.String("after", "", "the stream entry id <ms>-<seq> to wait past; default: the stream's last id read once at start, as WAIT ARMED prints it")
					f.Duration("timeout", 0, "how long to wait before WAIT NONE, a Go duration (1s, 2m); 0 is for ever")
					f.String("skip-subject", "PING,PONG", "subjects starting with one of these prefixes, comma-separated, are skipped; matched without case")
					f.String("wake-after", "", "0 replays the file from byte zero; otherwise the complete wake-after cursor returned by wait; requires --wake-file; preserves unread bytes across rearm and restart")
					f.String("wake-file", "", "a file whose lines, appended after the start, also end the wait (one line per message)")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					f.Check(func(c *tool.Call) {
						if v := c.Str("after"); v != "" && !streamID(v) {
							c.Problem(fmt.Sprintf("--after wants a stream entry id, <ms>-<seq> as WAIT ARMED and WAIT OK print it; %q is not one", v))
						}
						if v := c.Str("wake-after"); v != "" {
							if c.Str("wake-file") == "" {
								c.Problem("--wake-after wants --wake-file, the same append-only regular file")
							}
							if _, err := parseWakeCursor(v); err != nil && v != "0" {
								c.Problem(err.Error())
							}
						}
						if c.Dur("timeout") < 0 {
							c.Problem("--timeout wants a duration of at least 0, 0 for ever (a negative wait is no wait)")
						}
					})
				},
				Run: w.wait,
			},
			{
				Name:    "send",
				Usage:   "send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--token <t>] [--timeout <duration>] [--redis <addr>] [--dry-run]",
				Example: `send --as ada --to bob --subject hello --body "are you there?"`,
				Effect:  tool.Delivery + ": one entry on every recipient's stream and the log, in one transaction",
				DryRun:  true,
				Detail: `Prints SEND OK id=<ulid> to=<names> cc=<names> [kind=<k>] at=<RFC3339> bytes=<n> sha256=<hex>: the id is the
message's for ever, and the byte count and digest are the body's as the store holds it, so a sender
can check a --stdin or shell-built body arrived whole (a shell's $(cat f) drops the trailing newline).
You are the user the connection logged in as (NOVA_SPRINT_REDIS_USER): --as may name it or be left
out, and another name is refused. With no login (a store with no users) --as is your word for who you
are, and the line says login=none. The message lands whoever is listening: for the sender and each
recipient whose friend daemon has not proved its inbox push (a SESSION CHECK carried in by its
harness's deliver adapter and answered by the session, or a per-card harness's daemon up) under ten
minutes ago, one SEND NOTE push=<none|down|stale> for <name> line follows, saying the message waits
on its stream and what would prove the push; nothing is refused on it (nova-bus names shows each
name's push). With --require-push (or ` + RequirePushEnv + `=1) any such name is refused instead,
deaf: <name> has no proven push since <age> and the remedy, and nothing is written. --dry-run checks
the message as send does (every problem named) and prints the line with no id and the same NOTE
lines (or the same refusal), writing nothing.
--token <t> makes the send safe to retry: the same token and the same arguments within --token-life
(default 24h) print the first send's SEND OK line again (its id and at) and write nothing, so a send
whose answer was lost after the store took it is retried without a second message; the same token with
other arguments, or past its life, is refused naming the message that went. The store drops a token at
--token-cleanup (default 168h, never before its life ends). Without --token every send is a new message.
Delivery to a reader is still at least once: a reader may be handed one message twice, by its id.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the sender: the login user when there is one (then it may be left out)")
					f.Required("to", "the recipients, comma-separated names")
					f.String("cc", "", "more recipients, comma-separated names; each gets the message as well")
					f.Required("subject", "one line saying what the message is")
					f.String("body", "", "the message's text (or --stdin; at most 1 MiB)")
					f.Bool("stdin", false, "read the message's text from stdin")
					f.String("re", "", "the id of the message this one answers")
					f.String("kind", bus.KindStatus, "the kind of message, one of "+strings.Join(bus.Kinds, ", ")+": what a reader filters on")
					f.String("token", "", "your word for this one send, the same on every retry of it (letters, digits, . _ : -; at most 128 bytes)")
					f.Duration("token-life", bus.DefaultTokenLife, "how long a retry under --token answers the first send")
					f.Duration("token-cleanup", bus.DefaultTokenCleanup, "when the store drops the token (never before its life ends)")
					f.Bool("require-push", false, "refuse a name with no proven push (deaf: ...) instead of noting it; also "+RequirePushEnv+"=1")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(func(c *tool.Call) {
						refuseNonPositiveTimeout(c)
						if c.Given("body") == c.Given("stdin") {
							c.Problem("the body comes from exactly one of --body <text> or --stdin")
						}
						if c.Dur("token-life") <= 0 || c.Dur("token-cleanup") <= 0 {
							c.Problem("--token-life and --token-cleanup want a duration above zero")
						}
					})
				},
				Run: w.send,
			},
			{
				Name:    "peek",
				Usage:   "peek [--as <me>] [--kind <k>[,<k>]] [--timeout <duration>] [--redis <addr>]",
				Example: "peek --as bob",
				Effect:  tool.Inspection,
				Detail: `Prints PEEK OK pending=<n> new=<n>, then one PEEK MESSAGE state=<pending|new> id=<id> from=<name>
[kind=<k>] at=<RFC3339> subject=<s> line per message: pending is delivered and not acked, new is never delivered.
--kind <k>[,<k>] lists only messages of those kinds; kind=<k> is left off a status's line.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.String("kind", "", "only these kinds, comma-separated, of "+strings.Join(bus.Kinds, ", ")+" (default: every kind)")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(refuseNonPositiveTimeout)
				},
				Run: w.peek,
			},
			{
				Name:    "recv",
				Usage:   "recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--timeout <duration>] [--redis <addr>] [--dry-run]",
				Example: "recv --as bob --exec true",
				Effect:  tool.Delivery + ": moves one message to pending; with --exec it runs the command and acks on exit 0",
				DryRun:  true,
				Detail: `Prints one message: a line RECV OK id=<id> from=<name> to=<names> cc=<names> re=<id> [kind=<k>] at=<RFC3339>
subject=<s> (login=none when the connection has no login user), a blank line, the body; or RECV
NONE at exit 1 when nothing waits. You are the login user, as in send; a name with no proven push
reads all the same, with one RECV NOTE push=<none|down|stale> for <name> line on its first result
(--dry-run, --max, --all and --forever alike); with --require-push (or ` + RequirePushEnv + `=1) a
recv for a name with no proven push is refused instead (deaf: <name> ...). The oldest message a
reader lost (delivered, not acked, idle fifteen minutes) comes first, else the oldest new one; the
reader keeps it for fifteen minutes. --exec '<command>' runs the command with that same text on its stdin and
acks the message when it exits 0 (the line adds acked=true exec_exit=0); a non-zero exit leaves
it pending and is RECV FAILED at exit 1. --max <n> takes up to n messages in order and --all every
one waiting, each printed as its own RECV OK (or handed to --exec and acked on exit 0, stopping
at the first command that fails); --ack acks each after a plain recv prints it. --forever loops,
waiting for messages, and needs --exec; it stops on SIGINT or SIGTERM, or at the first command
that fails. --dry-run moves nothing: it prints RECV OK pending=<n> new=<n> next_new=<id>, what
waits (a pending message held past fifteen minutes comes before the oldest new one).
--kind <k>[,<k>] takes only messages of those kinds, in --all, --max, --forever and --dry-run too: a
message of another kind is skipped, neither acked nor held, and the next recv without the filter
gets it. kind=<k> is left off the line of a status (the default), so an absent kind is a status, as for a message sent
before kinds existed.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.String("kind", "", "only these kinds, comma-separated, of "+strings.Join(bus.Kinds, ", ")+" (default: every kind); others are left for the next reader")
					f.Int("max", 1, "how many messages to take, in order, each its own result; 1 is one message")
					f.Bool("all", false, "take every message waiting, in order, each its own result")
					f.Bool("ack", false, "ack each message after printing it (a plain recv leaves it pending)")
					f.Bool("forever", false, "loop over every message, delivering each with --exec, until a signal")
					f.String("exec", "", "a shell command run with each message on its stdin; exit 0 acks the message")
					f.Bool("require-push", false, "refuse a name with no proven push (deaf: ...) instead of noting it; also "+RequirePushEnv+"=1")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(func(c *tool.Call) {
						refuseNonPositiveTimeout(c)
						if c.Bool("forever") && c.Str("exec") == "" {
							c.Problem("--forever wants --exec <command>: a loop that acks nothing would hand out the same message for ever")
						}
						if c.Bool("all") && c.Given("max") {
							c.Problem("--all takes every message and --max <n> a count; give one or the other")
						}
						if c.Bool("forever") && (c.Bool("all") || c.Given("max")) {
							c.Problem("--forever takes every message as it arrives; --all and --max are for what waits now")
						}
						if c.Int("max") < 1 {
							c.Problem("--max wants a count of at least 1 (--all takes every message)")
						}
						if c.Bool("ack") && c.Str("exec") != "" {
							c.Problem("--exec acks on the command's exit 0; --ack is for a plain recv")
						}
					})
				},
				Run: w.recv,
			},
			{
				Name:    "ack",
				Usage:   "ack [--as <me>] --id <id,...> [--timeout <duration>] [--redis <addr>] [--dry-run]",
				Example: "ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV",
				Effect:  tool.Delivery + ": acks the messages on your stream",
				DryRun:  true,
				Detail: `Prints ACK OK acked=<n> asked=<n> (login=none when the connection has no login user), then one
ACK ID id=<id> acked=<true|false> line per id: false when the id is not pending for you (acked
already, never delivered, or not yours), so acking twice is safe and exits 0. You are the login
user, as in send. --dry-run acks nothing: acked= says which ids are pending for you.`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.Required("id", "the message ids, comma-separated, as recv printed them")
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(refuseNonPositiveTimeout)
				},
				Run: w.ack,
			},
			{
				Name:   "receipts",
				Usage:  "receipts [--as <me>] [--id <id,...>] [--max <n>] [--timeout <duration>] [--redis <addr>]",
				Effect: tool.Inspection,
				Detail: `Prints RECEIPTS OK count=<n>, then one RECEIPTS RECEIPT id=<id> state=<delivered|read|acted|none>
age=<duration> line per message: how far each message to you has come, and how long it has stood
there by the store's clock. A receipt moves only forward: delivered when a reader took the message
off your stream (recv), read when the friend daemon handed the turn carrying it to your session
and the session took it, acted when that turn ended at exit 0 or you sent a message naming it
(--re). --id <id,...> names the messages (none: no receipt, age=-); without it, every receipt you
hold, oldest first. You are the login user, as in send.
example: nova-bus receipts --as bob`,
				Flags: func(f *tool.Flags) {
					f.String("as", "", "your name, the recipient: the login user when there is one (then it may be left out)")
					f.String("id", "", "the message ids, comma-separated, as recv printed them (default: every receipt you hold)")
					f.Max()
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(refuseNonPositiveTimeout)
				},
				Run: w.receipts,
			},
			{
				Name:      "overdue",
				Usage:     "overdue [--older <duration>] [--max <n>] [--timeout <duration>] [--redis <addr>]",
				Effect:    tool.Inspection,
				ExitTable: "0 OVERDUE OK, nothing short of delivered past --older; 1 BUS OVERDUE, one or more; 2 could not run (a flag, a store that did not answer).",
				Detail: `The alarm the coordinator's loop and the seat check run. Looks at every known name's stream and
lists each message still short of delivered (new, never taken by a reader, or pending with no
delivered receipt) sent longer ago than --older (a Go duration, default 10m), by the store's clock.
Prints OVERDUE OK count=0 older=<d> at exit 0 when there is none; else OVERDUE OVERDUE count=<n>
older=<d>, then one OVERDUE MESSAGE name=<recipient> id=<id> state=<new|pending> from=<name>
age=<duration> subject=<s> line each, oldest first, at exit 1. A message acked is never listed.
example: nova-bus overdue --older 10m`,
				Flags: func(f *tool.Flags) {
					f.Duration("older", 10*time.Minute, "how long a message may wait short of delivered before it is overdue, a Go duration (10m, 1h)")
					f.Max()
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(func(c *tool.Call) {
						refuseNonPositiveTimeout(c)
						if c.Dur("older") < 0 {
							c.Problem("--older wants a duration of at least 0 (0 lists every message short of delivered)")
						}
					})
				},
				Run: w.overdue,
			},
			{
				Name:    "log",
				Usage:   "log [--bodies] [--from <name>] [--to <name>] [--max <n>] [--timeout <duration>] [--redis <addr>]",
				Example: "log --max 5",
				Effect:  tool.Inspection,
				Detail: `Prints LOG OK total=<n>, then one LOG MESSAGE id=<id> from=<name> to=<names> cc=<names> re=<id>
[kind=<k>] at=<RFC3339> subject=<s> line per message of the log, oldest first, with body=<text> too under --bodies.
--from <name> filters to messages sent by that sender; --to <name> filters to messages sent to that recipient.
Both can be combined. Filtering happens before --max.`,
				Flags: func(f *tool.Flags) {
					f.Bool("bodies", false, "print each message's body as well")
					f.String("from", "", "only messages from this sender")
					f.String("to", "", "only messages to this recipient")
					f.Max()
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(refuseNonPositiveTimeout)
				},
				Run: w.log,
			},
			{
				Name:    "names",
				Usage:   "names [--timeout <duration>] [--redis <addr>]",
				Example: "names",
				Effect:  tool.Inspection,
				Detail: `Prints NAMES OK count=<n> proven=<n>, then one NAMES NAME name=<name> push=<state> age=<age>
harness=<h> line per known name (nova-config's friend and machine rows). push is proven (its friend
daemon proved and renewed its inbox push under ten minutes ago), stale (the daemon stopped
renewing), down (its session did not answer the daemon's SESSION CHECK) or none (no daemon ever
recorded one); age is how long ago the daemon wrote it, never when there is none. The push is the
seat's liveness rule and a sender's advice: send and recv say it as a NOTE, never refuse on it.`,
				Flags: func(f *tool.Flags) {
					f.String("redis", w.getenv(RedisEnv), "the Redis address, host:port (default: "+RedisEnv+", else NOVA_SPRINT_REDIS, else fleet:bus)")
					callTimeoutFlag(f)
					f.Check(refuseNonPositiveTimeout)
				},
				Run: w.names,
			},
		},
	}
}

// callTimeoutFlag is --timeout on a verb whose calls do not park: how long one
// Redis call may take (SPEC-BUS.md, the deadlines). wait's own --timeout is
// how long the wait parks, and is not this flag.
func callTimeoutFlag(f *tool.Flags) {
	f.Duration("timeout", bus.CallTimeout, "how long one call to Redis may take before it is refused as unanswered; a blocking read gets this, its block, and "+bus.BlockMargin.String()+" more")
}

func refuseNonPositiveTimeout(c *tool.Call) {
	if c.Dur("timeout") <= 0 {
		c.Problem("--timeout must be above zero: a call with no deadline can wait for ever on a store that does not answer")
	}
}

// bus opens the store named by --redis for a verb, or says why not: an
// empty address is a usage refusal, an address off loopback and the tailnet
// is one (bus.CheckAddr, before any dial), and a store that did not answer
// is one too (exit 2, the banner's table), in redisconn's one line. timeout
// is the deadline of one call (SPEC-BUS.md, the deadlines); a blocking read
// gets its block and the margin more on top of it.
// requirePush says send's or recv's push gate refuses: --require-push, or RequirePushEnv=1.
func (w world) requirePush(c *tool.Call) bool {
	return c.Bool("require-push") || w.getenv(RequirePushEnv) == "1"
}

func (w world) bus(c *tool.Call, timeout time.Duration) (*bus.Bus, string, func(), *tool.Out) {
	ctx, cancel := context.WithTimeout(context.Background(), redisconn.OpenTimeout)
	defer cancel()
	addr, refused := w.address(ctx, c)
	if refused != nil {
		return nil, "", nil, refused
	}
	if why := bus.CheckAddr(ctx, addr, w.lookup); why != "" {
		return nil, "", nil, tool.Refuse(why)
	}
	st, login, closeStore, err := w.open(ctx, addr)
	if err != nil {
		return nil, "", nil, tool.Refuse(err.Error())
	}
	if timeout <= 0 {
		closeStore()
		return nil, "", nil, tool.Refuse("--timeout must be above zero: a call with no deadline can wait for ever on a store that does not answer")
	}
	if r, ok := st.(bus.Redis); ok {
		r.Timeout = timeout
		st = r
	}
	return &bus.Bus{Store: st}, login, closeStore, nil
}

// identity is who the verb acts as: the user the connection logged in as,
// which --as may repeat and never contradict (the store's login is the
// identity, not a word on the line; SPEC-BUS.md, the identity); with no
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
	var r *bus.Refusal
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
		raw, err := io.ReadAll(io.LimitReader(c.Stdin, bus.MaxBody+1))
		if err != nil {
			return tool.Refuse("--stdin: " + err.Error())
		}
		if len(raw) > bus.MaxBody {
			return tool.Refuse(fmt.Sprintf("the body on stdin is over 1 MiB; at most %d bytes", bus.MaxBody))
		}
		body = string(raw)
	}
	gated := w.requirePush(c)
	b, login, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	draft := bus.Message{
		From: as, To: names(c.Str("to")), CC: names(c.Str("cc")),
		Subject: c.Str("subject"), Re: c.Str("re"), Kind: c.Str("kind"), Body: body, Token: c.Str("token"),
	}
	b.TokenLife, b.TokenCleanup = c.Dur("token-life"), c.Dur("token-cleanup")
	send := b.Send
	if gated {
		// --require-push: the gate, after the message's own problems are named and
		// before anything is written
		send = func(ctx context.Context, m bus.Message) (bus.Message, error) {
			m, err := b.Check(ctx, m)
			if err != nil {
				return m, err
			}
			if err := b.Heard(ctx, slices.Concat([]string{m.From}, m.To, m.CC)...); err != nil {
				return m, err
			}
			return b.Send(ctx, m)
		}
	}
	if c.DryRun() {
		// the message as it would be sent, with no id: nothing is written, and the
		// push gate the write would meet is asked for by name
		send = func(ctx context.Context, m bus.Message) (bus.Message, error) {
			m, err := b.Check(ctx, m)
			if err != nil || !gated {
				return m, err
			}
			return m, b.Heard(ctx, slices.Concat([]string{m.From}, m.To, m.CC)...)
		}
	}
	m, err := send(context.Background(), draft)
	if err != nil {
		return answer(err)
	}
	// the push proof is advice, never a gate: the message landed (or would), and a
	// name nothing proven is pushing into is one NOTE each (SPEC-BUS.md,
	// bus-requires-inbox-push-proof); a gated send proved every name already
	var unheard []string
	if !gated {
		names := slices.Concat([]string{m.From}, m.To, m.CC)
		if m.At.IsZero() {
			// --dry-run: no message was stamped, so judge at the store's now
			unheard, err = b.Unheard(context.Background(), names...)
		} else {
			unheard, err = b.UnheardAt(context.Background(), m.At, names...)
		}
		if err != nil {
			return answer(err)
		}
	}
	sum := sha256.Sum256([]byte(m.Body))
	o := tool.Done().Fact("id", m.ID).Fact("to", strings.Join(m.To, ",")).Fact("cc", strings.Join(m.CC, ","))
	o = loginFact(kindFact(o, m).Fact("at", m.At.Format(time.RFC3339)).
		Fact("bytes", len(m.Body)).Fact("sha256", hex.EncodeToString(sum[:])), login)
	for _, line := range unheard {
		o.Note(line)
	}
	return o
}

// kindFact adds kind=<k> to a result when the message is not a status: a
// message without the word is a status, so the common line is unchanged.
func kindFact(o *tool.Out, m bus.Message) *tool.Out {
	if k := m.KindName(); k != bus.KindStatus {
		o.Fact("kind", k)
	}
	return o
}

// kindItem is the key and value kind=<k> for an item's fields when the
// message is not a status, as kindFact leaves it out for a status.
func kindItem(m bus.Message) []any {
	if k := m.KindName(); k != bus.KindStatus {
		return []any{"kind", k}
	}
	return nil
}

// message is a received message as one Out: the header line's facts and the
// body as the payload, so the text form is the header, a blank line and the
// body, and --json carries the same under facts and payload.
func message(m bus.Message, login string) *tool.Out {
	o := tool.Done()
	o.Verb = "recv" // the token of the line, also when text renders it for --exec before the skeleton has
	o.Fact("id", m.ID).Fact("from", m.From).Fact("to", strings.Join(m.To, ",")).Fact("cc", strings.Join(m.CC, ",")).
		Fact("re", m.Re)
	kindFact(o, m).Fact("at", m.At.Format(time.RFC3339))
	return loginFact(o, login).Fact("subject", tool.Text(m.Subject))
}

// text is the message as recv prints it and as --exec's command reads it:
// the header line, a blank line, the body ending in a newline.
func text(m bus.Message, login string) string {
	var b strings.Builder
	message(m, login).Render(&b, false)
	body := m.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return b.String() + "\n" + body
}

func (w world) recv(c *tool.Call) *tool.Out {
	gated := w.requirePush(c)
	b, login, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	kinds := names(c.Str("kind"))
	if p := bus.CheckKinds(kinds...); p != "" {
		return tool.Refuse(p)
	}
	// the push proof is advice, never a gate: a name nothing proven is pushing
	// into reads all the same, with one NOTE on its first result (SPEC-BUS.md,
	// bus-requires-inbox-push-proof); with --require-push a name not heard is
	// refused before anything is read, --dry-run alike
	var unheard []string
	dry := c.DryRun() // read before a refusal, so a refused --dry-run is still a dry run
	if gated {
		if err := b.Heard(context.Background(), as); err != nil {
			return answer(err)
		}
	} else {
		var err error
		if unheard, err = b.Unheard(context.Background(), as); err != nil {
			return answer(err)
		}
	}
	noted := false
	note := func(o *tool.Out) *tool.Out {
		if !noted {
			noted = true
			for _, line := range unheard {
				o.Note(line)
			}
		}
		return o
	}
	if dry {
		// what waits, read only: the next delivered is a pending one held past fifteen
		// minutes when there is one, else the oldest new one
		pending, fresh, err := b.Peek(context.Background(), as)
		if err != nil {
			return answer(err)
		}
		pending, fresh = bus.FilterKinds(pending, kinds), bus.FilterKinds(fresh, kinds)
		next := "-"
		if len(fresh) > 0 {
			next = fresh[0].Message().ID
		}
		return note(loginFact(tool.Done().Fact("pending", len(pending)).Fact("new", len(fresh)).Fact("next_new", next), login))
	}
	command := c.Str("exec")
	ctx, stop := w.signals(context.Background())
	defer stop()
	stopped := tool.Done().Note("stopped by a signal; a message being delivered stays pending")
	// one is one recv: the result, and whether a message was delivered
	one := func(block time.Duration) (*tool.Out, bool) {
		e, ok, err := b.RecvKinds(ctx, as, block, kinds)
		if ctx.Err() != nil {
			return stopped, false
		}
		if err != nil {
			return answer(err), false
		}
		if !ok {
			return note(tool.Fail("nothing for " + as).As("NONE")), false
		}
		m := e.Message()
		o := note(message(m, login))
		if command == "" {
			o.Payload = "\n" + m.Body
			if c.Bool("ack") {
				acked, err := b.AckEntry(ctx, as, e.Entry)
				if err != nil {
					return answer(err), false
				}
				o.Fact("acked", acked)
			}
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
	if !c.Bool("forever") && !c.Bool("all") && c.Int("max") == 1 {
		o, _ := one(0)
		return o
	}
	if !c.Bool("forever") {
		// the batch: what waits now, in order, each its own result, until the
		// count is met or nothing waits; none at all is the one NONE
		limit := c.Int("max")
		for taken := 0; c.Bool("all") || taken < limit; taken++ {
			res, ok := one(0)
			switch {
			case ok:
				res.Render(c.Stdout, c.Bool("json"))
			case res.Word == "NONE" && taken > 0:
				return tool.Exit(0)
			case taken == 0:
				return res
			default:
				res.Verb = "recv" // the token of the line, rendered here and not by the skeleton
				res.Render(c.Stderr, c.Bool("json"))
				return tool.Exit(res.Exit)
			}
		}
		return tool.Exit(0)
	}
	// the loop: every message in turn, each a line of its own, until a signal
	// or a command that fails; a NONE is a wait that ran out, not a line
	for {
		res, ok := one(ForeverBlock)
		switch {
		case ok:
			res.Render(c.Stdout, c.Bool("json"))
		case res.Word == "NONE":
		case res == stopped:
			return tool.Exit(0)
		default:
			res.Verb = "recv"
			res.Render(c.Stderr, c.Bool("json"))
			return tool.Exit(res.Exit)
		}
	}
}

func (w world) ack(c *tool.Call) *tool.Out {
	b, login, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	ids := names(c.Str("id"))
	ack := b.Ack
	if c.DryRun() {
		ack = b.WouldAck // which ids are pending for you, acking none
	}
	acked, err := ack(context.Background(), as, ids)
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
	b, login, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	kinds := names(c.Str("kind"))
	if p := bus.CheckKinds(kinds...); p != "" {
		return tool.Refuse(p)
	}
	pending, fresh, err := b.Peek(context.Background(), as)
	if err != nil {
		return answer(err)
	}
	pending, fresh = bus.FilterKinds(pending, kinds), bus.FilterKinds(fresh, kinds)
	o := tool.Done().Fact("pending", len(pending)).Fact("new", len(fresh))
	for _, state := range []struct {
		name string
		es   []bus.Entry
	}{{"pending", pending}, {"new", fresh}} {
		for _, e := range state.es {
			m := e.Message()
			o.Item("message", slices.Concat([]any{"state", state.name, "id", m.ID, "from", m.From}, kindItem(m), []any{"at", m.At.Format(time.RFC3339), "subject", tool.Text(m.Subject)})...)
		}
	}
	return o
}

func (w world) log(c *tool.Call) *tool.Out {
	b, _, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	got, err := b.Log(context.Background(), "-")
	if err != nil {
		return answer(err)
	}
	// Filter by --from and --to before applying --max (SPEC-BUS.md, log verb)
	from := c.Str("from")
	to := c.Str("to")
	if from != "" || to != "" {
		filtered := make([]bus.Entry, 0, len(got))
		for _, e := range got {
			m := e.Message()
			if from != "" && m.From != from {
				continue
			}
			if to != "" {
				found := false
				for _, recipient := range m.To {
					if recipient == to {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			filtered = append(filtered, e)
		}
		got = filtered
	}
	// Apply --max: log reads oldest first, --max returns the newest match
	total := len(got)
	if max := c.Int("max"); max > 0 && len(got) > max {
		got = got[len(got)-max:]
	}
	o := tool.Done().Fact("total", total)
	for _, e := range got {
		m := e.Message()
		kv := slices.Concat([]any{"id", m.ID, "from", m.From, "to", strings.Join(m.To, ","), "cc", strings.Join(m.CC, ","), "re", m.Re}, kindItem(m), []any{"at", m.At.Format(time.RFC3339), "subject", tool.Text(m.Subject)})
		if c.Bool("bodies") {
			kv = append(kv, "body", tool.Text(m.Body))
		}
		o.Item("message", kv...)
	}
	return o
}

func (w world) names(c *tool.Call) *tool.Out {
	b, _, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	got, err := b.Names(context.Background())
	if err != nil {
		return answer(err)
	}
	proofs, now, err := b.PushProofs(context.Background(), got...)
	if err != nil {
		return answer(err)
	}
	proven := 0
	for _, p := range proofs {
		if p.State(now) == bus.PushProven {
			proven++
		}
	}
	o := tool.Done().Fact("count", len(got)).Fact("proven", proven)
	for _, p := range proofs {
		harness := p.Harness
		if harness == "" {
			harness = "-"
		}
		o.Item("name", "name", p.Name, "push", p.State(now), "age", p.AgeWord(now), "harness", harness)
	}
	return o
}

// receipts is as's receipts: the named ids', or every one held (SPEC-BUS.md,
// message-receipts).
func (w world) receipts(c *tool.Call) *tool.Out {
	b, login, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	got, now, err := b.Stages(context.Background(), as, names(c.Str("id"))...)
	if err != nil {
		return answer(err)
	}
	o := tool.Done().Fact("count", len(got))
	for _, s := range got {
		state, age := s.State, "-"
		if state == "" {
			state = "none"
		} else {
			age = s.Age(now).Round(time.Second).String()
		}
		o.Item("receipt", "id", s.ID, "state", state, "age", age)
	}
	return loginFact(o, login)
}

// overdue is every message on every stream still short of delivered past
// --older: OVERDUE at exit 1 when there is one (SPEC-BUS.md, message-receipts).
func (w world) overdue(c *tool.Call) *tool.Out {
	b, _, closeStore, refused := w.bus(c, c.Dur("timeout"))
	if refused != nil {
		return refused
	}
	defer closeStore()
	older := c.Dur("older")
	late, _, err := b.Overdue(context.Background(), older)
	if err != nil {
		return answer(err)
	}
	o := tool.Done()
	if len(late) > 0 {
		o = tool.Fail().As("OVERDUE")
	}
	o.Fact("count", len(late)).Fact("older", older.String())
	for _, l := range late {
		o.Item("message", "name", l.Name, "id", l.ID, "state", l.State, "from", l.From, "age", l.Age.Round(time.Second).String(), "subject", tool.Text(l.Subject))
	}
	return o
}

// address is the store a verb opens: --redis (its default is RedisEnv),
// else the fleet row's bus field read from the sprint store at
// SprintRedisEnv; with none of the three, a refusal naming all three.
func (w world) address(ctx context.Context, c *tool.Call) (string, *tool.Out) {
	if addr := c.Str("redis"); strings.TrimSpace(addr) != "" {
		return addr, nil
	}
	sprint := w.getenv(SprintRedisEnv)
	if sprint == "" {
		return "", tool.Refuse("--redis is required: " + RedisEnv + " is unset, and with no " + SprintRedisEnv + " the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess")
	}
	addr, err := w.fleetBus(ctx, sprint)
	if err != nil {
		return "", tool.Refuse("--redis is required: " + RedisEnv + " is unset and the fleet's bus row could not be read from the sprint store: " + err.Error())
	}
	if addr == "" {
		return "", tool.Refuse("--redis is required: " + RedisEnv + " is unset and the fleet's bus row is empty; set it once: nova-config fleet set --bus <host:port> --as <you>, then nova-config apply")
	}
	return addr, nil
}

// The wait verb: the wake a harness runs beside a session, made general for
// any AI on the bus (docs/SPEC-BUS.md, the verbs: wait). A wait takes
// nothing: it reads the recipient's stream past a cursor with XREAD, never
// the consumer group, so a later recv still delivers and acks what the wait
// saw, and its cursor is the only state, the caller's to hold between runs.
// The decision over one batch is bus.WaitPick, a pure function; the blocking
// read is the store's; the clock and the wake file are the world's, so no
// test opens a socket or waits real time.

// WaitTick is how long one blocking read of a wait with a --wake-file is: the
// file is looked at once a tick, so a line appended to it is returned within
// one. A wait with neither a wake file nor a timeout parks on one read that
// never runs out (docs/SPEC-BUS.md, the verbs: wait).
const WaitTick = time.Second

// wakeLineMax bounds one read of a wake file: a harness appends one line a
// message, and one line a reader can use is far under this.
const wakeLineMax = 64 << 10

// waitMessage is one message of a wait's JSON, the fields the help names.
type waitMessage struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Bytes   int    `json:"bytes"`
}

// waitWake is the wake of a wait's JSON.
type waitWake struct {
	File string `json:"file"`
	Line string `json:"line"`
}

// waitJSON is the --json rendering of one wait: one object, printed when the
// wait ends (the ARMED line is the text form's). The subject and the wake
// line are oneline-escaped, so nothing they hold can reorder the line a
// reader reads.
type waitJSON struct {
	Status     string        `json:"status"`
	Word       string        `json:"word"`
	After      string        `json:"after"`
	Messages   []waitMessage `json:"messages"`
	Wake       *waitWake     `json:"wake,omitempty"`
	WakeAfter  string        `json:"wake_after,omitempty"`
	WakeOffset *int64        `json:"wake_offset,omitempty"`
}

// streamID is whether s is a stream entry id (<ms>-<seq>, both numbers): the
// cursor a wait re-arms with, as WAIT ARMED and WAIT OK print it.
func streamID(s string) bool {
	ms, seq, ok := strings.Cut(s, "-")
	if !ok {
		return false
	}
	_, errMS := strconv.ParseUint(ms, 10, 64)
	_, errSeq := strconv.ParseUint(seq, 10, 64)
	return errMS == nil && errSeq == nil
}

// waitLine prints one wait line on stdout: the verb prints as it goes (the
// ARMED line first, so a caller that re-arms with that id misses nothing
// between two runs), as recv --forever prints each message.
func waitLine(c *tool.Call, o *tool.Out) {
	o.Verb = "wait"
	o.Render(c.Stdout, false)
}

// waitObject prints the wait's one JSON object and stands for its exit.
func waitObject(c *tool.Call, v waitJSON, exit int) *tool.Out {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return tool.Refuse("the wait's result is no JSON: " + err.Error())
	}
	fmt.Fprintf(c.Stdout, "%s", b.String())
	return tool.Exit(exit)
}

// wait is the verb: it arms, prints WAIT ARMED, and returns on the first
// entries past the cursor that count, on a wake line, or at the timeout
// (docs/SPEC-BUS.md, the verbs: wait).
func (w world) wait(c *tool.Call) *tool.Out {
	// wait's --timeout is how long the wait parks (0 is for ever), not the
	// call bound. Non-blocking reads use CallTimeout; a blocking XREAD carries
	// its block and the margin more (SPEC-BUS.md, the deadlines).
	b, login, closeStore, refused := w.bus(c, bus.CallTimeout)
	if refused != nil {
		return refused
	}
	defer closeStore()
	as, refused := identity(c, login)
	if refused != nil {
		return refused
	}
	ctx := context.Background()
	cursor, err := b.WaitArm(ctx, as, c.Str("after"))
	if err != nil {
		return answer(err)
	}
	waiter, err := b.Waiter()
	if err != nil {
		return answer(err)
	}
	jsonOut := c.Bool("json")
	wakePath := c.Str("wake-file")
	var wake wakeCursor
	if wakePath != "" {
		wake, err = w.wakeArm(wakePath, c.Str("wake-after"))
		if err != nil {
			return wakeRefusal(err)
		}
	}
	if !jsonOut {
		waitLine(c, wakeFacts(tool.Done().As("ARMED").Fact("after", cursor), wakePath, wake))
	}
	// --skip-subject is a comma list of prefixes, empty words dropped; the
	// match without case is WaitPick's, the one place the rule lives
	// (docs/SPEC-BUS.md, the verbs: wait).
	skips := names(c.Str("skip-subject"))
	start := w.now()
	timeout := c.Dur("timeout")
	for {
		if wakePath != "" {
			text, next, err := w.wakeLine(wakePath, wake)
			if err != nil {
				return wakeRefusal(err)
			}
			wake = next
			if text != "" {
				if jsonOut {
					return waitObject(c, waitJSON{Status: "ok", Word: "WAKE", After: cursor,
						Messages: []waitMessage{}, Wake: &waitWake{File: wakePath, Line: oneline.Escape(text)}, WakeAfter: wake.token(), WakeOffset: &wake.Offset}, 0)
				}
				waitLine(c, wakeFacts(tool.Done().As("WAKE").Fact("file", wakePath).Fact("line", tool.Text(text)), wakePath, wake))
				return tool.Exit(0)
			}
		}
		block := time.Duration(0) // park for ever: nothing else is watched
		if wakePath != "" {
			block = WaitTick // the file is looked at once a tick
		}
		if timeout > 0 {
			left := timeout - w.now().Sub(start)
			if left <= 0 {
				if jsonOut {
					return waitObject(c, waitJSON{Status: "ok", Word: "NONE", After: cursor, Messages: []waitMessage{}, WakeAfter: wakeToken(wakePath, wake), WakeOffset: wakeOffset(wakePath, wake)}, 1)
				}
				o := wakeFacts(tool.Fail().As("NONE").Fact("after", cursor).Fact("waited", timeout.String()), wakePath, wake)
				o.Verb = "wait"
				o.Render(c.Stderr, false)
				return tool.Exit(1)
			}
			if block == 0 || left < block {
				block = left
			}
		}
		got, err := waiter.BlockRead(ctx, bus.StreamOf(as), cursor, block, bus.WaitRead)
		if err != nil {
			return answer(err)
		}
		kept, after := bus.WaitPick(got, as, skips)
		if after != "" {
			cursor = after
		}
		if len(kept) == 0 {
			continue // a skipped entry moved the cursor; the wait goes on
		}
		if jsonOut {
			msgs := make([]waitMessage, 0, len(kept))
			for _, e := range kept {
				m := e.Message()
				msgs = append(msgs, waitMessage{ID: m.ID, From: m.From, Subject: oneline.Escape(m.Subject), Bytes: len(m.Body)})
			}
			return waitObject(c, waitJSON{Status: "ok", Word: "OK", After: cursor, Messages: msgs, WakeAfter: wakeToken(wakePath, wake), WakeOffset: wakeOffset(wakePath, wake)}, 0)
		}
		for _, e := range kept {
			m := e.Message()
			waitLine(c, tool.Done().As("MESSAGE").Fact("id", m.ID).Fact("from", m.From).Fact("subject", m.Subject).Fact("bytes", len(m.Body)))
		}
		waitLine(c, wakeFacts(tool.Done().Fact("after", cursor), wakePath, wake))
		return tool.Exit(0)
	}
}
