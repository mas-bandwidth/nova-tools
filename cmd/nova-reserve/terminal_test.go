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
// a string, and the process's exit.
func onTerminal(t *testing.T, name string, args ...string) (*os.File, func(string) func() bool, <-chan error) {
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
	seen := func(s string) func() bool {
		return func() bool { mu.Lock(); defer mu.Unlock(); return strings.Contains(out.String(), s) }
	}
	return master, seen, done
}

// TestFunctionalCommandReadsTheTerminal runs nova-reserve as an interactive
// shell would, in the foreground of its own terminal, with a command that
// reads that terminal. A command in a background process group is stopped
// by SIGTTIN on its first read and holds its slot until SIGKILL.
func TestFunctionalCommandReadsTheTerminal(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	master, seen, done := onTerminal(t, bin, "--dir", dir, "--slots", "1", "--", "sh", "-c", "read x; echo got:$x")
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
	master, seen, done := onTerminal(t, bash, "--norc", "--noprofile", "-i")
	typed := func(s string) {
		_, err := master.Write([]byte(s))
		require.NoError(t, err)
	}
	typed(shellQuote(bin) + " --dir " + shellQuote(dir) + " --slots 1 -- sh -c 'echo re\"\"ady; read x; echo got:$x'\n")
	require.Eventually(t, seen("ready"), patience, 20*time.Millisecond)
	typed("\x1a")
	require.Eventually(t, seen("Stopped"), 10*time.Second, 20*time.Millisecond, "Ctrl-Z never gave the shell its terminal back")
	typed("fg\n")
	time.Sleep(500 * time.Millisecond) // a guess: the shell continues the job before the line is typed
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
	master, seen, done := onTerminal(t, bash, "--norc", "--noprofile", "-i")
	typed := func(s string) {
		_, err := master.Write([]byte(s))
		require.NoError(t, err)
	}
	foreground := func() int {
		pgrp, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPGRP)
		require.NoError(t, err)
		return pgrp
	}
	ps := func(field string, pid int) string {
		out, _ := exec.Command("ps", "-o", field+"=", "-p", strconv.Itoa(pid)).Output()
		return strings.TrimSpace(string(out))
	}
	shell := foreground() // the session leader's group, foreground from the start
	typed(shellQuote(bin) + " --dir " + shellQuote(dir) + " --slots 1 -- sh -c 'echo re\"\"ady; read x; echo got:$x'\n")
	// Ctrl-Z only once the job's group is nova-reserve itself: before its exec
	// the shell's child may still ignore SIGTSTP. One command, so the job's
	// group is nova-reserve's pid. It waits there, as the slot is held.
	reserve := 0
	require.Eventually(t, func() bool {
		reserve = foreground()
		return reserve != shell && strings.HasSuffix(ps("comm", reserve), "nova-reserve")
	}, patience, 20*time.Millisecond, "the shell never ran nova-reserve in the foreground")
	typed("\x1a")
	require.Eventually(t, seen("Stopped"), 10*time.Second, 20*time.Millisecond)
	typed("bg\n")
	require.NoError(t, holder.Process.Kill()) // SIGKILL: the kernel frees its slot
	require.Eventually(t, seen("ready"), patience, 20*time.Millisecond, "the backgrounded run never started its command")
	// fg only once the command's read has stopped it and nova-reserve has
	// relayed the stop by stopping itself. An fg before that continues a job
	// that is still running, which takes the terminal to nova-reserve and not
	// the command; the command's read then stops the job again
	// (tla/JobControl.tla, not modelled: fg of a job bg left running). The
	// shell's SIGCHLD is queued as nova-reserve stops, so the fg line reaches
	// a shell that already knows the job is stopped and continues it.
	require.Eventually(t, func() bool { return strings.HasPrefix(ps("stat", reserve), "T") }, patience, 20*time.Millisecond,
		"nova-reserve never stopped when its backgrounded command read the terminal")
	typed("fg\n")
	require.Eventually(t, func() bool { g := foreground(); return g != shell && g != reserve }, 10*time.Second, 20*time.Millisecond,
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
