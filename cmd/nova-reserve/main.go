// Command nova-reserve coordinates shared machine resources across local
// commands, CI runners, and sprint workers using crash-safe OS file locks.
package main

import (
	"bufio"
	"cmp"
	"encoding/json"
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

const banner = `nova-reserve: named multi-resource holds on a machine's shared capacity

A machine's tags, usable cores, and resources (cpu, memory, and custom devices)
live in DIR/capacity.json. Holds and waiters use OS file locks so the kernel
frees them on exit or SIGKILL.

  nova-reserve [run] [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--dir DIR] -- <cmd> [args...]
  nova-reserve hold [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--dir DIR]
  nova-reserve measure [--force] [--json] [--dir DIR]
  nova-reserve set [--cpu N] [--memory G] [--cores C] [--tags A,B] [--resources K=N,...] [--dir DIR]
  nova-reserve status [--json] [--dir DIR]
  nova-reserve help [run|hold|measure|set|status]

DIR: --dir, else /var/tmp/nova-reserve. When $NOVA_RESERVE_DIR matches DIR, nested
calls run directly without taking a second hold. Exit: 0 ok; 1 refused (never-fit,
NO ROOM, timeout, busy measure); 2 bad invocation or directory fault; 126/127
command launch failure; else the command's exit status.
`

var verbHelp = map[string]string{
	"run": `nova-reserve [run] [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--dir DIR] -- <cmd> [args...]

Acquire the needed resources, steer CPU count and OS priority, run the command,
and release the hold; exit with the command's status.
  --needs SPEC   comma-separated tags and resource amounts, e.g. "cpu 4, memory 8, gpu";
                 defaults to "cpu 1, memory 1" when omitted
  --exclusive    hold every resource on the machine
  --nowait       exit 1 (NO ROOM) immediately if resources are not free right now
  --wait D       give up after D (above 0: 90s, 15m), exit 1; default waits in line
  --holder H     bench, friend, ci, or person (default person); ci acquires immediately
                 and marks lowest-priority active holds paused
  --consumer ID  caller continuity id; resumes ahead of queued waiters after a CI pause
  --label L      board/status category (default ci for --holder ci, else work)
  --priority P   low, normal, high, read, critical, or blocker (default normal)
  --why TEXT     human note shown in status and timeout refusals
  --dir DIR      state directory (default /var/tmp/nova-reserve)
`,
	"hold": `nova-reserve hold [--needs SPEC | --exclusive] [--nowait | --wait D] [--holder H] [--consumer ID] [--label L] [--priority P] [--why TEXT] [--dir DIR]

Acquire a hold without spawning a child command, print one HELD line on stdout,
and hold until stdin reaches EOF or SIGINT/SIGTERM arrives.
  --needs SPEC   comma-separated tags and resource amounts (default "cpu 1, memory 1")
  --exclusive    hold every resource on the machine
  --nowait       exit 1 (NO ROOM) immediately if resources are not free right now
  --wait D       give up after D (above 0: 90s, 15m), exit 1
  --holder H     bench, friend, ci, or person (default person)
  --consumer ID  caller continuity id
  --label L      board/status category (default ci for --holder ci, else work)
  --priority P   low, normal, high, read, critical, or blocker (default normal)
  --why TEXT     human note shown in status and timeout refusals
  --dir DIR      state directory (default /var/tmp/nova-reserve)
`,
	"measure": `nova-reserve measure [--force] [--json] [--dir DIR]

Measure this machine's CPU units with the built-in compiler/test benchmark and
read usable memory (cgroup memory.max when bounded, else physical GiB), holding
--exclusive on DIR while measuring, then write DIR/capacity.json.
  --force    bypass the background CPU idle check
  --json     print JSON instead of MEASURE OK
  --dir DIR  state directory (default /var/tmp/nova-reserve)
`,
	"set": `nova-reserve set [--cpu N] [--memory G] [--cores C] [--tags A,B] [--resources K=N,...] [--dir DIR]

Atomically create or update DIR/capacity.json, even while holds are active.
  --cpu N          CPU units (>= 1)
  --memory G       usable memory in GiB (>= 1)
  --cores C        usable logical cores (>= 1)
  --tags A,B       comma-separated machine tags (empty string clears custom tags)
  --resources K=N  comma-separated custom whole-number resources (empty clears custom)
  --dir DIR        state directory (default /var/tmp/nova-reserve)
`,
	"status": `nova-reserve status [--json] [--dir DIR]

Show capacity, active and paused holds, queued waiters, and free resources.
  --json     print machine-readable status JSON
  --dir DIR  state directory (default /var/tmp/nova-reserve)
`,
}

var verbFlags = map[string][]string{
	"run":     {"needs", "exclusive", "nowait", "wait", "holder", "consumer", "label", "priority", "why", "dir"},
	"hold":    {"needs", "exclusive", "nowait", "wait", "holder", "consumer", "label", "priority", "why", "dir"},
	"measure": {"force", "json", "dir"},
	"set":     {"cpu", "memory", "cores", "tags", "resources", "dir"},
	"status":  {"json", "dir"},
}

var boolFlags = map[string]bool{"exclusive": true, "nowait": true, "force": true, "json": true}

var wants = map[string]string{
	"needs":     `a comma-separated resource/tag list like "cpu 4, memory 8"`,
	"wait":      "a duration above 0, like 90s or 15m",
	"holder":    "one of bench, friend, ci, person",
	"consumer":  "a non-empty consumer id",
	"label":     "a non-empty label",
	"priority":  "one of low, normal, high, read, critical, blocker",
	"why":       "text",
	"dir":       "a directory",
	"cpu":       "an integer >= 1",
	"memory":    "an integer >= 1",
	"cores":     "an integer >= 1",
	"tags":      "comma-separated tag names",
	"resources": "comma-separated k=n pairs with n >= 1",
}

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
	needs     []needItem
	exclusive bool
	nowait    bool
	wait      time.Duration
	holder    string
	consumer  string
	label     string
	priority  string
	why       string
	force     bool
	jsonOut   bool
	cpu       int
	memory    int
	cores     int
	tags      []string
	tagsSet   bool
	res       map[string]int
	resSet    bool
	dir       string
	dirSet    bool
	dirFrom   string
	argv      []string
	args      []string
}

