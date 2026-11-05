//go:build functional

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The functional tests run the real binary on real locks. The commands it
// runs are this test binary again, as a child: NOVA_RESERVE_CHILD=sleep writes
// its pid to NOVA_RESERVE_PIDFILE when that is set, and sleeps; =exit7 exits 7.
func TestMain(m *testing.M) {
	switch os.Getenv("NOVA_RESERVE_CHILD") {
	case "sleep":
		if p := os.Getenv("NOVA_RESERVE_PIDFILE"); p != "" {
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
	bin := filepath.Join(t.TempDir(), "nova-reserve")
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
	cmd.Env = append(os.Environ(), "NOVA_RESERVE_CHILD="+child)
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
	require.NoError(t, holder.Process.Kill()) // SIGKILL: nothing of nova-reserve runs after it
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
	assert.Equal(t, 1, ee.ExitCode(), "no exclusive hold beside a shared one")
	require.NoError(t, a.Process.Kill())
	err = slotCmd(t, bin, dir, "exit7", "--exclusive", "--wait", "20s").Run()
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 7, ee.ExitCode())
}

// TestHoldEndsOnSIGTERMNotStdin: a hold with stdin at /dev/null (an AI's tool
// call) stays held until SIGTERM, then says why it let go.
func TestHoldEndsOnSIGTERMNotStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGTERM to send on Windows")
	}
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	cmd := exec.Command(bin, "hold", "--slots", "1", "--why", "bench run", "--dir", dir)
	cmd.Stdin = nil // /dev/null
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	lines := bufio.NewScanner(out)
	require.True(t, lines.Scan())
	assert.Equal(t, "CAPACITY SET slots=1", lines.Text())
	require.True(t, lines.Scan())
	assert.True(t, strings.HasPrefix(lines.Text(), "HELD mode=shared slot=1/1 pid="), lines.Text())
	time.Sleep(time.Second) // stdin is at EOF all along; the hold must outlast it
	status, _ := exec.Command(bin, "status", "--dir", dir).Output()
	assert.Contains(t, string(status), "slot1 held pid="+strconv.Itoa(cmd.Process.Pid))
	assert.Contains(t, string(status), `why=bench\srun`)
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	require.True(t, lines.Scan())
	assert.True(t, strings.HasPrefix(lines.Text(), "RELEASED reason=signal signal=SIGTERM held_for="), lines.Text())
	require.NoError(t, cmd.Wait())
	status, _ = exec.Command(bin, "status", "--dir", dir).Output()
	assert.Contains(t, string(status), "slot1 free")
}

// TestOrphanedHoldEnds: a hold with no --for whose shell exits lets go.
func TestOrphanedHoldEnds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh on Windows")
	}
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	out := filepath.Join(t.TempDir(), "hold.out")
	require.NoError(t, exec.Command("sh", "-c", shellQuote(bin)+" hold --dir "+shellQuote(dir)+" >"+shellQuote(out)+" 2>&1 & sleep 0.5").Run())
	require.Eventually(t, func() bool {
		b, _ := os.ReadFile(out)
		return strings.Contains(string(b), "RELEASED reason=parent_exit held_for=")
	}, patience, 50*time.Millisecond)
	status, _ := exec.Command(bin, "status", "--dir", dir).Output()
	assert.Contains(t, string(status), "slot1 free")
}

func TestStatusDuringAnExclusiveRun(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	require.NoError(t, exec.Command(bin, "set", "--slots", "3", "--dir", dir).Run())
	x := startHolder(t, bin, dir, "--exclusive")
	var out []byte
	require.Eventually(t, func() bool {
		out, _ = exec.Command(bin, "status", "--dir", dir).Output()
		return strings.Contains(string(out), "all held pid="+strconv.Itoa(x.Process.Pid))
	}, patience, 50*time.Millisecond)
	assert.Contains(t, string(out), "free=0/3\n")
	assert.NotContains(t, string(out), "  ")
}

func TestHoldEndsAfterFor(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	out, err := exec.Command(bin, "hold", "--for", "300ms", "--dir", dir, "--slots", "2").Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), "\nRELEASED reason=for held_for=")
}

