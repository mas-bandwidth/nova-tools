// Command nova-slot runs a command holding one of N load slots on this
// machine, so builds, tests and benches queue instead of piling onto one box.
// The slots are OS file locks in one directory, so the kernel frees the slot
// of a holder that dies, however it dies.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const usage = `nova-slot: run a command holding one of N load slots on this machine.

  nova-slot [--slots N] [--wait D] [--exclusive] [--card ID] [--dir DIR] -- <cmd> [args...]
      Wait for a free slot, run the command, free the slot when it exits for
      any reason; exits with the command's status. --exclusive waits for every
      holder to end and keeps new ones out while it runs (timing-sensitive
      work). --wait bounds the wait (default: no bound); past it, exit 3.
  nova-slot hold [same flags]
      Take a slot, print one line starting HELD, and hold it until stdin
      closes. --dir is accepted and ignored here (winlock's form).
  nova-slot status [--dir DIR]
      Print each slot: free, or held with the holder's pid, card and command.

N defaults to the CPU count / 4 (at least 1). DIR defaults to $NOVA_SLOT_DIR,
else /tmp/nova-slot (C:\ProgramData\nova-slot on Windows); every caller on a
machine must use the same DIR. Exit: the command's status; 2 usage; 3 the
slot could not be taken.
`

// tryLock takes the OS lock on path, shared or exclusive, without waiting.
// ok is false when another holder has it; release lets go of a held lock.
type tryLock func(path string, exclusive bool) (release func(), ok bool, err error)

type options struct {
	slots     int
	wait      time.Duration
	exclusive bool
	card, dir string
	argv      []string
}

var errTimeout = errors.New("no slot within --wait")

// taker takes the locks of one hold: a shared taker passes the gate (held
// shared only while it takes its own locks), one free slot<k>, and "all"
// shared; an exclusive taker holds the gate exclusive, so no new shared taker
// passes, then "all" exclusive, which waits for every shared holder (tla/).
type taker struct {
	lock tryLock
	o    options
	gate func()
	held []func()
	slot int
}

func (t *taker) path(name string) string { return filepath.Join(t.o.dir, name) }

// step makes one attempt without waiting; it reports true once held.
func (t *taker) step() (bool, error) {
	if t.o.exclusive {
		if t.gate == nil {
			rel, ok, err := t.lock(t.path("gate"), true)
			if !ok || err != nil {
				return false, err
			}
			t.gate = rel
		}
		rel, ok, err := t.lock(t.path("all"), true)
		if ok {
			t.held = []func(){rel, t.gate}
			t.gate = nil
		}
		return ok, err
	}
	gate, ok, err := t.lock(t.path("gate"), false)
	if !ok || err != nil {
		return false, err
	}
	defer gate()
	for k := 1; k <= t.o.slots; k++ {
		slot, ok, err := t.lock(t.path("slot"+strconv.Itoa(k)), true)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		all, ok, err := t.lock(t.path("all"), false)
		if !ok || err != nil {
			slot()
			return false, err
		}
		t.held, t.slot = []func(){slot, all}, k
		return true, nil
	}
	return false, nil
}

func (t *taker) release() {
	if t.gate != nil {
		t.gate()
		t.gate = nil
	}
	for _, rel := range t.held {
		rel()
	}
	t.held = nil
}

// acquire polls step until it holds, or until o.wait (0: no bound) has passed.
func acquire(lock tryLock, o options, now func() time.Time, sleep func()) (*taker, error) {
	t := &taker{lock: lock, o: o}
	deadline := now().Add(o.wait)
	for {
		ok, err := t.step()
		if ok {
			return t, nil
		}
		if err == nil && o.wait > 0 && !now().Before(deadline) {
			err = errTimeout
		}
		if err != nil {
			t.release()
			return nil, err
		}
		sleep()
	}
}

// holderFile names the holder for status: slot<k>.holder, or all.holder.
func (t *taker) holderFile() string {
	if t.o.exclusive {
		return t.path("all.holder")
	}
	return t.path("slot" + strconv.Itoa(t.slot) + ".holder")
}

func (t *taker) describe() string {
	if t.o.exclusive {
		return fmt.Sprintf("mode=exclusive slot=all/%d card=%s pid=%d", t.o.slots, t.o.card, os.Getpid())
	}
	return fmt.Sprintf("mode=shared slot=%d/%d card=%s pid=%d", t.slot, t.o.slots, t.o.card, os.Getpid())
}

func defaultDir(env func(string) string) string {
	if d := env("NOVA_SLOT_DIR"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\nova-slot`
	}
	return "/tmp/nova-slot"
}

