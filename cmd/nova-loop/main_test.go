package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWorld is a machine with the given live pids, a fixed clock and an exec that
// records the command in place of running it.
type fakeWorld struct {
	world
	ran [][]string
}

func newFakeWorld(t *testing.T, pid int, live ...int) *fakeWorld {
	t.Helper()
	fw := &fakeWorld{}
	var mu sync.Mutex
	fw.world = world{
		pid:   pid,
		alive: func(p int) bool { return p == pid || containsPid(live, p) },
		now:   func() time.Time { return time.Unix(1_790_000_000, 0) },
		home:  t.TempDir(),
		guard: func(string) (func(), error) { mu.Lock(); return mu.Unlock, nil },
		exec:  func(argv []string) error { fw.ran = append(fw.ran, argv); return nil },
	}
	return fw
}

func containsPid(pids []int, p int) bool {
	for _, q := range pids {
		if q == p {
			return true
		}
	}
	return false
}

func runLoop(t *testing.T, w world, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := loopTool(w).Run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunTakesAFreeLockAndExecsTheCommand(t *testing.T) {
	t.Parallel()
	w := newFakeWorld(t, 4100)
	dir := filepath.Join(t.TempDir(), "run")
	code, out, errs := runLoop(t, w.world, "run", "--name", "disk-guard", "--run-dir", dir, "--", "nova-swarm", "disk-guard", "--dry-run")
	require.Equal(t, 0, code, "stderr: %s", errs)
	assert.Equal(t, [][]string{{"nova-swarm", "disk-guard", "--dry-run"}}, w.ran)
	b, err := os.ReadFile(filepath.Join(dir, "disk-guard.lock"))
	require.NoError(t, err)
	assert.Equal(t, "4100\n", string(b), "the lock holds the pid the command keeps")
	assert.Contains(t, out, "RUN OK loop=disk-guard pid=4100 lock=")
}

func TestRunRefusesASecondCopyWhileTheHolderLives(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "member.lock"), []byte("4200\n"), 0o644))
	w := newFakeWorld(t, 4300, 4200)
	code, out, _ := runLoop(t, w.world, "run", "--name", "member", "--run-dir", dir, "--", "nova-swarm", "member")
	assert.Equal(t, exitHeld, code)
	assert.Empty(t, w.ran, "a held loop's command never runs")
	assert.Contains(t, out, "RUN HELD loop=member pid=4200")
	b, err := os.ReadFile(filepath.Join(dir, "member.lock"))
	require.NoError(t, err)
	assert.Equal(t, "4200\n", string(b), "the holder's lock is left as it was")
}

func TestRunTakesTheLockOfADeadHolder(t *testing.T) {
	t.Parallel()
	for name, held := range map[string]string{"a dead pid": "4200\n", "no pid": "", "not a pid": "x\n"} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "member.lock"), []byte(held), 0o644))
		w := newFakeWorld(t, 4300)
		code, _, errs := runLoop(t, w.world, "run", "--name", "member", "--run-dir", dir, "--", "nova-swarm", "member")
		require.Equal(t, 0, code, "%s: stderr: %s", name, errs)
		b, err := os.ReadFile(filepath.Join(dir, "member.lock"))
		require.NoError(t, err)
		assert.Equal(t, "4300\n", string(b), "%s: the lock is this start's", name)
		assert.Len(t, w.ran, 1, name)
	}
}

func TestRunCountsEachStartForTheTextfileCollector(t *testing.T) {
	t.Parallel()
	dir, metrics := t.TempDir(), t.TempDir()
	w := newFakeWorld(t, 4400)
	for i := 1; i <= 2; i++ {
		code, out, errs := runLoop(t, w.world, "run", "--name", "mirror.refresh", "--run-dir", dir, "--metrics", metrics, "--", "true")
		require.Equal(t, 0, code, "stderr: %s", errs)
		assert.Contains(t, out, "starts="+string(rune('0'+i)))
	}
	b, err := os.ReadFile(filepath.Join(metrics, "nova_loop_mirror_refresh.prom"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "nova_loop_starts_total{loop=\"mirror.refresh\"} 2\n")
	assert.Contains(t, string(b), "nova_loop_last_start_seconds{loop=\"mirror.refresh\"} 1790000000\n")
	assert.NoFileExists(t, filepath.Join(metrics, "nova_loop_mirror_refresh.prom.tmp"))
}

func TestRunRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	for want, args := range map[string][]string{
		"--name is required":   {"run", "--", "true"},
		"is not a loop's name": {"run", "--name", "../x", "--", "true"},
		"no command":           {"run", "--name", "a"},
		"no verb and no file":  {},
		"runs no loop":         {"run", "--name", "a", "--", "true"},
	} {
		w := newFakeWorld(t, 4500)
		if want == "runs no loop" {
			w.refuse = "a Windows machine runs no loop"
		}
		code, _, errs := runLoop(t, w.world, args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Contains(t, errs, want, "%v", args)
		assert.Empty(t, w.ran, "%v: a refused start runs nothing", args)
	}
}

func TestRunSaysWhenTheCommandCannotStart(t *testing.T) {
	t.Parallel()
	w := newFakeWorld(t, 4600)
	w.exec = func([]string) error { return os.ErrNotExist }
	code, _, errs := runLoop(t, w.world, "run", "--name", "a", "--run-dir", t.TempDir(), "--", "no-such-program")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "RUN FAILED loop=a: the command could not be started")
}

func TestTheToolAnswersHelpAndVersion(t *testing.T) {
	t.Parallel()
	w := newFakeWorld(t, 4700)
	code, out, _ := runLoop(t, w.world, "help")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "nova-loop run --name <loop>")
	code, out, _ = runLoop(t, w.world, "run", "-h")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "--metrics")
	code, out, _ = runLoop(t, w.world, "version")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "nova-loop")
	assert.Empty(t, loopTool(w.world).Problems(), "the tool's own declaration is sound")
}