func (o options) hasSetField() bool {
	return o.cpu > 0 || o.memory > 0 || o.cores > 0 || o.tagsSet || o.resSet
}

func (o options) remedy(args ...string) string {
	if o.dirSet {
		args = append(args, "--dir", o.dir)
	}
	return "nova-reserve " + quoteAll(args)
}

func helpFor(verb string) string {
	if verb == "run" || verb == "" {
		return "nova-reserve -h"
	}
	return "nova-reserve " + verb + " -h"
}

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

func parse(args []string) (options, error) {
	o := options{verb: "run", holder: "person", priority: "normal", dir: systemDir, dirFrom: "the default directory", args: args}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		o.help, o.verb = true, ""
		return o, nil
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "run", "hold", "measure", "set", "status", "help":
			o.verb, args = args[0], args[1:]
		default:
			return o, refuse(2, "unknown verb %q; verbs: run, hold, measure, set, status, help; a command goes after --: nova-reserve -- %s; run: nova-reserve help",
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
			return o, refuse(2, "help takes one of run, hold, measure, set, status, not %q; run: nova-reserve -h", strings.Join(args, " "))
		}
		return o, nil
	}
	end := slices.Index(args, "--")
	hasDashDash := end >= 0
	if !hasDashDash {
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
			if _, known := wants[name]; known || boolFlags[name] {
				return o, refuse(2, "%s takes no --%s; it takes --%s; run: %s", o.verb, name, strings.Join(verbFlags[o.verb], ", --"), hint)
			}
			return o, refuse(2, "unknown flag %s; run: %s", a, hint)
		}
		if seen[name] {
			return o, refuse(2, "--%s given twice; run: %s", name, hint)
		}
		seen[name] = true
		if boolFlags[name] {
			if hasVal {
				return o, refuse(2, "--%s takes no value, not %q; run: %s", name, val, hint)
			}
			o.exclusive = o.exclusive || name == "exclusive"
			o.nowait = o.nowait || name == "nowait"
			o.force = o.force || name == "force"
			o.jsonOut = o.jsonOut || name == "json"
			continue
		}
		if !hasVal {
			if i+1 >= end {
				return o, refuse(2, "--%s wants %s; run: %s", name, wants[name], hint)
			}
			i++
			val = args[i]
		}
		if !o.setFlag(name, val) {
			return o, refuse(2, "--%s wants %s, not %q; run: %s", name, wants[name], val, hint)
		}
	}
	if hasDashDash {
		o.argv = args[end+1:]
	}
	if o.label == "" {
		o.label = map[bool]string{true: "ci", false: "work"}[o.holder == "ci"]
	}
	switch {
	case o.verb == "run" && len(o.argv) == 0:
		return o, refuse(2, "no command: nova-reserve [flags] -- <cmd> [args...]; run: %s", hint)
	case o.verb != "run" && hasDashDash:
		return o, refuse(2, "%s takes no command, not %q; run: %s", o.verb, strings.Join(o.argv, " "), hint)
	case o.exclusive && len(o.needs) > 0:
		return o, refuse(2, "--exclusive takes every resource; --needs does not apply; run: %s", hint)
	case o.nowait && o.wait > 0:
		return o, refuse(2, "--nowait and --wait cannot both be given; run: %s", hint)
	case o.verb == "set" && !o.hasSetField():
		return o, refuse(2, "set wants at least one of --cpu, --memory, --cores, --tags, --resources; run: %s", hint)
	}
	return o, nil
}

