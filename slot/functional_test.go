//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The functional tests run the real binary on real locks. The commands it
// runs are this test binary again, as a child: NOVA_SLOT_CHILD=sleep writes
// its pid to NOVA_SLOT_PIDFILE when that is set, and sleeps; =exit7 exits 7.
func TestMain(m *testing.M) {
	switch os.Getenv("NOVA_SLOT_CHILD") {
	case "sleep":
		if p := os.Getenv("NOVA_SLOT_PIDFILE"); p != "" {
			_ = os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o644)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "exit7":
		os.Exit(7)
	}
	os.Exit(m.Run())
}

const patience = 20 * time.Second

func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-slot")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	require.NoError(t, err, "%s", out)
	return bin
}

func slotCmd(t *testing.T, bin, dir, child string, args ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(bin, append(append([]string{"--dir", dir}, args...), "--", self)...)
	cmd.Env = append(os.Environ(), "NOVA_SLOT_CHILD="+child)
	return cmd
}

// startHolder starts a sleeping holder and waits until status shows it.
func startHolder(t *testing.T, bin, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := slotCmd(t, bin, dir, "sleep", args...)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	require.Eventually(t, func() bool {
		out, _ := exec.Command(bin, "status", "--dir", dir).Output()
		return strings.Contains(string(out), "held pid="+strconv.Itoa(cmd.Process.Pid))
	}, patience, 50*time.Millisecond)
	return cmd
}

func TestKilledHolderFreesItsSlot(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	holder := startHolder(t, bin, dir, "--slots", "1")
	waiter := slotCmd(t, bin, dir, "exit7", "--slots", "1")
	require.NoError(t, waiter.Start())
	done := make(chan error, 1)
	go func() { done <- waiter.Wait() }()
	select {
	case <-done:
		t.Fatal("the waiter ran while the only slot was held")
	case <-time.After(2 * time.Second):
	}
	require.NoError(t, holder.Process.Kill()) // SIGKILL: nothing of nova-slot runs after it
	select {
	case err := <-done:
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee)
		assert.Equal(t, 7, ee.ExitCode(), "the command's status passes through")
	case <-time.After(patience):
		t.Fatal("the killed holder's slot was never freed")
	}
}

func TestExclusiveWaitsForSharedHolders(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	a := startHolder(t, bin, dir, "--slots", "3")
	x := slotCmd(t, bin, dir, "exit7", "--exclusive", "--wait", "2s")
	err := x.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 3, ee.ExitCode(), "no exclusive hold beside a shared one")
	require.NoError(t, a.Process.Kill())
	err = slotCmd(t, bin, dir, "exit7", "--exclusive", "--wait", "20s").Run()
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 7, ee.ExitCode())
}

func TestHoldEndsWhenStdinCloses(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	cmd := exec.Command(bin, "hold", "--slots", "1")
	cmd.Env = append(os.Environ(), "NOVA_SLOT_DIR="+dir)
	in, err := cmd.StdinPipe()
	require.NoError(t, err)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	buf := make([]byte, 5)
	_, err = out.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "HELD ", string(buf))
	status, _ := exec.Command(bin, "status", "--dir", dir).Output()
	assert.Contains(t, string(status), "slot1 held")
	require.NoError(t, in.Close())
	require.NoError(t, cmd.Wait())
	status, _ = exec.Command(bin, "status", "--dir", dir).Output()
	assert.Contains(t, string(status), "slot1 free")
}

// TestKilledHolderTakesItsCommandWith SIGKILLs nova-slot itself: its command
// must not run on without the slot (Linux: Pdeathsig; Windows: the job), and
// the slot frees. On macOS and the BSDs the command outlives a SIGKILL, so
// only the slot is checked there.
func TestKilledHolderTakesItsCommandWith(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	holder := slotCmd(t, bin, dir, "sleep", "--slots", "1")
	holder.Env = append(holder.Env, "NOVA_SLOT_PIDFILE="+pidfile)
	require.NoError(t, holder.Start())
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	var child int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidfile)
		child, _ = strconv.Atoi(string(b))
		return err == nil && child > 0
	}, patience, 50*time.Millisecond)
	require.True(t, alive(child), "the command runs while nova-slot holds its slot")
	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait()
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		assert.Eventually(t, func() bool { return !alive(child) }, patience, 50*time.Millisecond,
			"the command outlived nova-slot")
	}
	assert.Eventually(t, func() bool {
		out, _ := exec.Command(bin, "status", "--dir", dir).Output()
		return strings.Contains(string(out), "slot1 free")
	}, patience, 50*time.Millisecond)
}
