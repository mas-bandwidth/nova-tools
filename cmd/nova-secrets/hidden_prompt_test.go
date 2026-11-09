//go:build functional && (darwin || linux)

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

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
