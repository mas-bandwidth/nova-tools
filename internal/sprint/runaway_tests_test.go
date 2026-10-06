package sprint_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/require"
)

// TestFleetBeatRunawayTestsRaisesOneAlarm is the twin store (store.Mem): a beat
// whose test-process count is over the threshold raises one judgment, a second
// beat over it raises none, and a beat under half the threshold ends the episode.
// The count is written on the beat record. Nothing here opens a socket, sleeps
// on the wall clock, or looks at a live process.
func TestFleetBeatRunawayTestsRaisesOneAlarm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := store.NewMem()
	var mu sync.Mutex
	now := time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)
	n := 0
	st := &store.Store{
		B:     m,
		Names: sprint.Names{Prefix: "t-"},
		Actor: "coordinator",
		Now:   func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		NewID: func() string { mu.Lock(); defer mu.Unlock(); n++; return strconv.Itoa(n) },
		Sleep: func(time.Duration) {},
	}
	require.NoError(t, st.Init(ctx))
	require.NoError(t, m.SetCoordinator(ctx, "coordinator"))
	res, err := st.Run(ctx, store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Who: "coordinator", Width: 1}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	_, _, _, err = st.SetMachine(ctx, true)
	require.NoError(t, err)
	snap, err := st.Load(ctx, store.All, nil)
	require.NoError(t, err)
	require.Equal(t, 1, snap.Width("m1"))
	require.Equal(t, sprint.TestProcsPerWidth, snap.RunawayTestLimit("m1"))

	beat := func(tests, oldest int) {
		t.Helper()
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		zero := 0.0
		_, err := st.Beat(ctx, "m1", &zero, hostload.Source{})
		require.NoError(t, err)
		raw, ok, err := m.GetKey(ctx, sprint.BeatRecordKey("m1"))
		require.NoError(t, err)
		require.True(t, ok, "the beat record")
		stamped, err := sprint.StampTests(raw, tests, oldest, false)
		require.NoError(t, err)
		require.NoError(t, m.SetKey(ctx, sprint.BeatRecordKey("m1"), stamped))
	}
	tick := func() {
		t.Helper()
		_, err := st.Tick(ctx)
		require.NoError(t, err)
	}
	open := func() []sprint.Note {
		t.Helper()
		s, err := st.Load(ctx, store.All, nil)
		require.NoError(t, err)
		var out []sprint.Note
		for _, o := range s.Open {
			if o.Note.Kind == sprint.Judgment && o.Note.Type == sprint.NRunawayTests {
				out = append(out, o.Note)
			}
		}
		return out
	}
	logged := func() int {
		t.Helper()
		lines, err := st.Log(ctx)
		require.NoError(t, err)
		k := 0
		for _, l := range lines {
			if l.Note != nil && l.Note.Kind == sprint.Judgment && l.Note.Type == sprint.NRunawayTests {
				k++
			}
		}
		return k
	}

	const want = "runaway test processes on m1: 5, oldest parent 4242"
	beat(5, 4242)
	tick()
	got := open()
	require.Len(t, got, 1)
	require.Equal(t, want, got[0].What)
	require.Equal(t, []string{"ack", "wait"}, got[0].Decisions)
	require.Equal(t, 1, logged())

	beat(9, 4242)
	tick()
	got = open()
	require.Len(t, got, 1, "a second beat over the threshold raises none")
	require.Equal(t, want, got[0].What, "the open judgment stays the first beat's")
	require.Equal(t, 1, logged())

	beat(1, 0)
	tick()
	require.Empty(t, open(), "a beat under half the threshold ends the episode")
	require.Equal(t, 1, logged(), "ending the episode writes no second judgment")
}
