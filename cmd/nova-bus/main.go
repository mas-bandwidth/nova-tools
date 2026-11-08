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
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

// verbUsage is each verb's usage block; the banner is all of them in order.
var verbUsage = map[string]string{
	"send": `  nova-bus send --as <me> --to <name>[,<name>] [--re <id>] [--new] <subject> <body | ->
      Append one message to each recipient's inbox; prints "<name> <id>" for each.
      Recipients are sent to once each. A body of - is read from stdin, less one
      trailing newline. --re takes an id as hook prints it. A name no hook (or
      recv) has ever read is refused before anything is sent, exit 1 (a typo
      would otherwise sit unread forever): --new sends to it anyway, for the
      first message to a name that is about to hook in. send, ack and pending
      act as <me> and run only while <me> is hooked in (a hook holds the name,
      or a recv does): otherwise exit 1 with the hook-in recipe
      for the harness this runs under. A failed send prints SEND FAILED and
      exits 1; it is never retried.
`,
	"hook": `  nova-bus hook --as <name> [--harness <harness> <its flags> | --exec <command>
                | --codex-thread <id> [--resolve <msg id>=woken|retry]]
                [--service [stop]]
      Deliver every waiting message for <name>, then each new one as it arrives,
      into the session that runs the hook, waking it when it is idle.
      --exec <command>: run the command (sh -c) for each message, its line on
      stdin and NOVA_BUS_ID, NOVA_BUS_FROM, NOVA_BUS_RE, NOVA_BUS_SUBJECT and
      NOVA_BUS_BODY in its environment, under the 8 s wake limit; exit 0 is the
      delivery (HOOK EXEC OK id=<id> on stdout), and only then is the message
      marked handled. Any other exit is HOOK EXEC FAILED id=<id> exit=<n> on
      stderr and the hook ends, exit 1, the message unhandled for the next hook.
      --codex-thread <id>: save each message to ~/.local/state/nova-bus/
      <name>-codex-<id>/<msg id>.json (not saved: HOOK WAKE FAILED, the hook
      ends, exit 1, the message unhandled), print its line as claude does, and
      wake the Codex chat <id> with codex queue --thread <id> --message <one
      line naming the file>, only while the chat is idle (its rollout under
      ~/.codex/sessions ends in task_complete, has settled, and holds the last
      wake's text): HOOK WAKE OK harness=codex id=<id> [messages=<n>]. Busy,
      nothing is queued (HOOK WAKE LATER); once idle, one wake names every
      message since. A failed codex queue is HOOK WAKE FAILED try=<n>/3 on
      stderr and is tried again at the next idle poll; after 3 it is a fault
      (result=failed), and a queue cut off, or found in flight at start, is
      one at once (result=unknown): HOOK WAKE FAULT, its files kept. A fault
      holds back no later message. A start retries each failed fault once;
      an unknown one waits for --resolve: <msg id>=woken once you have seen
      the wake arrive, =retry to have it tried once more (HOOK RESOLVE OK).
      The show verb's codex-wake lists them. Under Codex (CODEX_THREAD_ID set)
      the hook runs only with --service, exit 2 otherwise (nova-bus show
      codex-agents).
      How your session gets woken, per --harness:
        claude (the default): each message is one line on stdout,
          id<TAB>from<TAB>re<TAB>subject<TAB>body (re is - when none; \n, \r,
          \t and \\ escaped); a line starting "HOOK " is status, not mail (HOOK
          RECONNECTED, HOOK BAD). Claude Code and Muse Code run the hook under
          the session's Monitor tool, never with & or into a file: the
          Monitor turns each line into a notification, and that is the wake.
          Refused (exit 2) when stdout is /dev/null.
        grok --grok-home <dir>: each message is saved to <dir>/nova-bus/<id>.json
          and a line naming that file is appended to <dir>/nova-hook.wake;
          the session's Monitor runs tail -n 0 -F <dir>/nova-hook.wake first.
        antigravity --antigravity-dir <workspace> --antigravity-session <id>
          --antigravity-events-dir <dir> [--antigravity-server <port>]: each
          message is saved to <dir>/<id>.json and sent into the conversation
          with the workspace's language_server agentapi send-message.
      grok and antigravity print one receipt per message on stdout,
      HOOK WAKE OK harness=<h> id=<id> [confirmed=no reason=<r>], or
      HOOK WAKE FAILED harness=<h> id=<id> reason=<r> on stderr and exit 1 with
      the message unhandled and every later one behind it: once the harness
      answers, run the hook again (the service restarts it). The harness is
      checked at start, before the name is claimed: not there, exit 2; not
      answering, exit 1. A message is marked handled only after it is
      delivered, so a restarted hook resumes where it stopped; one delivered
      just before a crash may be delivered again. While it runs, <name> is
      present (see who). Runs until killed; exits 0 on SIGINT/SIGTERM.
      --service installs the hook as a per-user unit the system keeps running
      beside your session (launchd on macOS, systemd --user elsewhere), after
      the same checks, with NOVA_BUS_REDIS, NOVA_BUS_USER and NOVA_BUS_PASSWORD
      in its environment, logging to ~/.local/state/nova-bus/<name>.log; it
      waits for the unit's HOOK OK and prints HOOK SERVICE OK name=<name>
      unit=<unit> log=<log> changed=<bool>. Run again with the same flags, it
      changes nothing; with new ones, it rewrites and restarts the unit.
      --service stop stops and removes the unit. Not for claude without
      --codex-thread: its wake is the Monitor of the session itself.
      One hook per name: a hook that finds the name held waits up to 40 s for
      it to lapse (a restart after a crash), then claims it or, if it is still
      held, exits 1 "name already hooked". On stderr: HOOK HARNESS OK once the
      harness answered, HOOK WAIT while waiting, HOOK OK name=<name>
      waiting=<n> once claimed, then the line next: <the send line>. On stdout, where the
      Monitor sees it: HOOK RECONNECTED name=<name> waiting=<n> once a lost
      connection is back, and HOOK BAD id=<id> missing=<fields> for an entry
      send did not write (acked, never delivered as mail).
`,
	"recv": `  nova-bus recv --as <name> [--timeout <duration>]
      Wait for one message for <name> (the oldest unhandled first, as hook
      orders them), print it as one line, id<TAB>from<TAB>re<TAB>subject<TAB>body
      (as hook does), and exit 0 without marking it handled: ack does that.
      Nothing within --timeout (default: wait until one comes) is RECV NONE
      name=<name> waited=<d> on stderr, exit 1. A message received and not
      acked is received again by the next recv, so a reader that dies between
      the two sees it twice: by its id, a repeat is ignored. recv records
      <name>'s presence (see who) and takes no lock: it does not coexist
      with a hook for the same name, which would take the same messages.
`,
	"ack": `  nova-bus ack --as <name> --id <id>
      Mark the message <id>, received by recv, handled: ACK OK id=<id>, or
      ACK FAILED id=<id> reason=... (exit 1) when it was not received or is
      handled already.
`,
	"who": `  nova-bus who
      Print WHO OK present=<n>, then each name whose hook is running (or
      whose recv is reading), how long it has been up, and the hook's session id,
      one per line, name<TAB>uptime[<TAB>session | <TAB>recv holding <id>],
      sorted by name. A crashed hook drops out within 30 seconds.
`,
	"pending": `  nova-bus pending --as <name> [--list]
      Print PENDING OK waiting=<n>, the messages for <name> no hook has handled;
      with --list, then id<TAB>from<TAB>subject for each, oldest first. Nothing
      is consumed.
`,
	"show": `  nova-bus show agents | codex-agents | codex-wake --as <name> --codex-thread <id>
      Print the lines for your harness's standing instructions file (AGENTS.md,
      CLAUDE.md) that make every fresh session run the hook unasked: agents is
      the Monitor form (Claude Code, Muse Code); codex-agents the
      --codex-thread service form (Codex). codex-wake prints the wake state of
      a --codex-thread hook: CODEX WAKE OK name= thread= dir= woken= awaiting=
      faults=, then id<TAB>state<TAB>detail<TAB>file for each id in flight
      (intent-unknown), faulted (fault result=failed|unknown|retry attempts=
      first= error=) or waiting. Nothing else is read or written.
`,
	"help": `  nova-bus help [<verb>] | <verb> -h
      Print this, or one verb's usage.
`,
}

