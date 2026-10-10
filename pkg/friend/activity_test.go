package friend

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeatCarriesTheLastSessionActivity(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.passive = true
	wrote := t0.Add(-37 * time.Minute)
	r.d.Activity = func() time.Time { return wrote }
	r.run(t, 3)
	require.Len(t, r.actives, 3)
	for _, got := range r.actives {
		assert.True(t, got.Equal(wrote), "every beat carries the newest write the scan found, got %v", got)
	}
}

func TestABeatWithNoScanCarriesNoActivity(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.passive = true
	r.run(t, 2)
	for _, got := range r.actives {
		assert.True(t, got.IsZero())
	}
}

func TestTheScanIsRunAtMostOnceAnActivityWindow(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.passive = true
	scans := 0
	r.d.Activity = func() time.Time { scans++; return t0 }
	r.run(t, int(3*ActivityEvery/BeatEvery))
	assert.GreaterOrEqual(t, scans, 2)
	assert.Less(t, scans*2, len(r.actives), "one stat walk per window, not one per beat")
}

func TestNewestWriteIsTheNewestFileUnderTheRoots(t *testing.T) {
	t.Parallel()
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	fsys := fstest.MapFS{
		"outbox/job/REPORT.md": {ModTime: at(5)},
		"inbox/job/BRIEF.md":   {ModTime: at(9)},
		"jobs/job/repo/a.go":   {ModTime: at(7)},
		"other/x":              {ModTime: at(99)},
	}
	got := NewestWrite(fsys, []string{"outbox", "inbox", "jobs"}, func() time.Time { return t0 }, ActivityLimits{Files: 100, Time: time.Second})
	assert.True(t, got.Equal(at(9)), "got %v", got)
	assert.True(t, NewestWrite(fsys, []string{"nothing"}, func() time.Time { return t0 }, ActivityLimits{Files: 100, Time: time.Second}).IsZero(), "a root that is not there has no write")
}

func TestNewestWriteStopsAtTheFileBound(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"outbox/a": {ModTime: t0.Add(1 * time.Minute)},
		"outbox/b": {ModTime: t0.Add(2 * time.Minute)},
		"jobs/c":   {ModTime: t0.Add(50 * time.Minute)},
	}
	got := NewestWrite(fsys, []string{"outbox", "jobs"}, func() time.Time { return t0 }, ActivityLimits{Files: 2, Time: time.Second})
	assert.True(t, got.Equal(t0.Add(2*time.Minute)), "two entries read, the third never: got %v", got)
}

func TestNewestWriteStopsAtTheTimeBound(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"outbox/a": {ModTime: t0.Add(1 * time.Minute)},
		"outbox/b": {ModTime: t0.Add(2 * time.Minute)},
		"outbox/c": {ModTime: t0.Add(3 * time.Minute)},
	}
	tick := t0
	now := func() time.Time { tick = tick.Add(10 * time.Millisecond); return tick }
	got := NewestWrite(fsys, []string{"outbox"}, now, ActivityLimits{Files: 100, Time: 25 * time.Millisecond})
	assert.False(t, got.IsZero(), "what was read before the clock ran out stands")
	assert.False(t, got.Equal(t0.Add(3*time.Minute)), "the last file was never reached")
}

func TestNewestWriteSkipsTheDirectoriesNoSessionWritesIn(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"jobs/j/repo/.git/index":   {ModTime: t0.Add(90 * time.Minute)},
		"jobs/j/repo/main.go":      {ModTime: t0.Add(4 * time.Minute)},
		"jobs/j/.cache/go-build/x": {ModTime: t0.Add(80 * time.Minute)},
	}
	got := NewestWrite(fsys, []string{"jobs"}, func() time.Time { return t0 }, ActivityLimits{Files: 100, Time: time.Second})
	assert.True(t, got.Equal(t0.Add(4*time.Minute)), "got %v", got)
}

func TestNewestWriteReadsARootOnceAndTheRestOfTheDirectoryAfter(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"outbox/a": {ModTime: t0.Add(1 * time.Minute)},
		"notes.md": {ModTime: t0.Add(6 * time.Minute)},
	}
	// two files exist; a bound of two is enough only if outbox/a is not counted twice
	got := NewestWrite(fsys, ActivityRoots, func() time.Time { return t0 }, ActivityLimits{Files: 2, Time: time.Second})
	assert.True(t, got.Equal(t0.Add(6*time.Minute)), "got %v", got)
}
