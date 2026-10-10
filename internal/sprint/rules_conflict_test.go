package sprint_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

// A head the lander refuses for a cause of its own (it does not merge, changes files outside
// its PATHS or fails the tree gate) is reworked at the tip by the merge step that records it,
// and its stream lands on (sprint's landRefused; the owner, 2026-10-04: "the machine keeps
// itself fed"; "a hand step is a missing instruction"). That day every such head stopped its
// whole stream until the coordinator answered, and 19 streams sat stopped behind a hand loop.
// On the twin store (store.Mem), with the lander's facts given in the lander's own words.

// conflictRig is a sprint on the twin that answers by rule: flash and pro routes, members
// m1 and m2, readers reader-a..c, and stream s1 of three flash cards.
type conflictRig struct {
	t   *testing.T
	m   *store.Mem
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

// conflictRouteSeconds is a route's deadline, in seconds.
var conflictRouteSeconds = 600

func newConflictRig(t *testing.T) *conflictRig {
	t.Helper()
	r := &conflictRig{t: t, m: store.NewMem(), ctx: context.Background(), now: holdT0}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator", AnswerRules: true,
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
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: 3, Brief: "c: the work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

func (r *conflictRig) beat() {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
}

func (r *conflictRig) must(step store.Step) store.Result {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused", step.Verb)
	return res
}

// tick moves the clock a second, beats everyone and runs one tick of the machine.
func (r *conflictRig) tick() {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// snap is the sprint as the next pump leaves its work table (sprint.WithQueue): the state
// every step but the pump plans on.
func (r *conflictRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	pinned, err := r.st.Pinned(r.ctx)
	require.NoError(r.t, err)
	q, err := pinned.B.QueueRead(r.ctx)
	require.NoError(r.t, err)
	return sprint.WithQueue(s, q)
}

// toMerging drives each primary's dealt (or ready) attempt through work, its reads and the
// accept, to merging queued.
func (r *conflictRig) toMerging(id string) { r.t.Helper(); r.toMergingAt(id, "") }

// toMergingAt is toMerging with the attempt finished at head ("": no head of its own).
func (r *conflictRig) toMergingAt(id, head string) {
	r.t.Helper()
	if r.snap().Work.Card(id).Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	wc = r.snap().Fleet.Card(wc.ID)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Head: head}))
	for range 4 {
		s = r.snap()
		if pr := s.Work.Card(id); pr.Col != sprint.Review || sprint.ReadsWanted(s, pr) == 0 {
			break
		}
		r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
		for _, rc := range r.snap().Readers.Of(id) {
			if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
				r.must(store.ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: "ok", Finding: "f:1", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
			}
		}
	}
	r.must(store.AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}}))
	require.Equal(r.t, sprint.Merging, r.snap().Work.Card(id).Col, id)
}

// open is the open judgments of the type.
func (r *conflictRig) open(typ string) []sprint.Open {
	r.t.Helper()
	open, err := r.m.OpenNotes(r.ctx)
	require.NoError(r.t, err)
	var out []sprint.Open
	for _, o := range open {
		if o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

// answeredBy is the decided notes that say the rule answered.
func (r *conflictRig) answeredBy(rule string) []sprint.Note {
	r.t.Helper()
	all, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Kind == sprint.Decided && strings.HasPrefix(n.What, "answered by rule "+rule+":") {
			out = append(out, n)
		}
	}
	return out
}

func (r *conflictRig) clean(when string) {
	r.t.Helper()
	rep, _, err := r.st.Check(r.ctx, 3)
	require.NoError(r.t, err, when)
	require.Empty(r.t, rep.Violations, "%s: %v", when, rep.Violations)
}

func TestAConflictingHeadIsRedoneOnTheTipAndItsStreamKeepsLanding(t *testing.T) {
	t.Parallel()
	// the lander's three refusals of a head, as it reports them (cmd/nova-sprint, land.go and
	// landgo.go: mergeHead, checkCard, gateCard)
	for _, tc := range []struct {
		name string
		fact sprint.MergeReq
	}{
		{"does not merge", sprint.MergeReq{Note: "the head h1 of s1-2 does not merge: CONFLICT (content): Merge conflict in internal/x.go", ConflictKind: "file", ConflictPaths: []string{"internal/x.go"}}},
		{"outside its PATHS", sprint.MergeReq{Note: "the head h1 of s1-2 fails the lander's checks: it changes files outside its PATHS (E12): internal/y.go"}},
		{"fails the tree gate", sprint.MergeReq{Note: "the head h1 of s1-2 fails the tree gate: go vet ./...: exit status 1: x.go:5:26: fmt.Printf format %d has arg s of wrong type string"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newConflictRig(t)
			for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
				r.toMerging(id)
			}
			// the lander merged s1-1, pushed it, and the next head was refused
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-1"}}))
			f := tc.fact
			f.Stream, f.Batch, f.Conflict = "s1", 1, "s1-2"
			r.must(store.MergeStep(f))

			s := r.snap()
			assert.Equal(t, sprint.StreamMerging, s.StreamCtl("s1").F("state"), "the stream goes on")
			assert.Empty(t, r.open(sprint.NConflict), "no judgment to answer")
			pr := s.Work.Card("s1-2")
			if tc.name == "outside its PATHS" {
				// returned for the widen rule, as the conflict rule returned it (widen.go)
				require.Equal(t, sprint.Review, pr.Col)
				assert.Equal(t, sprint.RefusedPaths, pr.F(sprint.FieldRuleRefused))
				assert.Equal(t, "1", pr.F(sprint.FieldRuleRedo))
				assert.Len(t, r.open(sprint.NReturned), 1)
			} else {
				require.Equal(t, sprint.Ready, pr.Col, "reworked")
				assert.Equal(t, sprint.LandRefusedFix(tc.fact.Note), pr.F("fix"), "the refusal is its fix")
			}

			// the stream lands the rest of its batch while the refused card is redone
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-3"}}))
			assert.Equal(t, sprint.Landed, r.snap().Work.Card("s1-3").Col)
			r.tick()
			assert.Equal(t, 2, r.snap().Work.Card("s1-2").Int("attempt"), "redone as a new attempt")
			r.clean("redone on the tip")
		})
	}
}

