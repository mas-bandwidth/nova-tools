//go:build functional

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
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
	case "scrub_sublease":
		// Scrub NOVA_RESERVE_LEASE and NOVA_RESERVE_HOST to prove OS PPID-chain discovery!
		var env []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "NOVA_RESERVE_LEASE=") && !strings.HasPrefix(kv, "NOVA_RESERVE_HOST=") && !strings.HasPrefix(kv, "NOVA_RESERVE_CHILD=") {
				env = append(env, kv)
			}
		}
		bin := os.Getenv("NOVA_RESERVE_TEST_BIN")
		dir := os.Getenv("NOVA_RESERVE_TEST_DIR")
		c := exec.Command(bin, "run", "--dir", dir, "--needs", "cpu 1, memory 2", "--nowait", "--", "true")
		c.Env = env
		out, err := c.CombinedOutput()
		if err != nil {
			fmt.Fprintln(os.Stderr, string(out))
			os.Exit(1)
		}
		fmt.Print(string(out))
		os.Exit(0)
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
		if !strings.HasPrefix(kv, "NOVA_RESERVE_") {
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

func runChildCmd(t *testing.T, bin, dir, child string, args ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(bin, append(append([]string{"--dir", dir}, args...), "--", self)...)
	cmd.Env = append(cleanChildEnv(), "NOVA_RESERVE_CHILD="+child)
	return cmd
}

func startHolder(t *testing.T, bin, dir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := runChildCmd(t, bin, dir, "sleep", args...)
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

func startHoldPipe(t *testing.T, bin string, env []string, args ...string) (*exec.Cmd, func(), string) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"hold"}, args...)...)
	cmd.Env = env
	inPipe, err := cmd.StdinPipe()
	require.NoError(t, err)
	outPipe, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var se strings.Builder
	cmd.Stderr = &se
	require.NoError(t, cmd.Start())
	stop := func() {
		_ = inPipe.Close()
		_ = cmd.Wait()
	}
	t.Cleanup(func() { _ = inPipe.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	rd := bufio.NewReader(outPipe)
	line, err := rd.ReadString('\n')
	require.NoError(t, err, "hold failed: %s", se.String())
	return cmd, stop, strings.TrimSpace(line)
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

	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "queued=1")
	}, patience, 20*time.Millisecond)

	killAt := time.Now()
	require.NoError(t, holder.Process.Kill())
	select {
	case err := <-done:
		took := time.Since(killAt)
		require.NoError(t, err)
		assert.Contains(t, waiterOut.String(), "cpu=1 memory=1")
		assert.Less(t, took, 800*time.Millisecond, "socket disconnect should wake waiter immediately")
	case <-time.After(patience):
		t.Fatal("waiter never woke after holder SIGKILL")
	}

	var ee *exec.ExitError
	require.ErrorAs(t, runChildCmd(t, bin, dir, "exit7", "--needs", "cpu 1, memory 1").Run(), &ee)
	assert.Equal(t, 7, ee.ExitCode())
}

func TestFunctionalCIDisplacesAndPausedParentSubstepBeatsWaiter(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 2, 4)

	// 1. Outer parent lease holds cpu 2, memory 4.
	_, stopOuter, heldLine := startHoldPipe(t, bin, cleanChildEnv(), "--dir", dir, "--holder", "bench", "--source", "worker-1", "--priority", "high", "--needs", "cpu 2, memory 4")
	defer stopOuter()
	var outerID string
	for _, tok := range strings.Fields(heldLine) {
		if v, ok := strings.CutPrefix(tok, "lease_id="); ok {
			outerID = v
		}
	}
	require.NotEmpty(t, outerID)

	// 2. Unrelated waiter queues asking for cpu 2, memory 4.
	orderFile := filepath.Join(t.TempDir(), "order.txt")
	waiter := runChildCmd(t, bin, dir, "append", "--holder", "friend", "--source", "alice", "--needs", "cpu 2, memory 4")
	waiter.Env = append(waiter.Env, "NOVA_RESERVE_ORDER="+orderFile, "NOVA_RESERVE_TAG=waiter")
	require.NoError(t, waiter.Start())
	waiterDone := make(chan error, 1)
	go func() { waiterDone <- waiter.Wait() }()
	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "queued=1")
	}, patience, 20*time.Millisecond)

	// 3. CI arrives and immediately pauses outerID!
	_, stopCI, ciLine := startHoldPipe(t, bin, cleanChildEnv(), "--dir", dir, "--holder", "ci", "--needs", "cpu 2, memory 4")
	assert.Contains(t, ciLine, "cpu=2 memory=4")

	// Verify status --json shows outerID paused.
	stOut, err := exec.Command(bin, "status", "--json", "--dir", dir).Output()
	require.NoError(t, err)
	var snap ServerFrame
	require.NoError(t, json.Unmarshal(stOut, &snap))
	require.Len(t, snap.Machines, 1)
	assert.True(t, strings.Contains(string(stOut), `"paused":true`))

	// 4. Inner step2 under outerID starts while paused -> yields at gate until CI releases!
	step2 := runChildCmd(t, bin, dir, "append", "--holder", "bench", "--source", "worker-1", "--needs", "cpu 1, memory 2")
	step2.Env = append(step2.Env, "NOVA_RESERVE_LEASE="+outerID, "NOVA_RESERVE_HOST=local", "NOVA_RESERVE_ORDER="+orderFile, "NOVA_RESERVE_TAG=step2")
	require.NoError(t, step2.Start())
	step2Done := make(chan error, 1)
	go func() { step2Done <- step2.Wait() }()

	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--dir", dir)
		c.Env = cleanChildEnv()
		out, _ := c.Output()
		return strings.Contains(string(out), "queued=2")
	}, patience, 20*time.Millisecond)

	// 5. Release CI -> outerID unpauses and step2 runs IMMEDIATELY as sublease while waiter stays queued behind outerID; then release outerID -> waiter runs!
	stopCI()
	require.NoError(t, <-step2Done)
	stopOuter()
	require.NoError(t, <-waiterDone)

	orderBytes, err := os.ReadFile(orderFile)
	require.NoError(t, err)
	assert.Equal(t, "step2\nwaiter\n", string(orderBytes))
}

