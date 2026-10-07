package sprint

// Unit coverage for steps_clear.go's RestoreShape, the plan that gives a new
// epoch its stream and member control cards: the reader found it at 0.0% and no
// other test reaches it. A hand-built Snapshot and Shape, Now given rather than
// read: no store, no socket, no real time, no subprocess.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStepsClearCoverRestoreShape pins RestoreShape's main path (every stream
// and member gets a create unit carrying its status, hold and width) and its
// skip path (a control card the epoch already has is left as it is).
func TestStepsClearCoverRestoreShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	since := now.UTC().Format(time.RFC3339)

	t.Run("gives every stream and member a control card", func(t *testing.T) {
		t.Parallel()
		s := &Snapshot{Now: now, Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
		sh := Shape{
			Streams: []string{"s1", "s2"},
			Members: []string{"m1", "m2", "m3"},
			Status:  map[string]string{"m1": Up, "m2": Down, "m3": Up},
			Held:    []string{"m1", "m2"},
			HeldBy:  map[string]string{"m2": "a friend"},
			Width:   map[string]string{"m1": "3"},
		}
		p := RestoreShape(s, sh)

		require.Empty(t, p.Refused, "RestoreShape refuses nothing: %+v", p)
		require.Len(t, p.Units, 5, "two streams then three members: %+v", p)

		stream := p.Units[0]
		assert.Equal(t, CtlID("s1"), stream.Key)
		assert.Equal(t, "s1", stream.Stream)
		require.Len(t, stream.Changes, 1)
		assert.Equal(t, Merge, stream.Changes[0].Table)
		e := stream.Changes[0].Entry
		assert.Equal(t, CtlID("s1"), e.ID)
		require.NotNil(t, e.Expect, "a create guards the card absent")
		assert.True(t, e.Expect.Absent)
		require.NotNil(t, e.Create)
		assert.Equal(t, "s1", e.Create.Row)
		assert.Equal(t, Ctl, e.Create.Col)
		assert.Zero(t, e.Create.Score)
		assert.Equal(t, map[string]string{"kind": "stream", "state": StreamWaiting}, e.Set)
		assert.Equal(t, "stream s1 waiting", stream.Moved)
		assert.Equal(t, CtlID("s2"), p.Units[1].Key, "every stream, in order")

		m1 := p.Units[2]
		assert.Equal(t, CtlID("m1"), m1.Key)
		assert.Empty(t, m1.Stream, "a member unit is no stream's")
		require.Len(t, m1.Changes, 1)
		assert.Equal(t, Fleet, m1.Changes[0].Table)
		e = m1.Changes[0].Entry
		assert.Equal(t, CtlID("m1"), e.ID)
		require.NotNil(t, e.Expect)
		assert.True(t, e.Expect.Absent)
		require.NotNil(t, e.Create)
		assert.Equal(t, "m1", e.Create.Row)
		assert.Equal(t, Ctl, e.Create.Col)
		assert.Zero(t, e.Create.Score)
		assert.Equal(t, map[string]string{
			"kind": "member", "status": Up, "since": since, "held": since, FieldWidth: "3",
		}, e.Set)
		assert.Equal(t, "member m1 up", m1.Moved)

		m2 := p.Units[3]
		assert.Equal(t, CtlID("m2"), m2.Key)
		assert.Equal(t, map[string]string{
			"kind": "member", "status": Down, "since": since, "held": since, FieldHeldBy: "a friend",
		}, m2.Changes[0].Entry.Set)
		assert.Equal(t, "member m2 down", m2.Moved)

		m3 := p.Units[4]
		assert.Equal(t, CtlID("m3"), m3.Key)
		assert.Equal(t, map[string]string{
			"kind": "member", "status": Up, "since": since,
		}, m3.Changes[0].Entry.Set, "an unheld member of the default width carries neither held nor width")
		assert.Equal(t, "member m3 up", m3.Moved)
	})

	t.Run("leaves a control card the epoch already has", func(t *testing.T) {
		t.Parallel()
		merge := NewTable(Merge)
		merge.Put(&Card{ID: CtlID("s1"), Row: "s1", Col: Ctl})
		fleet := NewTable(Fleet)
		fleet.Put(&Card{ID: CtlID("m1"), Row: "m1", Col: Ctl})
		s := &Snapshot{Now: now, Merge: merge, Fleet: fleet}
		sh := Shape{
			Streams: []string{"s1", "s2"},
			Members: []string{"m1", "m2"},
			Status:  map[string]string{"m1": Up, "m2": Up},
		}
		p := RestoreShape(s, sh)

		require.Len(t, p.Units, 2, "the cards already present are skipped: %+v", p)
		assert.Equal(t, CtlID("s2"), p.Units[0].Key)
		assert.Equal(t, CtlID("m2"), p.Units[1].Key)
	})
}