// dealStep cuts and deals work cards by hand, the step the tick's deal replaced
// (sprint.TickDeal): a test that needs exact queues deals with it.
func dealStep(r sprint.DealReq) store.Step {
	return store.Step{Args: store.ArgsOf(r), Verb: "deal", Load: []string{sprint.Work, sprint.Fleet, sprint.Merge}, Mirrors: true, Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Deal(s, r) }}
}

// A landing refusal that is the card's own never stops its stream (the owner, 2026-10-06:
// "There should be no manual step you need to remember to do. Just a notification."). The
// card is reworked at the tip in the merge step itself, its pushed head kept for staging to
// carry, the stream's other cards land in the same pass, and the seat is told once.
func TestAConflictingCardIsReworkedAndTheStreamLandsOn(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	head := readHead
	r.toMergingAt("s1-1", head)
	r.toMerging("s1-2")
	why := "the head " + head + " of s1-1 does not merge: CONFLICT (content): Merge conflict in internal/x.go"
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1", Note: why, ConflictKind: "file", ConflictPaths: []string{"internal/x.go"}}))

	s := r.snap()
	assert.Equal(t, sprint.StreamMerging, s.StreamCtl("s1").F("state"), "the card's own refusal stops nothing")
	assert.Empty(t, r.open(sprint.NConflict), "no stop, no judgment")
	pr := s.Work.Card("s1-1")
	require.Equal(t, sprint.Ready, pr.Col, "reworked: its next attempt waits ready")
	assert.Equal(t, sprint.LandRefusedFix(why), pr.F("fix"))
	assert.Contains(t, pr.F("fix"), "the landing refused this head: "+why+"; rebase on the base tip, resolve, make the tree gate pass")
	assert.Equal(t, sprint.Returned, s.Merge.Card("s1-1").Col, "off the merge queue")

	// the other card of the stream lands in the same pass
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-2"}}))
	assert.Equal(t, sprint.Landed, r.snap().Work.Card("s1-2").Col)

	// one notice to the seat, naming the card and the reason, nothing to answer
	all, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(t, err)
	var told []sprint.Note
	for _, n := range all {
		if n.Type == sprint.NLandRefused {
			told = append(told, n)
		}
	}
	require.Len(t, told, 1)
	assert.Equal(t, sprint.Happened, told[0].Kind)
	assert.Equal(t, "coordinator", told[0].To)
	assert.Equal(t, "s1-1", told[0].Card)
	assert.Equal(t, why, told[0].What)

	// the next deal is attempt n+1, the refusal its fix
	r.tick()
	s = r.snap()
	pr = s.Work.Card("s1-1")
	require.Equal(t, 2, pr.Int("attempt"), "dealt again as attempt 2")
	assert.Equal(t, sprint.LandRefusedFix(why), s.Fleet.Card(pr.F("work")).F("fix"))
	// staging starts attempt 2 from the refused head, carried onto the tip (packet.go, BaseOf)
	earlier := []*sprint.Card{s.Fleet.Card(sprint.WorkCardID("s1-1", 1))}
	assert.Equal(t, sprint.Base{Attempt: 1, Head: head}, sprint.BaseOf(earlier))
	r.clean("reworked at the tip")
}

