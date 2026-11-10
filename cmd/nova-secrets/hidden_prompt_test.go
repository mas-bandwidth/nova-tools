//go:build functional && (darwin || linux)

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A line pasted at set's hidden prompt is set whole at any length up to the maximum, and one byte over is refused:
// never cut short, never left waiting. The terminal's modes are put back either way.
func TestFunctionalALongPasteAtTheHiddenPromptIsSetWhole(t *testing.T) {
	t.Parallel()
	for _, n := range []int{3000, 20000, maxValue, maxValue + 1} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()
			master, slave, err := openPty()
			if err != nil {
				t.Skipf("no pseudo-terminal: %v", err)
			}
			defer func() { _, _ = master.Close(), slave.Close() }() // ignored: test cleanup
			go func() { _, _ = io.Copy(io.Discard, master) }()      // ignored: drains what the terminal writes until the master is closed
			modes := func() (echo, canon bool) {
				tio, err := unix.IoctlGetTermios(int(master.Fd()), getTermios)
				require.NoError(t, err)
				return tio.Lflag&unix.ECHO != 0, tio.Lflag&unix.ICANON != 0
			}
			h := newHarness(t)
			h.in = slave
			done := make(chan int, 1)
			go func() { done <- h.main(h.set("NEW")...) }()
			for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) { // a guess: set reaches its prompt at once
				if echo, _ := modes(); !echo {
					break
				}
				require.True(t, time.Now().Before(deadline), "set never turned echo off at its prompt")
			}
			value := bytes.Repeat([]byte("0123456789abcdef"), n/16+1)[:n] // made up here: no secret
			go func() { _, _ = master.Write(append(value, '\r')) }()      // ignored: a write the terminal never takes is the failure below
			select {
			case code := <-done:
				if n > maxValue {
					assert.Equal(t, 2, code)
					assert.Contains(t, h.stderr.String(), "nova-secrets set: value is over 65536 bytes; a secret is a key or token; run: nova-secrets set -h\n")
					assert.NoFileExists(t, filepath.Join(h.store, "ada.yaml"), "nothing is written")
				} else {
					require.Equal(t, 0, code, h.stderr.String())
					got := h.open("ada")["NEW"]
					assert.True(t, got == string(value), "pasted %d bytes, set %d", n, len(got))
				}
				echo, canon := modes()
				assert.True(t, echo && canon, "the terminal's echo and line mode are back")
			case <-time.After(20 * time.Second): // a guess: far over the time a paste takes
				t.Fatalf("set did not return within 20 s of a %d-byte line and enter", n)
			}
		})
	}
}

// Ctrl-C at set's hidden prompt puts the terminal's echo back before the process ends by the signal.
func TestFunctionalCtrlCAtTheHiddenPromptPutsEchoBack(t *testing.T) {
	t.Parallel()
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	defer func() { _, _ = master.Close(), slave.Close() }() // ignored: test cleanup
	dir := t.TempDir()
	bin := filepath.Join(dir, "nova-secrets")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	require.NoError(t, build.Run())
	tools := filepath.Join(dir, "tools") // set asks for its value only when it could finish: stand-ins that answer yes
	require.NoError(t, os.Mkdir(tools, 0o755))
	for _, name := range []string{"sops", "age-keygen", "git"} {
		require.NoError(t, os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	cmd := exec.Command(bin, "set", "X") // a fresh node: the prompt comes before anything is made
	cmd.Env = []string{"PATH=" + tools, "HOME=" + dir, "NOVA_SECRETS_SEAT=ada"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0} // the pty is its terminal: Ctrl-C signals it
	require.NoError(t, cmd.Start())
	go func() { _, _ = io.Copy(io.Discard, master) }() // ignored: drains the prompt until the master is closed
	echo := func() bool {                              // read through the master: the slave is revoked once its session leader exits (macOS)
		tio, err := unix.IoctlGetTermios(int(master.Fd()), getTermios)
		require.NoError(t, err)
		return tio.Lflag&unix.ECHO != 0
	}
	require.True(t, echo(), "a new terminal echoes")
	for deadline := time.Now().Add(20 * time.Second); echo(); time.Sleep(10 * time.Millisecond) { // a guess: the build is done, start is quick
		require.True(t, time.Now().Before(deadline), "set never turned echo off at its prompt")
	}
	_, err = master.Write([]byte{3}) // ^C, the terminal's interrupt character
	require.NoError(t, err)
	err = cmd.Wait()
	var ee *exec.ExitError
	require.True(t, errors.As(err, &ee), "%v", err)
	ws, ok := ee.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	assert.Equal(t, syscall.SIGINT, ws.Signal(), "it ends by the signal, as an interrupted command does")
	assert.True(t, echo(), "echo is back on after the interrupt")
}
