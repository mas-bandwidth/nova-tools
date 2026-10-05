package sprint

// Unit coverage for the three steps_ack.go helpers that no other test reaches:
// silenceRefusal, Wait and waitStale — pure logic over a hand-built Snapshot,
// no store, no socket, no real time.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverUntil is the running-time deadline one Wait pins to.
var coverUntil = coverT0.Add(30 * time.Minute)

// TestStepsAckCoverSilenceRefusal pins silenceRefusal: the words an ack gives for
// the primary it would leave held by nobody. One path names the judgment's other
// decisions as commands; the other, a judgment whose only decision is ack, falls
// back to "rework, return or drop it".
func TestStepsAckCoverSilenceRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		noteID    string
		decisions []string
		primary   string
		want      string
	}{
		{
			name:      "commands the judgment's other decisions",
			noteID:    "j1",
			decisions: []string{"drop", "ack"},
			primary:   "p1",
			want:      "j1 is the last judgment on p1, which would then be held by nobody (no read outstanding, nothing the tick would do, no other judgment open on it); decide instead: drop: nova-sprint drop p1 --reason '<why>'",
		},
		{
			name:      "no decisions left but ack",
			noteID:    "j2",
			decisions: []string{"ack"},
			primary:   "p2",
			want:      "j2 is the last judgment on p2, which would then be held by nobody (no read outstanding, nothing the tick would do, no other judgment open on it); decide instead: rework, return or drop it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := Note{ID: tc.noteID, Kind: Judgment, Type: NBlocked, Stream: "s1", Decisions: tc.decisions}
			// silenceRefusal reads no field of the snapshot.
			var s Snapshot
			u := Unit{Closes: []Open{{Key: OpenKey(n.ID, tc.primary), Note: n}}}
			got := silenceRefusal(&s, u, tc.primary)
			assert.Equal(t, tc.want, got)
			assert.Contains(t, got, "held by nobody", "the refusal names the primary it would leave held by nobody")
		})
	}
}

// TestStepsAckCoverWait pins Wait: it holds a tick-kept judgment until a time,
// refusing when the note is not a judgment the tick keeps.
func TestStepsAckCoverWait(t *testing.T) {
	t.Parallel()
	t.Run("holds a tick-kept judgment until a time", func(t *testing.T) {
		t.Parallel()
		n := Note{ID: "j1", Kind: Judgment, Type: NStalled, Stream: "s1", Decisions: Decisions[NStalled]}
		s := &Snapshot{Now: coverT0, Coordinator: Coordinator, Epoch: 0,
			Open: []Open{{Key: OpenKey("j1", "p1"), Note: n}}}
		r := WaitReq{Note: "j1", Until: coverUntil, Who: Coordinator}
		p := Wait(s, r)

		require.Empty(t, p.Refused, "a held judgment is not refused")
		require.Len(t, p.Units, 1, "one unit closes the judgment")
		u := p.Units[0]
		assert.Equal(t, "j1", u.Key)
		assert.Equal(t, "s1", u.Stream)
		require.Len(t, u.Closes, 1)
		assert.Equal(t, "j1", u.Closes[0].Note.ID)
		require.Len(t, u.Notes, 2, "a decided note and a kept hold")
		decided := u.Notes[0]
		assert.Equal(t, Decided, decided.Kind)
		assert.Equal(t, "j1", decided.Answers)
		assert.Equal(t, NStalled, decided.Type)
		assert.Equal(t, "s1", decided.Stream)
		assert.Equal(t, Coordinator, decided.Who)
		assert.Equal(t, coverT0, decided.At)
		assert.Contains(t, decided.What, "wait until "+coverUntil.UTC().Format(time.RFC3339))
		hold := u.Notes[1]
		assert.Equal(t, Acknowledged, hold.Kind)
		assert.Equal(t, NStalled, hold.Type)
		assert.Equal(t, "s1", hold.Stream)
		assert.Equal(t, coverUntil, hold.Review, "the hold's review time is what was asked")
		assert.Equal(t, coverT0, hold.At)
		assert.Equal(t, []string{"p1"}, hold.Primaries, "the hold names the judgment's subject")
		assert.Contains(t, u.Moved, "j1 (stalled) held until "+coverUntil.UTC().Format(time.RFC3339)+" of running time")
	})
	t.Run("refuses a judgment the tick does not keep", func(t *testing.T) {
		t.Parallel()
		n := Note{ID: "j1", Kind: Judgment, Type: NBlocked, Stream: "s1", Decisions: Decisions[NBlocked]}
		s := &Snapshot{Now: coverT0, Coordinator: Coordinator, Epoch: 0,
			Open: []Open{{Key: OpenKey("j1", "p1"), Note: n}}}
		p := Wait(s, WaitReq{Note: "j1", Until: coverUntil, Who: Coordinator})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "is not a condition the tick keeps")
		assert.Empty(t, p.Units)
	})
}

// TestStepsAckCoverWaitStale pins waitStale: it quiets a stalled stream's judgment
// by setting its review time on the stream's control card, refusing when the note
// names an epoch the sprint has cleared.
func TestStepsAckCoverWaitStale(t *testing.T) {
	t.Parallel()
	t.Run("sets the stream's stale review time", func(t *testing.T) {
		t.Parallel()
		merge := NewTable(Merge)
		merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl})
		s := &Snapshot{Now: coverT0, Epoch: 0, Merge: merge}
		r := WaitReq{Note: StaleGroupID("s1", 0), Until: coverUntil, Who: Coordinator}
		p := waitStale(s, r, "s1")

		require.Empty(t, p.Refused, "the note is of this epoch: not refused")
		require.Len(t, p.Units, 1)
		u := p.Units[0]
		assert.Equal(t, CtlID("s1"), u.Key)
		assert.Equal(t, "s1", u.Stream)
		require.Len(t, u.Changes, 1)
		c := u.Changes[0]
		assert.Equal(t, Merge, c.Table)
		assert.Equal(t, CtlID("s1"), c.Entry.ID)
		assert.Equal(t, coverUntil.UTC().Format(time.RFC3339), c.Entry.Set[FieldStaleReview])
		assert.Contains(t, u.Moved, "stream s1 not shown stale until "+coverUntil.UTC().Format(time.RFC3339))
	})
	t.Run("refuses a judgment of another epoch", func(t *testing.T) {
		t.Parallel()
		s := &Snapshot{Now: coverT0, Epoch: 1, Merge: NewTable(Merge)}
		r := WaitReq{Note: StaleGroupID("s1", 0), Until: coverUntil, Who: Coordinator}
		p := waitStale(s, r, "s1")
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "belongs to epoch 0")
		assert.Contains(t, p.Refused[0].Why, "the sprint's epoch is 1")
		assert.Empty(t, p.Units)
	})
}
