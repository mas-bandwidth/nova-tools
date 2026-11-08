// Command nova-reserve takes a reservation on a machine's shared resources for the
// duration of a command. A machine has one capacity, N slots, stored in
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

A machine has one capacity, N slots, kept in DIR/capacity. A reservation holds one
slot, or with --exclusive every slot, while a command runs.

  nova-reserve [run] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR] -- <cmd> [args...]
  nova-reserve status [--dir DIR]
  nova-reserve set --slots N [--dir DIR]
  nova-reserve help [run|status|set]

DIR: --dir, else $NOVA_RESERVE_DIR, else /var/tmp/nova-reserve; one per machine, on a
local disk. Exit: 0 done; 1 refused (no slot within --wait, capacity in use); 2 could not
run; 126 the command cannot be executed; 127 the command is not found; else the command's
status. cmd/nova-reserve/README.md has the rest.
`

var verbHelp = map[string]string{
	"run": `nova-reserve [run] [--slots N | --exclusive] [--wait D] [--why TEXT] [--dir DIR] -- <cmd> [args...]

Wait for a slot, run the command, free the slot; exit with the command's status.
  --slots N    the capacity you expect, 1..1024; refused (exit 2) if the machine's differs.
               The first taker stores it (CAPACITY SET); with none, the stored one, else CPU/4
  --exclusive  take every slot: wait for every holder and keep new ones out
  --wait D     give up after D (above 0: 90s, 15m), exit 1; default no bound
  --why TEXT   a label for status and refusals
The command is found before a slot is taken (127, 126). It dies with nova-reserve, even on SIGKILL (a watchdog
process kills its group within about a second).
`,
	"status": `nova-reserve status [--dir DIR]

The capacity; each slot, free or held (pid= since= why= cmd=); gate and all; free=n/N.
An --exclusive run holds gate from when it starts waiting until it ends, and all while
it runs, when free=0. A holder is read from <lock>.holder, if its pid is alive.
`,
	"set": `nova-reserve set --slots N [--dir DIR]

