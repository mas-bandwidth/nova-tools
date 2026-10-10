//go:build !windows

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loop run, the Go nova-loop (docs/SPEC-CONFIG.md, "loop run"): the row's command or
// the unit's after --, under the loop's lock, on the in-memory store with a fake
// runner, so no command is started.

// fakeRunner records what loop run asked it to run, and ends it with code.
type fakeRunner struct {
	ran  [][]string
	code int
	// during runs while the command "runs": the lock is held then.
	during func()
}

func (f *fakeRunner) run(_ context.Context, argv []string, stdout, _ io.Writer) (int, error) {
	f.ran = append(f.ran, argv)
	if f.during != nil {
		f.during()
	}
	_, _ = io.WriteString(stdout, "child ran\n")
	return f.code, nil
}

func loopRunDeps(h *harness, f *fakeRunner) deps {
	d := h.deps()
	d.runLoop = f.run
	return d
}

func runWith(d deps, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, d)
	return code, out.String(), errb.String()
}

func TestLoopRunIsTheLoopsCommandUnderItsOneLock(t *testing.T) {
	t.Parallel()
	h := loopHarness(t, "m1")
	_, _, errs := h.run(t, "loop", "add", "refresh", "--machine", "m1", "--argv", `["/bin/refresh","--once"]`, "--every", "60")
	require.Empty(t, errs)
	_, _, errs = h.run(t, "loop", "add", "member-m1", "--machine", "m1", "--argv", `["/bin/member"]`, "--keepalive", "true", "--seat", "s1", "--keys", "A_KEY")
	require.Empty(t, errs)
	_, _, errs = h.run(t, "loop", "add", "off", "--machine", "m1", "--argv", `["/bin/off"]`, "--every", "5", "--enabled", "false")
	require.Empty(t, errs)
	dir := t.TempDir()
	metrics := filepath.Join(t.TempDir(), "textfile") // made by the verb

	t.Run("the row's command runs, counted, and its exit is the verb's", func(t *testing.T) {
		f := &fakeRunner{code: 7}
		code, out, errs := runWith(loopRunDeps(h, f), "loop", "run", "refresh", "--run-dir", dir, "--metrics", metrics)
		assert.Equal(t, 7, code, errs)
		assert.Equal(t, [][]string{{"/bin/refresh", "--once"}}, f.ran)
		assert.Equal(t, "LOOP RUN name=refresh starts=1 argv=[\"/bin/refresh\",\"--once\"]\nchild ran\nLOOP DONE name=refresh exit=7\n", out)
		f.code = 0
		code, out, _ = runWith(loopRunDeps(h, f), "loop", "run", "refresh", "--run-dir", dir, "--metrics", metrics)
		assert.Equal(t, 0, code)
		assert.Contains(t, out, "starts=2")
		prom, err := os.ReadFile(filepath.Join(metrics, "nova_loop_refresh.prom"))
		require.NoError(t, err)
		assert.Contains(t, string(prom), "nova_loop_starts_total{loop=\"refresh\"} 2\n")
		assert.Contains(t, string(prom), "nova_loop_last_start_seconds{loop=\"refresh\"} 1700000000\n")
	})

	t.Run("a second copy is refused with exit 3 while the first holds the lock", func(t *testing.T) {
		inner := &fakeRunner{}
		var code int
		var errs string
		outer := &fakeRunner{during: func() {
			code, _, errs = runWith(loopRunDeps(h, inner), "loop", "run", "refresh", "--run-dir", dir)
		}}
		first, _, ferrs := runWith(loopRunDeps(h, outer), "loop", "run", "refresh", "--run-dir", dir)
		assert.Equal(t, 0, first, ferrs)
		assert.Equal(t, config.LoopRunExitHeld, code)
		assert.Contains(t, errs, "loop refresh runs already (pid=")
		assert.Empty(t, inner.ran, "the second copy ran its command")
		// the lock is free once the first ends: a third copy runs
		third := &fakeRunner{}
		code, _, errs = runWith(loopRunDeps(h, third), "loop", "run", "refresh", "--run-dir", dir)
		assert.Equal(t, 0, code, errs)
		assert.Len(t, third.ran, 1)
	})

	t.Run("a lock another process holds refuses it the same", func(t *testing.T) {
		held, err := filelock.TryLock(config.LoopLockPath(dir, "refresh"), "another nova-loop")
		require.NoError(t, err)
		f := &fakeRunner{}
		code, _, errs := runWith(loopRunDeps(h, f), "loop", "run", "refresh", "--run-dir", dir)
		require.NoError(t, held.Unlock())
		assert.Equal(t, config.LoopRunExitHeld, code, errs)
		assert.Empty(t, f.ran)
	})

	t.Run("the unit's command after -- runs with no store opened", func(t *testing.T) {
		h2 := newHarness()
		f := &fakeRunner{}
		code, out, errs := runWith(loopRunDeps(h2, f), "loop", "run", "member-m1", "--run-dir", dir, "--", "nova-secrets", "exec", "--", "/bin/member")
		assert.Equal(t, 0, code, errs)
		assert.Equal(t, [][]string{{"nova-secrets", "exec", "--", "/bin/member"}}, f.ran)
		assert.Contains(t, out, "LOOP RUN name=member-m1 starts=1 ")
		assert.Zero(t, h2.opens, "a command after -- opens no store")
	})

	t.Run("--dry-run prints the run's line and takes no lock, writes nothing and runs nothing", func(t *testing.T) {
		fresh := filepath.Join(t.TempDir(), "run") // never made by a dry run
		f := &fakeRunner{}
		code, out, errs := runWith(loopRunDeps(h, f), "loop", "run", "refresh", "--run-dir", fresh, "--metrics", filepath.Join(fresh, "textfile"), "--dry-run")
		assert.Equal(t, 0, code, errs)
		assert.Equal(t, "LOOP RUN name=refresh starts=1 argv=[\"/bin/refresh\",\"--once\"] dry_run=true; no lock taken, nothing written or run\n", out)
		assert.Empty(t, f.ran, "a dry run ran the command")
		assert.NoDirExists(t, fresh, "a dry run made the run directory")
		code, _, errs = runWith(loopRunDeps(h, f), "loop", "run", "off", "--run-dir", fresh, "--dry-run")
		assert.Equal(t, 1, code, "a dry run refuses what the run refuses: %s", errs)
		assert.Empty(t, f.ran)
	})

	t.Run("refusals", func(t *testing.T) {
		for _, c := range []struct {
			args []string
			code int
			errs string
		}{
			{[]string{"loop", "run", "gone", "--run-dir", dir}, 1, "loop gone not found; run: nova-config loop list"},
			{[]string{"loop", "run", "off", "--run-dir", dir}, 1, "loop off is disabled; run: nova-config loop set off --enabled true"},
			{[]string{"loop", "run", "member-m1", "--run-dir", dir}, 1, "run: nova-config loop run member-m1 -- <the unit's command>"},
			{[]string{"loop", "run", "--run-dir", dir}, 2, "the name is required"},
			{[]string{"loop", "run", "refresh", "--run-dir", dir, "--"}, 2, "-- names no command"},
			{[]string{"loop", "run", "Bad_Name", "--run-dir", dir}, 2, "want lower-case letters"},
		} {
			f := &fakeRunner{}
			code, _, errs := runWith(loopRunDeps(h, f), c.args...)
			assert.Equal(t, c.code, code, "%v: %s", c.args, errs)
			assert.Contains(t, errs, c.errs, "%v", c.args)
			assert.Empty(t, f.ran, "%v ran a command", c.args)
		}
	})

	t.Run("its help states its effect and touches nothing", func(t *testing.T) {
		code, out, errs := h.run(t, "loop", "run", "-h")
		require.Equal(t, 0, code, errs)
		assert.Contains(t, out, "effect: local write: takes <run-dir>/<name>.lock")
		assert.Contains(t, out, "--dry-run")
		assert.Contains(t, out, "--run-dir")
		assert.Contains(t, out, "example: nova-config loop run sleeper --run-dir ./run -- sleep 1")
	})
}

