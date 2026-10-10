//go:build functional

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// THE SEAMS ARE FIELDS OF THE RUN, NOT PACKAGE VARS. These two tests hold that: two runs
// with their own deadline events never see each other's, and a run's STAGE lines land in
// the io.Writer the run holds rather than os.Stdout. Both open with t.Parallel(), so they
// run beside every other test in the package instead of swapping a package var under them.

// TestNativeSeamsTwoRunsWithDifferentDeadlinesDoNotSeeEachOther runs two cards at once,
// each handed its own deadline event through nativeRunConfig.deadlineFn. One child exits
// on its own, so its deadline never fires; the other is still sleeping when its own
// deadline fires, and the run records the kill. The recorded durations prove each run read
// its OWN field, and the two exit codes prove one run's deadline never ended the other.
func TestNativeSeamsTwoRunsWithDifferentDeadlinesDoNotSeeEachOther(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	rootA, slotA := aSlot(t)
	rootB, slotB := aSlot(t)
	require.NoError(t, os.WriteFile(filepath.Join(rootA, "card.md"), []byte("FAKE-SAY a\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(rootB, "card.md"), []byte("FAKE-SLEEP 60\n"), 0o644))

	// A's event never fires: its child exits on its own. B's fires only once B's own
	// harness has recorded its argv -- the event is on that observable, never a clock.
	afire := make(chan time.Time)
	bfire := make(chan time.Time)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(filepath.Join(slotB, "jobs", "seams-b", "argv")); err == nil {
				close(bfire)
				return
			}
			select {
			case <-quit:
				return
			case <-tick.C:
			}
		}
	}()

	type outcome struct {
		res  nativeRunResult
		err  string
		seen time.Duration
	}
	runCard := func(card []byte, slot, root, label string, deadline time.Duration, fire <-chan time.Time) <-chan outcome {
		out := make(chan outcome, 1)
		go func() {
			var errOut bytes.Buffer
			var seen time.Duration
			res, _ := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: label,
				card: card, slotDir: slot, root: root, deadline: deadline, noWall: true,
				deadlineFn: func(d time.Duration) (<-chan time.Time, func() bool) {
					seen = d
					return fire, func() bool { return true }
				},
			}, &errOut)
			out <- outcome{res: res, err: errOut.String(), seen: seen}
		}()
		return out
	}

	a := runCard([]byte("FAKE-SAY a\n"), slotA, rootA, "seams-a", 11*time.Second, afire)
	b := runCard([]byte("FAKE-SLEEP 60\n"), slotB, rootB, "seams-b", 22*time.Second, bfire)

	gotA := <-a
	require.Equal(t, 11*time.Second, gotA.seen, "run A armed its own deadline, got %s\n%s", gotA.seen, gotA.err)
	require.Equal(t, 0, gotA.res.rc, "run A's child exits on its own, so its deadline never fired; rc=%d\n%s", gotA.res.rc, gotA.err)

	gotB := <-b
	require.Equal(t, 22*time.Second, gotB.seen, "run B armed its own deadline, got %s\n%s", gotB.seen, gotB.err)
	require.Equal(t, -1, gotB.res.rc, "run B's own deadline killed its child; rc=%d\n%s", gotB.res.rc, gotB.err)
}

// TestNativeSeamsStageLinesGoToTheRunsWriter hands a run an io.Writer of its own and reads
// the STAGE lines back from it: the success path's STAGE OK and the refusal path's STAGE
// FAIL both land there, and nothing swaps os.Stdout.
func TestNativeSeamsStageLinesGoToTheRunsWriter(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	benchHome := filepath.Join(root, "bench-home")

	// The success case: a card naming no repo stages nothing and says STAGE OK.
	var okOut bytes.Buffer
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "seams-stage-ok",
		card:    []byte("RESULT: card-no-repo sha=123456789012\nKIND: read\n"),
		slotDir: slot, root: root, benchHome: benchHome, benchName: "bench-1",
		stageTimeout: 30 * time.Second, deadline: 30 * time.Second, noWall: true,
		stdout: &okOut,
	}, &errOut)
	require.Equal(t, 0, code, "a card with no repo line stages nothing and runs: code=%d\n%s", code, errOut.String())
	require.Contains(t, okOut.String(), "STAGE OK bench=bench-1", "the run's STAGE OK line lands in the writer it holds:\n%s", okOut.String())
	require.NotContains(t, errOut.String(), "STAGE OK", "the STAGE OK line is on the run's stdout, not its stderr:\n%s", errOut.String())

	// The refusal case: a card naming a repo with no bench mirror prints its STAGE FAIL
	// into the run's own writer, not os.Stdout.
	var failOut bytes.Buffer
	errOut.Reset()
	_, code = nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "seams-stage-fail",
		card:    []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nbase-sha: 1234567890123456789012345678901234567890\n"),
		slotDir: slot, root: root, benchHome: benchHome, benchName: "bench-1",
		stageTimeout: 30 * time.Second, deadline: 30 * time.Second, noWall: true,
		stdout: &failOut,
	}, &errOut)
	require.Equal(t, 2, code, "a card naming a repo with no bench mirror is refused: code=%d\n%s", code, errOut.String())
	require.Contains(t, failOut.String(), "STAGE FAIL bench=bench-1", "the run's STAGE FAIL line lands in the writer it holds:\n%s", failOut.String())
	require.Contains(t, failOut.String(), "missing-mirror", "the STAGE FAIL line names the repo it could not stage:\n%s", failOut.String())
}
