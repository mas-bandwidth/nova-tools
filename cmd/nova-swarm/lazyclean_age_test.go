package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lazy cleaner's age rule (lazyclean.go): a launch's small file in the slots
// (<launch>.native.log, .card.md, the frame and .pid) goes a day after its launch ended,
// whatever its epoch; a running, claimed or live-pid launch, and one the pool keeps for
// inspection, keep theirs. The round is bounded, old-epoch entries and aged files sharing
// lazyRound. Each round is driven by an explicit call with an injected now: nothing waits on
// the cleaner's clock.

// TestEndedLaunchFilesOfTheCurrentEpochGoAfterADay pins the rule: the small files of a
// current-epoch launch 25 hours old are removed even though the old-epoch rule never touches
// its epoch; its directory and its results stay.
func TestEndedLaunchFilesOfTheCurrentEpochGoAfterADay(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	name := p.oldLaunch("c1", 14, time.Now().Add(-25*time.Hour))
	p.r.Epoch(14)

	p.r.lazy(time.Now())

	assert.Equal(t, 2, p.entries(name), "the small files of a current-epoch launch go after a day; its directory and results stay")
	assert.DirExists(t, filepath.Join(p.slots, name))
	assert.DirExists(t, filepath.Join(p.results, name))
}

// TestEndedLaunchFilesYoungerThanADayStay pins the age: a launch's small files written less
// than a day ago stay.
func TestEndedLaunchFilesYoungerThanADayStay(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	name := p.oldLaunch("c1", 14, time.Now().Add(-time.Hour))
	p.r.Epoch(14)

	p.r.lazy(time.Now())

	assert.Equal(t, 5, p.entries(name), "files younger than a day stay")
}

// TestEndedLaunchFilesOfARunningClaimedOrLivePidLaunchStay pins the ending: a launch this
// process runs (claimed) and one whose pid file names a live process keep every small file.
func TestEndedLaunchFilesOfARunningClaimedOrLivePidLaunchStay(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	day := time.Now().Add(-25 * time.Hour)
	claimed := p.oldLaunch("claimed", 14, day)
	p.r.started(claimed)
	running := p.oldLaunch("running", 14, day)
	pid := filepath.Join(p.slots, running+".pid")
	write(t, pid, strconv.Itoa(os.Getpid())+"\n")
	require.NoError(t, os.Chtimes(pid, day, day))
	p.r.Epoch(14)

	p.r.lazy(time.Now())

	for _, name := range []string{claimed, running} {
		assert.Equal(t, 5, p.entries(name), "%s keeps its small files", name)
	}
	assert.FileExists(t, pid, "the pid file of a live launch stays")
}

// TestEndedLaunchFilesOfAKeptFailedLaunchStay pins the exception: a launch the pool keeps for
// inspection keeps its small files, the .native.log among them.
func TestEndedLaunchFilesOfAKeptFailedLaunchStay(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	name := p.oldLaunch("failed", 14, time.Now().Add(-25*time.Hour))
	p.r.mu.Lock()
	p.r.kept = map[string]bool{name: true}
	p.r.mu.Unlock()
	p.r.Epoch(14)

	p.r.lazy(time.Now())

	assert.Equal(t, 5, p.entries(name), "a kept failed launch keeps the files the inspection reads")
}

// TestEndedLaunchFilesShareTheRoundBound pins the shared bound: with more than lazyRound
// eligible aged files, one round removes exactly lazyRound entries, old-epoch entries and
// aged files together.
func TestEndedLaunchFilesShareTheRoundBound(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	day := time.Now().Add(-25 * time.Hour)
	n := lazyRound + 5
	names := make([]string, n)
	for i := range n {
		names[i] = p.oldLaunch("c"+strconv.Itoa(i), 14, day)
	}
	p.r.Epoch(14)

	p.r.lazy(time.Now())

	left := 0
	for _, name := range names {
		left += p.entries(name)
	}
	assert.Equal(t, 5*n-lazyRound, left, "one round removes at most lazyRound entries, old epochs and aged files together")
	assert.Contains(t, p.errb.String(), "CLEAN ended launch files: removed "+strconv.Itoa(lazyRound)+" entries")
}
