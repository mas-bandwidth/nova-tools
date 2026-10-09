package friend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTheFolderCheckAndTheBeatThatCarriesTheFileShareOneRunStep watches Run's
// StepBeatForTests seam (daemon.go: the walk, then Beat; activity.go
// NewestWrite; tla/Friend.tla FileBeforeBeat). Activity and Beat record that
// they were called and the rig step clock. They do not decide the order.
// The clock adds one BeatEvery per Now, and the loop reads it once per
// iteration. The file is written on that clock when the next walk is due,
// before either call of that step, so a beat that runs first still carries
// the previous walk. The first time the walk reads the file and the beat
// that carries that time share one step, and the read is earlier in the
// step. A walk that finds nothing still beats with the zero time; that beat
// is not this one.
func TestTheFolderCheckAndTheBeatThatCarriesTheFileShareOneRunStep(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.passive = true
	dir := r.d.Dir
	path := filepath.Join(dir, "outbox", "note.txt")
	fileAt := t0.Add(5 * time.Minute)

	type seamCall struct {
		kind   string
		step   time.Time
		active time.Time
	}
	var calls []seamCall
	stepOf := func() time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.now // already advanced once this iteration; reading it adds nothing
	}

	clock := r.d.Now
	var reads int
	var firstStep time.Time
	var placed bool
	r.d.Now = func() time.Time {
		now := clock()
		reads++
		// Start reads the clock once before the loop. The loop's first Now is
		// the empty walk; the next walk is one ActivityEvery of BeatEvery later.
		if reads == 2 {
			firstStep = now
		}
		if !placed && !firstStep.IsZero() && !now.Before(firstStep.Add(ActivityEvery)) {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte("note\n"), 0o644))
			require.NoError(t, os.Chtimes(path, fileAt, fileAt))
			placed = true
		}
		return now
	}

	r.d.Activity = func() time.Time {
		at := stepOf()
		// the walk's own clock is fixed, so the bound does not move the rig
		got := NewestWrite(os.DirFS(dir), ActivityRoots, func() time.Time { return t0 }, DefaultActivityLimits)
		r.mu.Lock()
		calls = append(calls, seamCall{kind: "activity", step: at, active: got})
		r.mu.Unlock()
		return got
	}
	beat := r.d.Beat
	r.d.Beat = func(ctx context.Context, active time.Time) error {
		at := stepOf()
		r.mu.Lock()
		calls = append(calls, seamCall{kind: "beat", step: at, active: active})
		r.mu.Unlock()
		return beat(ctx, active)
	}

	// one step past the second walk, so a beat that carries the file on the
	// following step is in the record and is not mistaken for this step
	r.run(t, int(ActivityEvery/BeatEvery)+2)

	visible, carried := -1, -1
	var zeroBeat, emptyWalk bool
	for i, c := range calls {
		if c.kind == "beat" && c.active.IsZero() {
			zeroBeat = true
		}
		if c.kind == "activity" && c.active.IsZero() {
			emptyWalk = true
		}
		if c.kind == "activity" && visible < 0 && !c.active.IsZero() {
			visible = i
		}
		if c.kind == "beat" && carried < 0 && c.active.Equal(fileAt) {
			carried = i
		}
	}
	require.True(t, placed, "the file is written when the next walk is due")
	require.True(t, emptyWalk, "the first walk finds nothing: %v", calls)
	require.True(t, zeroBeat, "an empty walk beats with the zero time, and that beat is outside this claim: %v", calls)
	require.GreaterOrEqual(t, visible, 0, "the folder check reads the file: %v", calls)
	require.True(t, calls[visible].active.Equal(fileAt), "the walk returns the file's time, got %v; calls %v", calls[visible].active, calls)
	require.GreaterOrEqual(t, carried, 0, "a beat carries the file's time: %v", calls)
	require.True(t, calls[visible].step.Equal(calls[carried].step),
		"the folder check and the beat that carries the file share one Run step: visible %s, beat %s; calls %v",
		calls[visible].step, calls[carried].step, calls)
	require.Less(t, visible, carried, "the folder check is earlier in that step than the beat that carries the file: %v", calls)
}
