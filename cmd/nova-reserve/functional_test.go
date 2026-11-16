//go:build functional

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	case "cpus":
		fmt.Printf("%d %s %s\n", runtime.NumCPU(), os.Getenv("NOVA_RESERVE_CPUS"), os.Getenv("GOMAXPROCS"))
		os.Exit(0)
	case "append":
		f, err := os.OpenFile(os.Getenv("NOVA_RESERVE_ORDER"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = fmt.Fprintln(f, os.Getenv("NOVA_RESERVE_TAG"))
			_ = f.Close()
		}
		os.Exit(0)
	case "waitfile":
		for {
			if _, err := os.Stat(os.Getenv("NOVA_RESERVE_DONE")); err == nil {
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

const patience = 20 * time.Second

func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-reserve")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	require.NoError(t, err, "%s", out)
	return bin
}

func cleanChildEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NOVA_RESERVE_DIR=") {
			out = append(out, kv)
		}
	}
	return out
}

func initCap(t *testing.T, bin, dir string, cpu, mem int) {
	t.Helper()
	cmd := exec.Command(bin, "set", "--dir", dir, "--cpu", strconv.Itoa(cpu), "--memory", strconv.Itoa(mem))
	cmd.Env = cleanChildEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func holdCmd(t *testing.T, bin, dir, child string, args ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(bin, append(append([]string{"--dir", dir}, args...), "--", self)...)
	cmd.Env = append(cleanChildEnv(), "NOVA_RESERVE_CHILD="+child)
	return cmd
}

func startHolder(t *testing.T, bin, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := holdCmd(t, bin, dir, "sleep", args...)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "pid="+strconv.Itoa(cmd.Process.Pid))
	}, patience, 20*time.Millisecond)
	return cmd
}

func TestFunctionalKilledHolderFreesResourcesFast(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	holder := startHolder(t, bin, dir, "--needs", "cpu 1, memory 1")

	waiter := exec.Command(bin, "hold", "--dir", dir, "--needs", "cpu 1, memory 1")
	waiter.Env = cleanChildEnv()
	waiter.Stdin = strings.NewReader("")
	var waiterOut strings.Builder
	waiter.Stdout = &waiterOut
	require.NoError(t, waiter.Start())
	done := make(chan error, 1)
	go func() { done <- waiter.Wait() }()

	// Wait until waiter is registered in line/.
	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "waiting=1")
	}, patience, 20*time.Millisecond)

	killAt := time.Now()
	require.NoError(t, holder.Process.Kill()) // SIGKILL: kernel closes FD, firing inotify/kqueue
	select {
	case err := <-done:
		took := time.Since(killAt)
		require.NoError(t, err)
		assert.Contains(t, waiterOut.String(), "HELD cpu=1 memory=1")
		assert.Less(t, took, 600*time.Millisecond, "push wakeup should resume waiter well under 1s poll")
	case <-time.After(patience):
		t.Fatal("waiter never woke after holder SIGKILL")
	}

	// Verify child exit status passthrough once resources are free.
	var ee *exec.ExitError
	require.ErrorAs(t, holdCmd(t, bin, dir, "exit7", "--needs", "cpu 1, memory 1").Run(), &ee)
	assert.Equal(t, 7, ee.ExitCode())
}