func (o *options) setFlag(name, val string) bool {
	switch name {
	case "needs":
		items, err := parseNeedItems(val)
		o.needs = items
		return err == nil
	case "wait":
		d, err := time.ParseDuration(val)
		o.wait = d
		return err == nil && d > 0
	case "holder":
		o.holder = val
		return val == "bench" || val == "friend" || val == "ci" || val == "person"
	case "consumer":
		o.consumer = val
		return val != "" && !strings.ContainsAny(val, " \t\r\n")
	case "label":
		o.label = val
		return identRe.MatchString(val)
	case "priority":
		_, ok := prioRanks[val]
		o.priority = val
		return ok
	case "why":
		o.why = val
		return val != ""
	case "dir":
		o.dir, o.dirSet, o.dirFrom = val, true, "--dir"
		return val != ""
	case "cpu", "memory", "cores":
		n, err := strconv.Atoi(val)
		switch name {
		case "cpu":
			o.cpu = n
		case "memory":
			o.memory = n
		case "cores":
			o.cores = n
		}
		return err == nil && n >= 1
	case "tags":
		o.tagsSet, o.tags = true, nil
		if val == "" {
			return true
		}
		seen := map[string]bool{}
		for _, t := range strings.Split(val, ",") {
			t = strings.TrimSpace(t)
			if !identRe.MatchString(t) || t == "cpu" || t == "memory" || seen[t] {
				return false
			}
			seen[t] = true
			o.tags = append(o.tags, t)
		}
		slices.Sort(o.tags)
		return true
	case "resources":
		o.resSet, o.res = true, map[string]int{}
		if val == "" {
			return true
		}
		for _, part := range strings.Split(val, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			n, err := strconv.Atoi(v)
			if !ok || !identRe.MatchString(k) || err != nil || n < 1 || o.res[k] > 0 {
				return false
			}
			o.res[k] = n
		}
		return true
	}
	return false
}

type tryLock func(path string, exclusive bool) (release func(), ok bool, err error)

type watcher interface {
	drain()
	wait(pids []int, d time.Duration)
	close()
}

type fallbackWatcher struct{}

func (fallbackWatcher) drain()                        {}
func (fallbackWatcher) wait(_ []int, d time.Duration) { time.Sleep(min(d, 50*time.Millisecond)) }
func (fallbackWatcher) close()                        {}

type world struct {
	env     func(string) string
	environ func() []string
	lock    tryLock
	watch   func(dir string) watcher
	now     func() time.Time
	sleep   func(d time.Duration)
	resolve func(name string) (string, error)
	exec    func(path string, argv, env []string, lower bool) int
	waitEOF func(in io.Reader)
	running func(pid int) bool
	bench   func(cores int, force bool) (cpuUnits, memGB int, err error)
	pid     int
	cpus    int
	goos    string
	goarch  string
	usable  func() ([]int, error)
	groups  func(usable []int) [][]int
	pin     func(cpus []int) error
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
}