// The same refusal twice is the same finding twice: the brief is wrong, not the worker
// (brief_bound.go). The second refusal raises the bound's judgment, never a third attempt.
func TestTheSameLandingRefusalTwiceIsABriefDefect(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	why := func(n int) string {
		return fmt.Sprintf("the head h%d of s1-1 does not merge: CONFLICT (content): Merge conflict in internal/x.go", n)
	}
	r.toMerging("s1-1")
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1", Note: why(1), ConflictKind: "file"}))
	require.Equal(t, sprint.Ready, r.snap().Work.Card("s1-1").Col, "the first refusal reworks it")
	r.toMerging("s1-1")
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1", Note: why(2), ConflictKind: "file"}))
	s := r.snap()
	pr := s.Work.Card("s1-1")
	assert.Equal(t, sprint.Review, pr.Col, "not a third attempt")
	assert.Equal(t, 2, pr.Int("attempt"))
	open := r.open(sprint.NBriefWrong)
	require.Len(t, open, 1, "the brief-defect judgment")
	assert.Contains(t, open[0].Note.What, "has failed the same way twice (attempts 1 and 2")
	assert.NotEqual(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"))
	r.clean("refused twice")
}

// A conflict the lander could not place on the card's own head (a generated ledger it could
// not resolve, a head origin does not hold) is the lander's failure, not the card's: the
// stream stops and a mind answers it, as the conflict rule leaves it (RefusalWay); a file
// conflict in the card's own head is reworked.
func TestALedgerTheLanderCouldNotResolveStopsTheStream(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind, why string
		stops     bool
	}{
		{"ledger", "the head h1 of s1-1 does not merge: the generated ledgers did not resolve: internal/ci/testdata/x.txt", true},
		{"", "the head " + strings.Repeat("ab", 20) + " of s1-1 is missing on origin", true},
		{"file", "the head h1 of s1-1 does not merge: CONFLICT (content): Merge conflict in internal/x.go", false},
	} {
		t.Run("kind="+tc.kind, func(t *testing.T) {
			t.Parallel()
			r := newConflictRig(t)
			r.toMerging("s1-1")
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1", Note: tc.why, ConflictKind: tc.kind}))
			s := r.snap()
			if tc.stops {
				assert.Equal(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"))
				assert.Equal(t, sprint.Stuck, s.Merge.Card("s1-1").Col)
				assert.Len(t, r.open(sprint.NConflict), 1, "a mind's")
			} else {
				assert.NotEqual(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"))
				assert.Equal(t, sprint.Ready, s.Work.Card("s1-1").Col)
				assert.Empty(t, r.open(sprint.NConflict))
			}
			r.clean(tc.kind)
		})
	}
}

// A refusal that is the base's (its tip fails the tree gate) still stops the stream with its
// judgment: a mind fixes the base.
func TestABaseFailureStillStopsTheStream(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	for _, id := range []string{"s1-1", "s1-2"} {
		r.toMerging(id)
	}
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", BaseRed: "go vet ./...: exit status 1"}))
	s := r.snap()
	assert.Equal(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"))
	assert.Len(t, r.open(sprint.NBaseRed), 1, "the base's one judgment")
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.Equal(t, sprint.Merging, s.Work.Card(id).Col, "no card is blamed for the base")
	}
	r.clean("the base fails")
}

// A card at its attempt bound is not reworked when the landing refuses its head: it goes back
// to review with the bound's judgment, and its stream lands on.
func TestACardAtItsBoundIsNotReworkedOnConflict(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.must(store.SetStep(sprint.SetReq{Streams: []string{"s1"}, Attempts: "1", Who: "coordinator"}))
	for _, id := range []string{"s1-1", "s1-2"} {
		r.toMerging(id)
	}
	why := "the head h1 of s1-1 fails the tree gate: go test ./internal/x/: exit status 1"
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1, Conflict: "s1-1", Note: why}))
	s := r.snap()
	assert.Equal(t, sprint.StreamMerging, s.StreamCtl("s1").F("state"))
	pr := s.Work.Card("s1-1")
	assert.Equal(t, sprint.Review, pr.Col, "not reworked at its bound")
	assert.Equal(t, 1, pr.Int("attempt"))
	assert.Empty(t, pr.F("fix"))
	open := r.open(sprint.NBriefWrong)
	require.Len(t, open, 1, "the bound's judgment")
	assert.Contains(t, open[0].Note.What, why)
	assert.Empty(t, r.open(sprint.NConflict))
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-2"}}))
	assert.Equal(t, sprint.Landed, r.snap().Work.Card("s1-2").Col, "the stream lands on")
	r.clean("at its bound")
}