func TestFunctionalExclusiveAndLiveSet(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 3, 6)
	a := startHolder(t, bin, dir)

	setCmd := exec.Command(bin, "set", "--dir", dir, "--cpu", "4", "--tags", "gpu")
	setCmd.Env = cleanChildEnv()
	out, err := setCmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "CAPACITY SET cpu=4 memory=6")

	x := runChildCmd(t, bin, dir, "exit7", "--exclusive", "--wait", "200ms")
	err = x.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode())

	require.NoError(t, a.Process.Kill())
	_ = a.Wait()
	err = runChildCmd(t, bin, dir, "exit7", "--exclusive", "--wait", "5s").Run()
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 7, ee.ExitCode())
}

func TestFunctionalNestedSubleasePPIDWalkAndDeadlockRefusal(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 2, 4)

	// 1. Nested --exclusive inside --exclusive gets 0-debit sublease and passes child exit status 7.
	cmd := exec.Command(bin, "--dir", dir, "--exclusive", "--", bin, "--dir", dir, "--exclusive", "--nowait", "--", "sh", "-c", "exit 7")
	cmd.Env = cleanChildEnv()
	err := cmd.Run()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 7, ee.ExitCode())

	// 2. Env-scrubbed child recovers parent lease via OS PPID chain walk!
	self, err := os.Executable()
	require.NoError(t, err)
	ppidCmd := exec.Command(bin, "run", "--dir", dir, "--needs", "cpu 2, memory 4", "--", self)
	ppidCmd.Env = append(cleanChildEnv(), "NOVA_RESERVE_CHILD=scrub_sublease", "NOVA_RESERVE_TEST_BIN="+bin, "NOVA_RESERVE_TEST_DIR="+dir)
	ppidOut, err := ppidCmd.CombinedOutput()
	require.NoError(t, err, "%s", ppidOut)
	assert.Contains(t, string(ppidOut), "sublease=true")

	// 3. Nested child asking beyond parent's grant while parent blocks host refuses immediately with RESERVE DEADLOCK!
	dlCmd := exec.Command(bin, "--dir", dir, "--needs", "cpu 1, memory 2", "--", bin, "--dir", dir, "--needs", "cpu 2, memory 4", "--", "true")
	dlCmd.Env = cleanChildEnv()
	dlOut, err := dlCmd.CombinedOutput()
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode())
	assert.Contains(t, string(dlOut), "RESERVE DEADLOCK:")
}

func TestFunctionalBenchWritesCapacityJSON(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	cmd := exec.Command(bin, "bench", "--force", "--dir", dir)
	cmd.Env = cleanChildEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "BENCH OK cpu=")
	c, have, err := readCapacity(dir)
	require.NoError(t, err)
	require.True(t, have)
	assert.GreaterOrEqual(t, c.Resources["cpu"], 1)
	assert.GreaterOrEqual(t, c.Resources["memory"], 1)
}

func TestFunctionalWaitBound(t *testing.T) {
	t.Parallel()
	bin, dir := build(t), t.TempDir()
	initCap(t, bin, dir, 1, 2)
	startHolder(t, bin, dir)
	x := runChildCmd(t, bin, dir, "exit7", "--wait", "250ms")
	var stderr strings.Builder
	x.Stderr = &stderr
	began := time.Now()
	err := x.Run()
	took := time.Since(began)
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 1, ee.ExitCode())
	assert.Less(t, took, 2*time.Second)
	assert.Contains(t, stderr.String(), "no hold within 250ms:")
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
	holder := runChildCmd(t, bin, dir, "sleep")
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

