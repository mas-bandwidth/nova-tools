package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

const (
	pushedA = "0123456789abcdef0123456789abcdef01234567"
	pushedB = "89abcdef0123456789abcdef0123456789abcdef"
)

// finishAttempt takes the primary's live work card and finishes it: ok at head, or failed at
// no commit (the member's finish when a child ends with nothing pushed).
func (h *harness) finishAttempt(id string, failed bool, head string) {
	h.t.Helper()
	wc := h.snap().Fleet.Card(h.snap().Work.Card(id).F("work"))
	require.NotNil(h.t, wc, id)
	g := map[string]int{wc.ID: wc.Int("gen")}
	h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g}))
	h.must(FinishStep(sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: g, Failed: failed, Head: head, Report: "r"}))
}

// reworkBroken has one reader of a finished attempt find it broken, and reworks. The machine's
// ask asks the attempt's readers here, every attempt: the finish asks no reader (one path asks).
func (h *harness) reworkBroken(id string) {
	h.t.Helper()
	h.must(AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
	rc := h.snap().Readers.Of(id)
	h.must(ReadStep(sprint.ReadReq{As: rc[0].Row, Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc[0].ID}}}))
	h.must(ReadStep(sprint.ReadReq{As: rc[1].Row, Verdict: "broken", Finding: "f:1", Sel: sprint.Sel{IDs: []string{rc[1].ID}}}))
	h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{id}}, Fix: "fix"}))
}

// A rework stages at the last pushed head of any earlier attempt, whatever happened between
// (docs/SPEC-CARD-CONTRACT.md layer 1, tla/CardContract.tla RestagedAtLastPushedHead), on the mem twin:
// attempt 1 pushes, attempt 2 fails with no commit, attempt 3 starts from attempt 1's head.
func TestAReworkStagesAtTheLastPushedHeadOfAnyEarlierAttempt(t *testing.T) {
	t.Parallel()
	packetOf := func(h *harness, id string) sprint.Packet {
		t.Helper()
		wc := h.snap().Fleet.Card(id)
		require.NotNil(t, wc, id)
		ps, err := h.st.Packets(h.ctx, []*sprint.Card{wc})
		require.NoError(t, err)
		return ps[0]
	}
	t.Run("attempt 1 pushes, attempt 2 fails with no commit", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", false, pushedA)
		h.reworkBroken("s1-1")
		h.finishAttempt("s1-1", true, "")
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		p := packetOf(h, "s1-1.w3")
		assert.Equal(t, pushedA, p.BaseHead)
		assert.Equal(t, 1, p.BaseAttempt)
	})
	t.Run("attempts 1 and 2 both push", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", false, pushedA)
		h.reworkBroken("s1-1")
		h.finishAttempt("s1-1", false, pushedB)
		h.reworkBroken("s1-1")
		p := packetOf(h, "s1-1.w3")
		assert.Equal(t, pushedB, p.BaseHead)
		assert.Equal(t, 2, p.BaseAttempt)
	})
	t.Run("no attempt pushed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.setup(1)
		h.must(DealStep(sprint.DealReq{}))
		h.finishAttempt("s1-1", true, "")
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		h.finishAttempt("s1-1", true, "")
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
		p := packetOf(h, "s1-1.w3")
		assert.Empty(t, p.BaseHead, "the base is staged")
		assert.Zero(t, p.BaseAttempt)
	})
}
