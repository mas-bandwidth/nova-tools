package sprint_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// accept --heavy records the coordinator's own heavy read (docs/SPEC-SPRINT.md section 6,
// accept-heavy-verdict-b.w1): a reader bounced a pro card as broken on a true finding's
// opposite, accept was refused at one of two ok reads, and the card waited for another read
// or a re-cut. The coordinator's heavy read, with its evidence file and that file's sha256,
// counts as one ok read toward the card's read rule; it is kept on the primary under
// coordinator:<actor>, never as a reader's ok and never under a reader's name, and the
// broken read stays, marked overruled. On the twin store (store.Mem).

// newHeavyRig is a running sprint on the twin: readers reader-a..c, member m1, and stream s1
// of one pro card.
func newHeavyRig(t *testing.T) *conflictRig {
	t.Helper()
	r := &conflictRig{t: t, m: store.NewMem(), ctx: context.Background(), now: holdT0}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.RowsAdd(r.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}))
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	r.m.SetRoutes([]sprint.Route{
		{Name: "flash-a", Tier: "flash", Provider: "prov-flash-a", Model: "model-flash-a", Tokens: 1000, Deadline: conflictRouteSeconds, Enabled: true},
		{Name: "pro-a", Tier: "pro", Provider: "prov-pro-a", Model: "model-pro-a", Tokens: 1000, Deadline: conflictRouteSeconds, Enabled: true},
	})
	r.beat()
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m2", Width: 4}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 1, Brief: "c: the work (s1) tier: pro\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

// toSplitReads drives the primary through work to review, then asks its two reads one at a
// time: the first comes back ok, the second broken.
func (r *conflictRig) toSplitReads(id string) (ok, broken *sprint.Card) {
	r.t.Helper()
	r.must(store.DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	wc = r.snap().Fleet.Card(wc.ID)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	require.Equal(r.t, 2, sprint.ReadsNeeded(r.snap().Work.Card(id)), "a pro card needs two reads")
	for _, verdict := range []string{"ok", "broken"} {
		r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
		for _, rc := range r.snap().Readers.Of(id) {
			if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
				r.must(store.ReadStep(sprint.ReadReq{As: rc.Row, Verdict: verdict, Finding: "f:1 the model trailer is wrong", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
				if verdict == "ok" {
					ok = r.snap().Readers.Card(rc.ID)
				} else {
					broken = r.snap().Readers.Card(rc.ID)
				}
			}
		}
	}
	require.NotNil(r.t, ok)
	require.NotNil(r.t, broken)
	require.Equal(r.t, sprint.Review, r.snap().Work.Card(id).Col)
	return ok, broken
}

// heavy is the accept of id with the coordinator's heavy read.
func heavy(id, evidence, sha string) sprint.AcceptReq {
	return sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}, Who: "coordinator",
		Heavy: true, Evidence: evidence, EvidenceSHA: sha, Reason: "the trailer names the model that did the work"}
}

func TestAcceptHeavyRecordsTheCoordinatorsReadNotAReaders(t *testing.T) {
	t.Parallel()
	const sha = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	r := newHeavyRig(t)
	id := "s1-1"
	ok, broken := r.toSplitReads(id)
	readersBefore := r.snap().Readers.Of(id)

	refused := func(req sprint.AcceptReq) string {
		t.Helper()
		res, err := r.st.Run(r.ctx, store.AcceptStep(req))
		require.NoError(t, err)
		require.Len(t, res.Refused, 1, "refused")
		return res.Refused[0].Why
	}
	t.Run("one ok of two is refused without the heavy read", func(t *testing.T) {
		assert.Contains(t, refused(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}}), "needs ok from two different readers")
	})
	t.Run("the heavy read is refused without its evidence", func(t *testing.T) {
		assert.Contains(t, refused(heavy(id, "", "")), "--evidence")
		assert.Contains(t, refused(heavy(id, "/evidence/read.md", "")), "sha256")
		assert.Equal(t, sprint.Review, r.snap().Work.Card(id).Col, "a refusal writes nothing")
	})

	r.must(store.AcceptStep(heavy(id, "/evidence/read.md", sha)))
	s := r.snap()
	pr := s.Work.Card(id)

	t.Run("the card is accepted on one reader ok and the coordinator heavy read", func(t *testing.T) {
		assert.Equal(t, sprint.Merging, pr.Col)
		assert.Equal(t, ok.Row, pr.F("readers"), "readers names the readers' oks alone")
	})
	t.Run("the card shows the coordinator verdict with its evidence", func(t *testing.T) {
		assert.Equal(t, "coordinator:coordinator", pr.F(sprint.FieldHeavyReader))
		assert.Equal(t, "heavy", pr.F(sprint.FieldHeavyKind))
		assert.Equal(t, "ok", pr.F(sprint.FieldHeavyVerdict))
		assert.Equal(t, "/evidence/read.md", pr.F(sprint.FieldHeavyEvidence))
		assert.Equal(t, sha, pr.F(sprint.FieldHeavySHA))
		assert.Equal(t, "the trailer names the model that did the work", pr.F(sprint.FieldHeavyReason))
		assert.Equal(t, pr.F("attempt"), pr.F(sprint.FieldHeavyAttempt))
		assert.Equal(t, pr.F("head"), pr.F(sprint.FieldHeavyHead))
	})
	t.Run("the broken read stays, marked overruled by the coordinator heavy read", func(t *testing.T) {
		assert.Equal(t, broken.ID, pr.F(sprint.FieldHeavyOverrules))
		assert.Equal(t, sprint.Broken, s.Readers.Card(broken.ID).Col, "the reader's verdict is its own")
		assert.Equal(t, broken.Rev, s.Readers.Card(broken.ID).Rev)
	})
	t.Run("no reader row is forged", func(t *testing.T) {
		after := s.Readers.Of(id)
		require.Len(t, after, len(readersBefore))
		for i, c := range after {
			assert.Equal(t, readersBefore[i].ID, c.ID)
			assert.Equal(t, readersBefore[i].Col, c.Col)
			assert.NotContains(t, c.F("reader"), "coordinator")
		}
		assert.Equal(t, []string{"reader-a", "reader-b", "reader-c"}, s.Readers.Rows())
	})
	t.Run("a card not in review is refused", func(t *testing.T) {
		assert.Contains(t, refused(heavy(id, "/evidence/read.md", sha)), "merging")
	})
}
