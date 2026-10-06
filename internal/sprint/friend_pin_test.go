package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pinJudgments are the open judgments that ready cards wait pinned to a friend down or held.
func pinJudgments(w *world) []Open {
	var out []Open
	for _, o := range w.s.Open {
		if o.Note.Type == NPinDown {
			out = append(out, o)
		}
	}
	return out
}

// The owner, 2026-10-05: 31 ready cards were pinned to friends down for the week or held,
// some for 20 hours, with no judgment, and ten were unpinned by hand. A ready card pinned to
// a friend down or held past the friend-idle setting is unpinned by rule when its pin is a
// preference, and one judgment per friend names the ones she owns, with the unpin printed
// complete; it closes when she is up again.
func TestACardPinnedToADownFriendRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: []CardAdd{
		{ID: "s1-1", Brief: friendBrief("only friend amy")},           // a generator's preference
		{ID: "s1-2", Brief: friendBrief("only friend zhi")},           // a preference, to a friend held
		{ID: "rate-amy-tools", Brief: friendBrief("only friend amy")}, // her rating: ownership
		{ID: "s1-3", Brief: friendBrief("friend amy (owner)")},        // her own tool: ownership
	}}))
	require.Equal(t, "only."+FriendRow("amy"), w.s.Primary("s1-3").F(FieldWho), "the (owner) mark is a hard pin")
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Down, Class: "flash,pro"},
		{Name: "zhi", Width: 2, Status: Held, Class: "flash,pro"},
		{Name: "bob", Width: 1, Status: Up, Class: "flash,pro"},
	}
	ids := []string{"s1-1", "s1-2", "rate-amy-tools", "s1-3"}

	// down or held, inside the friend-idle setting: they wait, no judgment, no unpin
	dealWith(w, seats...)
	w.tick(w.s.FriendIdleAfter() - time.Minute)
	dealWith(w, seats...)
	for _, id := range ids {
		require.Equal(t, Ready, w.s.StateOf(id), id)
		require.NotEmpty(t, w.s.Primary(id).F(FieldWho), id)
	}
	require.Empty(t, pinJudgments(w))
	require.Empty(t, w.notesOf(NUnpinnedByRule))

	// past it: the preferences are unpinned by rule and logged, the owned ones judged once
	w.tick(2 * time.Minute)
	dealWith(w, seats...)
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.Empty(t, w.s.Primary(id).F(FieldWho), "%s: a preference behind a friend down or held is unpinned by rule", id)
	}
	logged := w.notesOf(NUnpinnedByRule)
	require.Len(t, logged, 2)
	assert.Contains(t, logged[0].What+logged[1].What, "s1-1 dropped WHO: only friend amy")
	assert.Contains(t, logged[0].What+logged[1].What, "s1-2 dropped WHO: only friend zhi")
	assert.Contains(t, logged[0].What+logged[1].What, "held")
	for _, id := range []string{"rate-amy-tools", "s1-3"} {
		assert.Equal(t, "only."+FriendRow("amy"), w.s.Primary(id).F(FieldWho), "%s: ownership stays pinned", id)
	}
	js := pinJudgments(w)
	require.Len(t, js, 1, "one judgment for amy, none for zhi (nothing of hers is owned)")
	j := js[0].Note
	assert.Equal(t, Judgment, j.Kind)
	assert.Equal(t, []string{FriendRow("amy")}, j.Primaries)
	assert.Contains(t, j.What, "rate-amy-tools")
	assert.Contains(t, j.What, "s1-3")
	assert.NotContains(t, j.What, "s1-1")
	unpin := "nova-sprint unpin rate-amy-tools s1-3 --reason "
	assert.Contains(t, j.What, unpin, "the unpin printed complete")
	require.NotEmpty(t, j.Decisions)
	assert.True(t, strings.HasPrefix(j.Decisions[0], unpin), "the first decision is the whole unpin: %q", j.Decisions[0])

	// the next ticks keep the one judgment, and the unpinned cards are dealt
	for range 3 {
		w.tick(time.Minute)
		dealWith(w, seats...)
	}
	assert.Len(t, pinJudgments(w), 1)
	assert.Len(t, w.notesOf(NPinDown), 1, "raised once while it holds")
	assert.Len(t, w.notesOf(NUnpinnedByRule), 2, "each card unpinned once")
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.NotEqual(t, Ready, w.s.StateOf(id), "%s is dealt once unpinned", id)
	}

	// she is up: her cards go to her and the judgment closes
	seats[0].Status = Up
	dealWith(w, seats...)
	assert.Empty(t, pinJudgments(w))
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("rate-amy-tools.w1").Row)
}
