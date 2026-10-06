package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoopRunDryRunPrintsTheCommandAndWritesNothing(t *testing.T) {
	t.Parallel()
	h := loopRunHarness(t)
	code, out, errs := h.run(t, "loop", "run", "reader-m1", "--dry-run", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.Empty(t, errs)
	assert.Contains(t, out, "LOOP DRY-RUN name=reader-m1")
	assert.Contains(t, out, "LOOP ARG 0 nova-swarm")
	entries, err := os.ReadDir(h.dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, "try.json", e.Name())
	}
}

func TestLoopRunRefusesADisabledRowAndAStopFile(t *testing.T) {
	t.Parallel()
	h := loopRunHarness(t)
	code, _, errs := h.run(t, "loop", "set", "reader-m1", "--enabled", "false", "--as", "a1", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	code, _, errs = h.run(t, "loop", "run", "reader-m1", "--file", "try.json")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "disabled")

	h = loopRunHarness(t)
	stop := filepath.Join(h.dir, "stop")
	require.NoError(t, os.WriteFile(stop, []byte("free_gib=1\nstop=1\n"), 0o644))
	d := h.deps()
	d.exec = func([]string) error { t.Fatal("exec"); return nil }
	var out, errb strings.Builder
	code = run([]string{"loop", "run", "reader-m1", "--file", "try.json", "--stop-file", stop}, &out, &errb, d)
	assert.Equal(t, 3, code)
	assert.Contains(t, errb.String(), "stop=1")
	assert.Empty(t, out.String())
}

func TestLoopRunExecutesAndRefusesAHeldLock(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("loop run locks on unix")
	}
	h := loopRunHarness(t)
	runDir := t.TempDir()
	d := h.deps()
	var got []string
	d.exec = func(argv []string) error {
		got = append([]string{}, argv...)
		raw, err := os.ReadFile(filepath.Join(runDir, "reader-m1.lock"))
		require.NoError(t, err)
		assert.NotEmpty(t, strings.TrimSpace(string(raw)))
		return nil
	}
	var out, errb strings.Builder
	code := run([]string{"loop", "run", "reader-m1", "--file", "try.json", "--run-dir", runDir}, &out, &errb, d)
	require.Equal(t, 0, code, errb.String())
	assert.Equal(t, []string{"nova-swarm", "member", "--as", "reader-m1", "--reader"}, got)
	prom, err := os.ReadFile(filepath.Join(runDir, "reader-m1.prom"))
	require.NoError(t, err)
	assert.Contains(t, string(prom), "nova_loop_restarts_total")
	count, err := os.ReadFile(filepath.Join(runDir, "reader-m1.restarts"))
	require.NoError(t, err)
	assert.Equal(t, "1\n", string(count))

	d.lock = func(string) (loopHold, bool, error) { return loopHold{}, false, nil }
	d.exec = func([]string) error { t.Fatal("exec"); return nil }
	out.Reset()
	errb.Reset()
	code = run([]string{"loop", "run", "reader-m1", "--file", "try.json", "--run-dir", runDir}, &out, &errb, d)
	assert.Equal(t, 3, code)
	assert.Contains(t, errb.String(), "live lock")
}

func loopRunHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness()
	h.dir = t.TempDir()
	for _, line := range []string{
		"nova-config migrate --file try.json",
		"nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json",
		`nova-config loop add reader-m1 --machine m1 --argv '["nova-swarm","member","--as","reader-m1","--reader"]' --keepalive true --as a1 --file try.json`,
	} {
		args := words(line)[1:]

		code, _, errs := h.run(t, args...)
		require.Equal(t, 0, code, "%s: %s", line, errs)
	}
	return h
}