// The real runner: a command's exit code is the verb's, and one that cannot start is
// an error, not an exit code. The command is this test binary: -test.run=^$ runs no
// test and exits 0, and a flag it does not define exits 2 before any test runs.
func TestLoopRunsRealRunnerPassesTheExitCode(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	var out, errb bytes.Buffer
	code, err := startLoop(context.Background(), []string{self, "-test.run=^$"}, &out, &errb)
	require.NoError(t, err)
	assert.Equal(t, 0, code, errb.String())
	code, err = startLoop(context.Background(), []string{self, "-test.no-such-flag"}, &out, &errb)
	require.NoError(t, err)
	assert.Equal(t, 2, code, errb.String())
	_, err = startLoop(context.Background(), []string{filepath.Join(t.TempDir(), "no-such-program")}, &out, &errb)
	assert.Error(t, err)
}

// loopSignalHelperArg marks the child of TestLoopRunSignalHelper: startLoop
// hands it to this test binary, and only then does the helper end itself with a
// signal, so the suite's own run skips it.
const loopSignalHelperArg = "--looprun-signal-helper"

// TestLoopRunSignalHelper is the child a signal ends, started by the two tests
// below through startLoop. It is the child only when the marker stands in its
// words.
func TestLoopRunSignalHelper(t *testing.T) {
	t.Parallel()
	if !slices.Contains(os.Args, loopSignalHelperArg) {
		t.Skip("the helper process only")
	}
	p, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, p.Signal(syscall.SIGTERM))
}

