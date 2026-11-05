// Command nova-reserve takes a reservation on a machine's shared resources for the
// duration of a command or a hold. A machine has one capacity, N slots, stored in
// DIR/capacity; a reservation is one of them. The slots are OS file locks, so the
// kernel frees a dead holder's slot.
package main

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const banner = `nova-reserve: reservations on a machine's shared resources so people and AIs can coordinate

A machine has one capacity: N slots, kept in DIR/capacity. A reservation holds
one slot, or with --exclusive all of them, while a command runs or a hold lasts.

  nova-reserve [run] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR] -- <cmd> [args...]
  nova-reserve hold [--for D] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR]
  nova-reserve status [--dir DIR]
  nova-reserve set --slots N [--dir DIR]
  nova-reserve help [run|hold|status|set]

DIR defaults to $NOVA_RESERVE_DIR, else /tmp/nova-reserve (C:\ProgramData\nova-reserve
on Windows). Every caller on a machine uses the same DIR.

Exit: 0 done; 1 refused (no slot within --wait, capacity in use); 2 could not run
(a bad flag or value, a --slots that differs from the machine's capacity);
126 the command is not executable; 127 the command is not found; else the
command's own status (128+signal when a signal ended it).
`

var verbHelp = map[string]string{
	"run": `nova-reserve [run] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR] -- <cmd> [args...]

Wait for a free slot, hold it while the command runs, free it when the command
exits; exit with the command's status. Before the command starts, one line goes
to stderr: nova-reserve: HELD mode=shared slot=2/4 pid=1234

  --slots N     the capacity you expect (1..1024); refused (exit 2) if the machine's
                differs; the first taker on a machine sets it (CAPACITY SET slots=N);
                with no --slots the stored capacity is used, or CPU count / 4 when none
  --exclusive   take every slot: wait for every holder, keep new ones out
  --wait D      the longest wait, a Go duration (90s, 15m); default no bound; past it, exit 1
  --why TEXT    a label, shown by status and in refusals
  --dir DIR     the reservation directory

The command is found on PATH before any slot is taken (127 not found, 126 not
executable). It dies with nova-reserve on Linux and Windows; on macOS and the
BSDs a SIGKILL of nova-reserve leaves it running without its slot.
`,
	"hold": `nova-reserve hold [--for D] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR]

Take a reservation, print HELD ... to stdout, and hold it until SIGTERM or
SIGINT (or SIGHUP), or until --for has passed. Stdin is never read. On exit it prints
RELEASED reason=signal signal=SIGTERM held_for=12m3s, or RELEASED reason=for held_for=30m0s.

  --for D       release after D (a Go duration > 0); default: hold until a signal
  --why TEXT    a label; default the parent's command line, where it can be read
  --slots, --exclusive, --wait, --dir   as for run

A caller with no terminal starts it in the background and kills it when done:
  nova-reserve hold --for 2h --why "bench" & ... ; kill %1
`,
	"status": `nova-reserve status [--dir DIR]

Print the capacity, then one line per slot (free, or held with the holder's
pid, since, why and cmd), the gate and all locks, and free=<n>/<capacity>.
"all" is the exclusive lock: held only by an --exclusive run. "gate" is held by
an --exclusive run while it waits, so no new shared holder passes it.
`,
	"set": `nova-reserve set --slots N [--dir DIR]

Store this machine's capacity: N slots (1..1024). Prints CAPACITY SET slots=N.
Refused (exit 1) while any slot is held: capacity in use by pid ...; retry when free.
`,
}

// verbFlags is what each verb takes; any other flag is a refusal.
var verbFlags = map[string][]string{
	"run":    {"slots", "exclusive", "wait", "why", "dir"},
	"hold":   {"for", "slots", "exclusive", "wait", "why", "dir"},
	"status": {"dir"},
	"set":    {"slots", "dir"},
}

// wants is what a flag's value must be, for the one-line refusal.
var wants = map[string]string{
	"slots": "1..1024",
	"wait":  "a duration like 90s or 15m",
	"for":   "a duration like 30m, more than 0",
	"why":   "text",
	"dir":   "a directory",
}

