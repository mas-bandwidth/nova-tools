package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unit coverage for the reminder helpers of remind.go that no other test
// reaches: pure logic over fixed clock readings, no store, no socket.

func TestRemindCoverFind(t *testing.T) {
	t.Parallel()
	g := Goals{People: []Goal{{Name: "reader-a"}, {Name: "reader-b"}}}
	require.Equal(t, 0, g.Find("reader-a"), "the first person's index")
	require.Equal(t, 1, g.Find("reader-b"), "the second person's index")
	assert.Equal(t, -1, g.Find("reader-c"), "a name that is not there")
	var none Goals
	assert.Equal(t, -1, none.Find("reader-a"), "no people at all")
}

func TestRemindCoverFailureWhat(t *testing.T) {
	t.Parallel()
	g := Goal{Name: "reader-a", Route: "file:/tmp/notes", Fail: "no such file"}
	assert.Equal(t, "the reminder to reader-a over file:/tmp/notes failed: no such file", FailureWhat(g),
		"the judgment names the person, the route and the error")
}

func TestRemindCoverFailing(t *testing.T) {
	t.Parallel()
	t.Run("the failing routes", func(t *testing.T) {
		t.Parallel()
		g := Goals{People: []Goal{
			{Name: "reader-a", Route: "file:/tmp/notes", Fail: "no such file"},
			{Name: "reader-b", Route: "bus:/tmp/bus"},
		}}
		want := map[string]string{"reader-a": FailureWhat(g.People[0])}
		assert.Equal(t, want, g.Failing(), "only the failing person, by their judgment line")
	})
	t.Run("none failing", func(t *testing.T) {
		t.Parallel()
		g := Goals{People: []Goal{{Name: "reader-b", Route: "bus:/tmp/bus"}}}
		assert.Empty(t, g.Failing(), "a person whose route delivers is not judged")
		var none Goals
		assert.Empty(t, none.Failing(), "no people, no judgments")
	})
}

func TestRemindCoverRemindNotes(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	failing := Goal{Name: "reader-a", Route: "file:/tmp/notes", Fail: "no such file"}
	reached := Goal{Name: "reader-b", Route: "bus:/tmp/bus"}
	t.Run("a judgment per failing route", func(t *testing.T) {
		t.Parallel()
		p := RemindNotes(&Snapshot{Now: now}, Goals{People: []Goal{failing, reached}}, "coordinator")
		require.Len(t, p.Notes, 1, "one judgment, for the failing route alone")
		n := p.Notes[0]
		assert.Equal(t, Judgment, n.Kind)
		assert.Equal(t, NRemindFailed, n.Type)
		assert.Equal(t, FailureWhat(failing), n.What)
		assert.Equal(t, "coordinator", n.Who)
		assert.Equal(t, now, n.At)
		assert.True(t, n.StreamLevel, "the judgment is on the sprint as a whole")
		assert.True(t, n.Marked)
		assert.Equal(t, []string{"goal set reader-a --to <route>", "goal drop reader-a", "ack"}, n.Decisions,
			"the judgment lists the decisions that answer it")
		assert.Empty(t, p.Units, "the plan writes notes only")
	})
	t.Run("nothing failing writes nothing", func(t *testing.T) {
		t.Parallel()
		p := RemindNotes(&Snapshot{Now: now}, Goals{People: []Goal{reached}}, "coordinator")
		assert.Equal(t, Plan{}, p, "every route delivers: no judgment")
	})
	t.Run("an open judgment is not written again", func(t *testing.T) {
		t.Parallel()
		g := Goals{People: []Goal{failing}}
		open := Open{Key: OpenKey("j1", StreamSubject("")),
			Note: Note{ID: "j1", Kind: Judgment, Type: NRemindFailed, StreamLevel: true, What: FailureWhat(failing), At: now}}
		p := RemindNotes(&Snapshot{Now: now, Open: []Open{open}}, g, "coordinator")
		assert.Empty(t, p.Notes, "the judgment stands: not written once more")
		assert.Empty(t, p.Closes, "the route still fails: not closed")
	})
	t.Run("a judgment whose person is reached closes", func(t *testing.T) {
		t.Parallel()
		open := Open{Key: OpenKey("j1", StreamSubject("")),
			Note: Note{ID: "j1", Kind: Judgment, Type: NRemindFailed, StreamLevel: true, What: FailureWhat(reached), At: now}}
		p := RemindNotes(&Snapshot{Now: now, Open: []Open{open}}, Goals{People: []Goal{reached}}, "coordinator")
		require.Len(t, p.Closes, 1, "the delivery arrived: the judgment closes")
		assert.Equal(t, open, p.Closes[0])
	})
}