// TestTwoCapacitiesNeverBothRun is the per-caller bug on real locks: a
// --slots 1 holder, then a --slots 4 caller, which is refused and never runs.
func TestTwoCapacitiesNeverBothRun(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	startHolder(t, bin, dir, "--slots", "1")
	x := slotCmd(t, bin, dir, "exit7", "--slots", "4", "--wait", "2s")
	var stderr strings.Builder
	x.Stderr = &stderr
	err := x.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.ExitCode())
	assert.Contains(t, stderr.String(), "nova-reserve: this machine's capacity is 1 (set by pid ")
	assert.Contains(t, stderr.String(), "; you asked 4; run: nova-reserve set --slots 4 --dir ")
	assert.Equal(t, 1, strings.Count(stderr.String(), "\n"), "one line, nothing else on stderr")
}

// TestWaitIsABoundOnRealLocks: a 100 ms --wait on a held slot returns well inside a poll.
func TestWaitIsABoundOnRealLocks(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	startHolder(t, bin, dir, "--slots", "1")
	x := slotCmd(t, bin, dir, "exit7", "--wait", "100ms")
	var stderr strings.Builder
	x.Stderr = &stderr
	began := time.Now()
	err := x.Run()
	took := time.Since(began)
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode())
	assert.Less(t, took, 700*time.Millisecond)
	assert.Contains(t, stderr.String(), "nova-reserve: no slot within 100ms: slot1 held pid=")
}

func TestMissingCommandNeverHolds(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	cmd := exec.Command(bin, "--dir", filepath.Join(dir, "r"), "--", "no-such-command-anywhere")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 127, ee.ExitCode())
	assert.Equal(t, "nova-reserve: no-such-command-anywhere: command not found\n", string(out))
	assert.NoDirExists(t, filepath.Join(dir, "r"))
	if runtime.GOOS != "windows" {
		plain := filepath.Join(dir, "plain")
		require.NoError(t, os.WriteFile(plain, []byte("x"), 0o644))
		out, err = exec.Command(bin, "--dir", filepath.Join(dir, "r"), "--", plain).CombinedOutput()
		require.ErrorAs(t, err, &ee)
		assert.Equal(t, 126, ee.ExitCode(), "%s", out)
		assert.NoDirExists(t, filepath.Join(dir, "r"))
	}
}

func TestSetRefusedWhileHeldOnRealLocks(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	h := startHolder(t, bin, dir, "--slots", "2")
	out, err := exec.Command(bin, "set", "--slots", "3", "--dir", dir).CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode())
	assert.Contains(t, string(out), "nova-reserve set: capacity in use (slot1 held pid="+strconv.Itoa(h.Process.Pid))
	require.NoError(t, h.Process.Kill())
	_ = h.Wait()
	require.Eventually(t, func() bool {
		out, err := exec.Command(bin, "set", "--slots", "3", "--dir", dir).Output()
		return err == nil && string(out) == "CAPACITY SET slots=3\n"
	}, patience, 50*time.Millisecond)
	status, _ := exec.Command(bin, "status", "--dir", dir).Output()
	assert.Contains(t, string(status), "free=3/3\n")
}

// TestKilledHolderTakesItsCommandWith SIGKILLs nova-reserve itself: its command
// must not run on without the slot (Linux: Pdeathsig; Windows: the job), and
// the slot frees. On macOS and the BSDs the command outlives a SIGKILL, so
// only the slot is checked there.
func TestKilledHolderTakesItsCommandWith(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	holder := slotCmd(t, bin, dir, "sleep", "--slots", "1")
	holder.Env = append(holder.Env, "NOVA_RESERVE_PIDFILE="+pidfile)
	require.NoError(t, holder.Start())
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	var child int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidfile)
		child, _ = strconv.Atoi(string(b))
		return err == nil && child > 0
	}, patience, 50*time.Millisecond)
	require.True(t, alive(child), "the command runs while nova-reserve holds its slot")
	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait()
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		assert.Eventually(t, func() bool { return !alive(child) }, patience, 50*time.Millisecond,
			"the command outlived nova-reserve")
	}
	assert.Eventually(t, func() bool {
		out, _ := exec.Command(bin, "status", "--dir", dir).Output()
		return strings.Contains(string(out), "slot1 free")
	}, patience, 50*time.Millisecond)
}
