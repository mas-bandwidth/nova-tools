package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// I1: a group's id is its oldest open notification's, whatever sorts ahead
// of it and whatever deadline it is read with; its size is its members.
func TestAGroupIDIsItsOldestNoteAndDoesNotMove(t *testing.T) {
	t.Parallel()
	now := t0.Add(time.Hour)
	a := Note{ID: "n1", Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0}
	b := Note{ID: "n2", Kind: Judgment, Type: NWorkFailed, Stream: "s1", At: t0.Add(55 * time.Minute)}
	open := []Open{{Key: "n2|p2", Note: b}, {Key: "n1|p1", Note: a}}
	for _, deadline := range []time.Duration{10 * time.Minute, 2 * time.Hour} {
		g := Inbox(InboxReq{Now: now, Open: open, Deadline: deadline})
		require.Len(t, g, 1, "deadline %s: %+v", deadline, g)
		require.Equal(t, "n1", g[0].ID, "deadline %s: %+v", deadline, g)
		require.Equal(t, 2, g[0].Size, "deadline %s: %+v", deadline, g)
		require.Len(t, g[0].Members, 2, "deadline %s: %+v", deadline, g)
	}
	marked := Note{ID: "n3", Kind: Judgment, Type: NReadBroken, Stream: "s2", At: now, Marked: true}
	g := Inbox(InboxReq{Now: now, Open: append(open, Open{Key: "n3|p3", Note: marked}), Deadline: 2 * time.Hour})
	require.Equal(t, "n3", g[0].ID, "ids: %+v", g)
	require.Equal(t, "n1", g[1].ID, "ids: %+v", g)
	f, ok := FindGroup(g, "n1")
	require.True(t, ok, "find: %+v", f)
	require.Equal(t, NWorkFailed, f.Type, "find: %+v", f)
	stopped := Note{ID: "n4", Kind: Judgment, Type: NRed, Stream: "s3", At: now, StreamLevel: true, Primaries: []string{"q1", "q2", "q3"}}
	g = Inbox(InboxReq{Now: now, Open: []Open{{Key: "n4|stream:s3", Note: stopped}},
		Streams: []StreamClock{{Stream: "s4", State: StreamMerging, Progress: t0}}, Stale: time.Minute})
	require.Equal(t, "n4", g[0].ID, "a stopped stream and a stale one: %+v", g)
	require.Equal(t, 3, g[0].Size, "a stopped stream and a stale one: %+v", g)
	require.Equal(t, StaleGroupID("s4"), g[1].ID, "a stopped stream and a stale one: %+v", g)
}

// TestAGroupsMembersAreCardsAndNeverAStreamOrTheSprint pins Members: the
// subjects of a judgment's open notes that are cards. The subject of a stream
// level or sprint level judgment is no card, whatever the note says of itself.
func TestAGroupsMembersAreCardsAndNeverAStreamOrTheSprint(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		subject string
		want    []string
	}{
		{"a card", "p1", []string{"p1"}},
		{"a stream", StreamSubject("s1"), nil},
		{"the sprint", SprintSubject, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			n := Note{ID: "n1", Kind: Judgment, Type: NSprintDone, At: t0}
			g := Inbox(InboxReq{Now: t0.Add(time.Hour), Open: []Open{{Key: OpenKey(n.ID, c.subject), Note: n}}})
			if !assert.Len(t, g, 1, "%s: %+v, want members %v", c.name, g, c.want) {
				return
			}
			assert.ElementsMatch(t, c.want, g[0].Members, "%s: %+v, want members %v", c.name, g, c.want)
			assert.Equal(t, len(c.want), g[0].Size, "%s: %+v, want members %v", c.name, g, c.want)
			assert.Equal(t, 1, g[0].Count, "%s: %+v, want members %v", c.name, g, c.want)
		})
	}
}