func TestRemindCoverDueAt(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	stoppedFor := func(d time.Duration) func(time.Time, time.Time) time.Duration {
		return func(time.Time, time.Time) time.Duration { return d }
	}
	t.Run("who is due and who is not", func(t *testing.T) {
		t.Parallel()
		since := t0.Add(-2 * time.Hour) // this run of the machine began two hours ago
		for _, tc := range []struct {
			why     string
			goal    Goal
			stopped func(from, to time.Time) time.Duration
			want    bool
		}{
			{why: "just set pushes at once", goal: Goal{Name: "a", Pending: true}, want: true},
			{why: "never pushed to", goal: Goal{Name: "b"}, want: true},
			{why: "the last push was before this run began", goal: Goal{Name: "c", Last: since.Add(-time.Minute)}, want: true},
			{why: "a push a moment ago", goal: Goal{Name: "d", Last: t0.Add(-time.Second)}, want: false},
			{why: "the interval of running time passed", goal: Goal{Name: "e", Last: t0.Add(-RemindEvery)}, want: true},
			{why: "an hour of wall time is not running time when the machine was stopped", goal: Goal{Name: "f", Last: t0.Add(-time.Hour)},
				stopped: stoppedFor(time.Hour), want: false},
			{why: "the interval counts running time alone", goal: Goal{Name: "g", Last: t0.Add(-time.Hour - RemindEvery)},
				stopped: stoppedFor(time.Hour), want: true},
			{why: "a failed attempt is the attempt", goal: Goal{Name: "h", Last: t0.Add(-time.Hour), Tried: t0.Add(-RemindEvery)}, want: true},
			{why: "an earlier failed attempt is not the attempt", goal: Goal{Name: "i", Last: t0.Add(-RemindEvery), Tried: t0.Add(-time.Second)}, want: false},
		} {
			g := Goals{People: []Goal{tc.goal}}
			got := g.DueAt(t0, since, tc.stopped)
			assert.Equal(t, tc.want, len(got) == 1, tc.why)
		}
	})
	t.Run("the record keeps its order", func(t *testing.T) {
		t.Parallel()
		g := Goals{People: []Goal{
			{Name: "reader-a", Pending: true},
			{Name: "reader-b", Last: t0.Add(-time.Second)},
			{Name: "reader-c", Last: t0.Add(-RemindEvery)},
		}}
		due := g.DueAt(t0, t0.Add(-2*time.Hour), nil)
		require.Len(t, due, 2, "the push a moment ago is not due")
		assert.Equal(t, "reader-a", due[0].Name)
		assert.Equal(t, "reader-c", due[1].Name)
	})
}

func TestRemindCoverNotesStale(t *testing.T) {
	t.Parallel()
	failing := Goal{Name: "reader-a", Route: "file:/tmp/notes", Fail: "no such file"}
	want := map[string]string{"reader-a": FailureWhat(failing)}
	t.Run("the notes agree", func(t *testing.T) {
		t.Parallel()
		g := Goals{People: []Goal{failing}, Noted: map[string]string{"reader-a": FailureWhat(failing)}}
		got, stale := g.NotesStale()
		assert.False(t, stale, "the notes are the ones the failing route calls for")
		assert.Equal(t, want, got)
	})
	t.Run("the notes are stale", func(t *testing.T) {
		t.Parallel()
		g := Goals{People: []Goal{failing}, Noted: map[string]string{"reader-a": "an old line"}}
		got, stale := g.NotesStale()
		assert.True(t, stale, "an old line is not the judgment the route calls for")
		assert.Equal(t, want, got, "the plan names the notes to write")
	})
	t.Run("no failing routes and no notes", func(t *testing.T) {
		t.Parallel()
		got, stale := Goals{}.NotesStale()
		assert.False(t, stale)
		assert.Empty(t, got)
	})
}