func TestFunctionalTCPFleetAnyQueueAuthClaimsAndSubscribe(t *testing.T) {
	t.Parallel()
	bin := build(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	machines := []MachineSpec{
		{Host: "b1", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 4, Resources: map[string]int{"cpu": 2, "memory": 4}},
		{Host: "b2", Online: true, Tags: []string{"linux", "amd64"}, UsableCores: 4, Resources: map[string]int{"cpu": 2, "memory": 4}},
	}
	srv := NewServer(ServerConfig{Local: false, Password: "fleet-secret"}, machines)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx, ln) }()
	t.Cleanup(srv.Close)

	// Wrong password fails with exit 2.
	badCmd := exec.Command(bin, "status")
	badCmd.Env = append(cleanChildEnv(), "NOVA_RESERVE_ADDR="+addr, "NOVA_RESERVE_PASSWORD=wrong")
	badOut, err := badCmd.CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.ExitCode())
	assert.Contains(t, string(badOut), "authentication failed")

	env := append(cleanChildEnv(), "NOVA_RESERVE_ADDR="+addr, "NOVA_RESERVE_PASSWORD=fleet-secret")

	// Start subscriber stream and verify initial snapshot frame.
	subCmd := exec.Command(bin, "subscribe")
	subCmd.Env = env
	subOut, err := subCmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, subCmd.Start())
	t.Cleanup(func() { _ = subCmd.Process.Kill(); _ = subCmd.Wait() })
	subRd := bufio.NewReader(subOut)
	snapLine, err := subRd.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, snapLine, `"op":"snapshot"`)

	// Fill b1 and b2, then queue fleet-any waiter w3; release b2 -> w3 lands on b2!
	_, stop1, h1 := startHoldPipe(t, bin, env, "--host", "b1", "--needs", "cpu 2, memory 4", "--holder", "bench", "--source", "w1")
	defer stop1()
	assert.Contains(t, h1, "host=b1")

	_, stop2, h2 := startHoldPipe(t, bin, env, "--host", "b2", "--needs", "cpu 2, memory 4", "--holder", "bench", "--source", "w2")
	assert.Contains(t, h2, "host=b2")

	w3 := exec.Command(bin, "hold", "--needs", "cpu 2, memory 4, linux", "--holder", "friend", "--source", "alice")
	w3.Env = env
	w3.Stdin = strings.NewReader("")
	var w3Out strings.Builder
	w3.Stdout = &w3Out
	require.NoError(t, w3.Start())
	w3Done := make(chan error, 1)
	go func() { w3Done <- w3.Wait() }()

	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status")
		c.Env = env
		out, _ := c.Output()
		return strings.Contains(string(out), "queued=") && !strings.Contains(string(out), "queued=0\nfree host=b2")
	}, patience, 20*time.Millisecond)

	stop2()
	require.NoError(t, <-w3Done)
	assert.Contains(t, w3Out.String(), "host=b2")

	// Verify subscriber received live event frames too.
	evLine, err := subRd.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, evLine, `"op":"event"`)
}

func TestFunctionalTCPServerRestartReconnectPreservesLiveHolds(t *testing.T) {
	t.Parallel()
	bin := build(t)
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln1.Addr().String()
	machines := []MachineSpec{
		{Host: "space", Online: true, Tags: []string{"linux"}, UsableCores: 4, Resources: map[string]int{"cpu": 4, "memory": 8}},
	}
	srv1 := NewServer(ServerConfig{Local: false, Password: "reconn-pass"}, machines)
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_ = srv1.Serve(ctx1, ln1)
	}()

	env := append(cleanChildEnv(), "NOVA_RESERVE_ADDR="+addr, "NOVA_RESERVE_PASSWORD=reconn-pass")
	_, stopHold, heldLine := startHoldPipe(t, bin, env, "--host", "space", "--needs", "cpu 4, memory 8", "--holder", "friend", "--source", "alice")
	defer stopHold()
	var origID string
	for _, f := range strings.Fields(heldLine) {
		if v, ok := strings.CutPrefix(f, "lease_id="); ok {
			origID = v
		}
	}
	require.NotEmpty(t, origID)

	// Kill server 1 completely, then bring up fresh server 2 on the exact same TCP port with a 1.5s reconnect window!
	cancel1()
	srv1.Close()
	<-done1

	ln2, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv2 := NewServer(ServerConfig{Local: false, Password: "reconn-pass", ReconnectGraceMs: 1_500}, machines)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = srv2.Serve(ctx2, ln2) }()
	t.Cleanup(srv2.Close)

	// Live hold client automatically reconnects and restores its exact LeaseID on srv2!
	require.Eventually(t, func() bool {
		c := exec.Command(bin, "status", "--json")
		c.Env = env
		out, err := c.Output()
		return err == nil && strings.Contains(string(out), origID)
	}, patience, 50*time.Millisecond)
}

func TestFunctionalTCPCIFailOpenWithinThreeSeconds(t *testing.T) {
	t.Parallel()
	bin := build(t)
	// Blackhole TCP listener that accepts and never responds.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()

	env := append(cleanChildEnv(), "NOVA_RESERVE_ADDR="+ln.Addr().String(), "NOVA_RESERVE_PASSWORD=secret")
	began := time.Now()
	cmd := exec.Command(bin, "hold", "--host", "space", "--holder", "ci", "--needs", "cpu 2, memory 4")
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run())
	took := time.Since(began)
	assert.Less(t, took, 4500*time.Millisecond)
	assert.Contains(t, stdout.String(), "note=fail-open")
	assert.Contains(t, stderr.String(), "fail-open mode")
}
