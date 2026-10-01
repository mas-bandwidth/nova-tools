package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// nothingWritten says no table moved, no row was added and the fence is empty.
func (h *harness) nothingWritten(before map[string]uint64) {
	h.t.Helper()
	for tb, rev := range before {
		if got := h.m.Revision(tb); got != rev {
			h.t.Fatalf("table %s moved %d -> %d", tb, rev, got)
		}
	}
	if h.m.Pending() != nil {
		h.t.Fatalf("the fence holds %s", h.m.Pending().ID)
	}
}

func (h *harness) revisions() map[string]uint64 {
	out := map[string]uint64{}
	for _, t := range All {
		out[h.st.Names.Table(t)] = h.m.Revision(h.st.Names.Table(t))
	}
	return out
}

// S2 (c). A card text field over MaxCardTextBytes refuses the step whole
// before anything is written, naming the field and its size.
func TestACardTextOverTheBoundRefusesTheStepWhole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	before := h.revisions()
	res, err := h.st.Run(h.ctx, AddStep(sprint.AddReq{Stream: "s2", Count: 2, Brief: strings.Repeat("b", MaxCardTextBytes+1)}))
	if err != nil || len(res.Moved) != 0 || len(res.Refused) != 2 || res.Attempts != 1 {
		t.Fatalf("an over-long brief: %+v %v", res, err)
	}
	why := res.Refused[0].Why
	if !strings.Contains(why, "field brief") || !strings.Contains(why, fmt.Sprint(MaxCardTextBytes+1)) || strings.Contains(why, "kept changing") {
		t.Fatalf("the refusal: %s", why)
	}
	h.nothingWritten(before)
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 2, Brief: strings.Repeat("b", MaxCardTextBytes)}))
}

// S2 (a). A step the table layer's own validation refuses (a field value
// over its bound, on the work table after a merge-table change) is refused
// whole before anything is written, in the table layer's words: before, the
// merge change applied, the work manifest was refused, and the operation
// wedged the sprint.
func TestAStepTheTableLayerRefusesIsRefusedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	before := h.revisions()
	step := Step{Verb: "probe", Load: []string{sprint.Work, sprint.Merge}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		ctl, p := s.Merge.Card(sprint.CtlID("s1")), s.Work.Card("s1-1")
		guard := func(c *sprint.Card) *ntable.MemberExpect {
			return &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}
		}
		return sprint.Plan{Units: []sprint.Unit{{Key: "s1-1", Moved: "s1-1 probed", Changes: []sprint.Change{
			{Table: sprint.Merge, Entry: ntable.BatchMemberEntry{ID: ctl.ID, Expect: guard(ctl), Set: map[string]string{"probe": "1"}}},
			{Table: sprint.Work, Entry: ntable.BatchMemberEntry{ID: p.ID, Expect: guard(p), Set: map[string]string{"probe": strings.Repeat("v", ntable.LimitFieldValueBytes+1)}}},
		}}}}
	}}
	res, err := h.st.Run(h.ctx, step)
	if err != nil || len(res.Moved) != 0 || len(res.Refused) != 1 || res.Attempts != 1 {
		t.Fatalf("an unwritable step: %+v %v", res, err)
	}
	why := res.Refused[0].Why
	if !strings.Contains(why, "LIMIT") || !strings.Contains(why, "field value bytes") || strings.Contains(why, "kept changing") {
		t.Fatalf("the refusal: %s", why)
	}
	h.nothingWritten(before)
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
}

// S2 (b). A step's manifests are split by bytes as well as by entries: 128
// primaries with briefs at the bound are one step in several manifests.
func TestAStepIsSplitByBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	res := h.must(AddStep(sprint.AddReq{Stream: "s2", Count: 128, Brief: strings.Repeat("b", MaxCardTextBytes)}))
	if len(res.Moved) != 128 {
		t.Fatalf("moved %d", len(res.Moved))
	}
	n := 0
	for _, c := range h.snap().Work.Cards() {
		if strings.HasPrefix(c.ID, "s2-") {
			n++
		}
	}
	if n != 128 {
		t.Fatalf("%d of 128 on the table", n)
	}
	h.clean("split by bytes")
}

// refusing is a store whose apply refuses a table's manifests on a bound the
// sprint's own validation does not know (a store of another build). It is over
// the Mem itself, which keeps the machine's records a tick reads.
type refusing struct {
	*Mem
	table string
	calls int
}

func (r *refusing) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	if m.Table == r.table {
		r.calls++
		return ntable.Receipt{}, &ntable.Refusal{Code: "LIMIT", Location: "store", Sentence: "limit exceeded: changed entries: bound 1, observed 2", Guarded: true}
	}
	return r.Mem.Apply(ctx, m)
}

