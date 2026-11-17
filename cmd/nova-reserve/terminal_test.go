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

func onTerminal(t *testing.T, name string, args ...string) (*os.File, func(string) func() bool, func(string) int, <-chan error) {
	t.Helper()
	master, slavePath, err := openPty()
	require.NoError(t, err)
	t.Cleanup(func() { _ = master.Close() })
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	require.NoError(t, err)
	cmd := exec.Command(name, args...)
	cmd.Env = cleanChildEnv()
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

func foreground(t *testing.T, master *os.File) int {
	t.Helper()
	pgrp, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPGRP)
	require.NoError(t, err)
	return pgrp
}

func TestFunctionalCommandReadsTheTerminal(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	master, seen, _, done := onTerminal(t, bin, "--dir", dir, "--", "sh", "-c", "read x; echo got:$x")
	require.Eventually(t, seen("cpu=1 memory=1"), patience, 20*time.Millisecond)
	_, err := master.Write([]byte("hi\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the command never read the terminal")
	}
	require.Eventually(t, seen("got:hi"), patience, 20*time.Millisecond)
}

func TestFunctionalCtrlZStopsTheJob(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to drive job control")
	}
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	master, seen, _, done := onTerminal(t, bash, "--norc", "--noprofile", "-i")
	typed := func(s string) {
		_, err := master.Write([]byte(s))
		require.NoError(t, err)
	}
	typed(shellQuote(bin) + " --dir " + shellQuote(dir) + " -- sh -c 'echo re\"\"ady; read x; echo got:$x'\n")
	require.Eventually(t, seen("ready"), patience, 20*time.Millisecond)
	command := foreground(t, master)
	typed("\x1a")
	require.Eventually(t, seen("Stopped"), 10*time.Second, 20*time.Millisecond)
	typed("fg\n")
	require.Eventually(t, func() bool { return foreground(t, master) == command }, 10*time.Second, 20*time.Millisecond)
	typed("hi\n")
	require.Eventually(t, seen("got:hi"), 10*time.Second, 20*time.Millisecond)
	typed("echo status=$?; exit\n")
	require.Eventually(t, seen("status=0"), 10*time.Second, 20*time.Millisecond)
	select {
	case <-done:
	case <-time.After(patience):
		t.Fatal("the shell never exited")
	}
}

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
	initCap(t, bin, dir, 1, 2)
	holder := startHolder(t, bin, dir, "--needs", "cpu 1, memory 2")
	master, seen, count, done := onTerminal(t, bash, "--norc", "--noprofile", "-i", "-b")
	typed := func(s string) {
		_, err := master.Write([]byte(s))
		require.NoError(t, err)
	}
	ps := func(field string, pid int) string {
		out, _ := exec.Command("ps", "-o", field+"=", "-p", strconv.Itoa(pid)).Output()
		return strings.TrimSpace(string(out))
	}
	shell := foreground(t, master)
	typed(shellQuote(bin) + " --dir " + shellQuote(dir) + " -- sh -c 'echo re\"\"ady; read x; echo got:$x'\n")
	reserve := 0
	require.Eventually(t, func() bool {
		reserve = foreground(t, master)
		return reserve != shell && strings.HasSuffix(ps("comm", reserve), "nova-reserve")
	}, patience, 20*time.Millisecond)
	typed("\x1a")
	require.Eventually(t, seen("Stopped"), 10*time.Second, 20*time.Millisecond)
	typed("bg\n")
	require.NoError(t, holder.Process.Kill())
	require.Eventually(t, seen("ready"), patience, 20*time.Millisecond)
	require.Eventually(t, func() bool { return count("Stopped") >= 2 }, patience, 20*time.Millisecond)
	typed("fg\n")
	require.Eventually(t, func() bool { g := foreground(t, master); return g != shell && g != reserve }, 10*time.Second, 20*time.Millisecond)
	typed("hi\n")
	require.Eventually(t, seen("got:hi"), 10*time.Second, 20*time.Millisecond)
	typed("exit\n")
	select {
	case <-done:
	case <-time.After(patience):
		t.Fatal("the shell never exited")
	}
}

// TestFunctionalCtrlZWhenSessionLeaderDoesNotSelfDeadlock: when nova-reserve
// runs directly as the session leader of a PTY (as ssh -t runs a remote
// command with no parent job-control shell), Ctrl-Z must not make nova-reserve
// SIGSTOP itself with nobody above it to send SIGCONT.
func TestFunctionalCtrlZWhenSessionLeaderDoesNotSelfDeadlock(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	master, seen, _, done := onTerminal(t, bin, "--dir", dir, "--", "sh", "-c", "echo re\"\"ady; read x; echo got:$x")
	require.Eventually(t, seen("ready"), patience, 20*time.Millisecond)
	_, err := master.Write([]byte("\x1a"))
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)
	_, err = master.Write([]byte("hi\n"))
	require.NoError(t, err)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("session-leader nova-reserve deadlocked on Ctrl-Z")
	}
	require.Eventually(t, seen("got:hi"), patience, 20*time.Millisecond)
}
