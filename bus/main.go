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
	"os/signal"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

// verbUsage is each verb's usage block; the banner is all of them in order.
var verbUsage = map[string]string{
	"send": `  nova-bus send --as <me> --to <name>[,<name>] [--re <id>] <subject> <body | ->
      Append one message to each recipient's inbox; prints "<name> <id>" for each.
      Recipients are sent to once each. A body of - is read from stdin, less one
      trailing newline. --re takes an id as hook prints it. A failed send prints
      SEND FAILED and exits 1; it is never retried.
`,
	"hook": `  nova-bus hook --as <name> [--harness <harness> <its flags>]
      Deliver every waiting message for <name>, then each new one as it arrives,
      into the session that runs the hook, waking it when it is idle.
      How your session gets woken, per --harness:
        claude (the default): each message is one line on stdout,
          id<TAB>from<TAB>re<TAB>subject<TAB>body (re is - when none; \n, \r,
          \t and \\ escaped). Run the hook under the session's Monitor tool,
          never with & or into a file: the Monitor turns each line into a
          notification, and that is the wake. Refused (exit 2) when stdout is
          /dev/null.
        codex --codex-socket <path> --codex-thread <id> --codex-events-dir
          <dir>: each message is saved to <dir>/<id>.json and a turn naming
          that file is started in the thread through the Codex app's socket
          (~/.codex/ipc/ipc.sock; the README says how to find the thread).
        grok --grok-home <dir>: each message line is appended to
          <dir>/nova-hook.wake; the session's Monitor runs
          tail -n 0 -F <dir>/nova-hook.wake.
        antigravity --antigravity-dir <workspace> --antigravity-session <id>
          --antigravity-events-dir <dir> [--antigravity-server <port>]: each
          message is saved to <dir>/<id>.json and sent into the conversation
          with the workspace's language_server agentapi send-message.
      Every harness but claude prints one receipt per message on stdout:
      HOOK WAKE harness=<h> id=<id> ok, or HOOK WAKE ... FAILED: <reason>. A
      failed wake is not retried and the message is not marked handled: the
      next hook (or reconnect) delivers it. The harness is checked at start,
      before the name is claimed: not there, exit 2; not answering, exit 1.
      A message is marked handled only after it is delivered, so a restarted
      hook resumes where it stopped; one delivered just before a crash may be
      delivered again. While it runs, <name> is present (see who). Runs until
      killed; exits 0 on SIGINT/SIGTERM.
      One hook per name: a hook that finds the name held waits up to 40 s for
      it to lapse (a restart after a crash), then claims it or, if it is still
      held, exits 1 "name already hooked". On stderr: HOOK HARNESS OK once the
      harness answered, HOOK WAIT while waiting, HOOK OK name=<name>
      waiting=<n> once claimed.
`,
	"who": `  nova-bus who
      Print WHO OK present=<n>, then each name whose hook is running and how
      long it has been up, one per line, name<TAB>uptime, sorted by name. A
      crashed hook drops out within 30 seconds.
`,
	"pending": `  nova-bus pending --as <name> [--list]
      Print PENDING OK waiting=<n>, the messages for <name> no hook has handled;
      with --list, then id<TAB>from<TAB>subject for each, oldest first. Nothing
      is consumed.
`,
	"help": `  nova-bus help [<verb>] | <verb> -h
      Print this, or one verb's usage.
`,
}

var verbOrder = []string{"send", "hook", "who", "pending", "help"}

