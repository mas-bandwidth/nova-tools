package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// A LAUNCH THAT DIES FAST ON A PROVIDER 5XX IS RETRIED (issue #900). These tests hold the
// inherited grace path: the retry keeps the task, the usage rows carry attempt=1,2,3, and a
// slow failure is not retried. The tail text is not evidence the provider never accepted
// the request.

// TestNativeRetriesAProvider5xxLaunch: the native path retries a launch that dies inside the
// grace on a provider server error, keeps the same job, harvests the second attempt's result
// and writes one usage row per attempt for the one job.
func TestNativeRetriesAProvider5xxLaunch(t *testing.T) {
	t.Parallel()

	// The retry wait is pinned to zero for the whole package by TestMain.
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "retry-5xx"

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a card that dies at request start\nFAKE-LAUNCHES\nFAKE-5XX-FIRST\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())
	require.Equal(t, 0, res.rc, "the second attempt succeeds and the run records rc=0, got %d:\n%s", res.rc, errOut.String())
	// The harvested result is the second attempt's: the first died before publishing and
	// the second published, so RESULT.md exists in the job directory.
	jobDir := filepath.Join(slot, "jobs", label)
	_, err := os.Stat(filepath.Join(jobDir, "RESULT.md"))
	assert.NoError(t, err, "the second attempt's result was not harvested")
	// One usage row per launch, attempt=1 and attempt=2, for the one job.
	raw, err := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
	require.NoError(t, err)
	rows := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, rows, 3, "a retried card wants a header and two rows, got %d:\n%s", len(rows), raw)
	head := strings.Split(rows[0], "\t")
	attemptAt := -1
	usdAt := -1
	for i, name := range head {
		switch name {
		case "attempt":
			attemptAt = i
		case "usd":
			usdAt = i
		}
	}
	require.GreaterOrEqual(t, attemptAt, 0, "the header does not carry attempt and usd:\n%s", rows[0])
	require.GreaterOrEqual(t, usdAt, 0, "the header does not carry attempt and usd:\n%s", rows[0])
	got := strings.Split(rows[1], "\t")[attemptAt]
	assert.Equal(t, "1", got, "the first launch row carries attempt=%s, want 1", got)
	got = strings.Split(rows[2], "\t")[attemptAt]
	assert.Equal(t, "2", got, "the second launch row carries attempt=%s, want 2", got)
	got = strings.Split(rows[1], "\t")[usdAt]
	assert.Equal(t, "-", got, "an unreported usd stays a dash, got %q", got)
}

// A lost response is one launch, and the usage row stays unknown. The fake
// harness prints the timeout and exits. It does not prove the installed
// OpenCode consumer honors headerTimeout; it proves this loop does not turn
// that tail into done or failed, and does not start a second launch.
func TestNativeLostResponseStaysUnknownAndLaunchesOnce(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "lost-response"
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("FAKE-LAUNCHES\nFAKE-LOST-RESPONSE\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())
	require.True(t, res.lost, "lost=%v end=%s, want a retained unknown", res.lost, res.end)
	require.Equal(t, "unknown", res.end, "lost=%v end=%s, want a retained unknown", res.lost, res.end)
	jobDir := filepath.Join(slot, "jobs", label)
	launches, err := os.ReadFile(filepath.Join(jobDir, "launches"))
	require.NoError(t, err)
	n := strings.Count(string(launches), "launch")
	require.Equal(t, 1, n, "a lost response launched %d times, want 1:\n%s", n, launches)
	mark, err := os.ReadFile(filepath.Join(jobDir, "provider-acceptance"))
	require.NoError(t, err)
	require.Equal(t, oneline.Escape("unknown\n"), string(mark), "provider-acceptance = %q, want the escaped marker", mark)
	_, err = os.Stat(filepath.Join(jobDir, "RESULT.md"))
	require.Error(t, err, "a lost response must not publish a result")
}

func TestUnrecordedUnknownIsStillAHarvestHold(t *testing.T) {
	windowsIsNotABench(t)
	prev := persistUnknownFn
	persistUnknownFn = func(string) error { return errors.New("disk full") }
	t.Cleanup(func() { persistUnknownFn = prev })

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "unrecorded"
	cardPath := filepath.Join(root, label+".md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-LOST-RESPONSE\n"), 0o644))
	var stdout, stderr bytes.Buffer
	code := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.NotEqual(t, 0, code, "a failed record exited 0:\n%s", stdout.String())
	require.Contains(t, stdout.String(), "why=unknown-acceptance", "the refusal returned before the unknown verdict:\n%s\n%s", stdout.String(), stderr.String())

	// The nova-pulse harvest hold that followed here left with internal/pulse (deleted 2026-09-25, #3969);
	// the native refusal above is the property that remains.
}

