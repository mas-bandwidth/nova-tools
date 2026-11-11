//go:build functional && (linux || darwin)

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// onTerminal starts name with args as the session leader of a new
// pseudo-terminal, in its foreground, as a login gives a shell. It returns
// the master, whose writes are typed, a check that the output so far contains
// a string, a count of a string in the output so far, and the process's exit.
func onTerminal(t *testing.T, name string, args ...string) (*os.File, func(string) func() bool, func(string) int, <-chan error) {
	t.Helper()
	master, slavePath, err := openPty()
	require.NoError(t, err)
	t.Cleanup(func() { _ = master.Close() })
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	require.NoError(t, err)
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	require.NoError(t, cmd.Start())
	require.NoError(t, slave.Close())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	var mu sync.Mutex
	var out strings.Builder
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if t.Failed() {
			mu.Lock()
			defer mu.Unlock()
			t.Logf("terminal output: %q", out.String())
		}
	})
	count := func(s string) int { mu.Lock(); defer mu.Unlock(); return strings.Count(out.String(), s) }
	seen := func(s string) func() bool {
		return func() bool { return count(s) > 0 }
	}
	return master, seen, count, done
}

// foreground is the terminal's foreground process group, read off the master.
func foreground(t *testing.T, master *os.File) int {
	t.Helper()
	pgrp, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPGRP)
	require.NoError(t, err)
	return pgrp
}

// TestFunctionalCommandReadsTheTerminal runs nova-reserve as an interactive
// shell would, in the foreground of its own terminal, with a command that
// reads that terminal. A command in a background process group is stopped
// by SIGTTIN on its first read and holds its slot until SIGKILL.
func TestFunctionalCommandReadsTheTerminal(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	master, seen, _, done := onTerminal(t, bin, "--dir", dir, "--slots", "1", "--", "sh", "-c", "read x; echo got:$x")
	require.Eventually(t, seen("HELD mode=shared slot=1/1"), patience, 20*time.Millisecond)
	_, err := master.Write([]byte("hi\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the command never read the terminal (stopped, holding its slot)")
	}
	require.Eventually(t, seen("got:hi"), patience, 20*time.Millisecond)
}

// TestFunctionalCtrlZStopsTheJob is job control from an interactive shell:
// Ctrl-Z stops the command, the shell gets the terminal back and says the job
// stopped, and fg gives the command the terminal again.
func TestFunctionalCtrlZStopsTheJob(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to drive job control")
	}
	bin, dir := build(t), t.TempDir()
	master, seen, _, done := onTerminal(t, bash, "--norc", "--noprofile", "-i")
	typed := func(s string) {
		_, err := master.Write([]byte(s))
		require.NoError(t, err)
	}
	typed(shellQuote(bin) + " --dir " + shellQuote(dir) + " --slots 1 -- sh -c 'echo re\"\"ady; read x; echo got:$x'\n")
	require.Eventually(t, seen("ready"), patience, 20*time.Millisecond)
	command := foreground(t, master) // started in the foreground: its own group
	typed("\x1a")
	require.Eventually(t, seen("Stopped"), 10*time.Second, 20*time.Millisecond, "Ctrl-Z never gave the shell its terminal back")
	typed("fg\n")
	require.Eventually(t, func() bool { return foreground(t, master) == command }, 10*time.Second, 20*time.Millisecond,
		"fg never gave the command the terminal")
	typed("hi\n")
	require.Eventually(t, seen("got:hi"), 10*time.Second, 20*time.Millisecond, "fg never gave the command the terminal")
	typed("echo status=$?; exit\n")
	require.Eventually(t, seen("status=0"), 10*time.Second, 20*time.Millisecond)
	select {
	case <-done:
	case <-time.After(patience):
		t.Fatal("the shell never exited")
	}
}

// TestFunctionalBackgroundedWhileWaitingStillRelays: a run put in the
// background (Ctrl-Z, bg) while it waits for a slot starts its command without
// the terminal; fg later, the command's first read stops it, and nova-reserve
// must still hand it the terminal instead of leaving the shell waiting.
func TestFunctionalBackgroundedWhileWaitingStillRelays(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to drive job control")
	}
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("no ps to read the job's process state")
	}
	bin, dir := build(t), t.TempDir()
	holder := startHolder(t, bin, dir, "--slots", "1") // the slot is held
	// -b: the shell reports a job's change of state when it records it, not
	// at its next prompt, so its report shows its job table has the stop.
	master, seen, count, done := onTerminal(t, bash, "--norc", "--noprofile", "-i", "-b")
	typed := func(s string) {
		_, err := master.Write([]byte(s))
		require.NoError(t, err)
	}
	ps := func(field string, pid int) string {
		out, _ := exec.Command("ps", "-o", field+"=", "-p", strconv.Itoa(pid)).Output()
		return strings.TrimSpace(string(out))
	}
	shell := foreground(t, master) // the session leader's group, foreground from the start
	typed(shellQuote(bin) + " --dir " + shellQuote(dir) + " --slots 1 -- sh -c 'echo re\"\"ady; read x; echo got:$x'\n")
	// Ctrl-Z only once the job's group is nova-reserve itself: before its exec
	// the shell's child may still ignore SIGTSTP. One command, so the job's
	// group is nova-reserve's pid. It waits there, as the slot is held.
	reserve := 0
	require.Eventually(t, func() bool {
		reserve = foreground(t, master)
		return reserve != shell && strings.HasSuffix(ps("comm", reserve), "nova-reserve")
	}, patience, 20*time.Millisecond, "the shell never ran nova-reserve in the foreground")
	typed("\x1a")
	require.Eventually(t, seen("Stopped"), 10*time.Second, 20*time.Millisecond)
	typed("bg\n")
	require.NoError(t, holder.Process.Kill()) // SIGKILL: the kernel frees its slot
	require.Eventually(t, seen("ready"), patience, 20*time.Millisecond, "the backgrounded run never started its command")
	// fg only once the command's read has stopped it, nova-reserve has
	// relayed the stop by stopping itself, and the shell has recorded it: the
	// shell's second "Stopped", the first being Ctrl-Z's. An fg before that
	// reaches a shell whose job table says running, which does not continue
	// the job (tla/JobControl.tla, not modelled: fg of a job bg left running).
	require.Eventually(t, func() bool { return count("Stopped") >= 2 }, patience, 20*time.Millisecond,
		"the shell never reported the job stopped when its backgrounded command read the terminal")
	typed("fg\n")
	require.Eventually(t, func() bool { g := foreground(t, master); return g != shell && g != reserve }, 10*time.Second, 20*time.Millisecond,
		"fg never gave the command the terminal")
	typed("hi\n")
	require.Eventually(t, seen("got:hi"), 10*time.Second, 20*time.Millisecond, "fg never gave the command the terminal")
	typed("exit\n")
	select {
	case <-done:
	case <-time.After(patience):
		t.Fatal("the shell never exited")
	}
}
