package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyWaitingReasonsAndChains(t *testing.T) {
	t.Parallel()

	t.Run("held", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 0)
		w.s.Work.Put(&Card{ID: "h", Row: "s1", Col: Waiting, Fields: map[string]string{"held": "2026-10-09T00:00:00Z"}})
		v := ClassifyWaiting(w.s, "s1")
		require.Len(t, v.Cards, 1)
		assert.Equal(t, "held", v.Cards[0].Reason)
		assert.Equal(t, "h", v.Cards[0].Head)
		assert.Equal(t, 0, v.Cards[0].Length)
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
		var c3 WaitingCard
		for _, c := range v.Cards {
			if c.ID == "c3" {
				c3 = c
			}
		}
		assert.Equal(t, "heldcard", c3.Head)
		assert.Equal(t, 2, c3.Length)
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