Store this machine's capacity, N slots (1..1024); prints CAPACITY SET slots=N.
Refused (exit 1) while any slot is held.
`,
}

// verbFlags is what each verb takes; any other flag is a refusal.
var verbFlags = map[string][]string{
	"run":    {"slots", "exclusive", "wait", "why", "dir"},
	"status": {"dir"},
	"set":    {"slots", "dir"},
}

// wants is what a flag's value must be, for the one-line refusal.
var wants = map[string]string{
	"slots": "1..1024",
	"wait":  "a duration above 0, like 90s or 15m",
	"why":   "text",
	"dir":   "a directory",
}

// exitError is a refusal with its exit code: 1 the tool said no, 2 could not run,
// 126 and 127 the command could not be started; an empty msg passes a code on.
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
	wait      time.Duration // 0: no bound
	why       string
	dir       string
	dirSet    bool
	argv      []string
	args      []string // the command line as given, for a remedy that reruns it
	dirFrom   string   // where dir came from, to name it in a refusal
}

// remedy is a nova-reserve command line that keeps an explicit --dir.
func (o options) remedy(args ...string) string {
	if o.dirSet {
		args = append(args, "--dir", o.dir)
	}
	return "nova-reserve " + quoteAll(args)
}

// helpFor is a malformed invocation's remedy (one refusal line plus at most one hint line).
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

func quoteAll(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
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
	o := options{verb: "run", dir: defaultDir(env), args: args, dirFrom: "the default directory"}
	if env("NOVA_RESERVE_DIR") != "" {
		o.dirFrom = "NOVA_RESERVE_DIR"
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		o.help, o.verb = true, ""
		return o, nil
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "run", "status", "set", "help":
			o.verb, args = args[0], args[1:]
		default:
			return o, refuse(2, "unknown verb %q; verbs: run, status, set, help; a command goes after --: nova-reserve -- %s; run: nova-reserve help",
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
			return o, refuse(2, "help takes one of run, status, set, not %q; run: nova-reserve -h", strings.Join(args, " "))
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
		if !o.set(name, val) {
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

// set stores one flag's value; false when the value is not what it wants.
func (o *options) set(name, val string) bool {
	switch name {
	case "slots":
		n, err := strconv.Atoi(val)
		o.slots = n
		return err == nil && n >= 1 && n <= 1024
	case "wait":
		d, err := time.ParseDuration(val)
		o.wait = d
		return err == nil && d > 0
	case "why":
		o.why = val
	case "dir":
		o.dir, o.dirSet, o.dirFrom = val, true, "--dir"
	}
	return val != ""
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
	running        func(pid int) bool
	pid            int
	cpus           int
	stdout, stderr io.Writer
}

// poll is how often a waiting taker tries again; a --wait shorter than the
// remaining poll cuts the last sleep short.
const poll = time.Second

func main() {
	os.Exit(run(os.Args[1:], world{os.Getenv, osLock, time.Now, time.Sleep,
		exec.LookPath, runCommand, running, os.Getpid(), runtime.NumCPU(), os.Stdout, os.Stderr}))
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
			if strings.ContainsAny(o.argv[0], `/\`) {
				return refuse(127, "%s: no such file", o.argv[0])
			}
			return refuse(127, "%s: command not found on PATH=%s", o.argv[0], w.env("PATH"))
		}
		path = p
	}
	if o.verb == "status" {
		// status reads; a directory no one ever reserved in is a missing input
		if fi, err := os.Stat(o.dir); err != nil || !fi.IsDir() {
			return refuse(2, "%s %s is not a reservation directory (no reservation was taken there); run: %s",
				o.dirFrom, shellQuote(o.dir), helpFor("status"))
		}
		return status(o, w)
	}
	// the one directory is the create target; its parent must exist
	if err := os.Mkdir(o.dir, 0o777); err != nil && !errors.Is(err, fs.ErrExist) {
		parent := filepath.Dir(o.dir)
		if _, perr := os.Stat(parent); errors.Is(perr, fs.ErrNotExist) {
			return refuse(2, "%s %s: parent %s does not exist; run: mkdir -p %s && %s", o.dirFrom, o.dir, parent,
				shellQuote(parent), "nova-reserve "+quoteAll(o.args))
		}
		if errors.Is(err, fs.ErrPermission) {
			return refuse(2, "%s %s: parent %s is not writable by this user; run: ls -ld %s", o.dirFrom, o.dir, parent, shellQuote(parent))
		}
		return refuse(2, "%s %s: %v; run: ls -ld %s", o.dirFrom, o.dir, errors.Unwrap(err), shellQuote(parent))
	}
	if fi, err := os.Stat(o.dir); err == nil && !fi.IsDir() {
		return refuse(2, "%s %s is a file, not a directory; run: ls -ld %s", o.dirFrom, o.dir, shellQuote(o.dir))
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
	if err := t.mark(t.name()); err != nil {
		return err
	}
	if t.capSet > 0 {
		fmt.Fprintf(w.stderr, "nova-reserve: CAPACITY SET slots=%d\n", t.capSet)
	}
	fmt.Fprintf(w.stderr, "nova-reserve: HELD %s\n", t.describe())
	if code := w.exec(path, o.argv); code != 0 {
		return &exitError{code: code}
	}
	return nil
}

// --- capacity --------------------------------------------------------------

// capacity is DIR/capacity: the slot count on the first line, then who set it
// ("pid 123 at 2026-11-05T22:03:32Z"), read as pid and at.
type capacity struct {
	n       int
	pid, at string
}

func (c capacity) who() string {
	if c.pid == "" {
		return "an unknown taker"
	}
	return "pid " + c.pid + " at " + c.at
}

// corrupt is a capacity file that holds no capacity: refused, never guessed.
type corrupt string

func (c corrupt) Error() string { return string(c) }

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
		return capacity{}, false, corrupt(fmt.Sprintf("%s holds %q, not a capacity of 1..1024",
			filepath.Join(dir, "capacity"), strings.TrimSpace(first)))
	}
	who, _, _ := strings.Cut(rest, "\n")
	c := capacity{n: n}
	if f := strings.Fields(who); len(f) == 4 && f[0] == "pid" && f[2] == "at" {
		c.pid, c.at = f[1], f[3]
	}
	return c, true, nil
}

// writeCapacity is called with gate and all held exclusive. A new file is a
// complete, synced temporary file renamed into place. An existing one (maybe
// another user's, which a sticky directory forbids replacing) is rewritten
// by one write at offset 0 and then cut to length: a kill between the two
// leaves the record followed by old bytes, which readCapacity ignores, so no
// kill leaves it empty (tla/Capacity.tla writes it in one step).
func writeCapacity(dir string, n, pid int, at time.Time) error {
	path := filepath.Join(dir, "capacity")
	rec := fmt.Appendf(nil, "%d\npid %d at %s\n", n, pid, at.UTC().Format(time.RFC3339))
	target, flags := path, os.O_RDWR // no O_CREATE: fs.protected_regular refuses it on another user's file
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		target, flags = path+"."+strconv.Itoa(pid)+".tmp", os.O_RDWR|os.O_CREATE
	}
	f, err := os.OpenFile(target, flags, 0o666)
	if err != nil {
		return err
	}
	_, err = f.WriteAt(rec, 0)
	err = errors.Join(err, f.Truncate(int64(len(rec))), f.Sync(), f.Close())
	// ignored: only the file's owner may widen it, and only a new file needs it
	_ = os.Chmod(target, 0o666)
	if err == nil && target != path {
		err = os.Rename(target, path)
		if d, derr := os.Open(dir); derr == nil {
			// ignored: Windows syncs no directory; the rename is the record elsewhere
			_ = d.Sync()
			_ = d.Close()
		}
	}
	if err != nil && target != path {
		// ignored: a temporary file that is already gone
		_ = os.Remove(target)
	}
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
	var bad corrupt
	if err != nil && !errors.As(err, &bad) {
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
		in := cmp.Or(strings.Join(holders(w, o.dir, c.n), ", "), "a taker mid-take")
		return refuse(1, "capacity in use (%s); retry when free; run: %s", in, o.remedy("status"))
	}
	defer free()
	if err := writeCapacity(o.dir, o.slots, w.pid, w.now()); err != nil {
		p := filepath.Join(o.dir, "capacity")
		return refuse(2, "%v; run: sudo rm -f %s && nova-reserve %s", err, shellQuote(p), quoteAll(o.args))
	}
	fmt.Fprintf(w.stdout, "CAPACITY SET slots=%d\n", o.slots)
	return nil
}

// --- taking ----------------------------------------------------------------

// taker takes the locks of one hold (tla/SlotFiles.tla, tla/Capacity.tla): a
// shared taker passes the gate (held shared only while it reads the capacity
// and takes its own locks), one free slot<k> of the stored capacity, and "all"
// shared; an exclusive taker holds the gate exclusive, so no new shared taker
// passes, then "all" exclusive, which waits for every shared holder; it keeps
// both until it ends.
type taker struct {
	w      world
	o      options
	held   []func() // released last to first: locks, and the holder files on them
	slot   int      // 0 until a shared hold; an exclusive hold names no slot
	cap    int      // the capacity the shared hold was taken under
	capSet int      // the capacity this taker stored, if it was the first
	// claiming: this taker found no capacity and polls its claim, never the
	// gate shared, which would keep another first taker's claim out
	// (tla/Capacity.tla SClaimOK); once the file exists it goes back to the
	// gate (SClaimSeen), since the claim waits on all, which a holder keeps.
	claiming bool
}

func (t *taker) path(name string) string { return filepath.Join(t.o.dir, name) }

// mark writes <name>.holder for status to read, and empties and removes it on
// release before the lock it names. A holder is believed only while its pid
// lives. Another user's file in the sticky directory cannot be removed, so it
// is rewritten in place, opened without O_CREATE, which fs.protected_regular
// refuses there. A file that cannot be written is a refusal: status would
// show the slot free, or held by a stale pid.
func (t *taker) mark(name string) error {
	f := []string{"pid=" + strconv.Itoa(t.w.pid), "since=" + t.w.now().UTC().Format(time.RFC3339)}
	if t.o.why != "" {
		f = append(f, "why="+esc(t.o.why))
	}
	if len(t.o.argv) > 0 {
		f = append(f, "cmd="+esc(strings.Join(t.o.argv, " ")))
	}
	p := t.path(name + ".holder")
	// ignored: a file that stays is rewritten in place
	_ = os.Remove(p)
	file, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if errors.Is(err, fs.ErrNotExist) {
		file, err = os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
		// ignored: only the file's owner may widen it, and this process made it
		_ = os.Chmod(p, 0o666)
	}
	if err == nil {
		_, err = file.WriteString(strings.Join(f, " ") + "\n")
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		return refuse(2, "%v; status could not show this hold; run: sudo rm -f %s && nova-reserve %s", err, shellQuote(p), quoteAll(t.o.args))
	}
	// ignored: emptied, a file that cannot be removed names no holder
	t.held = append(t.held, func() { _ = os.Truncate(p, 0); _ = os.Remove(p) })
	return nil
}

// step makes one attempt without waiting; it reports true once held.
func (t *taker) step() (bool, error) {
	if t.o.exclusive {
		if len(t.held) == 0 {
			gate, ok, err := t.w.lock(t.path("gate"), true)
			if !ok || err != nil {
				return false, err
			}
			t.held = []func(){gate}
			if err := t.mark("gate"); err != nil {
				return false, err
			}
		}
		all, ok, err := t.w.lock(t.path("all"), true)
		if ok {
			t.held = append(t.held, all)
		}
		return ok, err
	}
	for range 3 { // read (no file), store it, read again and take under it
		if t.claiming {
			if _, have, err := readCapacity(t.o.dir); have || err != nil {
				t.claiming = false // another first taker stored it: SClaimSeen
				continue
			}
			if ok, err := t.storeCapacity(); !ok || err != nil {
				return false, err
			}
			t.claiming = false
		}
		gate, ok, err := t.w.lock(t.path("gate"), false)
		if !ok || err != nil {
			return false, err
		}
		c, have, err := readCapacity(t.o.dir)
		if err != nil || have {
			defer gate()
			if bad := (corrupt("")); errors.As(err, &bad) {
				return false, refuse(2, "%s; run: %s", bad, setRemedy(t.o, cmp.Or(t.o.slots, t.defaultSlots())))
			}
			if err != nil {
				return false, err
			}
			return t.scan(c)
		}
		gate()
		t.claiming = true
	}
	return false, nil
}

// setRemedy repairs a corrupt capacity file; n is a suggestion, so it comes
// last, after --dir, with a comment the shell ignores.
func setRemedy(o options, n int) string {
	return o.remedy("set") + " --slots " + strconv.Itoa(n) + "  # your choice"
}

func (t *taker) defaultSlots() int { return max(1, t.w.cpus/4) }

// storeCapacity is the first taker's write of --slots, or of CPU count / 4:
// under gate and all exclusive, and only if no capacity is there by then
// (tla/Capacity.tla SClaim2; "racyfirst" is the witness without the locks).
func (t *taker) storeCapacity() (bool, error) {
	free, err := locksFree(t.w, t.o.dir)
	if free == nil || err != nil {
		return false, err
	}
	defer free()
	if _, have, err := readCapacity(t.o.dir); have || err != nil {
		return err == nil, err
	}
	n := cmp.Or(t.o.slots, t.defaultSlots())
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
			c.n, c.who(), t.o.slots, t.o.remedy("set", "--slots", strconv.Itoa(t.o.slots)))
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
			what := map[bool]string{false: "slot", true: "exclusive hold"}[o.exclusive]
			return nil, refuse(1, "no %s within %s: %s; run: %s", what, o.wait,
				cmp.Or(strings.Join(holders(w, o.dir, c.n), ", "), "every slot busy"), o.remedy("status"))
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

func (t *taker) describe() string {
	if t.o.exclusive {
		return fmt.Sprintf("mode=exclusive slot=all pid=%d", t.w.pid)
	}
	return fmt.Sprintf("mode=shared slot=%d/%d pid=%d", t.slot, t.cap, t.w.pid)
}

// --- who holds what --------------------------------------------------------

// holderOf reads <name>.holder. It takes no lock, so it can never be seen as
// a holder or make a scanning taker skip a free slot (tla/Capacity.tla
// "lockprobe"); a file is believed only while the pid in it is alive, so a
// SIGKILLed holder's leftover file reads as free.
func holderOf(w world, dir, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name+".holder"))
	line := strings.TrimSpace(string(b))
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, "pid="); ok && err == nil {
			pid, err := strconv.Atoi(v)
			return line, err == nil && pid > 0 && w.running(pid)
		}
	}
	return "", false
}

// holders is each held lock (the slots, gate and all) as status prints it.
func holders(w world, dir string, n int) []string {
	var in []string
	for _, name := range append(slots(dir, n), "gate", "all") {
		if line, held := holderOf(w, dir, name); held {
			in = append(in, name+" held "+line)
		}
	}
	return in
}

// slots is slot1..slot<n> and any other slot file on disk, in order.
func slots(dir string, n int) []string {
	nums := map[int]bool{}
	for k := 1; k <= n; k++ {
		nums[k] = true
	}
	files, _ := filepath.Glob(filepath.Join(dir, "slot*"))
	for _, p := range files {
		num := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "slot"), ".holder")
		if k, err := strconv.Atoi(num); err == nil && k > 0 {
			nums[k] = true
		}
	}
	var s []string
	for _, k := range slices.Sorted(maps.Keys(nums)) {
		s = append(s, "slot"+strconv.Itoa(k))
	}
	return s
}

func status(o options, w world) error {
	c, have, err := readCapacity(o.dir)
	var bad corrupt
	out := bufio.NewWriter(w.stdout)
	switch {
	case errors.As(err, &bad):
		fmt.Fprintf(out, "capacity=? # corrupt: %s; %s\n", bad, setRemedy(o, max(1, w.cpus/4)))
	case err != nil:
		return err
	case have:
		fmt.Fprintf(out, "capacity=%d set_by_pid=%s set_at=%s\n", c.n, cmp.Or(c.pid, "?"), cmp.Or(c.at, "?"))
	default:
		fmt.Fprintf(out, "capacity=unset # the first run stores its --slots (or CPU count / 4); or: %s\n",
			o.remedy("set", "--slots", "N"))
	}
	free := 0
	line := func(name, note string) bool {
		holder, held := holderOf(w, o.dir, name)
		parts := []string{name, "free"}
		if held {
			parts = []string{name, "held", holder}
		}
		if note != "" {
			parts = append(parts, "#", note)
		}
		fmt.Fprintln(out, strings.Join(parts, " "))
		return held
	}
	for i, name := range slots(o.dir, c.n) {
		if have && i >= c.n {
			line(name, "beyond capacity, from an older one; no new holder takes it")
		} else if !line(name, "") {
			free++
		}
	}
	line("gate", "held by an --exclusive run from when it starts waiting until it ends")
	if line("all", "the exclusive lock: held by an --exclusive run while it runs") {
		free = 0
	}
	if have {
		fmt.Fprintf(out, "free=%d/%d\n", free, c.n)
	} else {
		fmt.Fprintln(out, "free=?")
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

// runCommand runs the resolved path with argv and this process's stdio, tied
// to this process so it cannot outlive the slot (tie, per OS), forwards
// interrupts to it, and returns its exit status (128+signal when a signal
// ended it; 126 when it was found but could not be started).
func runCommand(path string, argv []string) int {
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	untie, err := tie(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nova-reserve:", err)
		return 126
	}
	defer untie()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sigs {
			forward(cmd, s)
		}
	}()
	ws, err := wait(cmd)
	signal.Stop(sigs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nova-reserve:", err)
		return 126
	}
	if ws.Signaled() {
		fmt.Fprintf(os.Stderr, "nova-reserve: %s ended by signal %s; exit %d\n", argv[0], signalName(ws.Signal()), 128+int(ws.Signal()))
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}
