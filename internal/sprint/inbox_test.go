package sprint

import (
	"testing"
	"time"
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
		if len(g) != 1 || g[0].ID != "n1" || g[0].Size != 2 || len(g[0].Members) != 2 {
			t.Fatalf("deadline %s: %+v", deadline, g)
		}
	}
	marked := Note{ID: "n3", Kind: Judgment, Type: NReadBroken, Stream: "s2", At: now, Marked: true}
	g := Inbox(InboxReq{Now: now, Open: append(open, Open{Key: "n3|p3", Note: marked}), Deadline: 2 * time.Hour})
	if g[0].ID != "n3" || g[1].ID != "n1" {
		t.Fatalf("ids: %+v", g)
	}
	if f, ok := FindGroup(g, "n1"); !ok || f.Type != NWorkFailed {
		t.Fatalf("find: %+v", f)
	}
	stopped := Note{ID: "n4", Kind: Judgment, Type: NRed, Stream: "s3", At: now, StreamLevel: true, Primaries: []string{"q1", "q2", "q3"}}
	g = Inbox(InboxReq{Now: now, Open: []Open{{Key: "n4|stream:s3", Note: stopped}},
		Streams: []StreamClock{{Stream: "s4", State: StreamMerging, Progress: t0}}, Stale: time.Minute})
	if g[0].ID != "n4" || g[0].Size != 3 || g[1].ID != StaleGroupID("s4") {
		t.Fatalf("a stopped stream and a stale one: %+v", g)
	}
}