var verbOrder = []string{"send", "hook", "recv", "ack", "who", "pending", "show", "help"}

const usageFooter = `
A name is letters, digits, '.', '_' and '-'. Exit 0 ok, 1 refused or failed,
2 usage error. A verb that acts as you (send, ack, pending) refuses until you
are hooked in, and says how. Environment: NOVA_BUS_REDIS=host:port (required),
NOVA_BUS_USER and NOVA_BUS_PASSWORD (optional; AUTH as that user, else default).
`

func usage() string {
	b := "nova-bus: messages between named inboxes, kept in Redis.\n\n"
	for _, v := range verbOrder {
		b += verbUsage[v]
	}
	return b + usageFooter
}

// verbs maps each verb to the flags it takes.
var verbs = map[string][]string{"send": {"as", "to", "re", "new"}, "hook": append([]string{"as", "harness", "service", "exec", "codex-thread", "resolve"}, slices.Sorted(maps.Keys(flagWants))...),
	"recv": {"as", "timeout"}, "ack": {"as", "id"}, "who": {}, "pending": {"as", "list"}, "show": {"as", "codex-thread"}}

// Group is the consumer group, and consumer, every hook reads its inbox with.
const group = "hook"

// A hook holds its presence key for presenceTTL and sets it again every
// presenceRefresh, so a live hook never lapses and a crashed one lapses
// within presenceTTL (tla/Presence.tla: TTL, R). A read waits at most
// readBlock, because a blocked read is not interrupted by a signal: it bounds
// how long a clean exit takes. An ack or a release outlives the signal by at
// most cleanupTimeout. A hook that finds its name held polls every
// claimPoll for at most presenceTTL+presenceRefresh: a dead hook's key has
// lapsed by then, a live one's has been refreshed.
const (
	presenceTTL     = 30 * time.Second
	checkoutTTL     = 15 * time.Minute // a recv's claim while its message is out, unacked
	presenceRefresh = 10 * time.Second
	readBlock       = time.Second
	cleanupTimeout  = 2 * time.Second
	claimPoll       = time.Second
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
	// Hooked says whether a hook (or recv) has ever read stream: its group
	// exists.
	Hooked(ctx context.Context, stream string) (bool, error)
	// Read returns this consumer's delivered-but-unacked entries when
	// pending, else waits up to block for new ones (none on a quiet timeout).
	Read(ctx context.Context, stream string, pending bool, block time.Duration) ([]Msg, error)
	// Ack marks the entry handled and deletes it; it returns how many were
	// acked (0: not handed, or already handled).
	Ack(ctx context.Context, stream, id string) (int64, error)
	// Count is the number of entries no hook has acked; Waiting returns
	// them, oldest first. Neither consumes anything. Trim deletes every entry
	// older than id.
	Count(ctx context.Context, stream string) (int64, error)
	Waiting(ctx context.Context, stream string) ([]Msg, error)
	Trim(ctx context.Context, stream, id string) error
	// Claim sets name's presence to value for ttl unless another value is
	// present, which it returns ("" when claimed); Release deletes it only if
	// it still holds value.
	Claim(ctx context.Context, name, value string, ttl time.Duration) (string, error)
	// Hold is Claim for a recv: it sets the key unless a value that is not a
	// recv's (no "\trecv" in it: a hook's) is present, which it returns.
	Hold(ctx context.Context, name, value string, ttl time.Duration) (string, error)
	Release(ctx context.Context, name, value string) error
	// Left is how long name's presence key has to live (0: none).
	Left(ctx context.Context, name string) (time.Duration, error)
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

var (
	errHooked       = errors.New("name already hooked")
	errHookedByRecv = errors.New("name already hooked by a recv")
	errTaken        = errors.New("name taken by another hook")
)

// hookedByRecv is errHookedByRecv with what the recv holds: a checked-out
// message (id, and how long its claim has left) or nothing (it is waiting).
type hookedByRecv struct {
	id   string
	left time.Duration
}

func (hookedByRecv) Error() string        { return errHookedByRecv.Error() }
func (hookedByRecv) Is(target error) bool { return target == errHookedByRecv }

// main stops a hook cleanly on SIGINT or SIGTERM, releasing its presence, and
// ignores SIGPIPE so a closed stdout is a write error (EPIPE) and the same
// clean exit. A kill at any other point is safe too: a message is acked only
// after its line is written (tla/Bus.tla), and presence lapses by its TTL
// (tla/Presence.tla).
func main() {
	signal.Ignore(syscall.SIGPIPE)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	home, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	s := sys{uid: os.Getuid(), goos: runtime.GOOS, home: home, exe: exe, devOf: realDevOf, outGone: stdoutGone, environ: os.Environ,
		exec: realExec, look: exec.LookPath, program: realProgram, shell: realShell}
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, dialRedis, realClock{}, s)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer, dial func(addr, user, password string) Store, clk Clock, s sys) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || (args[0] == "help" && len(args) == 1) {
		fmt.Fprint(stdout, usage())
		return 0
	}
	verb := args[0]
	if verb == "help" {
		verb = args[1]
	}
	takes, ok := verbs[verb]
	if !ok && verb != "help" {
		fmt.Fprintf(stderr, "nova-bus: unknown verb %q; verbs: %s; run: nova-bus help\n", verb, strings.Join(verbOrder, ", "))
		return 2
	}
	if args[0] == "help" {
		fmt.Fprint(stdout, verbUsage[verb]+usageFooter)
		return 0
	}
	fs := flag.NewFlagSet("nova-bus "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	as := fs.String("as", "", "")
	to := fs.String("to", "", "")
	re := fs.String("re", "", "")
	list := fs.Bool("list", false, "")
	fresh := fs.Bool("new", false, "")
	harness := fs.String("harness", "claude", "")
	service := fs.Bool("service", false, "")
	command := fs.String("exec", "", "")
	codexThread := fs.String("codex-thread", "", "")
	resolve := fs.String("resolve", "", "")
	timeout := fs.Duration("timeout", 0, "")
	id := fs.String("id", "", "")
	hflags := map[string]*string{}
	for f := range flagWants {
		hflags[f] = fs.String(f, "", "")
	}
	if verb == "show" && len(args) > 2 && args[1] == "codex-wake" { // show codex-wake --as <name> ...: the topic first, its flags after
		args = append(append([]string{verb}, args[2:]...), args[1])
	}
	if err := fs.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stdout, verbUsage[verb]+usageFooter)
		return 0
	} else if err != nil {
		return misuse(stderr, verb, "%s", flagError(err))
	}
	var bad []string
	fs.Visit(func(f *flag.Flag) {
		if !slices.Contains(takes, f.Name) {
			bad = append(bad, "--"+f.Name)
		}
	})
	if len(bad) > 0 {
		return misuse(stderr, verb, "unknown flag %s", strings.Join(bad, ", "))
	}
	var missing []string
	codexShowing := verb == "show" && fs.NArg() == 1 && fs.Arg(0) == "codex-wake"
	if (verb != "who" && verb != "show" || codexShowing) && strings.TrimSpace(*as) == "" {
		missing = append(missing, "--as <name>")
	}
	if verb == "send" && *to == "" {
		missing = append(missing, "--to <name>")
	}
	if verb == "send" && fs.NArg() < 2 {
		missing = append(missing, []string{"<subject> <body>", "<body>"}[fs.NArg()])
	}
	stop := verb == "hook" && *service && fs.NArg() == 1 && fs.Arg(0) == "stop"
	var given, ignored []string // the harness flags set, and those this harness does not take
	fs.Visit(func(f *flag.Flag) {
		if _, ok := flagWants[f.Name]; ok {
			given = append(given, f.Name)
		}
	})
	_, knownHarness := harnessFlags[*harness]
	if knownHarness && !stop {
		var need []string
		ignored, need = harnessCheck(*harness, given)
		missing = append(missing, need...)
	}
	if env("NOVA_BUS_REDIS") == "" && !stop && verb != "show" && *resolve == "" {
		missing = append(missing, "NOVA_BUS_REDIS (host:port)")
	}
	var names []string // --to, trimmed, each once, in order
	for _, n := range strings.Split(*to, ",") {
		if n = strings.TrimSpace(n); !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	bad = nil // the names this verb uses that are not a name's shape
	if verb == "send" {
		bad = slices.DeleteFunc(slices.Clone(names), validName)
	}
	if (verb != "who" && verb != "show" || *as != "") && !validName(*as) {
		bad = append([]string{*as}, bad...)
	}
	switch {
	case !knownHarness:
		return misuse(stderr, verb, "unknown --harness %q; harnesses: %s", *harness, strings.Join(harnessOrder, ", "))
	case len(ignored) > 0:
		return misuse(stderr, verb, "%s not taken by --harness %s", strings.Join(ignored, ", "), *harness)
	case len(missing) > 0:
		return misuse(stderr, verb, "missing %s", strings.Join(missing, ", "))
	case env("NOVA_BUS_USER") != "" && env("NOVA_BUS_PASSWORD") == "":
		return misuse(stderr, verb, "NOVA_BUS_USER is set but NOVA_BUS_PASSWORD is empty: a named user needs its password")
	case verb == "send" && slices.Contains(names, ""):
		return misuse(stderr, verb, "empty name in --to %q", *to)
	case len(bad) > 0:
		return misuse(stderr, verb, "bad name %q: a name is letters, digits, '.', '_' and '-'", bad[0])
	case verb == "ack" && *id == "":
		return misuse(stderr, verb, "missing --id <id>")
	case verb == "ack" && !idShape(*id):
		return misuse(stderr, verb, "--id %q is not a message id like 1730800000000-0 (as recv prints it)", *id)
	case verb == "recv" && *timeout < 0:
		return misuse(stderr, verb, "--timeout %s is negative", *timeout)
	case *re != "" && !idShape(*re):
		return misuse(stderr, verb, "--re %q is not a message id like 1730800000000-0 (as hook prints it)", *re)
	case verb == "hook" && *service && fs.NArg() > 0 && !stop:
		return misuse(stderr, verb, "--service takes only stop after it, not %q", strings.Join(fs.Args(), " "))
	case verb == "show" && (fs.NArg() != 1 || agentsSnippets[fs.Arg(0)] == "" && !codexShowing):
		return misuse(stderr, verb, "show takes one of agents, codex-agents, codex-wake")
	case verb == "show" && !codexShowing && (*as != "" || *codexThread != ""):
		return misuse(stderr, verb, "show %s takes no flags; --as and --codex-thread are for show codex-wake", fs.Arg(0))
	case (codexShowing || *resolve != "") && !safeID(*codexThread):
		return misuse(stderr, verb, "missing --codex-thread <id> (the chat's thread id, as the hook was given it)")
	case verb != "send" && verb != "show" && fs.NArg() != 0 && !stop:
		return misuse(stderr, verb, "%s takes no arguments", verb)
	case verb == "send" && fs.NArg() > 2:
		return misuse(stderr, verb, "send takes one subject and one body, not %d arguments", fs.NArg())
	case verb == "send" && strings.TrimSpace(fs.Arg(0)) == "":
		return misuse(stderr, verb, "empty subject")
	case verb == "hook" && *command != "" && *harness != "claude":
		return misuse(stderr, verb, "--exec delivers each message to a command of yours; it takes no --harness")
	case verb == "hook" && *codexThread != "" && (*command != "" || *harness != "claude"):
		return misuse(stderr, verb, "--codex-thread wakes a Codex chat beside the stdout lines; it takes no --exec or --harness")
	case verb == "hook" && *command != "" && strings.TrimSpace(*command) == "":
		return misuse(stderr, verb, "--exec wants a command")
	case verb == "hook" && *service && !stop && *harness == "claude" && *command == "" && *codexThread == "":
		return misuse(stderr, verb, "--service with --harness claude wakes nothing: Claude Code's wake is the session's Monitor; run the hook under the Monitor tool, give --exec a command to run beside the session, or --codex-thread a Codex chat to wake")
	case verb == "hook" && !*service && *harness == "claude" && *command == "" && *codexThread == "" && stdoutIsNull(s):
		return misuse(stderr, verb, "stdout is /dev/null, nothing would read the messages")
	}
	if codexShowing {
		codexShow(s, *as, *codexThread, stdout)
		return 0
	}
	if verb == "hook" && *resolve != "" {
		return codexResolve(s, *as, *codexThread, *resolve, stdout, stderr)
	}
	if verb == "show" {
		fmt.Fprint(stdout, agentsSnippets[fs.Arg(0)])
		return 0
	}
	if why := codexHook(env); verb == "hook" && !*service && why != "" {
		return misuse(stderr, verb, "%s", why)
	}
	if why := codexRecv(env, s.home); verb == "recv" && why != "" {
		return misuse(stderr, verb, "%s", why)
	}
	if stop {
		return stopService(ctx, s, *as, stdout, stderr)
	}
	var w waker
	again := "nova-bus"
	for _, a := range args {
		again += " " + shellQuote(a)
	}
	if verb == "hook" && *service {
		if code := needManager(s, unitFor(s, *as), stderr); code != 0 {
			return code
		}
	}
	if verb == "hook" {
		f := map[string]string{}
		for k, v := range hflags {
			f[k] = *v
		}
		var err error
		if *command != "" {
			w = execWake{*command, s}
		} else if *codexThread != "" {
			if w, err = newCodex(*codexThread, *as, env("NOVA_BUS_REDIS"), env, s, clk, stderr); err != nil {
				return misuse(stderr, verb, "%v", err)
			}
		} else if w, err = newWaker(ctx, *harness, *as, f, s, stderr); err != nil {
			var se startError
			if errors.As(err, &se) && se.bad {
				return misuse(stderr, verb, "%v", err)
			}
			return fail(stderr, verb, "%v; once it answers, run: %s", err, again)
		}
		if *service {
			unitArgs := []string{"hook", "--as", *as, "--harness", *harness}
			if *command != "" {
				unitArgs = append(unitArgs, "--exec", *command)
			}
			if *codexThread != "" {
				unitArgs = append(unitArgs, "--codex-thread", *codexThread)
			}
			for _, k := range slices.Sorted(maps.Keys(flagWants)) {
				if slices.Contains(given, k) {
					unitArgs = append(unitArgs, "--"+k, *hflags[k])
				}
			}
			return installService(ctx, s, clk, *as, unitArgs, env, stdout, stderr)
		}
	}
	addr := env("NOVA_BUS_REDIS")
	store := dial(addr, env("NOVA_BUS_USER"), env("NOVA_BUS_PASSWORD"))
	// A verb that acts as <me> runs only while <me> is hooked in (a hook
	// holds the name, or a recv does): everybody hooks in, so
	// every answer has a reader. who, show, hook and recv stay open.
	if verb == "send" || verb == "ack" || verb == "pending" {
		present, err := store.Presence(ctx)
		if err != nil && verb == "send" {
			return fail(stderr, verb, "SEND FAILED to %s: %s", names[0], describe(err, addr))
		}
		if err != nil {
			return fail(stderr, verb, "%s", describe(err, addr))
		}
		if _, ok := present[*as]; !ok {
			var environ []string
			if s.environ != nil {
				environ = s.environ()
			}
			return fail(stderr, verb, "%s is not hooked in, so nothing would read an answer; hook in first: %s", *as, hookInRecipe(harnessOf(environ), *as, addr))
		}
	}
	switch verb {
	case "hook":
		if stdoutIsNull(s) { // no reader to lose; Darwin's poll(2) reports POLLNVAL for /dev/null
			s.outGone = nil
		}
		return runHook(ctx, store, *as, addr, clk, stdout, stderr, w, again, s.outGone)
	case "recv":
		return recv(ctx, store, *as, addr, *timeout, clk, stdout, stderr)
	case "ack":
		return ack(ctx, store, *as, addr, *id, stdout, stderr)
	case "who":
		return who(ctx, store, addr, clk.Now(), stdout, stderr)
	case "pending":
		return pending(ctx, store, *as, addr, *list, stdout, stderr)
	}
	body := fs.Arg(1)
	if body == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return fail(stderr, verb, "reading body from stdin: %v", err)
		}
		body = strings.TrimSuffix(string(b), "\n")
	}
	msg := map[string]string{"from": *as, "to": strings.Join(names, ","), "subject": fs.Arg(0), "body": body, "re": *re}
	for _, name := range names {
		hooked, err := store.Hooked(ctx, "bus:"+name)
		if err != nil {
			return fail(stderr, verb, "SEND FAILED to %s: %s", name, describe(err, addr))
		}
		if !hooked && !*fresh {
			return fail(stderr, verb, "no hook has ever held %s, so nothing would read it; run: nova-bus who, or send --new to a name about to hook in", name)
		}
	}
	for _, name := range names {
		id, err := store.Add(ctx, "bus:"+name, msg)
		if err != nil {
			return fail(stderr, verb, "SEND FAILED to %s: %s", name, describe(err, addr))
		}
		fmt.Fprintf(stdout, "%s %s\n", name, id)
	}
	return 0
}

