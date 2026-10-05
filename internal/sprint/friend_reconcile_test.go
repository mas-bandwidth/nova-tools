package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// friend reconcile (docs/SPEC-SPRINT.md section 1, friend reconcile; the owner, 2026-10-04:
// "trust but VERIFY"; "Are they actually doing the work that is shown in the friend table?
// Really?"): each card working on a friend's row is compared with her inbox/QUEUE.json and
// her outbox, and is collected (her report is there), kept (her queue holds it queued or
// working) or returned to ready (her queue says done or does not hold it, and no report is
// there).

const reconcileHead = "0123456789abcdef0123456789abcdef01234567"

func TestFriendReconcileCollectsOrReturnsEachCard(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 4, Status: Up})
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1"} {
		require.Equal(t, Working, w.s.Fleet.Card(id).Col, "every card is working on her row")
	}

	// her own account: s1-1 is done and reported, s1-2 she still works, s1-3 she says
	// done with no report, s1-4 her queue does not hold; one id is no card of her row
	tasks, err := ReadFriendQueue([]byte(`{"tasks":[{"id":"s1-1.w1","state":"done"},{"id":"s1-2.w1~3","state":"working"},{"id":"s1-3.w1","state":"done"},{"id":"zz-9.w1","state":"working"}]}`))
	require.NoError(t, err)
	queue := FriendAccount{Tasks: tasks, Written: t0.Add(time.Hour)}
	reported := map[string]bool{"s1-1.w1": true}
	want := map[string]string{"s1-1.w1": ReconcileCollect, "s1-2.w1": ReconcileKeep, "s1-3.w1": ReconcileReturn, "s1-4.w1": ReconcileReturn}
	var held []string
	var back []FriendReturnCard
	for _, id := range []string{"s1-1.w1", "s1-2.w1", "s1-3.w1", "s1-4.w1"} {
		job := StoredID(id, 0)
		if id == "s1-2.w1" {
			job = StoredID(id, 3) // her queue may name the job directory, the card at its epoch
		}
		action, why := FriendReconcileOf(w.s.Fleet.Card(id), job, reported[id], queue)
		assert.Equal(t, want[id], action, id)
		assert.NotEmpty(t, why, "%s: every action says why", id)
		held = append(held, id, job)
		if action == ReconcileReturn {
			back = append(back, FriendReturnCard{ID: id, Gen: w.s.Fleet.Card(id).Int("gen"), Why: why})
		}
	}
	assert.Equal(t, []string{"zz-9.w1"}, FriendQueueStrays(tasks, held), "a queue id that is no card of her row is named, never acted on")

	// collected: her LAND finishes the card as hers, and its primary goes to review
	collect := w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Who: FriendRow("amy"),
		Gens: w.gens("s1-1.w1"), Head: reconcileHead, Branch: "sprint/s1-1.w1.g1.e0", Report: "friend amy LAND: pushed"}))
	require.Len(t, collect.Units, 1, "one history line for the collected card")
	assert.Contains(t, collect.Units[0].Moved, "s1-1.w1")
	assert.Equal(t, Review, w.state("s1-1"))

	// returned: each abandoned card is retired off her row and its primary is ready for
	// the tick to deal again, one history line and one happened note each
	before := len(w.notesOf(NFriendReturned))
	ret := w.must(FriendReturn(w.s, FriendReturnReq{Friend: "amy", Who: "coordinator", Cards: back}))
	require.Len(t, ret.Units, 2, "one history line for each returned card")
	for i, id := range []string{"s1-3.w1", "s1-4.w1"} {
		u := ret.Units[i]
		assert.Equal(t, 1, strings.Count(u.Moved, id+" working -> retired, returned by friend reconcile"), "%s: %s", id, u.Moved)
		wc := w.s.Fleet.Card(id)
		assert.False(t, wc.Placed(), "%s: off her row, its record kept", id)
		assert.Equal(t, RetiredByReconcile, wc.F("retired_by"))
		assert.Equal(t, back[i].Why, wc.F("return_reason"))
		assert.Empty(t, wc.F(FieldTakeEnded), "a return spends no redeal: the take was never the card's")
		assert.Equal(t, Ready, w.state(strings.TrimSuffix(id, ".w1")), "%s: its primary is ready again", id)
		assert.Empty(t, w.s.Primary(strings.TrimSuffix(id, ".w1")).F("work"), "%s: its primary names no work card", id)
	}
	notes := w.notesOf(NFriendReturned)[before:]
	require.Len(t, notes, 2)
	assert.Equal(t, "coordinator", notes[0].Who)
	assert.Contains(t, notes[0].What, "QUEUE.json says done")
	assert.Contains(t, notes[1].What, "written at 2030-01-02T04:04:05Z after it was dealt to her at 2030-01-02T03:04:05Z, does not hold it")

	// kept: the card she still works is left as it was
	assert.Equal(t, Working, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, Working, w.state("s1-2"))
	w.clean("after the reconcile")

	// the tick deals a returned card again, as its next attempt, to a friend up with room
	dealWith(w, FriendSeat{Name: "amy", Width: 4, Status: Up})
	assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w2").Col, "a returned card is dealt again at its next attempt")
	w.clean("after the deal again")

	// a second return of the same card, a card on another row, and a stale generation are refused
	again := FriendReturn(w.s, FriendReturnReq{Friend: "amy", Who: "coordinator", Cards: []FriendReturnCard{
		{ID: "s1-3.w1", Gen: 1, Why: "again"}, {ID: "s1-2.w1", Gen: 9, Why: "stale"}, {ID: "s1-2.w1", Gen: 1, Why: "twice"}}})
	require.Len(t, again.Refused, 3, "%v", again.Refused)
	assert.Contains(t, again.Refused[0].Why, "retired by friend reconcile")
	assert.Contains(t, again.Refused[1].Why, "it moved since it was read")
	assert.Contains(t, again.Refused[2].Why, "named twice")
	assert.Empty(t, FriendReturn(w.s, FriendReturnReq{Friend: "bob", Who: "coordinator", Cards: []FriendReturnCard{{ID: "s1-2.w1", Gen: 1, Why: "not hers"}}}).Units)
}

func TestReadFriendQueueRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, in, want string }{
		{"not json", `{`, "QUEUE.json"},
		{"no tasks", `{}`, "tasks"},
		{"a task with no id", `{"tasks":[{"state":"done"}]}`, "no id"},
		{"an id twice", `{"tasks":[{"id":"a.w1","state":"done"},{"id":"a.w1","state":"working"}]}`, "twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadFriendQueue([]byte(tc.in))
			assert.ErrorContains(t, err, tc.want)
		})
	}
	q, err := ReadFriendQueue([]byte(`{"tasks":[{"id":"a.w1","state":"Working "}],"other":1}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.w1": "working"}, q, "a state is read in lower case, trimmed; other keys are passed by")
	wc := &Card{ID: "a.w1", Row: FriendRow("amy"), Col: Working, Fields: map[string]string{"taken": stamp(t0)}}
	for _, tc := range []struct {
		name, want, why string
		a               FriendAccount
	}{
		{"working", ReconcileKeep, "says working", FriendAccount{Tasks: q, Written: t0.Add(time.Minute)}},
		{"a state it does not know is kept and said, never guessed at", ReconcileKeep, "paused", FriendAccount{Tasks: map[string]string{"a.w1": "paused"}, Written: t0.Add(time.Minute)}},
		{"absent, written after the deal", ReconcileReturn, "does not hold it, and outbox", FriendAccount{Written: t0.Add(time.Second)}},
		{"absent, written in the second of the deal", ReconcileKeep, "has not accounted for it yet", FriendAccount{Written: t0.Add(999 * time.Millisecond)}},
		{"absent, written before the deal", ReconcileKeep, "has not accounted for it yet", FriendAccount{Written: t0.Add(-time.Hour)}},
	} {
		action, why := FriendReconcileOf(wc, "a.w1~2", false, tc.a)
		assert.Equal(t, tc.want, action, "%s: %s", tc.name, why)
		assert.Contains(t, why, tc.why, tc.name)
	}
	action, why := FriendReconcileOf(&Card{ID: "a.w1", Row: FriendRow("amy"), Col: Working}, "a.w1", false, FriendAccount{Written: t0})
	assert.Equal(t, ReconcileKeep, action, "a card with no deal stamp is kept: %s", why)
}