// A command a signal ended is 128+N, env(1)'s status, never a made-up one: the
// unit and the shell around it read a signal death the same way the bash
// nova-loop wrapper's exec left it.
func TestLoopRunsRealRunnerReportsASignalDeathAsItsStatus(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	var out, errb bytes.Buffer
	code, err := startLoop(context.Background(), []string{self, "-test.run=^TestLoopRunSignalHelper$", "--", loopSignalHelperArg}, &out, &errb)
	require.NoError(t, err, errb.String())
	assert.Equal(t, 128+int(syscall.SIGTERM), code, errb.String())
}

// The tool skeleton cancels the context it hands a verb on SIGINT (pkg/tool's
// RunContext); loop run passes SIGINT and SIGTERM on to the command instead of
// letting that cancellation kill it, so a context already cancelled still runs
// the command to its own end.
func TestLoopCommandsOutliveTheSkeletonsInterruptCancellation(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb bytes.Buffer
	code, err := startLoop(ctx, []string{self, "-test.run=^TestLoopRunSignalHelper$", "--", loopSignalHelperArg}, &out, &errb)
	require.NoError(t, err, errb.String())
	assert.Equal(t, 128+int(syscall.SIGTERM), code, errb.String())
}

// The loop's lock lives in the wrapper process (loopRunCall), so a command that
// outlived a killed wrapper would run with no lock holder and the restarted unit's
// second copy would start beside it. loopChildAttr ties the command's life to the
// wrapper: this test kills the real wrapper and watches the real command die, then
// restarts the real wrapper to show the restart clean. Both starts below are this
// test binary's own real helper processes, so the exec path is the shipped one.
const (
	// loopWrapperHelperArg marks the child that runs the real loop run verb.
	loopWrapperHelperArg = "--looprun-wrapper-helper"
	// loopChildHelperArg marks the child that is the loop's command.
	loopChildHelperArg = "--looprun-child-helper"
)