// validName is a name's shape: letters, digits, '.', '_' and '-'.
func validName(n string) bool {
	return n != "" && strings.Trim(n, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == ""
}

// idShape is a stream id as hook prints it: <ms>-<seq>.
func idShape(id string) bool {
	ms, seq, ok := strings.Cut(id, "-")
	digits := func(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }
	return ok && digits(ms) && digits(seq)
}

// describe names a Redis error, saying an unreachable or lost server plainly.
func describe(err error, addr string) string {
	msg := err.Error()
	for _, c := range [][2]string{{"connection refused", "connection refused"}, {"no such host", "no such host"}, {"timeout", "timed out"},
		{"connection reset", "connection lost"}, {"broken pipe", "connection lost"}, {io.EOF.Error(), "connection lost"}} {
		if strings.Contains(msg, c[0]) {
			return fmt.Sprintf("cannot reach redis at %s (NOVA_BUS_REDIS): %s; check that NOVA_BUS_REDIS=%s is the fleet's Redis and that it is up", addr, c[1], addr)
		}
	}
	// Redis's own words are never shown raw: a login that is missing, wrong
	// or short of permission, a client that talked before logging in, an
	// address that is not Redis (a protocol error, or a reply that does not
	// parse), and anything else, which is named as Redis's after "redis:".
	switch {
	case strings.Contains(msg, "NOAUTH") || strings.Contains(msg, "unauthenticated"):
		return fmt.Sprintf("redis at %s wants a login: set NOVA_BUS_USER and NOVA_BUS_PASSWORD", addr)
	case strings.Contains(msg, "WRONGPASS"):
		return fmt.Sprintf("redis at %s refused the login: check NOVA_BUS_USER and NOVA_BUS_PASSWORD", addr)
	case strings.Contains(msg, "NOPERM"):
		return fmt.Sprintf("the login lacks permission on this stream at %s: ask whoever runs Redis for the bus ACL (see the README)", addr)
	case strings.Contains(msg, "Protocol error") || strings.Contains(msg, "parse"):
		return fmt.Sprintf("%s is not talking Redis protocol: check that NOVA_BUS_REDIS=%s is the fleet's Redis", addr, addr)
	}
	return fmt.Sprintf("redis at %s (NOVA_BUS_REDIS) answered with an error; redis: %s", addr, msg)
}

// missingFields names the fields an inbox entry must carry to be a message
// (send writes them all): one without them was not written by send.
func missingFields(m Msg) string {
	var missing []string
	for _, k := range []string{"from", "subject"} {
		if m.Fields[k] == "" {
			missing = append(missing, k)
		}
	}
	return strings.Join(missing, ",")
}

// runHook runs hook, reconnecting after an error, until ctx ends (exit 0), stdout
// fails or loses its reader, or a wake fails (exit 1), releasing presence; or until
// the name is found hooked by another process (exit 1, its presence left alone)
// (tla/Presence.tla Exit, Claim: OnlyOwnerDeletes, GoneAfterExit).
func runHook(ctx context.Context, store Store, name, addr string, clk Clock, stdout, stderr io.Writer, w waker, again string, gone func() bool) int {
	var sid [16]byte
	_, _ = rand.Read(sid[:]) // ignored: crypto/rand.Read never fails
	value := fmt.Sprintf("%s\t%x", clk.Now().UTC().Format(time.RFC3339), sid)
	var logged time.Time
	announced := false
	onClaim := func(waiting int64) { // HOOK OK once; after a reconnect, HOOK RECONNECTED where the Monitor sees it
		if !announced {
			fmt.Fprintf(stderr, "HOOK OK name=%s waiting=%d\nnext: NOVA_BUS_REDIS=%s nova-bus send --as %s --to <name> <subject> <body>\n", name, waiting, addr, name)
			announced = true
		} else if !logged.IsZero() {
			fmt.Fprintf(stdout, "HOOK RECONNECTED name=%s waiting=%d\n", name, waiting)
			logged = time.Time{}
		}
	}
	for {
		err := hook(ctx, store, name, value, clk, stdout, stderr, onClaim, w, gone)
		var we writeError
		var wf wakeFailed
		var byRecv hookedByRecv
		if errors.As(err, &byRecv) {
			what := "a recv is waiting on it and claims it every 10 s"
			if byRecv.id != "" {
				what = fmt.Sprintf("a recv holds message %s checked out, for %s more unless it is acked", byRecv.id, byRecv.left.Truncate(time.Second))
			}
			return fail(stderr, "hook", "name already hooked by a recv: %s after %s; %s: stop that reader first, or ack; run: nova-bus who", name, presenceTTL+presenceRefresh, what)
		}
		if errors.Is(err, errHooked) {
			return fail(stderr, "hook", "name already hooked: %s after %s; run: nova-bus who", name, presenceTTL+presenceRefresh)
		}
		if errors.Is(err, errTaken) {
			return fail(stderr, "hook", "name taken by another hook while this one lapsed: %s; run: nova-bus who", name)
		}
		if ctx.Err() == nil && !errors.As(err, &we) && !errors.As(err, &wf) {
			if t := clk.Now(); logged.IsZero() || t.Sub(logged) >= time.Minute {
				fmt.Fprintf(stderr, "nova-bus hook: %s; reconnecting\n", describe(err, addr))
				logged = t
			}
			select {
			case <-ctx.Done():
			case <-clk.After(time.Second):
			}
			continue
		}
		rel, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		if rerr := store.Release(rel, name, value); rerr != nil {
			fmt.Fprintf(stderr, "nova-bus hook: releasing presence: %s\n", describe(rerr, addr))
		}
		cancel()
		if ctx.Err() != nil {
			return 0
		}
		if errors.As(err, &wf) {
			return fail(stderr, "hook", "the wake of %s failed and it waits unhandled; once the harness answers, run: %s", wf.id, again)
		}
		return fail(stderr, "hook", "writing stdout: %v", err)
	}
}

// hook claims name's presence, waiting for another hook's key to lapse
// (tla/Presence.tla Start, Poll, GiveUp: WaitClaims, WaitEnds), then delivers
// and acks the pending entries, then the new ones, until an error. Each call
// starts from the pending list, so a batch handed by a read whose reply was
// lost is delivered after the reconnect. With no waker an entry is delivered
// by printing its line; with one, by its wake and a receipt. A wake that
// surely failed ends the hook, unacked, so nothing behind it arrives first
// and the next hook delivers it (tla/Bus.tla FailWake); an unconfirmed one is
// acked. Presence is refreshed when due before every delivery, and a wake is
// bounded by wakeLimit, so a batch never outlives the key (tla/Presence.tla R).
func hook(ctx context.Context, store Store, name, value string, clk Clock, out, stderr io.Writer, onClaim func(waiting int64), w waker, gone func() bool) error {
	stream := "bus:" + name
	held, err := store.Claim(ctx, name, value, presenceTTL)
	if err == nil && held != "" {
		since, _, _ := strings.Cut(held, "\t")
		fmt.Fprintf(stderr, "HOOK WAIT name=%s held_since=%s ttl=%s budget=%s\n", name, since, presenceTTL, presenceTTL+presenceRefresh)
		for end := clk.Now().Add(presenceTTL + presenceRefresh); err == nil && held != "" && clk.Now().Before(end); {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-clk.After(claimPoll):
			}
			held, err = store.Claim(ctx, name, value, presenceTTL)
		}
	}
	if err == nil && held != "" {
		err = errHooked
		if _, rest, ok := strings.Cut(held, "\trecv"); ok { // a recv's claim: waiting, or holding a message
			left, _ := store.Left(ctx, name) // 0 when it cannot be read
			err = hookedByRecv{strings.TrimPrefix(rest, "\t"), left}
		}
	}
	if err != nil {
		return err
	}
	waiting, err := store.Count(ctx, stream)
	if err != nil {
		return err
	}
	// The name is held: only now is a Codex wake's state read and repaired
	// (no other hook can be mid-queue on it).
	if c, ok := w.(*codexWake); ok && !c.recovered {
		if err := c.recover(stderr); err != nil {
			fmt.Fprintf(stderr, "HOOK WAKE FAILED harness=codex reason=recovering the wake state: %s\n", escaper.Replace(err.Error()))
			return wakeFailed{"-", err}
		}
	}
	onClaim(waiting)
	due := clk.Now().Add(presenceRefresh)
	refresh := func() error {
		t := clk.Now()
		if t.Before(due) {
			return nil
		}
		due = t.Add(presenceRefresh)
		held, err := store.Claim(ctx, name, value, presenceTTL)
		if err == nil && held != "" {
			err = errTaken
		}
		return err
	}
	if err := store.Group(ctx, stream); err != nil {
		return err
	}
	pending, trimmed := true, false
	for {
		if err := refresh(); err != nil {
			return err
		}
		msgs, err := store.Read(ctx, stream, pending, min(max(due.Sub(clk.Now()), time.Millisecond), readBlock))
		if err != nil {
			return err
		}
		// The reader of stdout (the Monitor, the session) is gone: nothing
		// would read a line, and the name must free for the next hook; a
		// read returns within readBlock, so this is seen within a second.
		if gone != nil && gone() {
			return writeError{errors.New("stdout closed: its reader is gone")}
		}
		// A quiet read is when a Codex chat that was busy is seen idle and
		// woken for what waits (codex.go).
		if c, ok := w.(*codexWake); ok && len(msgs) == 0 {
			if _, err := c.poll(ctx, out, stderr); err != nil {
				return asWrite(err)
			}
		}
		if pending && len(msgs) == 0 {
			pending = false
		}
		// The first pending entry is the oldest unacked: older is acked history
		// from an older build. Never a new entry: a paused hook may hold an
		// older one, handed and not yet printed.
		if pending && !trimmed && len(msgs) > 0 {
			if err := store.Trim(ctx, stream, msgs[0].ID); err != nil {
				return err
			}
			trimmed = true
		}
		for _, m := range msgs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := refresh(); err != nil {
				return err
			}
			// An entry send did not write (no from or subject) is not mail:
			// said where the Monitor sees it, then acked, so it never blocks.
			if missing := missingFields(m); missing != "" {
				if _, err := fmt.Fprintf(out, "HOOK BAD id=%s missing=%s\n", m.ID, missing); err != nil {
					return writeError{err}
				}
			} else if err := deliver(ctx, w, m, out, stderr); err != nil {
				return err
			}
			ack, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			_, err := store.Ack(ack, stream, m.ID)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

// wakeFailed is a wake that surely did not arrive: it ends the hook.
type wakeFailed struct {
	id  string
	err error
}

func (e wakeFailed) Error() string { return e.err.Error() }

// deliver prints m's line, or wakes the session with it and prints its
// receipt: HOOK WAKE OK on stdout (confirmed=no when its outcome is
// unknown), or HOOK WAKE FAILED on stderr and a wakeFailed.
func deliver(ctx context.Context, w waker, m Msg, out, stderr io.Writer) error {
	if c, ok := w.(*codexWake); ok {
		return c.deliver(ctx, m, out, stderr)
	}
	if w == nil {
		if _, err := io.WriteString(out, line(m)); err != nil {
			return writeError{err}
		}
		return nil
	}
	limit := wakeLimit
	if l, ok := w.(interface{ limit() time.Duration }); ok {
		limit = l.limit()
	}
	wctx, cancel := context.WithTimeout(ctx, limit)
	err := w.wake(wctx, m)
	cancel()
	receipt := "HOOK WAKE OK harness=" + harnessName(w) + " id=" + m.ID
	var u unconfirmed
	var x exitError
	switch {
	case errors.As(err, &x):
		fmt.Fprintf(stderr, "HOOK EXEC FAILED id=%s exit=%d\n", m.ID, x.code)
		return wakeFailed{m.ID, err}
	case err != nil && harnessName(w) == "exec":
		fmt.Fprintf(stderr, "HOOK EXEC FAILED id=%s exit=-1 reason=%s\n", m.ID, escaper.Replace(err.Error()))
		return wakeFailed{m.ID, err}
	case harnessName(w) == "exec":
		receipt = "HOOK EXEC OK id=" + m.ID
	case errors.As(err, &u):
		receipt += " confirmed=no reason=" + escaper.Replace(err.Error())
	case err != nil:
		fmt.Fprintf(stderr, "HOOK WAKE FAILED harness=%s id=%s reason=%s\n", harnessName(w), m.ID, escaper.Replace(err.Error()))
		return wakeFailed{m.ID, err}
	}
	if _, err := io.WriteString(out, receipt+"\n"); err != nil {
		return writeError{err}
	}
	return nil
}

// recv prints one message for name, the oldest unhandled first, without
// acking it: for a reader that cannot run the hook, which receives, hands
// the line on, then acks. It is refused while a hook holds the name (both read the same
// group, and a recv would take the hook's messages). Presence is set with
// the token "recv" and refreshed while it waits, so a name read this way
// shows in who. A read hands up to 100 entries to the pending list, which
// the next recv reads first, so the order is kept (tla/Bus.tla: recv is a
// Print with the ack left to a later process).
func recv(ctx context.Context, store Store, name, addr string, timeout time.Duration, clk Clock, stdout, stderr io.Writer) int {
	stream := "bus:" + name
	start := clk.Now()
	if err := store.Group(ctx, stream); err != nil {
		return fail(stderr, "recv", "%s", describe(err, addr))
	}
	value := start.UTC().Format(time.RFC3339) + "\trecv"
	present, err := store.Presence(ctx)
	if err != nil {
		return fail(stderr, "recv", "%s", describe(err, addr))
	}
	if v, ok := present[name]; ok && !strings.Contains(v, "\trecv") {
		return fail(stderr, "recv", "name hooked: a hook holds %s and would take the same messages; run: nova-bus who", name)
	} else if ok {
		value = v // the uptime is since the first recv
	}
	if holding := strings.Count(value, "\t"); holding == 2 { // the last recv's message is still checked out
		value = value[:strings.LastIndex(value, "\t")]
	}
	claim := func(v string, ttl time.Duration) error {
		held, err := store.Hold(ctx, name, v, ttl)
		if err == nil && held != "" {
			err = fmt.Errorf("name hooked: a hook took %s; run: nova-bus who", name)
		}
		return err
	}
	if err := claim(value, presenceTTL); err != nil {
		return fail(stderr, "recv", "%s", describe(err, addr))
	}
	due := clk.Now().Add(presenceRefresh)
	msgs, err := store.Read(ctx, stream, true, 0)
	for err == nil && len(msgs) == 0 {
		left := readBlock
		if timeout > 0 {
			if left = timeout - clk.Now().Sub(start); left <= 0 {
				break
			}
			left = min(left, readBlock)
		}
		if ctx.Err() != nil {
			break
		}
		if t := clk.Now(); !t.Before(due) { // present while it waits
			if err = claim(value, presenceTTL); err != nil {
				break
			}
			due = t.Add(presenceRefresh)
		}
		msgs, err = store.Read(ctx, stream, false, left)
	}
	if err != nil && ctx.Err() == nil {
		return fail(stderr, "recv", "%s", describe(err, addr))
	}
	if len(msgs) == 0 {
		fmt.Fprintf(stderr, "RECV NONE name=%s waited=%s\n", name, clk.Now().Sub(start).Truncate(time.Second))
		return 1
	}
	if missing := missingFields(msgs[0]); missing != "" { // not mail: acked away, and the next recv reads on
		fmt.Fprintf(stderr, "RECV BAD id=%s missing=%s\n", msgs[0].ID, missing)
		if _, err := store.Ack(ctx, stream, msgs[0].ID); err != nil {
			return fail(stderr, "recv", "%s", describe(err, addr))
		}
		return 1
	}
	// Checked out: the name stays held, naming the message, until it is
	// acked or the next recv, for as long as a relay may take (checkoutTTL),
	// so the reader's ack and its parent's reply pass the gate however slow
	// the relay; a reader that dies leaves it to lapse, and the next recv
	// hands the message out again.
	if err := claim(value+"\t"+msgs[0].ID, checkoutTTL); err != nil {
		return fail(stderr, "recv", "%s", describe(err, addr))
	}
	if _, err := io.WriteString(stdout, line(msgs[0])); err != nil {
		return fail(stderr, "recv", "writing stdout: %v", err)
	}
	return 0
}

// ack marks one received message handled.
func ack(ctx context.Context, store Store, name, addr, id string, stdout, stderr io.Writer) int {
	n, err := store.Ack(ctx, "bus:"+name, id)
	if err != nil {
		return fail(stderr, "ack", "%s", describe(err, addr))
	}
	if n == 0 {
		return fail(stderr, "ack", "ACK FAILED id=%s reason=not received by recv, or handled already; run: nova-bus pending --as %s --list", id, name)
	}
	// The ack ends the checkout: the recv's claim goes back to its 30 s.
	if present, err := store.Presence(ctx); err == nil && strings.HasSuffix(present[name], "\trecv\t"+id) {
		if _, err := store.Hold(ctx, name, strings.TrimSuffix(present[name], "\t"+id), presenceTTL); err != nil {
			return fail(stderr, "ack", "%s", describe(err, addr))
		}
	}
	fmt.Fprintf(stdout, "ACK OK id=%s\n", id)
	return 0
}

// who prints WHO OK present=<n>, then name<TAB>uptime[<TAB>session] for every
// present name, sorted by name; ? when the value is not a start time.
func who(ctx context.Context, store Store, addr string, now time.Time, stdout, stderr io.Writer) int {
	present, err := store.Presence(ctx)
	if err != nil {
		return fail(stderr, "who", "%s", describe(err, addr))
	}
	fmt.Fprintf(stdout, "WHO OK present=%d\n", len(present))
	names := make([]string, 0, len(present))
	for n := range present {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		up := "?"
		start, rest, _ := strings.Cut(present[n], "\t")
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			up = max(now.Sub(t), 0).Truncate(time.Second).String()
		}
		if token, id, ok := strings.Cut(rest, "\t"); ok && token == "recv" { // a recv's checked-out message
			up += "\trecv holding " + id
		} else if rest != "" && rest != "recv" {
			up += "\t" + rest
		}
		fmt.Fprintf(stdout, "%s\t%s\n", n, up)
	}
	return 0
}

// pending prints PENDING OK waiting=<n>, the entries no hook has acked, then
// with list each id<TAB>from<TAB>subject.
func pending(ctx context.Context, store Store, name, addr string, list bool, stdout, stderr io.Writer) int {
	var msgs []Msg
	n, err := store.Count(ctx, "bus:"+name)
	if list && err == nil {
		msgs, err = store.Waiting(ctx, "bus:"+name)
		n = int64(len(msgs))
	}
	if err != nil {
		return fail(stderr, "pending", "%s", describe(err, addr))
	}
	fmt.Fprintf(stdout, "PENDING OK waiting=%d\n", n)
	for _, m := range msgs {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", m.ID, escaper.Replace(m.Fields["from"]), escaper.Replace(m.Fields["subject"]))
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

// fail reports a refusal or a runtime failure in one line, exit 1.
func fail(stderr io.Writer, verb, format string, a ...any) int {
	fmt.Fprintf(stderr, "nova-bus %s: %s\n", verb, fmt.Sprintf(format, a...))
	return 1
}

// misuse refuses a malformed invocation in one line, exit 2.
func misuse(stderr io.Writer, verb, format string, a ...any) int {
	fmt.Fprintf(stderr, "nova-bus %s: %s; run: nova-bus %s -h\n", verb, fmt.Sprintf(format, a...), verb)
	return 2
}

// flagError rewords the flag package's parse error, naming the flag as --x.
func flagError(err error) string {
	msg, name := err.Error(), "--?"
	if i := strings.LastIndex(msg, " -"); i >= 0 {
		name = "--" + strings.TrimLeft(strings.TrimRight(strings.Fields(msg[i+1:])[0], ":"), "-")
	}
	switch {
	case strings.HasPrefix(msg, "flag provided but not defined"):
		return "unknown flag " + name
	case strings.HasPrefix(msg, "flag needs an argument"):
		return name + " needs a value"
	}
	return "bad value for " + name
}