// exitError is a refusal with its exit code: 1 the tool said no, 2 could not run,
// 126 and 127 the command could not be started.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func refuse(code int, format string, a ...any) *exitError {
	return &exitError{code, fmt.Sprintf(format, a...)}
}

type options struct {
	verb      string
	help      bool
	slots     int // 0: no --slots
	exclusive bool
	wait      time.Duration
	hold      time.Duration // hold --for; 0: until a signal
	why       string
	dir       string
	dirSet    bool
	argv      []string
}

// remedy is a nova-reserve command line that keeps an explicit --dir.
func (o options) remedy(args ...string) string {
	if o.dirSet {
		args = append(args, "--dir", o.dir)
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(append([]string{"nova-reserve"}, q...), " ")
}

// helpFor is a malformed invocation's remedy (CLI-STYLE (f)).
func helpFor(verb string) string {
	if verb == "run" {
		return "nova-reserve -h"
	}
	return "nova-reserve " + verb + " -h"
}

// prefix starts every refusal line: the tool, and the verb when one was named.
func prefix(verb string) string {
	if verb == "run" || verb == "" {
		return "nova-reserve:"
	}
	return "nova-reserve " + verb + ":"
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:=@%+,-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func defaultDir(env func(string) string) string { return cmp.Or(env("NOVA_RESERVE_DIR"), systemDir) }

// parse reads a command line. Every malformed one is one line and exit 2.
func parse(args []string, env func(string) string) (options, error) {
	o := options{verb: "run", dir: defaultDir(env)}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "run", "hold", "status", "set", "help":
			o.verb, args = args[0], args[1:]
		default:
			return o, refuse(2, "unknown verb %q; verbs: run, hold, status, set, help; a command goes after --: nova-reserve -- %s; run: nova-reserve help",
				args[0], shellQuote(args[0]))
		}
	}
	if o.verb == "help" {
		switch {
		case len(args) == 0:
			o.help, o.verb = true, ""
		case len(args) == 1 && verbHelp[args[0]] != "":
			o.help, o.verb = true, args[0]
		default:
			return o, refuse(2, "help takes one of run, hold, status, set, not %q; run: nova-reserve -h", strings.Join(args, " "))
		}
		return o, nil
	}
	end := slices.Index(args, "--")
	if end < 0 {
		end = len(args)
	}
	if slices.ContainsFunc(args[:end], func(a string) bool { return a == "-h" || a == "--help" }) {
		o.help = true
		return o, nil
	}
	hint := helpFor(o.verb)
	seen := map[string]bool{}
	for i := 0; i < end; i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			return o, refuse(2, "unexpected %q; a command goes after --; run: %s", a, hint)
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !slices.Contains(verbFlags[o.verb], name) {
			if _, known := wants[name]; known || name == "exclusive" {
				return o, refuse(2, "%s takes no --%s; it takes --%s; run: %s", o.verb, name,
					strings.Join(verbFlags[o.verb], ", --"), hint)
			}
			return o, refuse(2, "unknown flag %s; run: %s", a, hint)
		}
		if seen[name] {
			return o, refuse(2, "--%s given twice; run: %s", name, hint)
		}
		seen[name] = true
		if name == "exclusive" {
			if hasVal {
				return o, refuse(2, "--exclusive takes no value, not %q; run: %s", val, hint)
			}
			o.exclusive = true
			continue
		}
		if !hasVal {
			if i+1 >= end {
				return o, refuse(2, "--%s wants %s; run: %s", name, wants[name], hint)
			}
			i++
			val = args[i]
		}
		if err := o.set(name, val); err != nil {
			return o, refuse(2, "--%s wants %s, not %q; run: %s", name, wants[name], val, hint)
		}
	}
	if end < len(args) {
		o.argv = args[end+1:]
	}
	switch {
	case o.verb == "run" && len(o.argv) == 0:
		return o, refuse(2, "no command: nova-reserve [flags] -- <cmd> [args...]; run: %s", hint)
	case o.verb != "run" && end < len(args):
		return o, refuse(2, "%s takes no command, not %q; run: %s", o.verb, strings.Join(o.argv, " "), hint)
	case o.exclusive && o.slots > 0:
		return o, refuse(2, "--exclusive takes every slot; --slots does not apply; run: %s", hint)
	case o.verb == "set" && o.slots == 0:
		return o, refuse(2, "set wants --slots N (1..1024); run: %s", hint)
	}
	return o, nil
}

