package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
)

// gcApp is an app whose home, AI root and land root are temp directories, on a fixed clock.
func gcApp(t *testing.T) (a *app, home, ai string, now time.Time) {
	t.Helper()
	base := t.TempDir()
	home, ai = filepath.Join(base, "home"), filepath.Join(base, "ai")
	land := filepath.Join(base, "land")
	for _, d := range []string{home, ai, land} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	env := map[string]string{"HOME": home, "NOVA_AI_ROOT": ai}
	a = newApp(func(k string) string { return env[k] })
	now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	a.landRoot = func() (string, error) { return land, nil }
	return a, home, ai, now
}

// gcFile writes a file and sets its time, and its directory's, age before now.
func gcFile(t *testing.T, path string, now time.Time, age time.Duration) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("scratch\n"), 0o644))
	for _, p := range []string{path, filepath.Dir(path)} {
		require.NoError(t, os.Chtimes(p, now.Add(-age), now.Add(-age)))
	}
}

func gcRun(a *app, args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := a.run(append([]string{"gc"}, args...), &out, &errs)
	return code, out.String(), errs.String()
}

// nova-sprint gc on this machine: the dry run says each removal and its bytes and removes
// nothing; the run removes the finished job and the old bench run, keeps the live job, and
// ends GC OK with what it freed and the volume's use.
func TestGcVerbReclaimsThisMachinesFinishedScratch(t *testing.T) {
	t.Parallel()
	a, home, ai, now := gcApp(t)
	w := filepath.Join(ai, "buds", "b1", "working")
	done, live := filepath.Join(w, "jobs", "done"), filepath.Join(w, "jobs", "live")
	gcFile(t, filepath.Join(done, "JOB.md"), now, 5*time.Hour)
	gcFile(t, filepath.Join(live, "JOB.md"), now, 5*time.Hour)
	gcFile(t, filepath.Join(w, "outbox", "done", "REPORT.md"), now, 4*time.Hour)
	gcFile(t, filepath.Join(ai, "buds", "b1", "runner.log"), now, time.Hour)
	require.NoError(t, os.WriteFile(filepath.Join(ai, "buds", "b1", "runner.log"),
		[]byte("t START done\nt START live\nt END done model=m exit=0 report=Verdict: LAND\n"), 0o644))
	run := filepath.Join(home, "nova-bench", "runs", "run.OLD00001")
	gcFile(t, filepath.Join(run, "go.mod"), now, 3*24*time.Hour)
	require.NoError(t, os.Chtimes(run, now.Add(-3*24*time.Hour), now.Add(-3*24*time.Hour)))

	code, out, errs := gcRun(a, "--dry-run")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "GC WOULD-REMOVE class=jobs path="+done)
	assert.Contains(t, out, "GC WOULD-REMOVE class=bench path="+run)
	assert.Regexp(t, `(?m)^GC OK freed=[1-9][0-9]* volume=[0-9]+% dry-run: nothing was removed$`, out)
	assert.DirExists(t, done)
	assert.DirExists(t, run)

	code, out, errs = gcRun(a)
	require.Equal(t, 0, code, errs)
	for _, class := range []string{"jobs", "reads", "landers", "bench", "cache"} {
		assert.Regexp(t, `(?m)^GC `+class+` count=\d+ bytes=\d+ `, out)
	}
	assert.Contains(t, out, "GC jobs count=1 ")
	assert.Contains(t, out, "GC bench count=1 ")
	assert.Regexp(t, `(?m)^GC OK freed=[1-9][0-9]* volume=[0-9]+%$`, out)
	assert.NoDirExists(t, done)
	assert.NoDirExists(t, run)
	assert.DirExists(t, live)
	assert.FileExists(t, filepath.Join(w, "outbox", "done", "REPORT.md"))
}

func TestGcVerbRefusesItsUsage(t *testing.T) {
	t.Parallel()
	a, _, _, _ := gcApp(t)
	for _, args := range [][]string{{"--max-age", "0d"}, {"--max-age", "soon"}, {"word"}, {"--machine", "a b"}} {
		code, _, errs := gcRun(a, args...)
		assert.Equal(t, 2, code, "%v: %s", args, errs)
	}
}

