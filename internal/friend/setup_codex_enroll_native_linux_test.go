//go:build linux

package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/procgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func codexTestTerminal(t *testing.T) (*os.File, int) {
	t.Helper()
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, unix.Close(master)) })
	require.NoError(t, unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0))
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	require.NoError(t, err)
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, slave.Close()) })
	return slave, master
}

func TestNativeCodexConsentOutputIsTerminalOnlyAndStatusSwitchesBack(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home, config := filepath.Join(root, "home"), filepath.Join(root, "config")
	program := filepath.Join(root, "fake-codex")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\ncase \"$*\" in\n*--device-auth) printf 'synthetic-consent-out'; printf 'synthetic-consent-error' >&2 ;;\n*) printf 'synthetic-status-stdout'; printf 'Logged in using ChatGPT\\n' >&2 ;;\nesac\n"), 0700))
	o := SetupOptions{Harness: "codex", Friend: "worker", Profile: "account", Dir: filepath.Join(root, "work"), Home: home, ConfigDir: config, Program: program, ProfileOnly: true, DenySelf: []string{filepath.Join(root, "coordinator")}}
	runner := NativeSetupRunner()
	p, err := PlanSetup(o, runner.FS, func(o SetupOptions, fs SettingsFS) (SetupPlan, error) {
		return PlanCodexEnrollment(o, fs, "synthetic account", 30*time.Second)
	})
	require.NoError(t, err)
	terminal, master := codexTestTerminal(t)
	ctx, err := CodexConsentContext(context.Background(), terminal)
	require.NoError(t, err)
	result, err := runner.Apply(ctx, p, filepath.Join(config, ".nova-friend-setup"))
	require.NoError(t, err)
	buffer := make([]byte, 4096)
	n, err := unix.Read(master, buffer)
	require.NoError(t, err)
	display := string(buffer[:n])
	assert.Contains(t, display, "synthetic-consent-out")
	assert.Contains(t, display, "synthetic-consent-error")
	assert.NotContains(t, display, "synthetic-status-stdout")
	assert.NotContains(t, display, "Logged in using ChatGPT")
	for _, fact := range result.Facts {
		assert.NotContains(t, fact.Detail, "synthetic-consent")
	}
	for _, name := range []string{"RUN", "PROFILE"} {
		raw, err := os.ReadFile(filepath.Join(config, ".nova-friend-setup", name))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "synthetic-consent")
	}
	_, err = ReadSetupProfile(runner.FS, SetupProfilePath(config))
	require.NoError(t, err)
}

func TestEnrollmentTransportCancellationReapsResistantFakeDescendants(t *testing.T) {
	t.Parallel()
	terminal, _ := codexTestTerminal(t)
	root := t.TempDir()
	pidPath := filepath.Join(root, "pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := "trap '' TERM; (trap '' TERM; sleep 60) & printf '%s' \"$$\" > '" + pidPath + "'; wait"
	done := make(chan error, 1)
	receiptRoot := t.TempDir()
	go func() {
		done <- codexEnrollmentTransport(ctx, terminal, "/bin/sh", []string{"-c", script}, time.Minute, receiptRoot)
	}()
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(pidPath)
		if err != nil {
			return false
		}
		var pid int
		_, err = fmt.Sscan(string(raw), &pid)
		return err == nil && pid > 0
	}, 30*time.Second, 10*time.Millisecond, "fake child must publish its own marker before cancellation")
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		require.FailNow(t, "transport cancellation did not finish verified cleanup")
	}
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "trap")
	raw, readErr := os.ReadFile(pidPath)
	require.NoError(t, readErr)
	var pid int
	_, readErr = fmt.Sscan(string(raw), &pid)
	require.NoError(t, readErr)
	require.Eventually(t, func() bool { return !procgroup.GroupRunnable(pid) }, 10*time.Second, 10*time.Millisecond, "transport must reap the resistant native helper group, not only its leader")
}

func TestCodexEnrollmentTransportRefusesNonTerminalBeforeChild(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "not-terminal")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	marker := filepath.Join(t.TempDir(), "must-not-launch")
	err = codexEnrollmentTransport(context.Background(), file, "/usr/bin/touch", []string{marker}, time.Minute, t.TempDir())
	require.ErrorContains(t, err, "terminal")
	_, err = os.Stat(marker)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestCodexNativeLogPathRefusesSymlink(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	o := codexSetupOptions()
	fs.Symlink("/outside", filepath.Join(o.ConfigDir, "log"))
	_, err := PlanCodexEnrollment(o, fs, "account", time.Minute)
	require.Error(t, err)
}
