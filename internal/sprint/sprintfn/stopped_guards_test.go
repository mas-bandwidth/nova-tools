package sprintfn

import (
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/stepbuild"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// R17's step at the largest look a sprint can make: the read is at most
// MaxReadRecords cards (10,000) over MaxStreams streams, with MaxMembers
// members (250), each with a control card and a beat. The guards of the step
// are one for each table's cards, two counters, and a control card and a beat
// for each member: they do not grow with the cards, so the step encodes inside
// the request bound (4 MiB, both halves) and inside the count of guard-only
// members a step may name (stepbuild.LimitGuardOnly), where a guard for each
// card would not. Follows stopinputs in tla/SprintEvents.tla (errata 3, H14,
// H17) and the bounds of section 6.
func TestStoppedStepAtTheLargestLookEncodes(t *testing.T) {
	t.Parallel()
	in := sprint.StopInputs{Next: 1 << 40, Streams: 250,
		Cards: map[sprint.CardRef]uint64{}, Members: map[string]uint64{}, Beats: map[string]int64{}}
	for i := 0; i < sprint.MaxReadRecords; i++ {
		in.Cards[sprint.CardRef{Table: sprint.Work, ID: "p" + strconv.Itoa(i)}] = uint64(i%7 + 1)
	}
	for i := 0; i < sprint.MaxMembers; i++ {
		m := "member-" + strconv.Itoa(i)
		in.Members[m], in.Beats[m] = uint64(i+1), 1790000000000+int64(i)
	}
	if len(in.Cards) <= stepbuild.LimitGuardOnly {
		t.Fatalf("a guard for each of the %d cards would fit %d guard-only members: the test proves nothing", len(in.Cards), stepbuild.LimitGuardOnly)
	}
	// the look: STOPPED for an hour, one move due, the span's ten minutes gone
	clock := sprint.Clock{StoppedSinceMs: 1790000000000 - 3600000}
	c := ntable.BatchMemberEntry{ID: "p1", Set: map[string]string{"x": "y"}}
	dry := []sprint.RulePlan{{Plan: sprint.Plan{Units: []sprint.Unit{{Key: "p1", Changes: []sprint.Change{{Table: sprint.Work, Entry: c}}}}}}}
	p := sprint.StoppedLook(dry, sprint.StopRead{Clock: clock, Wall: 1790000000000, Inputs: in})
	if len(p.Guards) == 0 {
		t.Fatalf("the look plans no step: %+v", p)
	}
	most := 4 + 1 + 2 + 2*sprint.MaxMembers // the clock, the tables' folds (one here), the counters, a member's control card and beat
	if len(p.Guards) > most {
		t.Fatalf("the step carries %d guards, want at most %d", len(p.Guards), most)
	}
	if len(p.Guards) > stepbuild.LimitGuardOnly {
		t.Fatalf("the step carries %d guards, over the %d guard-only members a step may name", len(p.Guards), stepbuild.LimitGuardOnly)
	}
	req := &Request{Epoch: "1", Meta: Meta{Rule: "stopped", Tick: true, Gen: 1}, Body: Body{Guards: p.Guards, Notes: p.Notes}}
	enc, ref := encodeStep(testPrefix, req)
	if ref != nil {
		t.Fatalf("the step is refused: %+v", ref)
	}
	if got := len(enc.raw) + len(enc.sprint); got > tset.MaxWriteRequestBytes/16 {
		t.Fatalf("the step encodes to %d bytes, want well inside the request bound %d", got, tset.MaxWriteRequestBytes)
	}
}