func (o *options) set(name, val string) error {
	bad := errors.New("bad value")
	switch name {
	case "slots":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > 1024 {
			return bad
		}
		o.slots = n
	case "wait", "for":
		d, err := time.ParseDuration(val)
		if err != nil || d < 0 || (name == "for" && d == 0) {
			return bad
		}
		if name == "wait" {
			o.wait = d
		} else {
			o.hold = d
		}
	case "why":
		if val == "" {
			return bad
		}
		o.why = val
	case "dir":
		if val == "" {
			return bad
		}
		o.dir, o.dirSet = val, true
	}
	return nil
}

// tryLock takes the OS lock on path, shared or exclusive, without waiting.
// ok is false when another holder has it; release lets go of a held lock.
type tryLock func(path string, exclusive bool) (release func(), ok bool, err error)

// world is everything run touches outside itself, so the unit tests fake it.
type world struct {
	env            func(string) string
	lock           tryLock
	now            func() time.Time
	sleep          func(d time.Duration)
	resolve        func(name string) (string, error) // exec.LookPath
	exec           func(path string, argv []string) int
	hold           func(d time.Duration) (signal string) // blocks; "" when d passed
	parent         func() string                         // the parent's command line, or ""
	pid            int
	cpus           int
	stdout, stderr io.Writer
}

// poll is how often a waiting taker tries again; a --wait shorter than the
// remaining poll cuts the last sleep short.
const poll = time.Second

func main() {
	os.Exit(run(os.Args[1:], world{os.Getenv, osLock, time.Now, time.Sleep,
		exec.LookPath, runCommand, waitHold, parentCommand, os.Getpid(), runtime.NumCPU(), os.Stdout, os.Stderr}))
}

func run(args []string, w world) int {
	o, err := parse(args, w.env)
	if err == nil && o.help {
		fmt.Fprint(w.stdout, cmp.Or(verbHelp[o.verb], banner))
		return 0
	}
	if err == nil {
		err = do(o, w)
	}
	var e *exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &e):
		if e.msg != "" {
			fmt.Fprintln(w.stderr, prefix(o.verb), e.msg)
		}
		return e.code
	default:
		fmt.Fprintln(w.stderr, prefix(o.verb), err)
		return 2
	}
}

