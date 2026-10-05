package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockedBy puts roots in stream "roots" and, for each root, n cards in a stream of their
// own that need it (transitively: each needs the one before it, the first the root).
func blockedBy(w *world, root string, n int) {
	w.t.Helper()
	w.must(Add(w.s, AddReq{Stream: "roots", IDs: []string{root}}))
	prev := root
	for i := 1; i <= n; i++ {
		id := root + "-b" + itoa(i)
		w.must(Add(w.s, AddReq{Stream: "behind-" + root, IDs: []string{id}, Needs: []string{prev}}))
		prev = id
	}
}

// NeedsRank (docs/SPEC-SPRINT.md, "view-coordinator-needs.w1"): the decisions waiting on
// the coordinator, ranked by the cards blocked behind each, ties by age.
func TestNeedsRanksDecisionsByCardsBlockedBehindThem(t *testing.T) {
	t.Parallel()
	t.Run("judgments rank by cards behind, with their evidence", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		blockedBy(w, "r1", 1)
		blockedBy(w, "r2", 5)
		blockedBy(w, "r3", 12)
		one := judgment("blocked", "roots", t0.Add(-3*time.Hour), 0, "r1")
		one.What = "see outbox/one/REPORT.md and findings/one.md."
		w.note(one,
			judgment("blocked", "roots", t0.Add(-1*time.Hour), 0, "r2"),
			judgment("blocked", "roots", t0.Add(-2*time.Hour), 0, "r3"))
		got := NeedsRank(w.s)
		require.Len(t, got, 3)
		assert.Equal(t, []int{12, 5, 1}, []int{got[0].Behind, got[1].Behind, got[2].Behind})
		assert.Equal(t, []string{"r3", "r2", "r1"}, []string{got[0].Cards[0], got[1].Cards[0], got[2].Cards[0]})
		for _, n := range got {
			assert.Equal(t, NeedJudgment, n.Kind)
			assert.NotEmpty(t, n.ID, "the judgment id")
		}
		assert.Equal(t, []string{"outbox/one/REPORT.md", "findings/one.md"}, got[2].Paths)
		assert.Equal(t, 2*time.Hour, got[0].Age)
	})

	t.Run("a tie breaks by age, the older first", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		blockedBy(w, "x", 3)
		blockedBy(w, "y", 3)
		w.note(judgment("blocked", "roots", t0.Add(-time.Hour), 0, "x"))
		w.note(judgment("blocked", "roots", t0.Add(-5*time.Hour), 0, "y"))
		got := NeedsRank(w.s)
		require.Len(t, got, 2)
		assert.Equal(t, "y", got[0].Cards[0])
		assert.Equal(t, "x", got[1].Cards[0])
	})

	t.Run("held sentinels, stopped streams and held cards count through needs and stream order", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"gate"}, Sentinel: true, Held: true}))
		w.must(Add(w.s, AddReq{Stream: "c", IDs: []string{"c1", "c2", "c3"}}))
		w.must(Add(w.s, AddReq{Stream: "d", IDs: []string{"d1"}, Needs: []string{"c1"}}))
		w.must(Add(w.s, AddReq{Stream: "e", IDs: []string{"e1", "e2"}}))
		w.must(Add(w.s, AddReq{Stream: "f", IDs: []string{"f1"}, Needs: []string{"e1"}}))
		w.must(Add(w.s, AddReq{Stream: "g", IDs: []string{"h1"}, Held: true}))
		w.must(Add(w.s, AddReq{Stream: "g2", IDs: []string{"h2", "h3"}, Needs: []string{"h1"}}))
		ctl := w.s.StreamCtl("e")
		ctl.Fields["state"], ctl.Fields["cause"], ctl.Fields["since"] = StreamStopped, "conflict", stamp(t0.Add(-time.Hour))
		got := NeedsRank(w.s)
		kinds := map[string]Need{}
		for _, n := range got {
			kinds[n.Kind+":"+n.ID] = n
		}
		require.Contains(t, kinds, NeedSentinel+":gate")
		assert.Equal(t, 4, kinds[NeedSentinel+":gate"].Behind, "c1 c2 c3, and d1 behind c1")
		require.Contains(t, kinds, NeedStream+":e")
		assert.Equal(t, 1, kinds[NeedStream+":e"].Behind, "f1 behind e1")
		assert.Equal(t, []string{"e1", "e2"}, kinds[NeedStream+":e"].Cards)
		require.Contains(t, kinds, NeedHeld+":h1")
		assert.Equal(t, 2, kinds[NeedHeld+":h1"].Behind)
		for i := 1; i < len(got); i++ {
			assert.GreaterOrEqual(t, got[i-1].Behind, got[i].Behind, "ranked heaviest first: %+v", got)
		}
	})
}