func TestLoopRunWrapperDeathEndsTheCommandAndTheLock(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("only linux carries the parent-death signal loopChildAttr sets")
	}
	self, err := os.Executable()
	require.NoError(t, err)
	runDir := t.TempDir()
	pidFile := filepath.Join(runDir, "command.pid")
	wrapper := loopWrapperProcess(self, runDir, "orphan", "hold", pidFile)
	var werr bytes.Buffer
	wrapper.Stderr = &werr
	require.NoError(t, wrapper.Start())
	t.Cleanup(func() {
		if wrapper.Process != nil {
			_ = wrapper.Process.Kill()
		}
		_ = wrapper.Wait()
	})
	pid := waitForCommandPid(t, pidFile)
	t.Cleanup(func() {
		if !commandGone(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	require.NoError(t, syscall.Kill(pid, 0), "the command did not start")
	require.NoError(t, wrapper.Process.Kill())
	require.Eventually(t, func() bool { return commandGone(pid) }, 10*time.Second, 20*time.Millisecond,
		"the command (pid %d) outlived the wrapper that held its lock", pid)

	// the lock is free: the restart runs a command of its own and ends
	again := loopWrapperProcess(self, runDir, "orphan", "exit", filepath.Join(runDir, "again.pid"))
	out, err := again.CombinedOutput()
	require.NoError(t, err, "the restart: %s", out)
	assert.Contains(t, string(out), "LOOP DONE name=orphan exit=0")
}

// loopWrapperProcess is one real nova-config start, as this test binary's wrapper
// helper: -test.run reaches only the helper, and NOVA_CONFIG_TEST_HELPER keeps the
// package's throwaway Postgres out of the helper. The depth the guard counts is
// cleared, because the helper's own command is a second test binary and the guard
// allows at most two in a chain.
func loopWrapperProcess(self, runDir, name, behavior, pidFile string) *exec.Cmd {
	cmd := exec.Command(self, "-test.run=^TestLoopRunWrapperHelper$", "--", loopWrapperHelperArg, runDir, name, behavior, pidFile)
	depth := testbin.DepthEnv("nova-config")
	for _, e := range os.Environ() {
		if k, _, ok := strings.Cut(e, "="); ok && k == depth {
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	cmd.Env = append(cmd.Env, "NOVA_CONFIG_TEST_HELPER=1")
	return cmd
}

// waitForCommandPid is the command's pid, from the file it writes at its start.
func waitForCommandPid(t *testing.T, pidFile string) int {
	t.Helper()
	pid := 0
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || n <= 0 {
			return false
		}
		pid = n
		return true
	}, 10*time.Second, 20*time.Millisecond, "the command never wrote its pid")
	return pid
}

// commandGone reports whether the command process has ended: kill(pid, 0) finds no
// process, or /proc/<pid>/stat says a process the kernel has not reaped yet is a
// zombie. The plain signal check is not enough, because a zombie still answers it.
func commandGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	i := strings.LastIndex(string(b), ")")
	return i >= 0 && len(b) > i+2 && b[i+2] == 'Z'
}

// TestLoopRunWrapperHelper is one real `nova-config loop run <name> -- <command>`:
// the suite's other starts skip it. The command is the child helper below.
func TestLoopRunWrapperHelper(t *testing.T) {
	t.Parallel()
	i := slices.Index(os.Args, loopWrapperHelperArg)
	if i < 0 || i+4 >= len(os.Args) {
		t.Skip("the wrapper process only")
	}
	runDir, name, behavior, pidFile := os.Args[i+1], os.Args[i+2], os.Args[i+3], os.Args[i+4]
	self, err := os.Executable()
	require.NoError(t, err)
	args := append([]string{"loop", "run", name, "--run-dir", runDir, "--"},
		self, "-test.run=^TestLoopRunChildHelper$", "--", loopChildHelperArg, behavior, pidFile)
	os.Exit(run(args, os.Stdout, os.Stderr, realDeps()))
}

// TestLoopRunChildHelper is the command the wrapper helper runs: it writes its pid
// and, in hold, waits to be killed; in exit it returns at once, so a restart's
// command ends by itself.
func TestLoopRunChildHelper(t *testing.T) {
	t.Parallel()
	i := slices.Index(os.Args, loopChildHelperArg)
	if i < 0 || i+2 >= len(os.Args) {
		t.Skip("the command process only")
	}
	behavior, pidFile := os.Args[i+1], os.Args[i+2]
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644))
	if behavior != "hold" {
		return
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	<-sigs
}