// do carries out a parsed command line; an exitError from it is a refusal,
// any other error could not run (exit 2). run's exit is the command's.
func do(o options, w world) error {
	path := ""
	if o.verb == "run" {
		// the command is found before any slot is taken, so a typo never holds one
		p, err := w.resolve(o.argv[0])
		switch {
		case errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EISDIR):
			return refuse(126, "%s: not executable", o.argv[0])
		case err != nil:
			return refuse(127, "%s: command not found", o.argv[0])
		}
		path = p
	}
	if o.verb == "status" {
		// status reads; a directory no one ever reserved in is a missing input
		if fi, err := os.Stat(o.dir); err != nil || !fi.IsDir() {
			return refuse(2, "--dir %s is not a reservation directory (no reservation was taken there); run: %s",
				shellQuote(o.dir), helpFor("status"))
		}
		return status(o, w)
	}
	// the one directory is the create target; its parent must exist
	if err := os.Mkdir(o.dir, 0o777); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	// ignored: only the directory's owner may widen it; others share it as it is
	_ = os.Chmod(o.dir, 0o777|os.ModeSticky)
	if o.verb == "set" {
		return setCapacity(o, w)
	}
	t, err := acquire(w, o)
	if err != nil {
		return err
	}
	defer t.release()
	out := w.stderr
	prefix := "nova-reserve: "
	if o.verb == "hold" {
		out, prefix = w.stdout, ""
		if o.why == "" {
			o.why = w.parent()
		}
	}
	if t.capSet > 0 {
		fmt.Fprintf(out, "%sCAPACITY SET slots=%d\n", prefix, t.capSet)
	}
	since := w.now()
	f := []string{"pid=" + strconv.Itoa(w.pid), "since=" + since.UTC().Format(time.RFC3339)}
	if o.why != "" {
		f = append(f, "why="+esc(o.why))
	}
	if o.verb == "run" {
		f = append(f, "cmd="+esc(strings.Join(o.argv, " ")))
	}
	holder := filepath.Join(o.dir, t.name()+".holder")
	// ignored: the holder file is for status to read; the lock is the slot either way
	_ = os.Remove(holder)
	_ = os.WriteFile(holder, []byte(strings.Join(f, " ")+"\n"), 0o666)
	_ = os.Chmod(holder, 0o666)
	// ignored: a leftover holder file names a pid whose lock is gone; status probes the lock
	defer os.Remove(holder)
	fmt.Fprintf(out, "%sHELD %s\n", prefix, t.describe(w.pid))
	if o.verb == "run" {
		code := w.exec(path, o.argv)
		if code != 0 {
			return &exitError{code: code, msg: ""}
		}
		return nil
	}
	sig := w.hold(o.hold)
	reason := "reason=for"
	if sig != "" {
		reason = "reason=signal signal=" + sig
	}
	fmt.Fprintf(out, "RELEASED %s held_for=%s\n", reason, w.now().Sub(since).Round(time.Millisecond))
	return nil
}

// --- capacity --------------------------------------------------------------

// capacity is DIR/capacity: the slot count on the first line, then who set it.
type capacity struct {
	n   int
	who string // "pid 123 at 2026-11-05T22:03:32Z"
}

func readCapacity(dir string) (capacity, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, "capacity"))
	if errors.Is(err, fs.ErrNotExist) {
		return capacity{}, false, nil
	}
	if err != nil {
		return capacity{}, false, err
	}
	first, rest, _ := strings.Cut(string(b), "\n")
	n, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || n < 1 || n > 1024 {
		return capacity{}, false, refuse(2, "%s holds %q, not a capacity of 1..1024; run: nova-reserve set --slots N",
			filepath.Join(dir, "capacity"), strings.TrimSpace(first))
	}
	return capacity{n, strings.TrimSpace(rest)}, true, nil
}

// writeCapacity writes in place, under the gate and all held exclusive (no
// reader is past the gate); the file is world-writable because a sticky
// directory lets no other user replace it.
func writeCapacity(dir string, n, pid int, at time.Time) error {
	path := filepath.Join(dir, "capacity")
	err := os.WriteFile(path, fmt.Appendf(nil, "%d\npid %d at %s\n", n, pid, at.UTC().Format(time.RFC3339)), 0o666)
	// ignored: only the file's owner may widen it, and only a new file needs it
	_ = os.Chmod(path, 0o666)
	return err
}

// locksFree takes gate and all exclusive without waiting: no shared taker is
// past the gate and no holder is left. It returns nil when either is taken.
func locksFree(w world, dir string) (func(), error) {
	gate, ok, err := w.lock(filepath.Join(dir, "gate"), true)
	if !ok || err != nil {
		return nil, err
	}
	all, ok, err := w.lock(filepath.Join(dir, "all"), true)
	if !ok || err != nil {
		gate()
		return nil, err
	}
	return func() { all(); gate() }, nil
}

