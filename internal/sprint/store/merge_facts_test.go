package store

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A cross fact names a card that is placed, in another stream, and not
// landed; otherwise the merge step refuses it, saying which condition failed,
// and the stream keeps merging.
func TestCrossFactNeedsAnOpenCardOfAnotherStream(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ other, why string }{
		{"s1-2", "same stream"},
		{"s1-1", "the card itself"},
		{"nosuch", "not on the table"},
		{"", "names no other card"},
		{"b1", "landed already"},
	} {
		t.Run("other="+tc.other, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(2)
			h.through("s1-1", "s1-2")
			if tc.other == "b1" {
				h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1"}}))
				h.through("b1")
				h.must(MergeStep(sprint.MergeReq{Stream: "s2"}))
				if h.state("b1") != sprint.Landed {
					t.Fatalf("b1 is %s", h.state("b1"))
				}
			}
			res := h.run(MergeStep(sprint.MergeReq{Stream: "s1", Cross: "s1-1=" + tc.other}))
			if len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Why, tc.why) || len(res.Moved) != 0 {
				t.Fatalf("cross s1-1=%s: moved %v refused %v, want refused saying %q", tc.other, res.Moved, res.Refused, tc.why)
			}
			if st := h.snap().StreamCtl("s1").F("state"); st == sprint.StreamStopped {
				t.Fatalf("a refused cross fact stopped the stream")
			}
			h.clean("refused cross")
		})
	}
}

// A cross fact on an open card of another stream stops the stream, and the
// notification offers return and drop, which move the stuck card.
func TestCrossStopOffersReturnAndDrop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.through("s1-1")
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"b1"}}))
	h.through("b1")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Cross: "s1-1=b1"}))
	s := h.snap()
	if s.StreamCtl("s1").F("state") != sprint.StreamStopped || s.Merge.Placed("s1-1").Col != sprint.Stuck {
		t.Fatalf("cross on an open card of another stream did not stop the stream")
	}
	var found bool
	for _, o := range s.Open {
		if o.Note.Type == sprint.NCross {
			found = true
			for _, d := range []string{"return", "drop"} {
				if !hasString(o.Note.Decisions, d) {
					t.Fatalf("the cross notification's decisions %v lack %q", o.Note.Decisions, d)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no open cross notification: %v", s.Open)
	}
	h.must(ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}, Reason: "waits for b1"}))
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s1"}))
	h.clean("returned and resumed")
}

func hasString(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