// S2. A first manifest the store refuses on a bound is refused whole at once
// with the store's text: nothing applied, the fence released, no retry.
func TestAFirstManifestRefusedOnABoundIsNotRetried(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	st := *h.st
	r := &refusing{Mem: h.m, table: "t-fleet"}
	st.B = r
	before := h.revisions()
	res, err := st.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 2}}))
	if err != nil || len(res.Moved) != 0 || len(res.Refused) != 2 || r.calls != 1 {
		t.Fatalf("a first manifest refused on a bound: %+v %v; %d sends", res, err, r.calls)
	}
	if why := res.Refused[0].Why; !strings.Contains(why, "changed entries: bound 1") || strings.Contains(why, "kept changing") {
		t.Fatalf("the refusal: %s", why)
	}
	h.nothingWritten(before)
}

// pendingOp puts an operation in the fence as a writer cut after acquiring
// it would leave it.
func (h *harness) pendingOp(op OpRecord) {
	h.t.Helper()
	f, err := h.m.ReadFence(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	if ok, err := h.m.Acquire(h.ctx, f.Gen, op); !ok || err != nil {
		h.t.Fatalf("acquire: %v %v", ok, err)
	}
}

// S2 (d). A later manifest the store refuses on a bound is skipped by repair
// as F5 skips an entry: the entries that can apply do, the one over the bound
// is listed in one judgment with the store's text, and the fence is released.
func TestRepairSkipsALaterManifestRefusedOnABound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	s := h.snap()
	p1, p2 := s.Work.Card("s1-1"), s.Work.Card("s1-2")
	guard := func(c *sprint.Card) *ntable.MemberExpect {
		return &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}
	}
	ctl := s.Merge.Card(sprint.CtlID("s1"))
	op := OpRecord{ID: "cut-1", Verb: "test", At: h.st.Now(), Manifests: []ntable.BatchManifest{
		{Schema: 1, Table: "t-merge", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Merge.Revision), OperationID: "cut-1-1", Actor: "tester",
			Members: []ntable.BatchMemberEntry{{ID: ctl.ID, Expect: guard(ctl), Set: map[string]string{"probe": "1"}}}},
		{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision), OperationID: "cut-1-2", Actor: "tester",
			Members: []ntable.BatchMemberEntry{
				{ID: p1.ID, Expect: guard(p1), Set: map[string]string{"probe": strings.Repeat("v", ntable.LimitFieldValueBytes+1)}},
				{ID: p2.ID, Expect: guard(p2), Set: map[string]string{"probe": "2"}}}},
	}}
	if _, err := h.m.Apply(h.ctx, op.Manifests[0]); err != nil {
		t.Fatal(err)
	}
	h.pendingOp(op)
	rr, err := h.st.Repair(h.ctx)
	if err != nil || len(rr) != 1 || rr[0].Done != RepairSkipped || len(rr[0].Skipped) != 1 {
		t.Fatalf("repair: %+v %v", rr, err)
	}
	if !strings.Contains(rr[0].Skipped[0], "s1-1") || !strings.Contains(rr[0].Skipped[0], "LIMIT") {
		t.Fatalf("the skip does not carry the store's text: %s", rr[0].Skipped[0])
	}
	if h.m.Pending() != nil {
		t.Fatalf("the fence is still held")
	}
	s = h.snap()
	if s.Work.Card("s1-2").F("probe") != "2" || s.Work.Card("s1-1").F("probe") != "" {
		t.Fatalf("applied: s1-1 %q s1-2 %q", s.Work.Card("s1-1").F("probe"), s.Work.Card("s1-2").F("probe"))
	}
	if n := len(h.skipNotes()); n != 1 {
		t.Fatalf("%d skip judgments", n)
	}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
}

// S2 (d). A pending operation whose first manifest can never apply (a bound)
// is abandoned by the next verb at once, even within the grace, instead of
// spinning the verb's attempts into "the sprint is busy".
func TestAFirstManifestOverABoundIsAbandonedAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	s := h.snap()
	p1 := s.Work.Card("s1-1")
	h.pendingOp(OpRecord{ID: "cut-2", Verb: "test", At: h.st.Now(), Manifests: []ntable.BatchManifest{
		{Schema: 1, Table: "t-work", Epoch: "0", ExpectedTableRevision: fmt.Sprint(s.Work.Revision), OperationID: "cut-2-1", Actor: "tester",
			Members: []ntable.BatchMemberEntry{{ID: p1.ID, Set: map[string]string{"probe": strings.Repeat("v", ntable.LimitFieldValueBytes+1)}}}},
	}})
	res, err := h.st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m3"}))
	if err != nil || len(res.Repaired) != 1 || !strings.HasSuffix(res.Repaired[0], RepairAbandoned) {
		t.Fatalf("the verb after an unwritable pending operation: %+v %v", res, err)
	}
	var pe *PendingError
	if errors.As(err, &pe) {
		t.Fatal(pe)
	}
}