func setCapacity(o options, w world) error {
	c, have, err := readCapacity(o.dir)
	var e *exitError
	if err != nil && !errors.As(err, &e) {
		return err
	}
	if have && c.n == o.slots {
		fmt.Fprintf(w.stdout, "CAPACITY SET slots=%d note=unchanged\n", o.slots)
		return nil
	}
	free, err := locksFree(w, o.dir)
	if err != nil {
		return err
	}
	if free == nil {
		in := holders(w, o.dir, max(c.n, o.slots))
		if len(in) == 0 {
			in = []string{"a taker mid-take"}
		}
		return refuse(1, "capacity in use: %s; retry when free", strings.Join(in, ", "))
	}
	defer free()
	if err := writeCapacity(o.dir, o.slots, w.pid, w.now()); err != nil {
		return err
	}
	fmt.Fprintf(w.stdout, "CAPACITY SET slots=%d\n", o.slots)
	return nil
}

// --- taking ----------------------------------------------------------------

// taker takes the locks of one hold (tla/SlotFiles.tla, tla/Capacity.tla): a
// shared taker passes the gate (held shared only while it reads the capacity
// and takes its own locks), one free slot<k> of the stored capacity, and "all"
// shared; an exclusive taker holds the gate exclusive, so no new shared taker
// passes, then "all" exclusive, which waits for every shared holder.
type taker struct {
	w      world
	o      options
	held   []func() // what release lets go of; an exclusive waiter's gate too
	slot   int      // 0 until a shared hold; an exclusive hold names no slot
	cap    int      // the capacity the shared hold was taken under
	capSet int      // the capacity this taker stored, if it was the first
}

func (t *taker) path(name string) string { return filepath.Join(t.o.dir, name) }

// step makes one attempt without waiting; it reports true once held.
func (t *taker) step() (bool, error) {
	if t.o.exclusive {
		if len(t.held) == 0 {
			gate, ok, err := t.w.lock(t.path("gate"), true)
			if !ok || err != nil {
				return false, err
			}
			t.held = []func(){gate}
		}
		all, ok, err := t.w.lock(t.path("all"), true)
		if ok {
			t.held = append(t.held, all)
		}
		return ok, err
	}
	for range 2 { // a first taker stores the capacity, then takes under it
		gate, ok, err := t.w.lock(t.path("gate"), false)
		if !ok || err != nil {
			return false, err
		}
		c, have, err := readCapacity(t.o.dir)
		if err != nil || have {
			defer gate()
			if err != nil {
				return false, err
			}
			return t.scan(c)
		}
		gate()
		if ok, err := t.storeCapacity(); !ok || err != nil {
			return false, err
		}
	}
	return false, nil
}

// storeCapacity is the first taker's write of --slots, or of CPU count / 4:
// under gate and all exclusive, and only if no capacity is there by then
// (tla/Capacity.tla ClaimAll; "racyfirst" is the witness without the locks).
func (t *taker) storeCapacity() (bool, error) {
	free, err := locksFree(t.w, t.o.dir)
	if free == nil || err != nil {
		return false, err
	}
	defer free()
	if _, have, err := readCapacity(t.o.dir); have || err != nil {
		return err == nil, err
	}
	n := cmp.Or(t.o.slots, max(1, t.w.cpus/4))
	if err := writeCapacity(t.o.dir, n, t.w.pid, t.w.now()); err != nil {
		return false, err
	}
	t.capSet = n
	return true, nil
}

// scan takes slot1..slot<c.n> in order, the first free one, then all shared.
// A --slots that differs from the stored capacity is refused, never counted
// on its own (tla/Capacity.tla, "percaller" is the witness of that bug).
func (t *taker) scan(c capacity) (bool, error) {
	if t.o.slots > 0 && t.o.slots != c.n {
		return false, refuse(2, "this machine's capacity is %d (set by %s); you asked %d; run: %s",
			c.n, cmp.Or(c.who, "an unknown taker"), t.o.slots, t.o.remedy("set", "--slots", strconv.Itoa(t.o.slots)))
	}
	for k := 1; k <= c.n; k++ {
		slot, ok, err := t.w.lock(t.path("slot"+strconv.Itoa(k)), true)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		all, ok, err := t.w.lock(t.path("all"), false)
		if !ok || err != nil {
			slot()
			return false, err
		}
		t.held, t.slot, t.cap = []func(){slot, all}, k, c.n
		return true, nil
	}
	return false, nil
}