// gc --judgment writes one open judgment through the sprint store (the disk
// guard's below-stop seat judgment), removes nothing, and a store that is not
// there refuses: a judgment with no store is nowhere.
func TestGcJudgmentWritesOneOpenJudgment(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1 --coordinator lead --owner ada")
	out := ta.ok("gc --judgment 'studio /Volumes/nova at 10GB: deals held'")
	assert.Contains(t, out, "GC OK judgment=studio /Volumes/nova at 10GB: deals held")
	assert.Contains(t, ta.ok("inbox"), "studio /Volumes/nova at 10GB: deals held")

	// no store address: gc --judgment is refused, exit 1, nothing written
	a, _, _, _ := gcApp(t)
	code, _, errs := gcRun(a, "--judgment", "studio /Volumes/nova at 10GB: deals held")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no store address to write the judgment to")
}

// --machine runs the same verb on that machine through the fleet runner, its flags carried;
// a machine that does not answer is GC FAILED, exit 1.
func TestGcMachineRunsTheVerbThroughTheFleetRunner(t *testing.T) {
	t.Parallel()
	var host, line string
	answer := 0
	run := func(_ context.Context, h, l string, stdout, _ io.Writer) (int, error) {
		host, line = h, l
		if answer == 0 {
			_, _ = io.WriteString(stdout, "GC OK freed=7 volume=41%\n")
		}
		return answer, nil
	}
	var out, errs bytes.Buffer
	code := gcOn(context.Background(), run, "bench-a", true, "36h", "", &out, &errs)
	require.Equal(t, 0, code, errs.String())
	assert.Equal(t, "bench-a", host)
	assert.Equal(t, gcRemoteBin+" gc --max-age '36h' --dry-run", line)
	assert.Equal(t, "GC OK freed=7 volume=41%\n", out.String())

	answer = bench.NoAnswer
	errs.Reset()
	code = gcOn(context.Background(), run, "bench-a", false, "2d", "", io.Discard, &errs)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs.String(), "GC FAILED machine=bench-a: did not answer")
	assert.True(t, strings.HasSuffix(line, " gc --max-age '2d'"), line)
}

// A machine that exports no NOVA_AI_ROOT and has no ~/ai: the verb finds the AI root through
// the home's <name>-working links (to <ai-root>/<name>/working and
// <ai-root>/buds/<name>/working) and reclaims the finished jobs there. --ai-root names it
// outright and is carried to another machine.
func TestGcVerbFindsTheAIRootWithoutNovaAIRoot(t *testing.T) {
	t.Parallel()
	a, home, ai, now := gcApp(t)
	env := map[string]string{"HOME": home}
	a.getenv = func(k string) string { return env[k] }
	var dones []string
	for _, w := range []string{filepath.Join(ai, "rowan", "working"), filepath.Join(ai, "buds", "b1", "working")} {
		name := filepath.Base(filepath.Dir(w))
		require.NoError(t, os.MkdirAll(w, 0o755))
		require.NoError(t, os.Symlink(w, filepath.Join(home, name+"-working")))
		done := filepath.Join(w, "jobs", "done")
		gcFile(t, filepath.Join(done, "JOB.md"), now, 5*time.Hour)
		gcFile(t, filepath.Join(w, "outbox", "done", "REPORT.md"), now, 4*time.Hour)
		dones = append(dones, done)
	}
	code, out, errs := gcRun(a)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "GC jobs count=2 ")
	assert.NotContains(t, out, "GC REFUSED")
	for _, d := range dones {
		assert.NoDirExists(t, d)
	}

	assert.Equal(t, gcRemoteBin+" gc --max-age '2d' --ai-root '/v/ai'", gcLine(false, "2d", "/v/ai"))
	code, _, errs = gcRun(a, "--ai-root", "relative")
	assert.Equal(t, 2, code, errs)
}