func TestFunctionalCIDisplacesAndContinuingConsumerBeatsWaiter(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 2, 4)

	// 1. Consumer card-1@1 Step 1 takes cpu 2, memory 4.
	doneFile := filepath.Join(t.TempDir(), "step1.done")
	step1 := holdCmd(t, bin, dir, "waitfile", "--holder", "bench", "--consumer", "card-1@1", "--priority", "high", "--needs", "cpu 2, memory 4")
	step1.Env = append(step1.Env, "NOVA_RESERVE_DONE="+doneFile)
	require.NoError(t, step1.Start())
	t.Cleanup(func() { _ = step1.Process.Kill(); _ = step1.Wait() })
	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "pid="+strconv.Itoa(step1.Process.Pid))
	}, patience, 20*time.Millisecond)

	// 2. A queued waiter enters line/ asking for cpu 2, memory 4.
	orderFile := filepath.Join(t.TempDir(), "order.txt")
	waiter := holdCmd(t, bin, dir, "append", "--holder", "friend", "--needs", "cpu 2, memory 4")
	waiter.Env = append(waiter.Env, "NOVA_RESERVE_ORDER="+orderFile, "NOVA_RESERVE_TAG=waiter")
	require.NoError(t, waiter.Start())
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- waiter.Wait() }()
	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "waiting=1")
	}, patience, 20*time.Millisecond)

	// 3. CI arrives via `nova-reserve hold --holder ci` and acquires immediately, pausing step1!
	ci := exec.Command(bin, "hold", "--dir", dir, "--holder", "ci", "--needs", "cpu 2, memory 4")
	ci.Env = cleanChildEnv()
	ciStdin, err := ci.StdinPipe()
	require.NoError(t, err)
	ciStdout, err := ci.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, ci.Start())
	t.Cleanup(func() { _ = ciStdin.Close(); _ = ci.Process.Kill(); _ = ci.Wait() })

	rd := bufio.NewReader(ciStdout)
	line, err := rd.ReadString('\n')
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(line, "HELD cpu=2 memory=4"), line)

	// Verify status --json shows card-1@1 paused behind CI.
	stCmd := exec.Command(bin, "status", "--json", "--dir", dir)
	stCmd.Env = cleanChildEnv()
	stOut, err := stCmd.Output()
	require.NoError(t, err)
	var sj statusJSON
	require.NoError(t, json.Unmarshal(stOut, &sj))
	assert.True(t, sj.AnyPaused)
	assert.Equal(t, []string{"card-1@1"}, sj.PausedConsumers)

	// 4. Step 1 finishes cleanly while paused behind CI.
	require.NoError(t, os.WriteFile(doneFile, []byte("ok\n"), 0o644))
	require.NoError(t, step1.Wait())

	// 5. Step 2 of card-1@1 starts while CI still holds -> blocks between commands!
	step2 := holdCmd(t, bin, dir, "append", "--holder", "bench", "--consumer", "card-1@1", "--priority", "high", "--needs", "cpu 2, memory 4")
	step2.Env = append(step2.Env, "NOVA_RESERVE_ORDER="+orderFile, "NOVA_RESERVE_TAG=step2")
	require.NoError(t, step2.Start())
	step2Done := make(chan error, 1)
	go func() { step2Done <- step2.Wait() }()

	// Wait until step2 attaches to its paused hold on disk.
	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "pid="+strconv.Itoa(step2.Process.Pid))
	}, patience, 20*time.Millisecond)
	select {
	case <-step2Done:
		t.Fatal("step2 must not run while CI still holds")
	default:
	}

	// 6. Close CI stdin -> CI releases cleanly -> step2 MUST run BEFORE waiter!
	require.NoError(t, ciStdin.Close())
	require.NoError(t, ci.Wait())
	require.NoError(t, <-step2Done)
	require.NoError(t, <-waiterDone)

	orderBytes, err := os.ReadFile(orderFile)
	require.NoError(t, err)
	assert.Equal(t, "step2\nwaiter\n", string(orderBytes), "continuing consumer must resume ahead of queued waiter")
}

func TestFunctionalExclusiveAndLiveSetOnRealLocks(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 3, 6)
	a := startHolder(t, bin, dir)

	// Live set succeeds even while a hold is active!
	setCmd := exec.Command(bin, "set", "--dir", dir, "--cpu", "4", "--tags", "gpu")
	setCmd.Env = cleanChildEnv()
	out, err := setCmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "CAPACITY SET cpu=4 memory=6")

	// Exclusive hold times out while shared hold runs.
	x := holdCmd(t, bin, dir, "exit7", "--exclusive", "--wait", "200ms")
	err = x.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode())

	require.NoError(t, a.Process.Kill())
	_ = a.Wait()
	err = holdCmd(t, bin, dir, "exit7", "--exclusive", "--wait", "5s").Run()
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 7, ee.ExitCode())
}