func (t *taker) release() {
	for _, rel := range slices.Backward(t.held) {
		rel()
	}
	t.held = nil
}

// acquire polls step until it holds, or until o.wait (0: no bound) has passed.
func acquire(w world, o options) (*taker, error) {
	t := &taker{w: w, o: o}
	deadline := w.now().Add(o.wait)
	for {
		ok, err := t.step()
		if ok {
			return t, nil
		}
		if err == nil && o.wait > 0 && !w.now().Before(deadline) {
			t.release()
			c, _, _ := readCapacity(o.dir)
			in := holders(w, o.dir, c.n)
			if len(in) == 0 {
				in = []string{"every slot busy"}
			}
			what := "slot"
			if o.exclusive {
				what = "exclusive hold"
			}
			return nil, refuse(1, "no %s within %s: %s; run: %s", what, o.wait, strings.Join(in, ", "), o.remedy("status"))
		}
		if err != nil {
			t.release()
			return nil, err
		}
		d := poll
		if o.wait > 0 {
			d = min(d, deadline.Sub(w.now()))
		}
		w.sleep(d)
	}
}

// name is the hold's lock, whose <name>.holder file status reads: slot<k>, or all.
func (t *taker) name() string {
	if t.o.exclusive {
		return "all"
	}
	return "slot" + strconv.Itoa(t.slot)
}

func (t *taker) describe(pid int) string {
	if t.o.exclusive {
		return fmt.Sprintf("mode=exclusive slot=all pid=%d", pid)
	}
	return fmt.Sprintf("mode=shared slot=%d/%d pid=%d", t.slot, t.cap, pid)
}

// --- who holds what --------------------------------------------------------

// probe says whether a lock is held, by trying it (exclusive for a slot,
// shared for all and gate, which only an exclusive run holds).
func probe(w world, path string, exclusive bool) (bool, error) {
	rel, ok, err := w.lock(path, exclusive)
	if ok {
		rel()
	}
	return !ok && err == nil, err
}

// holderFields reads <name>.holder: pid=.. since=.. why=.. cmd=.., escaped.
func holderFields(dir, name string) map[string]string {
	b, _ := os.ReadFile(filepath.Join(dir, name+".holder"))
	m := map[string]string{}
	for _, f := range strings.Fields(string(b)) {
		if k, v, ok := strings.Cut(f, "="); ok {
			m[k] = unesc(v)
		}
	}
	return m
}

// holders names, in prose, each held slot1..slot<n> and an exclusive run.
func holders(w world, dir string, n int) []string {
	var in []string
	say := func(name string) {
		m := holderFields(dir, name)
		s := name + " held"
		if m["pid"] != "" {
			s += " by pid " + m["pid"]
		}
		if m["since"] != "" {
			s += " since " + m["since"]
		}
		if m["why"] != "" {
			s += " (why: " + m["why"] + ")"
		}
		in = append(in, s)
	}
	for k := 1; k <= n; k++ {
		if held, _ := probe(w, filepath.Join(dir, "slot"+strconv.Itoa(k)), true); held {
			say("slot" + strconv.Itoa(k))
		}
	}
	if held, _ := probe(w, filepath.Join(dir, "all"), false); held {
		say("all")
	} else if held, _ := probe(w, filepath.Join(dir, "gate"), false); held {
		in = append(in, "gate held by an --exclusive run waiting for every holder")
	}
	return in
}