const poll = time.Second

func main() {
	os.Exit(run(os.Args[1:], world{
		env: os.Getenv, environ: os.Environ, lock: osLock, watch: newOSWatcher,
		now: time.Now, sleep: time.Sleep, resolve: exec.LookPath, exec: runCommand,
		waitEOF: waitHoldSignalOrEOF, running: running, bench: defaultBench,
		pid: os.Getpid(), cpus: runtime.NumCPU(), goos: runtime.GOOS, goarch: runtime.GOARCH,
		usable: usableCPUs, groups: smtGroups, pin: pin,
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
	}))
}

func run(args []string, w world) int {
	o, err := parse(args)
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

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if aa, e1 := filepath.Abs(a); e1 == nil {
		if ab, e2 := filepath.Abs(b); e2 == nil && aa == ab {
			return true
		}
	}
	if ra, e1 := filepath.EvalSymlinks(a); e1 == nil {
		if rb, e2 := filepath.EvalSymlinks(b); e2 == nil && ra == rb {
			return true
		}
	}
	if fa, e1 := os.Stat(a); e1 == nil {
		if fb, e2 := os.Stat(b); e2 == nil && os.SameFile(fa, fb) {
			return true
		}
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func flushOut(w io.Writer) {
	if fl, ok := w.(interface{ Flush() error }); ok {
		// ignored: stdout flush best-effort
		_ = fl.Flush()
	}
}

func do(o options, w world) error {
	path := ""
	if o.verb == "run" {
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
	if (o.verb == "run" || o.verb == "hold") && sameDir(w.env("NOVA_RESERVE_DIR"), o.dir) {
		if o.verb == "hold" {
			fmt.Fprintf(w.stdout, "HELD cpu=1 memory=1 cores=1 pid=%d note=reentrant\n", w.pid)
			flushOut(w.stdout)
			w.waitEOF(w.stdin)
			return nil
		}
		if code := w.exec(path, o.argv, nil, false); code != 0 {
			return &exitError{code: code}
		}
		return nil
	}
	if o.verb == "status" {
		fi, err := os.Lstat(o.dir)
		if err != nil {
			return refuse(2, "%s %s is not a reservation directory (no reservation was taken there); run: %s",
				o.dirFrom, shellQuote(o.dir), helpFor("status"))
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return refuse(2, "%s %s is a symbolic link; nova-reserve uses no link as its directory: give the directory it names, or remove the link; run: ls -ld %s",
				o.dirFrom, o.dir, shellQuote(o.dir))
		}
		if !fi.IsDir() {
			return refuse(2, "%s %s is a file, not a directory; run: ls -ld %s", o.dirFrom, o.dir, shellQuote(o.dir))
		}
		return statusCmd(o, w)
	}
	if err := ensureLayout(o.dir, o.dirFrom, "nova-reserve "+quoteAll(o.args)); err != nil {
		return err
	}
	switch o.verb {
	case "set":
		return setCapacity(o, w)
	case "measure":
		return measureCmd(o, w)
	case "hold":
		h, err := acquire(w, o)
		if err != nil {
			return err
		}
		defer h.release(false)
		if h.measured != nil {
			fmt.Fprintf(w.stderr, "nova-reserve: MEASURE OK cpu=%d memory=%d cores=%d\n",
				h.measured.Resources["cpu"], h.measured.Resources["memory"], h.measured.UsableCores)
		}
		fmt.Fprintf(w.stdout, "HELD %s\n", describeHold(h.rec, nil))
		flushOut(w.stdout)
		w.waitEOF(w.stdin)
		return nil
	default:
		h, err := acquire(w, o)
		if err != nil {
			return err
		}
		if h.measured != nil {
			fmt.Fprintf(w.stderr, "nova-reserve: MEASURE OK cpu=%d memory=%d cores=%d\n",
				h.measured.Resources["cpu"], h.measured.Resources["memory"], h.measured.UsableCores)
		}
		if h.pinWarn != nil {
			fmt.Fprintf(w.stderr, "nova-reserve: running unpinned: %v\n", h.pinWarn)
		}
		fmt.Fprintf(w.stderr, "nova-reserve: HELD %s\n", describeHold(h.rec, h.pinWarn))
		code := w.exec(path, o.argv, steerEnv(w.environ, o.dir, h.rec.Cores), o.holder != "ci")
		h.release(true)
		if code != 0 {
			return &exitError{code: code}
		}
		return nil
	}
}

func describeHold(r holdRecord, pinWarn error) string {
	s := fmt.Sprintf("cpu=%d memory=%d cores=%d pid=%d", r.Needs["cpu"], r.Needs["memory"], r.Cores, r.PID)
	if pinWarn == nil && len(r.CPUs) > 0 {
		s += " cpus=" + cpuList(r.CPUs)
	}
	return s
}

func steerEnv(baseFn func() []string, dir string, cores int) []string {
	absDir := dir
	if a, err := filepath.Abs(dir); err == nil {
		absDir = a
	}
	cs := strconv.Itoa(max(1, cores))
	overrides := map[string]string{
		"NOVA_RESERVE_DIR": absDir, "NOVA_RESERVE_CPUS": cs, "GOMAXPROCS": cs,
		"CARGO_BUILD_JOBS": cs, "RAYON_NUM_THREADS": cs, "CMAKE_BUILD_PARALLEL_LEVEL": cs,
	}
	var base []string
	if baseFn != nil {
		base = baseFn()
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); overrides[k] == "" {
			out = append(out, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(overrides)) {
		out = append(out, k+"="+overrides[k])
	}
	return out
}

func waitHoldSignalOrEOF(in io.Reader) {
	done := make(chan struct{}, 1)
	if in != nil {
		go func() {
			var buf [64]byte
			for {
				if _, err := in.Read(buf[:]); err != nil {
					done <- struct{}{}
					return
				}
			}
		}()
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	select {
	case <-done:
	case <-sigs:
	}
}

// measureInto runs the benchmark while holding exclusive access on o.dir.
// When auto is true, it skips measurement if another caller already wrote capacity.json.
func measureInto(w world, o options, auto bool, deadline time.Time) (capRecord, bool, error) {
	for {
		var existing capRecord
		var haveExisting bool
		c, have, err := readCapacity(o.dir)
		if bad := corrupt(""); err != nil && !errors.As(err, &bad) {
			return capRecord{}, false, err
		}
		if have {
			if auto {
				return c, false, nil
			}
			existing, haveExisting = c, true
		}

		var exHold *activeHold
		var bootUnlock func()
		var bootSeq uint64
		if haveExisting {
			exOpts := options{
				verb: "hold", exclusive: true, holder: "person", label: "measure",
				priority: "blocker", why: "measure", dir: o.dir, dirSet: o.dirSet, dirFrom: o.dirFrom,
			}
			exHold, err = acquire(w, exOpts)
			if err != nil {
				return capRecord{}, false, err
			}
		} else {
			var already bool
			merr := withMutex(w, o.dir, true, deadline, func() error {
				cur, ok, cerr := readCapacity(o.dir)
				if bad := corrupt(""); cerr != nil && !errors.As(cerr, &bad) {
					return cerr
				}
				if ok && auto {
					c, already = cur, true
					return nil
				}
				holds, waiters, lerr := loadStateLocked(w, o.dir, false)
				if lerr != nil || len(holds) > 0 {
					return lerr
				}
				seq, serr := nextSeqLocked(o.dir, holds, waiters, w.pid)
				if serr != nil {
					return serr
				}
				lp := lockPath(o.dir, "holds", seq)
				u, ok, lerr := w.lock(lp, true)
				if lerr != nil || !ok {
					return lerr
				}
				h := &holdRecord{
					Seq: seq, PID: w.pid, Since: w.now().UTC().Format(time.RFC3339),
					Holder: "person", Label: "measure", Priority: "blocker", Exclusive: true,
					Needs: map[string]int{"cpu": 1, "memory": 1}, Cores: max(1, w.cpus), Why: "measure",
				}
				if serr := saveHoldLocked(o.dir, h, w.pid); serr != nil {
					u()
					return serr
				}
				bootUnlock, bootSeq = u, seq
				return nil
			})
			if merr != nil {
				if errors.Is(merr, errTimedOut) {
					return capRecord{}, false, refuse(1, "no hold within %s: measure in progress; run: %s", o.wait, o.remedy("status"))
				}
				return capRecord{}, false, merr
			}
			if already {
				return c, false, nil
			}
			if bootUnlock == nil {
				if !deadline.IsZero() && !w.now().Before(deadline) {
					return capRecord{}, false, refuse(1, "no hold within %s: measure in progress; run: %s", o.wait, o.remedy("status"))
				}
				d := min(poll, 100*time.Millisecond)
				if !deadline.IsZero() {
					d = min(d, deadline.Sub(w.now()))
				}
				w.sleep(d)
				continue
			}
		}

		cores := usableCoreCount(w)
		if u, uerr := w.usable(); uerr == nil && len(u) > 0 {
			// ignored: advisory affinity across all usable CPUs for benchmark
			_ = w.pin(u)
		}
		cpuU, memG, berr := w.bench(cores, o.force || auto)

		var written capRecord
		werr := withMutex(w, o.dir, true, time.Time{}, func() error {
			if bootUnlock != nil {
				removePair(seqFiles(o.dir, "holds", bootSeq))
				bootUnlock()
			}
			if berr != nil {
				return refuse(1, "%v; run: %s", berr, o.remedy("measure", "--force"))
			}
			res := map[string]int{"cpu": cpuU, "memory": memG}
			tags := effectiveTags(nil, w)
			if haveExisting {
				for k, v := range existing.Resources {
					if k != "cpu" && k != "memory" {
						res[k] = v
					}
				}
				tags = slices.Clone(existing.Tags)
			}
			written = capRecord{
				Tags: tags, UsableCores: cores, Resources: res,
				SetByPID: w.pid, SetAt: w.now().UTC().Format(time.RFC3339),
			}
			if err := writeCapacity(o.dir, written, w.pid); err != nil {
				return err
			}
			if hs, ws, lerr := loadStateLocked(w, o.dir, false); lerr == nil {
				// ignored: best-effort unpause pass after measure update
				_, _ = reconcileLocked(w, o.dir, written, hs, ws)
			}
			notifyLocked(o.dir, w.pid, w.now())
			return nil
		})
		if exHold != nil {
			exHold.release(false)
		}
		if werr != nil {
			return capRecord{}, false, werr
		}
		return written, true, nil
	}
}

func measureCmd(o options, w world) error {
	written, _, err := measureInto(w, o, false, time.Time{})
	if err != nil {
		return err
	}
	if o.jsonOut {
		out := struct {
			CPU         int      `json:"cpu"`
			Memory      int      `json:"memory"`
			UsableCores int      `json:"usable_cores"`
			Tags        []string `json:"tags"`
		}{written.Resources["cpu"], written.Resources["memory"], written.UsableCores, effectiveTags(written.Tags, w)}
		// ignored: marshaling fixed primitive struct cannot fail
		b, _ := json.Marshal(out)
		fmt.Fprintln(w.stdout, string(b))
		return nil
	}
	fmt.Fprintf(w.stdout, "MEASURE OK cpu=%d memory=%d cores=%d\n",
		written.Resources["cpu"], written.Resources["memory"], written.UsableCores)
	return nil
}

func setCapacity(o options, w world) error {
	var unchanged bool
	var next capRecord
	err := withMutex(w, o.dir, true, time.Time{}, func() error {
		cur, have, cerr := readCapacity(o.dir)
		if bad := corrupt(""); cerr != nil && !errors.As(cerr, &bad) {
			return cerr
		}
		res := map[string]int{}
		var tags []string
		cores := usableCoreCount(w)
		if have {
			res, tags, cores = maps.Clone(cur.Resources), slices.Clone(cur.Tags), cur.UsableCores
		}
		if o.resSet {
			kept := map[string]int{}
			for _, base := range []string{"cpu", "memory"} {
				if res[base] > 0 {
					kept[base] = res[base]
				}
			}
			maps.Copy(kept, o.res)
			res = kept
		}
		if o.cpu > 0 {
			res["cpu"] = o.cpu
		}
		if o.memory > 0 {
			res["memory"] = o.memory
		}
		if o.cores > 0 {
			cores = o.cores
		}
		if o.tagsSet {
			tags = slices.Clone(o.tags)
		} else if !have {
			tags = effectiveTags(nil, w)
		}
		if res["cpu"] < 1 || res["memory"] < 1 {
			return refuse(2, "%s %s has no capacity.json; pass both --cpu and --memory, or measure first; run: %s",
				o.dirFrom, o.dir, o.remedy("measure", "--force"))
		}
		effTags := effectiveTags(tags, w)
		for k := range res {
			if slices.Contains(tags, k) || (k != "cpu" && k != "memory" && slices.Contains(effTags, k)) {
				return refuse(2, "%q cannot be both a tag and a resource; run: %s", k, helpFor("set"))
			}
		}
		if tags == nil {
			tags = []string{}
		}
		slices.Sort(tags)
		unchanged = have && cur.UsableCores == cores && slices.Equal(cur.Tags, tags) && maps.Equal(cur.Resources, res)
		next = capRecord{Tags: tags, UsableCores: cores, Resources: res, SetByPID: w.pid, SetAt: w.now().UTC().Format(time.RFC3339)}
		if unchanged {
			return nil
		}
		if err := writeCapacity(o.dir, next, w.pid); err != nil {
			if odd := (*exitError)(nil); errors.As(err, &odd) {
				return err
			}
			return refuse(2, "%v; run: sudo rm -f %s && nova-reserve %s", err, shellQuote(capPath(o.dir)), quoteAll(o.args))
		}
		if hs, ws, lerr := loadStateLocked(w, o.dir, false); lerr == nil {
			// ignored: best-effort unpause pass after live capacity update
			_, _ = reconcileLocked(w, o.dir, next, hs, ws)
		}
		notifyLocked(o.dir, w.pid, w.now())
		return nil
	})
	if err != nil {
		return err
	}
	note := map[bool]string{true: " note=unchanged"}[unchanged]
	fmt.Fprintf(w.stdout, "CAPACITY SET cpu=%d memory=%d cores=%d%s\n",
		next.Resources["cpu"], next.Resources["memory"], next.UsableCores, note)
	return nil
}

type statusJSON struct {
	Tags            []string                  `json:"tags"`
	UsableCores     int                       `json:"usable_cores"`
	Resources       map[string]int            `json:"resources"`
	HeldSprint      map[string]int            `json:"held_sprint"`
	HeldOther       map[string]int            `json:"held_other"`
	HeldByLabel     map[string]map[string]int `json:"held_by_label"`
	PausedByLabel   map[string]map[string]int `json:"paused_by_label"`
	AnyPaused       bool                      `json:"any_paused"`
	SprintConsumers []string                  `json:"sprint_consumers"`
	PausedConsumers []string                  `json:"paused_consumers"`
}

func addByLabel(dst map[string]map[string]int, label string, src map[string]int) {
	label = cmp.Or(label, "work")
	if dst[label] == nil {
		dst[label] = map[string]int{}
	}
	for r, v := range src {
		dst[label][r] += v
	}
}

func statusCmd(o options, w world) error {
	var c capRecord
	var have bool
	var capErr error
	var holds []*holdRecord
	var waiters []*waiterRecord

	err := withMutex(w, o.dir, false, time.Time{}, func() error {
		c, have, capErr = readCapacity(o.dir)
		if bad := corrupt(""); capErr != nil && !errors.As(capErr, &bad) {
			return capErr
		}
		var lerr error
		holds, waiters, lerr = loadStateLocked(w, o.dir, true)
		return lerr
	})
	if err != nil {
		return err
	}
	var bad corrupt
	if o.jsonOut {
		if errors.As(capErr, &bad) {
			return refuse(2, "%s; run: %s", bad, o.remedy("measure", "--force"))
		}
		sj := statusJSON{
			Tags: effectiveTags(c.Tags, w), UsableCores: c.UsableCores,
			Resources: map[string]int{}, HeldSprint: map[string]int{}, HeldOther: map[string]int{},
			HeldByLabel: map[string]map[string]int{}, PausedByLabel: map[string]map[string]int{},
			SprintConsumers: []string{}, PausedConsumers: []string{},
		}
		if have {
			sj.Resources = maps.Clone(c.Resources)
		}
		sprintSet, pausedSet := map[string]bool{}, map[string]bool{}
		for _, h := range holds {
			if h.Paused {
				sj.AnyPaused = true
				addByLabel(sj.PausedByLabel, h.Label, h.Needs)
				if h.Consumer != "" {
					pausedSet[h.Consumer] = true
				}
				continue
			}
			dst := sj.HeldOther
			if h.Holder == "bench" {
				dst = sj.HeldSprint
				if h.Consumer != "" {
					sprintSet[h.Consumer] = true
				}
			}
			for r, v := range h.Needs {
				dst[r] += v
			}
			addByLabel(sj.HeldByLabel, h.Label, h.Needs)
		}
		if len(sprintSet) > 0 {
			sj.SprintConsumers = slices.Sorted(maps.Keys(sprintSet))
		}
		if len(pausedSet) > 0 {
			sj.PausedConsumers = slices.Sorted(maps.Keys(pausedSet))
		}
		b, merr := json.Marshal(sj)
		if merr != nil {
			return merr
		}
		fmt.Fprintln(w.stdout, string(b))
		return nil
	}

	out := bufio.NewWriter(w.stdout)
	switch {
	case errors.As(capErr, &bad):
		fmt.Fprintf(out, "capacity=? # corrupt: %s; %s\n", bad, o.remedy("measure", "--force"))
	case have:
		tags := strings.Join(effectiveTags(c.Tags, w), ",")
		fmt.Fprintf(out, "capacity cpu=%d memory=%d cores=%d tags=%s set_by_pid=%d set_at=%s\n",
			c.Resources["cpu"], c.Resources["memory"], c.UsableCores, cmp.Or(tags, "-"), c.SetByPID, cmp.Or(c.SetAt, "?"))
	default:
		fmt.Fprintf(out, "capacity=unset # run: %s\n", o.remedy("measure", "--force"))
	}
	// ignored: exclusive flag not needed when printing resource free counts
	used, _ := sumUnpaused(holds)
	pausedCount := 0
	for _, h := range holds {
		state := "held"
		if h.Paused {
			state = "paused"
			pausedCount++
		}
		fmt.Fprintf(out, "hold %d state=%s holder=%s label=%s priority=%s %s cores=%d pid=%d since=%s",
			h.Seq, state, h.Holder, h.Label, h.Priority, formatNeedsKV(h.Needs), h.Cores, h.PID, cmp.Or(h.Since, "?"))
		if h.Consumer != "" {
			fmt.Fprintf(out, " consumer=%s", esc(h.Consumer))
		}
		if h.Why != "" {
			fmt.Fprintf(out, " why=%s", esc(h.Why))
		}
		if h.Cmd != "" {
			fmt.Fprintf(out, " cmd=%s", esc(h.Cmd))
		}
		fmt.Fprintln(out)
	}
	for _, wr := range waiters {
		fmt.Fprintf(out, "waiter %d holder=%s label=%s priority=%s %s pid=%d since=%s",
			wr.Seq, wr.Holder, wr.Label, wr.Priority, formatNeedsKV(wr.Needs), wr.PID, cmp.Or(wr.Since, "?"))
		if wr.Consumer != "" {
			fmt.Fprintf(out, " consumer=%s", esc(wr.Consumer))
		}
		if wr.Why != "" {
			fmt.Fprintf(out, " why=%s", esc(wr.Why))
		}
		fmt.Fprintln(out)
	}
	if have {
		var fparts []string
		for _, r := range slices.Sorted(maps.Keys(c.Resources)) {
			fparts = append(fparts, fmt.Sprintf("%s=%d/%d", r, max(0, c.Resources[r]-used[r]), c.Resources[r]))
		}
		fmt.Fprintf(out, "free %s paused=%d waiting=%d\n", strings.Join(fparts, " "), pausedCount, len(waiters))
	} else {
		fmt.Fprintf(out, "free=? paused=%d waiting=%d\n", pausedCount, len(waiters))
	}
	return out.Flush()
}

func formatNeedsKV(needs map[string]int) string {
	var parts []string
	for _, r := range slices.Sorted(maps.Keys(needs)) {
		parts = append(parts, fmt.Sprintf("%s=%d", r, needs[r]))
	}
	return strings.Join(parts, " ")
}

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
