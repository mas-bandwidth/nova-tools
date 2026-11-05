// Command nova-reserve runs a command holding one of N load slots on this machine.
// The slots are OS file locks, so the kernel frees a dead holder's slot.
package main

import (
	"bufio"
	"cmp"
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

const usage = `nova-reserve: run a command holding one of N load slots on this machine.

  nova-reserve [--slots N] [--wait D] [--exclusive] [--dir DIR] -- <cmd> [args...]
      Wait for a free slot, run the command, free the slot when it exits; exit
      with its status. --exclusive runs alone: it waits for every holder and
      keeps new ones out. --wait bounds the wait (default none; past it, exit 3).
      The command dies with nova-reserve on Linux and Windows; on macOS and the
      BSDs a SIGKILL of nova-reserve leaves it running without its slot.
  nova-reserve hold [same flags]     take a slot, print HELD..., hold it until stdin closes (--dir ignored)
  nova-reserve status [--dir DIR]   each slot: free, or held by pid and command

N defaults to the CPU count / 4 (at least 1). DIR defaults to $NOVA_RESERVE_DIR,
else /tmp/nova-reserve (C:\ProgramData\nova-reserve on Windows); every caller on a
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
	dir       string
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
	held []func() // what release lets go of; an exclusive waiter's gate too
	slot int      // 0 until a shared hold; an exclusive hold names no slot
}

func (t *taker) path(name string) string { return filepath.Join(t.o.dir, name) }

// step makes one attempt without waiting; it reports true once held.
func (t *taker) step() (bool, error) {
	if t.o.exclusive {
		if len(t.held) == 0 {
			gate, ok, err := t.lock(t.path("gate"), true)
			if !ok || err != nil {
				return false, err
			}
			t.held = []func(){gate}
		}
		all, ok, err := t.lock(t.path("all"), true)
		if ok {
			t.held = append(t.held, all)
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
		if !ok || err != nil {
			if err != nil {
				return false, err
			}
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

// name is the hold's lock, whose <name>.holder file status reads: slot<k>, or all.
func (t *taker) name() string {
	return map[bool]string{false: "slot" + strconv.Itoa(t.slot), true: "all"}[t.o.exclusive]
}

func (t *taker) describe() string {
	mode := map[bool]string{false: "shared", true: "exclusive"}[t.o.exclusive]
	return fmt.Sprintf("mode=%s slot=%s/%d pid=%d", mode, strings.TrimPrefix(t.name(), "slot"), t.o.slots, os.Getpid())
}

func defaultDir(env func(string) string) string { return cmp.Or(env("NOVA_RESERVE_DIR"), systemDir) }

func parse(verb string, args []string, env func(string) string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("nova-reserve "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	o := options{}
	fs.IntVar(&o.slots, "slots", max(1, runtime.NumCPU()/4), "")
	fs.DurationVar(&o.wait, "wait", 0, "")
	fs.BoolVar(&o.exclusive, "exclusive", false, "")
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
		return o, fmt.Errorf("no command: nova-reserve [flags] -- <cmd> [args...]")
	case verb != "run" && len(o.argv) > 0:
		return o, fmt.Errorf("nova-reserve %s takes no command, not %q", verb, o.argv[0])
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
			fmt.Fprintln(w.stderr, "nova-reserve:", err)
		}
		return 2
	}
	if verb == "hold" {
		o.dir = defaultDir(w.env) // hold takes the machine's directory, never --dir
	}
	if err := os.MkdirAll(o.dir, 0o777); err != nil {
		return fail(w, err)
	}
	// ignored: only the directory's owner may widen it; others share it as it is
	_ = os.Chmod(o.dir, 0o777|os.ModeSticky)
	if verb == "status" {
		return status(o.dir, w)
	}
	t, err := acquire(w.lock, o, w.now, w.sleep)
	if err != nil {
		return fail(w, err)
	}
	defer t.release()
	holder := t.path(t.name() + ".holder")
	// ignored: the holder file is for status to read; the lock is the slot either way
	_ = os.Remove(holder)
	_ = os.WriteFile(holder, []byte(fmt.Sprintf("pid=%d start=%s cmd=%s\n",
		os.Getpid(), w.now().UTC().Format(time.RFC3339), strings.Join(o.argv, " "))), 0o666)
	// ignored: a leftover holder file names a pid whose lock is gone; status probes the lock
	defer os.Remove(holder)
	if verb == "hold" {
		fmt.Fprintln(w.stdout, "HELD", t.describe())
		// ignored: EOF or an error both mean the caller is gone, which ends the hold
		_, _ = io.Copy(io.Discard, w.stdin)
		return 0
	}
	fmt.Fprintln(w.stderr, "nova-reserve: HELD", t.describe())
	return w.exec(o.argv)
}

// status probes each lock in dir: a lock it can take is free.
func status(dir string, w world) int {
	names, err := filepath.Glob(filepath.Join(dir, "slot*"))
	if err != nil {
		return fail(w, err)
	}
	out := bufio.NewWriter(w.stdout)
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
	if err := out.Flush(); err != nil {
		return fail(w, err)
	}
	return 0
}

// fail is exit 3: the slot, its directory or its output could not be had.
func fail(w world, err error) int {
	fmt.Fprintln(w.stderr, "nova-reserve:", err)
	return 3
}

// runCommand runs argv with this process's stdio, tied to this process so it
// cannot outlive the slot (tie, per OS), forwards interrupts to it, and
// returns its exit status (128+signal when a signal ended it).
func runCommand(argv []string) int {
	cmd := exec.Command(argv[0], argv[1:]...)
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
