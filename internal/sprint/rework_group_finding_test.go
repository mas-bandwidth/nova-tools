package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A group rework gives each card its own finding (docs/SPEC-SPRINT.md, rework; the card
// group-rework-keeps-each-finding, after 2026-10-05, when a grouped "a reader found it
// broken" was answered with one card's finding as the --fix of all three and every next
// attempt was told to fix another card's defect): each card's next attempt carries the
// finding of the read that put it in the group, never the group's first; a --fix given
// once is every card's fix as written; a --fix that is one card's finding, given to a group
// whose findings differ, is refused whole, nothing written.

const (
	findingOne = "internal/x.go:12: the guard is missing. Add it."
	findingTwo = "internal/units/units.go:40: the unit is uncatalogued. Catalogue it."
)

// twoFoundBroken is a world with s1-1 and s1-2 dealt, finished and found broken, each with
// its own finding.
func twoFoundBroken(t *testing.T) *world {
	t.Helper()
	w := setup(t, 2)
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1", "s1-2"}}}))
	attemptFoundBrokenBy(w, "s1-1", findingOne)
	attemptFoundBrokenBy(w, "s1-2", findingTwo)
	return w
}

// nextAttempt is what the card's next attempt is told: the packet of its attempt-2 work card.
func nextAttempt(t *testing.T, w *world, id string) Packet {
	t.Helper()
	pr := w.s.Work.Card(id)
	require.Equal(t, 2, pr.Int("attempt"), "%s reworked", id)
	wc := w.s.Fleet.Card(WorkCardID(id, 2))
	require.NotNil(t, wc, "%s's next attempt is dealt", id)
	return PacketOf("sprint", 1, wc, pr, nil, nil)
}

func TestAGroupReworkGivesEachCardItsOwnFinding(t *testing.T) {
	t.Parallel()
	group := Sel{IDs: []string{"s1-1", "s1-2"}}
	own := map[string]string{"s1-1": findingOne, "s1-2": findingTwo}
	other := map[string]string{"s1-1": findingTwo, "s1-2": findingOne}

	t.Run("no --fix: each card takes its own finding as its fix", func(t *testing.T) {
		t.Parallel()
		w := twoFoundBroken(t)
		w.must(Rework(w.s, ReworkReq{Sel: group}))
		for id, f := range own {
			p := nextAttempt(t, w, id)
			assert.Equal(t, f, p.Finding, "%s: its own finding", id)
			assert.Equal(t, f, p.Fix, "%s: its own finding is its fix", id)
			assert.NotContains(t, p.Finding+p.Fix+p.Why, other[id], "%s: never the other card's finding", id)
		}
	})

	t.Run("a --fix given once applies to every card as written", func(t *testing.T) {
		t.Parallel()
		w := twoFoundBroken(t)
		w.must(Rework(w.s, ReworkReq{Sel: group, Fix: "run the gate before finishing"}))
		for id, f := range own {
			p := nextAttempt(t, w, id)
			assert.Equal(t, "run the gate before finishing", p.Fix, "%s: the fix as written", id)
			assert.Equal(t, f, p.Finding, "%s: its own finding beside it", id)
			assert.NotContains(t, p.Finding+p.Why, other[id], "%s", id)
		}
	})

	t.Run("one card's finding as the --fix of a group whose findings differ is refused", func(t *testing.T) {
		t.Parallel()
		w := twoFoundBroken(t)
		p := Rework(w.s, ReworkReq{Sel: group, Fix: "The reader's finding, fix exactly this: " + findingOne})
		assert.Empty(t, p.Units, "nothing written: the group is answered whole")
		require.Len(t, p.Refused, 2, "%v", p.Refused)
		for _, r := range p.Refused {
			assert.Contains(t, r.Why, "is the reader finding of s1-1, not of s1-2", r.Key)
			assert.Contains(t, r.Why, "rework the group without --fix", r.Key)
		}
		for id := range own {
			assert.Equal(t, 1, w.s.Work.Card(id).Int("attempt"), "%s stays at its attempt", id)
			assert.Equal(t, Review, w.s.Work.Card(id).Col)
		}

		// without the --fix each takes its own, and the group is answered
		w.must(Rework(w.s, ReworkReq{Sel: group}))
		for id, f := range own {
			assert.Equal(t, f, nextAttempt(t, w, id).Finding, id)
		}
	})

	t.Run("one card's finding as its own --fix is its own", func(t *testing.T) {
		t.Parallel()
		w := twoFoundBroken(t)
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "The reader's finding, fix exactly this: " + findingOne}))
		assert.Equal(t, findingOne, nextAttempt(t, w, "s1-1").Finding)
	})
}
