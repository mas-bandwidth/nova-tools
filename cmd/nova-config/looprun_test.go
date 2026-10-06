package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
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
		assert.Contains(t, errs, "nova-config loop run REFUSED: loop refresh runs already (pid=")
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
		assert.Contains(t, out, "effect: process: takes <run-dir>/<name>.lock")
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
