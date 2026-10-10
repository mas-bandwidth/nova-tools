package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyWaitingReasonsAndChains(t *testing.T) {
	t.Parallel()

	t.Run("held names who holds it and why", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "h", Row: "s1", Col: Waiting, Fields: map[string]string{
			FieldHeld: "2026-10-09T00:00:00Z", FieldHeldBy: "coordinator", FieldHeldReason: "waiting on the vendor",
		}})
		v := ClassifyWaiting(w.s, "s1")
		require.Len(t, v.Cards, 1)
		assert.Equal(t, WaitHeld, v.Cards[0].Kind)
		assert.Equal(t, "held by coordinator: waiting on the vendor", v.Cards[0].Reason)
		assert.Equal(t, "h", v.Cards[0].Head)
		assert.Equal(t, 0, v.Cards[0].Length)
	})

	t.Run("held names the coordinator when it records no who or why", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "h", Row: "s1", Col: Waiting, Fields: map[string]string{FieldHeld: "2026-10-09T00:00:00Z"}})
		v := ClassifyWaiting(w.s, "s1")
		require.Len(t, v.Cards, 1)
		assert.Equal(t, "held by the coordinator (add --held) until release", v.Cards[0].Reason)
	})

	t.Run("a waived need in another column is not a blocker", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "dep", Row: "s1", Col: Working})
		w.s.Work.Put(&Card{ID: "card1", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "dep", "waived": "dep"}})
		v := ClassifyWaiting(w.s, "s1")
		var found WaitingCard
		for _, c := range v.Cards {
			if c.ID == "card1" {
				found = c
			}
		}
		assert.NotEqual(t, WaitNeedColumn, found.Kind, "a waived need still blocked the card: %+v", found)
		assert.Equal(t, WaitUnmoved, found.Kind, found.Reason)
	})

	t.Run("a waived need naming no card is not missing", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "card1", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "ghost", "waived": "ghost"}})
		v := ClassifyWaiting(w.s, "s1")
		var found WaitingCard
		for _, c := range v.Cards {
			if c.ID == "card1" {
				found = c
			}
		}
		assert.NotEqual(t, WaitNeedMissing, found.Kind, "a waived need was called missing: %+v", found)
	})

	t.Run("a waived waiting need does not head the chain", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "root", Row: "s1", Col: Waiting, Fields: map[string]string{FieldHeld: "t"}})
		w.s.Work.Put(&Card{ID: "w", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "root"}})
		w.s.Work.Put(&Card{ID: "c", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "w", "waived": "w"}})
		v := ClassifyWaiting(w.s, "s1")
		var found WaitingCard
		for _, c := range v.Cards {
			if c.ID == "c" {
				found = c
			}
		}
		assert.Equal(t, "c", found.Head, "the chain followed a waived need: %+v", found)
		assert.Equal(t, 0, found.Length)
	})

	t.Run("behind sentinel not released", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "stop", Row: "s1", Col: Waiting, Score: 10, Fields: map[string]string{"kind": "sentinel"}})
		w.s.Work.Put(&Card{ID: "m1", Row: "s1", Col: Waiting, Score: 20})
		v := ClassifyWaiting(w.s, "s1")
		var found WaitingCard
		for _, c := range v.Cards {
			if c.ID == "m1" {
				found = c
			}
		}
		assert.Equal(t, "behind sentinel stop not released", found.Reason)
	})

	t.Run("needs in column", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "dep", Row: "s1", Col: Ready})
		w.s.Work.Put(&Card{ID: "card1", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "dep"}})
		v := ClassifyWaiting(w.s, "s1")
		require.Len(t, v.Cards, 1)
		assert.Equal(t, "needs dep in ready", v.Cards[0].Reason)
	})

	t.Run("needs missing", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "card1", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "ghost"}})
		v := ClassifyWaiting(w.s, "s1")
		require.Len(t, v.Cards, 1)
		assert.Equal(t, "needs ghost missing", v.Cards[0].Reason)
	})

	t.Run("needs landed and tidied", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "l", Row: "s1", Col: Landed})
		w.s.Work.Put(&Card{ID: "c2", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "l"}})
		v := ClassifyWaiting(w.s, "s1")
		var found WaitingCard
		for _, c := range v.Cards {
			if c.ID == "c2" {
				found = c
			}
		}
		assert.Equal(t, "needs l landed and tidied", found.Reason)
	})

	t.Run("chain of three whose head is a held card", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "heldcard", Row: "s1", Col: Waiting, Fields: map[string]string{"held": "today"}})
		w.s.Work.Put(&Card{ID: "c2", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "heldcard"}})
		w.s.Work.Put(&Card{ID: "c3", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "c2"}})
		v := ClassifyWaiting(w.s, "s1")
		byID := map[string]WaitingCard{}
		for _, c := range v.Cards {
			byID[c.ID] = c
		}
		assert.Equal(t, WaitHeld, byID["c2"].Kind)
		assert.Equal(t, "heldcard", byID["c2"].Head)
		assert.Equal(t, 1, byID["c2"].Length)
		assert.Equal(t, WaitHeld, byID["c3"].Kind, byID["c3"].Reason)
		assert.Contains(t, byID["c3"].Reason, "held", "the chain's root block is not named: %+v", byID["c3"])
		assert.Equal(t, "heldcard", byID["c3"].Head)
		assert.Equal(t, 2, byID["c3"].Length)
	})

	t.Run("chain of two whose root is a card in another column", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "dep", Row: "s1", Col: Ready})
		w.s.Work.Put(&Card{ID: "c2", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "dep"}})
		w.s.Work.Put(&Card{ID: "c3", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "c2"}})
		v := ClassifyWaiting(w.s, "s1")
		byID := map[string]WaitingCard{}
		for _, c := range v.Cards {
			byID[c.ID] = c
		}
		assert.Equal(t, WaitNeedColumn, byID["c2"].Kind)
		assert.Equal(t, "needs dep in ready", byID["c2"].Reason)
		assert.Equal(t, "dep", byID["c2"].Head)
		assert.Equal(t, 1, byID["c2"].Length)
		assert.Equal(t, WaitNeedColumn, byID["c3"].Kind, byID["c3"].Reason)
		assert.Equal(t, "needs dep in ready", byID["c3"].Reason)
		assert.Equal(t, "dep", byID["c3"].Head)
		assert.Equal(t, 2, byID["c3"].Length)
	})

	t.Run("chain of two whose root is a missing id", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "m2", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "ghost"}})
		w.s.Work.Put(&Card{ID: "m1", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "m2"}})
		v := ClassifyWaiting(w.s, "s1")
		byID := map[string]WaitingCard{}
		for _, c := range v.Cards {
			byID[c.ID] = c
		}
		assert.Equal(t, WaitNeedMissing, byID["m2"].Kind)
		assert.Equal(t, "needs ghost missing", byID["m2"].Reason)
		assert.Equal(t, "ghost", byID["m2"].Head)
		assert.Equal(t, 1, byID["m2"].Length)
		assert.Equal(t, WaitNeedMissing, byID["m1"].Kind, byID["m1"].Reason)
		assert.Equal(t, "needs ghost missing", byID["m1"].Reason)
		assert.Equal(t, "ghost", byID["m1"].Head)
		assert.Equal(t, 2, byID["m1"].Length)
	})

	t.Run("chain of two whose root is an unreleased sentinel", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "stop", Row: "s1", Col: Waiting, Fields: map[string]string{"kind": "sentinel"}})
		w.s.Work.Put(&Card{ID: "c1", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "stop"}})
		w.s.Work.Put(&Card{ID: "c2", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "c1"}})
		v := ClassifyWaiting(w.s, "s1")
		byID := map[string]WaitingCard{}
		for _, c := range v.Cards {
			byID[c.ID] = c
		}
		assert.Equal(t, WaitBehind, byID["c1"].Kind)
		assert.Equal(t, "behind sentinel stop not released", byID["c1"].Reason)
		assert.Equal(t, "stop", byID["c1"].Head)
		assert.Equal(t, 1, byID["c1"].Length)
		assert.Equal(t, WaitBehind, byID["c2"].Kind)
		assert.Equal(t, "behind sentinel stop not released", byID["c2"].Reason)
		assert.Equal(t, "stop", byID["c2"].Head)
		assert.Equal(t, 2, byID["c2"].Length)
	})

	t.Run("cycle reported as cycle", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "cx1", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "cx2"}})
		w.s.Work.Put(&Card{ID: "cx2", Row: "s1", Col: Waiting, Fields: map[string]string{"needs": "cx1"}})

		v := ClassifyWaiting(w.s, "s1")
		var found WaitingCard
		for _, c := range v.Cards {
			if c.ID == "cx1" {
				found = c
			}
		}
		assert.Contains(t, found.Reason, "cycle")
	})
}