func parse(verb string, args []string, env func(string) string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("nova-slot "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	o := options{}
	fs.IntVar(&o.slots, "slots", max(1, runtime.NumCPU()/4), "")
	fs.DurationVar(&o.wait, "wait", 0, "")
	fs.BoolVar(&o.exclusive, "exclusive", false, "")
	fs.StringVar(&o.card, "card", "-", "")
	fs.StringVar(&o.dir, "dir", defaultDir(env), "")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	o.argv = fs.Args()
	switch {
	case o.slots < 1 || o.slots > 1024:
		return o, fmt.Errorf("--slots wants 1..1024, not %d", o.slots)
	case o.wait < 0:
		return o, fmt.Errorf("--wait wants a duration >= 0")
	case verb == "run" && len(o.argv) == 0:
		return o, fmt.Errorf("no command: nova-slot [flags] -- <cmd> [args...]")
	case verb != "run" && len(o.argv) > 0:
		return o, fmt.Errorf("nova-slot %s takes no command, not %q", verb, o.argv[0])
	}
	return o, nil
}

// world is everything run touches outside itself, so the unit tests fake it.
type world struct {
	env            func(string) string
	lock           tryLock
	now            func() time.Time
	sleep          func()
	exec           func(argv []string) int
	stdin          io.Reader
	stdout, stderr io.Writer
}

func main() {
	os.Exit(run(os.Args[1:], world{os.Getenv, osLock, time.Now, func() { time.Sleep(time.Second) },
		runCommand, os.Stdin, os.Stdout, os.Stderr}))
}

func run(args []string, w world) int {
	verb := "run"
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprint(w.stdout, usage)
			return 0
		case "hold", "status":
			verb, args = args[0], args[1:]
		}
	}
	o, err := parse(verb, args, w.env, w.stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(w.stderr, "nova-slot:", err)
		}
		return 2
	}
	if verb == "hold" {
		o.dir = defaultDir(w.env) // winlock's --dir named its holder files, never the count
	}
	if err := makeDir(o.dir); err != nil {
		fmt.Fprintln(w.stderr, "nova-slot:", err)
		return 3
	}
	if verb == "status" {
		return status(o.dir, w)
	}
	t, err := acquire(w.lock, o, w.now, w.sleep)
	if err != nil {
		fmt.Fprintln(w.stderr, "nova-slot:", err)
		return 3
	}
	defer t.release()
	// ignored: the holder file is for status to read; the lock is the slot either way
	_ = os.Remove(t.holderFile())
	_ = os.WriteFile(t.holderFile(), []byte(fmt.Sprintf("pid=%d card=%s start=%s cmd=%s\n",
		os.Getpid(), o.card, w.now().UTC().Format(time.RFC3339), strings.Join(o.argv, " "))), 0o666)
	defer os.Remove(t.holderFile())
	if verb == "hold" {
		fmt.Fprintln(w.stdout, "HELD", t.describe())
		// ignored: EOF or an error both mean the caller is gone, which ends the hold
		_, _ = io.Copy(io.Discard, w.stdin)
		return 0
	}
	fmt.Fprintln(w.stderr, "nova-slot: HELD", t.describe())
	return w.exec(o.argv)
}

func makeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return fmt.Errorf("the slot directory: %w", err)
	}
	// ignored: only the directory's owner may widen it; others share it as it is
	_ = os.Chmod(dir, 0o777|os.ModeSticky)
	return nil
}

// status probes each lock in dir: a lock it can take is free.
func status(dir string, w world) int {
	names, err := filepath.Glob(filepath.Join(dir, "slot*"))
	if err != nil {
		fmt.Fprintln(w.stderr, "nova-slot:", err)
		return 3
	}
	out := bufio.NewWriter(w.stdout)
	defer out.Flush()
	line := func(name string, exclusive bool) {
		rel, ok, err := w.lock(filepath.Join(dir, name), exclusive)
		switch {
		case err != nil:
			fmt.Fprintf(out, "%s unknown %v\n", name, err)
		case ok:
			rel()
			fmt.Fprintf(out, "%s free\n", name)
		default:
			b, _ := os.ReadFile(filepath.Join(dir, name+".holder"))
			fmt.Fprintf(out, "%s held %s\n", name, strings.TrimSpace(string(b)))
		}
	}
	for _, n := range names {
		if !strings.HasSuffix(n, ".holder") {
			line(filepath.Base(n), true)
		}
	}
	line("all", false) // taken shared by every shared holder; held only by an exclusive one
	return 0
}

// runCommand runs argv with this process's stdio, forwards interrupts to it,
// and returns its exit status (128+signal when a signal ended it).
func runCommand(argv []string) int {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "nova-slot:", err)
		return 127
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		for s := range sigs {
			// ignored: a process that has already ended needs no signal
			_ = cmd.Process.Signal(s)
		}
	}()
	err := cmd.Wait()
	signal.Stop(sigs)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nova-slot:", err)
		return 127
	}
	return 0
}
