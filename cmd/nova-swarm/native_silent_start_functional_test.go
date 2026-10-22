//go:build functional

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// The silent-harness runs start the built fake harness through nativeRun, a real child
// process, so they are functional by the house rule (docs/TESTING.md, "The two tiers").
// The idle watch and the deadline are still the run's seams, handed the end with the time
// passed in: nothing here waits on a clock. The classification they rest on
// (TestKilledRunsAreProviderEndsOnlyForCredit, TestIdleCauseNeedsSilenceNoTokensAndNoStore)
// stays in native_silent_start_test.go, with nothing started.

// silentRunOf runs one fake-harness card whose idle end is delivered at once, after before
// has written into the job's capture and the data home what the harness had left by then.
// It returns the log native printed (stderr then stdout), the result the member reads from
// it, and the member's finish reason.
func silentRunOf(t *testing.T, label, card string, before func(capture, harnessLog string)) (log string, res nativeRunResult, result member.Result, why string) {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	job := filepath.Join(slot, "jobs", label)
	idleFn := func(swarm.IdleWatch, <-chan struct{}) <-chan swarm.IdleEnd {
		if before != nil {
			before(filepath.Join(job, "harness-output.log"), filepath.Join(slot, "data", filepath.FromSlash(harnessLogFile)))
		}
		out := make(chan swarm.IdleEnd, 1)
		out <- swarm.IdleEnd{Idle: 300 * time.Second}
		return out
	}
	cfg := nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte(card), slotDir: slot, root: root, deadline: 30 * time.Second, idle: 300 * time.Second, noWall: true,
		idleFn: idleFn,
		deadlineFn: func(time.Duration) (<-chan time.Time, func() bool) {
			return nil, func() bool { return true } // the deadline never fires: the watch ends this run
		},
	}
	var errOut, stdout, stderr bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "the run started:\n%s", errOut.String())
	require.True(t, res.idled, "the idle watch ended the run:\n%s", errOut.String())
	printNativeResult(&stdout, &stderr, cfg, res, code, false)
	log = errOut.String() + stderr.String() + stdout.String()
	logPath := filepath.Join(t.TempDir(), "native.log")
	require.NoError(t, os.WriteFile(logPath, []byte(log), 0o644))
	done := make(chan struct{})
	close(done)
	child := &nativeChild{card: label, logPath: logPath, results: filepath.Join(t.TempDir(), "results"), job: res.job, done: done}
	result = child.Result()
	fin, why := member.Judge(result, member.Push{None: "the child committed nothing"})
	require.Equal(t, member.FinishFailed, fin)
	return log, res, result, why
}

// appendTo adds text to a file the harness keeps, creating its directory.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestASilentHarnessIdleReapNamesItsCause(t *testing.T) {
	t.Parallel()

	t.Run("a harness that never started names it on every line", func(t *testing.T) {
		t.Parallel()
		log, res, result, why := silentRunOf(t, "silentstart", "FAKE-SILENT\n", nil)
		require.Equal(t, "silent", res.harness, "nothing was printed:\n%s", log)
		require.Equal(t, swarm.IdleCauseNeverStarted, res.idleEnd.Cause)
		require.Regexp(t, `NATIVE INCOMPLETE .* reason=idle-reap cause=harness-never-started`, log, "the NATIVE line names the cause")
		require.Contains(t, log, "CARD IDLE task=silentstart step=- idle=300s cause=harness-never-started", "the CARD IDLE line names the cause")
		require.NotContains(t, log, "NATIVE PROVIDER-", "no provider refusal was recorded, so this is not a provider end")
		require.Equal(t, member.EndIdleReap, result.End)
		require.Equal(t, swarm.IdleCauseNeverStarted, result.EndCause)
		require.Equal(t, "idle-reap: harness-never-started: no RESULT.md shape", why, "the member's finish carries the cause, never only the shape")
	})

	t.Run("a refusal for credit makes the killed run a provider end", func(t *testing.T) {
		t.Parallel()
		log, _, result, why := silentRunOf(t, "silentcredit", "FAKE-SILENT\n", func(_, harnessLog string) {
			// the refusal the harness logged and printed nothing of: its record alone
			appendTo(t, harnessLog, "timestamp=2026-10-15T17:20:05.176Z level=ERROR run=r1 message=\"stream error\" providerID=openrouter error.error=\"AI_APICallError: Insufficient credits. Add more using https://openrouter.test/settings/credits\"\n")
		})
		require.Contains(t, log, "NATIVE PROVIDER-FAIL label=silentcredit", "the run was killed and still ends as the provider's:\n%s", log)
		require.Contains(t, log, "class=out-of-credit")
		require.Equal(t, member.EndProvider, result.End, "the provider line is read before the idle reap")
		require.Contains(t, result.Provider, "class=out-of-credit")
		require.Contains(t, why, "class=out-of-credit", "the finish is the provider's, so the sprint rests the route and redeals the card")
		require.NotContains(t, why, "no RESULT.md shape")
		require.NotContains(t, log, "cause=harness-never-started", "a refused run is not a harness that never started")
	})

	t.Run("a harness that worked and then went still keeps the plain idle reap", func(t *testing.T) {
		t.Parallel()
		log, res, result, why := silentRunOf(t, "workedthenstill", "FAKE-SILENT\n", func(capture, _ string) {
			// it printed a line of its own before it went still
			appendTo(t, capture, "thinking about the sixteenth step\n")
		})
		require.Equal(t, "ok", res.harness, "it printed before it stopped:\n%s", log)
		require.Empty(t, res.idleEnd.Cause)
		require.Contains(t, log, " reason=idle-reap")
		require.NotContains(t, log, "cause=", "an idle reap after work began names no cause")
		require.Equal(t, member.EndIdleReap, result.End)
		require.Empty(t, result.EndCause)
		require.Equal(t, "idle-reap: no RESULT.md shape", why)
	})
}