// status probes each lock in dir: a lock it can take is free.
func status(o options, w world) error {
	c, have, err := readCapacity(o.dir)
	if err != nil {
		return err
	}
	out := bufio.NewWriter(w.stdout)
	if have {
		fmt.Fprintf(out, "capacity=%d set_by=%s\n", c.n, esc(c.who))
	} else {
		fmt.Fprintf(out, "capacity=unset # the first run stores its --slots (or CPU count / 4); or: %s\n",
			o.remedy("set", "--slots", "N"))
	}
	nums := map[int]bool{}
	for k := 1; k <= c.n; k++ {
		nums[k] = true
	}
	names, _ := filepath.Glob(filepath.Join(o.dir, "slot*"))
	for _, p := range names {
		if k, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(p), "slot")); err == nil && k > 0 {
			nums[k] = true
		}
	}
	free := 0
	line := func(name string, exclusive bool, note string) {
		held, err := probe(w, filepath.Join(o.dir, name), exclusive)
		switch {
		case err != nil:
			fmt.Fprintf(out, "%s unknown error=%s", name, esc(err.Error()))
		case held:
			b, _ := os.ReadFile(filepath.Join(o.dir, name+".holder"))
			fmt.Fprintf(out, "%s held %s", name, strings.TrimSpace(string(b)))
		default:
			fmt.Fprintf(out, "%s free", name)
			if strings.HasPrefix(name, "slot") && note == "" {
				free++
			}
		}
		if note != "" {
			fmt.Fprintf(out, " # %s", note)
		}
		fmt.Fprintln(out)
	}
	for _, k := range slices.Sorted(maps.Keys(nums)) {
		note := ""
		if k > c.n {
			note = "beyond capacity, from an older one; no new holder takes it"
		}
		line("slot"+strconv.Itoa(k), true, note)
	}
	line("gate", false, "held by an --exclusive run while it waits, so no new holder passes")
	line("all", false, "the exclusive lock; held only by an --exclusive run")
	if have {
		fmt.Fprintf(out, "free=%d/%d\n", free, c.n)
	} else {
		fmt.Fprintln(out, "free=unset")
	}
	return out.Flush()
}

// --- the outside -------------------------------------------------------------

// esc makes text one whitespace-free field: backslash, space, tab, newline
// and other control bytes become \\, \s, \t, \n and \xHH.
func esc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			b.WriteString(`\\`)
		case c == ' ':
			b.WriteString(`\s`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func unesc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 's':
			b.WriteByte(' ')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'x':
			if v, err := strconv.ParseUint(s[i+1:min(i+3, len(s))], 16, 8); err == nil && i+3 <= len(s) {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
			b.WriteString(`\x`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// joinArgs turns NUL-separated arguments (/proc/<pid>/cmdline) into one line.
func joinArgs(b []byte) string {
	return strings.Join(strings.FieldsFunc(string(b), func(r rune) bool { return r == 0 }), " ")
}

// procArgs reads macOS's kern.procargs2: argc (4 bytes, little-endian), the
// executable path, NUL padding, then argc NUL-terminated arguments.
func procArgs(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	argc := int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24
	rest := b[4:]
	i := slices.Index(rest, 0)
	if i < 0 {
		return ""
	}
	for rest = rest[i:]; len(rest) > 0 && rest[0] == 0; rest = rest[1:] {
	}
	var args []string
	for len(args) < argc && len(rest) > 0 {
		a, after, _ := strings.Cut(string(rest), "\x00")
		args, rest = append(args, a), []byte(after)
	}
	return strings.Join(args, " ")
}

var signalNames = map[os.Signal]string{os.Interrupt: "SIGINT", syscall.SIGTERM: "SIGTERM", syscall.SIGHUP: "SIGHUP"}

// waitHold blocks until SIGINT, SIGTERM or SIGHUP, or until d (0: no bound)
// has passed; it names the signal, or returns "" for d.
func waitHold(d time.Duration) string {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	var timeout <-chan time.Time
	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case s := <-sigs:
		return signalNames[s]
	case <-timeout:
		return ""
	}
}

// runCommand runs the resolved path with argv and this process's stdio, tied
// to this process so it cannot outlive the slot (tie, per OS), forwards
// interrupts to it, and returns its exit status (128+signal when a signal ended it).
func runCommand(path string, argv []string) int {
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	untie, err := tie(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nova-reserve:", err)
		return 127
	}
	defer untie()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sigs {
			forward(cmd, s)
		}
	}()
	err = cmd.Wait()
	signal.Stop(sigs)
	if cmd.ProcessState == nil {
		fmt.Fprintln(os.Stderr, "nova-reserve:", err)
		return 127
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return cmd.ProcessState.ExitCode()
}