func TestPersistUnknownFallsBackWhenTheMarkerCannotBeWritten(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(job, "provider-acceptance"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(job, "harness-output.log"), 0o755))
	require.NoError(t, persistUnknown(job))
	raw, err := os.ReadFile(filepath.Join(job, "harness.log"))
	require.NoError(t, err, "the fallback log does not hold the unknown: %q, %v", raw, err)
	require.Contains(t, string(raw), "why=unknown-acceptance", "the fallback log does not hold the unknown: %q, %v", raw, err)
}

func TestPersistUnknownFailsWhenNothingCanBeWritten(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	require.NoError(t, os.Chmod(job, 0o555))
	t.Cleanup(func() { _ = os.Chmod(job, 0o755) })
	require.Error(t, persistUnknown(job), "an unwritable job recorded the unknown")
}

func TestNativeLostResponseLineSaysUnknownAcceptance(t *testing.T) {
	t.Parallel()

	out := nativeVerdict(t, "lost", "FAKE-LOST-RESPONSE\n")
	require.NotContains(t, out, "NATIVE OK", "a lost response said OK:\n%s", out)
	require.Contains(t, out, "why=unknown-acceptance", "the verdict did not keep unknown-acceptance:\n%s", out)
	require.NotContains(t, out, "why=no-result", "a lost response was filed as an ordinary incomplete:\n%s", out)
	require.NotContains(t, out, "why=rc", "a lost response was filed as an ordinary incomplete:\n%s", out)
}

// A transient wrapper does not make a current credit/key refusal retryable
// (SPEC-SPRINT section 5: the provider rests until its funds or key return).
func TestNativeDoesNotRetryARefusalInsideAServerError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, card, class string
	}{
		{"credit", "FAKE-SAY Unexpected server error: ref=err_credit\nFAKE-CREDIT-REFUSAL\n", "out-of-credit"},
		{"auth", "FAKE-SAY level=ERROR message=server_error statusCode=401 error=Unauthorized Unexpected server error\nFAKE-NORESULT\n", "auth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := nativeHarness(t)
			root, slot := aSlot(t)
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: tc.name,
				card:    []byte("FAKE-LAUNCHES\n" + tc.card),
				slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
			}, &errOut)
			require.Equal(t, 0, code, errOut.String())
			launches, err := os.ReadFile(filepath.Join(slot, "jobs", tc.name, "launches"))
			require.NoError(t, err)
			assert.Equal(t, 1, strings.Count(string(launches), "launch"), errOut.String())
			assert.Contains(t, errOut.String(), "reason=provider: class="+tc.class)
		})
	}
}

// An earlier job's credit refusal is not this launch's refusal. A later
// transient failure still earns its retry (SPEC-SPRINT section 5).
func TestNativeRetriesA5xxAfterAnOlderCreditRefusal(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "older-credit",
		card:    []byte("FAKE-CREDIT-REFUSAL\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, errOut.String())
	require.Contains(t, errOut.String(), "class=out-of-credit")
	// Make the old session deterministically older than the next launch.
	output, err := exec.Command(swarm.SQLiteBinary, filepath.Join(slot, "data", "opencode", "opencode.db"), "UPDATE message SET time_created=0").CombinedOutput()
	require.NoError(t, err, string(output))
	errOut.Reset()
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "later-5xx",
		card:    []byte("FAKE-LAUNCHES\nFAKE-5XX-FIRST\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, errOut.String())
	assert.Equal(t, 0, res.rc, errOut.String())
	launches, err := os.ReadFile(filepath.Join(slot, "jobs", "later-5xx", "launches"))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(launches), "launch"), errOut.String())
}