const usageFooter = `
A name is letters, digits, '.', '_' and '-'. Exit 0 ok, 1 refused or failed,
2 usage error. Environment: NOVA_BUS_REDIS=host:port (required),
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
var verbs = map[string][]string{"send": {"as", "to", "re"}, "hook": append([]string{"as", "harness"}, slices.Sorted(maps.Keys(flagWants))...),
	"who": {}, "pending": {"as", "list"}}

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
	// Read returns this consumer's delivered-but-unacked entries with an id
	// past after ("0" for all), or, when after is ">", waits up to block for
	// new ones (none on a quiet timeout).
	Read(ctx context.Context, stream, after string, block time.Duration) ([]Msg, error)
	Ack(ctx context.Context, stream, id string) error
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

var (
	errHooked = errors.New("name already hooked")
	errTaken  = errors.New("name taken by another hook")
)

// main stops a hook cleanly on SIGINT or SIGTERM, releasing its presence, and
// ignores SIGPIPE so a closed stdout is a write error (EPIPE) and the same
// clean exit. A kill at any other point is safe too: a message is acked only
// after its line is written (tla/Bus.tla), and presence lapses by its TTL
// (tla/Presence.tla).
func main() {
	signal.Ignore(syscall.SIGPIPE)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	s := sys{uid: os.Getuid(), devOf: realDevOf, connect: connectCodex, exec: realExec}
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
	harness := fs.String("harness", "claude", "")
	hflags := map[string]*string{}
	for f := range flagWants {
		hflags[f] = fs.String(f, "", "")
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
	if verb != "who" && strings.TrimSpace(*as) == "" {
		missing = append(missing, "--as <name>")
	}
	if verb == "send" && *to == "" {
		missing = append(missing, "--to <name>")
	}
	if verb == "send" && fs.NArg() < 2 {
		missing = append(missing, []string{"<subject> <body>", "<body>"}[fs.NArg()])
	}
	var given, ignored []string // the harness flags set, and those this harness does not take
	fs.Visit(func(f *flag.Flag) {
		if _, ok := flagWants[f.Name]; ok {
			given = append(given, f.Name)
		}
	})
	_, knownHarness := harnessFlags[*harness]
	if knownHarness {
		var need []string
		ignored, need = harnessCheck(*harness, given)
		missing = append(missing, need...)
	}
	if env("NOVA_BUS_REDIS") == "" {
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
	if verb != "who" && !validName(*as) {
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
	case *re != "" && !idShape(*re):
		return misuse(stderr, verb, "--re %q is not a message id like 1730800000000-0 (as hook prints it)", *re)
	case verb != "send" && fs.NArg() != 0:
		return misuse(stderr, verb, "%s takes no arguments", verb)
	case verb == "send" && fs.NArg() > 2:
		return misuse(stderr, verb, "send takes one subject and one body, not %d arguments", fs.NArg())
	case verb == "send" && strings.TrimSpace(fs.Arg(0)) == "":
		return misuse(stderr, verb, "empty subject")
	case verb == "hook" && *harness == "claude" && stdoutIsNull(s):
		return misuse(stderr, verb, "stdout is /dev/null, nothing would read the messages")
	}
	var w waker
	if verb == "hook" {
		f := map[string]string{}
		for k, v := range hflags {
			f[k] = *v
		}
		var err error
		if w, err = newWaker(ctx, *harness, *as, f, s, stderr); err != nil {
			var se startError
			if errors.As(err, &se) && se.missing {
				return misuse(stderr, verb, "%v", err)
			}
			return fail(stderr, verb, "%v; start the harness, then run the same hook again", err)
		}
	}
	addr := env("NOVA_BUS_REDIS")
	store := dial(addr, env("NOVA_BUS_USER"), env("NOVA_BUS_PASSWORD"))
	switch verb {
	case "hook":
		return runHook(ctx, store, *as, addr, clk, stdout, stderr, w)
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
	for _, c := range [][2]string{{"connection refused", "connection refused"}, {"no such host", "no such host"}, {"timeout", "timed out"},
		{"connection reset", "connection lost"}, {"broken pipe", "connection lost"}, {io.EOF.Error(), "connection lost"}} {
		if strings.Contains(err.Error(), c[0]) {
			return fmt.Sprintf("cannot reach redis at %s (NOVA_BUS_REDIS): %s", addr, c[1])
		}
	}
	return err.Error()
}

// runHook runs hook, reconnecting after an error, until ctx ends (exit 0) or
// stdout fails (exit 1), releasing presence either way; or until the name is
// found hooked by another process (exit 1, its presence left alone)
// (tla/Presence.tla Exit, Claim: OnlyOwnerDeletes, GoneAfterExit).
// A reconnect is logged once, then at most once a minute.
func runHook(ctx context.Context, store Store, name, addr string, clk Clock, stdout, stderr io.Writer, w waker) int {
	value := clk.Now().UTC().Format(time.RFC3339) + "\t" + rand.Text()
	var logged time.Time
	announced := false
	onClaim := func(waiting int64) { // HOOK OK once, not on each reconnect
		if !announced {
			fmt.Fprintf(stderr, "HOOK OK name=%s waiting=%d\n", name, waiting)
			announced = true
		}
	}
	for {
		err := hook(ctx, store, name, value, clk, stdout, stderr, onClaim, w)
		var we writeError
		if errors.Is(err, errHooked) {
			return fail(stderr, "hook", "name already hooked: %s after %s; run: nova-bus who", name, presenceTTL+presenceRefresh)
		}
		if errors.Is(err, errTaken) {
			return fail(stderr, "hook", "name taken by another hook while this one lapsed: %s; run: nova-bus who", name)
		}
		if ctx.Err() == nil && !errors.As(err, &we) {
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
		return fail(stderr, "hook", "writing stdout: %v", err)
	}
}

// hook claims name's presence, waiting for another hook's key to lapse
// (tla/Presence.tla Start, Poll, GiveUp: WaitClaims, WaitEnds), then delivers
// and acks the pending entries, then the new ones, until an error. Each call
// starts from the pending list, so a batch handed by a read whose reply was
// lost is delivered after the reconnect. With no waker an entry is delivered
// by printing its line; with one, by its wake and a receipt line. A failed
// wake is a receipt and no ack: the pending list is read past it (after), so
// the entry waits for the next call (tla/Bus.tla WakeFails, NoLoss).
// It claims again whenever presenceRefresh has passed, and a read waits no
// longer than that is due (tla/Presence.tla Start, Refresh: StartOnlyWhenFree,
// LiveIsPresent). An ack is not cut short by a signal: a printed line is acked.
func hook(ctx context.Context, store Store, name, value string, clk Clock, out, stderr io.Writer, onClaim func(waiting int64), w waker) error {
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
	}
	if err != nil {
		return err
	}
	waiting, err := store.Count(ctx, stream)
	if err != nil {
		return err
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
	after, trimmed := "0", false // after: the pending entries past this id, or > for new ones
	for {
		if err := refresh(); err != nil {
			return err
		}
		msgs, err := store.Read(ctx, stream, after, min(max(due.Sub(clk.Now()), time.Millisecond), readBlock))
		if err != nil {
			return err
		}
		pending := after != ">"
		if pending && len(msgs) == 0 {
			after = ">"
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
			if pending {
				after = m.ID
			}
			receipt, werr := line(m), error(nil)
			if w != nil {
				receipt = "HOOK WAKE harness=" + harnessName(w) + " id=" + m.ID + " ok\n"
				if werr = w.wake(ctx, m); werr != nil {
					receipt = strings.Replace(receipt, " ok\n", " FAILED: "+escaper.Replace(werr.Error())+"\n", 1)
				}
			}
			if _, err := io.WriteString(out, receipt); err != nil {
				return writeError{err}
			}
			if werr != nil {
				continue
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

// who prints WHO OK present=<n>, then name<TAB>uptime for every present name,
// sorted by name; ? when the value is not a start time.
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
		start, _, _ := strings.Cut(present[n], "\t")
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			up = max(now.Sub(t), 0).Truncate(time.Second).String()
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
