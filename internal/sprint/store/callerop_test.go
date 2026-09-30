package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// S6. A caller's operation id recorded for another verb is refused as a
// conflict naming the recorded verb; the other verb's result is never
// returned as a replay, and nothing is done.
func TestACallerOpOfAnotherVerbIsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	start := DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 2}})
	start.CallerOp = "op-A"
	h.must(start)
	before := h.revisions()
	c := h.snap().Fleet.Card("s1-1.w1")
	take := TakeStep(sprint.TakeReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}})
	take.CallerOp = "op-A"
	res, err := h.st.Run(h.ctx, take)
	var ce *OpConflictError
	if !errors.As(err, &ce) || ce.Recorded != "deal" || res.Replay || len(res.Moved) != 0 || !strings.Contains(err.Error(), "deal") {
		t.Fatalf("take under start's operation id: %+v %v", res, err)
	}
	h.nothingWritten(before)
	if again := h.must(start); !again.Replay {
		t.Fatalf("start's own retry: %+v", again)
	}
}

// S6. The same verb with other arguments under a recorded caller's operation
// id is a conflict too; the same arguments replay.
func TestACallerOpWithOtherArgumentsIsAConflict(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	add := func(n int) Step {
		r := sprint.AddReq{Stream: "s2", Count: n}
		s := AddStep(r)
		s.CallerOp, s.Args = "op-B", ArgsOf(r)
		return s
	}
	first := h.must(add(1))
	if again := h.must(add(1)); !again.Replay || again.Op != first.Op {
		t.Fatalf("the same add again: %+v", again)
	}
	before := h.revisions()
	res, err := h.st.Run(h.ctx, add(2))
	var ce *OpConflictError
	if !errors.As(err, &ce) || !ce.OtherArgs || ce.Recorded != "add" || res.Replay {
		t.Fatalf("add with other arguments under the same id: %+v %v", res, err)
	}
	h.nothingWritten(before)
}