func TestFunctionalReentrantNestedRunDoesNotDeadlock(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 2, 4)
	cmd := exec.Command(bin, "--dir", dir, "--exclusive", "--", bin, "--dir", dir, "--exclusive", "--nowait", "--", "sh", "-c", "exit 7")
	cmd.Env = cleanChildEnv()
	err := cmd.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 7, ee.ExitCode())
}

func TestFunctionalMeasureWritesCapacityJSON(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	cmd := exec.Command(bin, "measure", "--force", "--dir", dir)
	cmd.Env = cleanChildEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "MEASURE OK cpu=")
	c, have, err := readCapacity(dir)
	require.NoError(t, err)
	require.True(t, have)
	assert.GreaterOrEqual(t, c.Resources["cpu"], 1)
	assert.GreaterOrEqual(t, c.Resources["memory"], 1)
}

func TestFunctionalWaitIsABoundOnRealLocks(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	startHolder(t, bin, dir)
	x := holdCmd(t, bin, dir, "exit7", "--wait", "100ms")
	var stderr strings.Builder
	x.Stderr = &stderr
	began := time.Now()
	err := x.Run()
	took := time.Since(began)
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode())
	assert.Less(t, took, 700*time.Millisecond)
	assert.Contains(t, stderr.String(), "nova-reserve: no hold within 100ms:")
}

func TestFunctionalMissingCommandNeverHolds(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	cmd := exec.Command(bin, "--dir", filepath.Join(dir, "r"), "--", "no-such-command-anywhere")
	cmd.Env = cleanChildEnv()
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 127, ee.ExitCode())
	assert.Contains(t, string(out), "no-such-command-anywhere: command not found on PATH=")
	assert.NoDirExists(t, filepath.Join(dir, "r"))

	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.WriteFile(plain, []byte("x"), 0o644))
	cmd2 := exec.Command(bin, "--dir", filepath.Join(dir, "r"), "--", plain)
	cmd2.Env = cleanChildEnv()
	out, err = cmd2.CombinedOutput()
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 126, ee.ExitCode(), "%s", out)
	assert.NoDirExists(t, filepath.Join(dir, "r"))
}

func TestFunctionalKilledHolderTakesItsCommandWith(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	holder := holdCmd(t, bin, dir, "sleep")
	holder.Env = append(holder.Env, "NOVA_RESERVE_PIDFILE="+pidfile)
	require.NoError(t, holder.Start())
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	var child int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidfile)
		child, _ = strconv.Atoi(string(b))
		return err == nil && child > 0
	}, patience, 30*time.Millisecond)
	require.True(t, alive(child))
	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait()
	assert.Eventually(t, func() bool { return !alive(child) }, 3*time.Second, 50*time.Millisecond)
}

func TestFunctionalKilledHolderTakesItsWholeGroupWith(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	self, err := os.Executable()
	require.NoError(t, err)
	pidfile := filepath.Join(t.TempDir(), "job.pid")
	holder := exec.Command(bin, "--dir", dir, "--", "sh", "-c", `"$0" & wait`, self)
	holder.Env = append(cleanChildEnv(), "NOVA_RESERVE_CHILD=sleep", "NOVA_RESERVE_PIDFILE="+pidfile)
	require.NoError(t, holder.Start())
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	var job int
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidfile)
		job, _ = strconv.Atoi(string(b))
		return err == nil && job > 0
	}, patience, 30*time.Millisecond)
	t.Cleanup(func() { _ = syscall.Kill(job, syscall.SIGKILL) })
	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait()
	assert.Eventually(t, func() bool { return !alive(job) }, 3*time.Second, 50*time.Millisecond)
}

func TestFunctionalSignalledCommandIsNamed(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	cmd := exec.Command(bin, "--dir", dir, "--", "sh", "-c", "kill -TERM $$")
	cmd.Env = cleanChildEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 143, ee.ExitCode())
	assert.True(t, strings.HasSuffix(stderr.String(), "\nnova-reserve: sh ended by signal SIGTERM; exit 143\n"), stderr.String())
}
