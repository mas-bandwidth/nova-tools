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

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A head the lander refuses is the tick's to answer, by rule (docs/SPEC-SPRINT.md section 8,
// the rules table's row conflict; the owner, 2026-10-04: "the machine keeps itself fed"; "a
// hand step is a missing instruction"). That day every head that did not merge, changed
// files outside its PATHS or failed the tree gate stopped its whole stream until the
// coordinator answered, and 19 streams sat stopped behind a hand loop. Now such a head is
// returned, its next attempt is dealt at flash with the refusal as its fix, to be redone on
// the base's tip, and its stream lands the rest of its batch; the stream stops and a
// judgment stays only when the same card is refused the same way twice (a brief defect).
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
func (r *conflictRig) toMerging(id string) {
	r.t.Helper()
	if r.snap().Work.Card(id).Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	wc = r.snap().Fleet.Card(wc.ID)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
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
		name  string
		fact  sprint.MergeReq
		again sprint.MergeReq // another refusal of the same way, at the next attempt's head
	}{
		{"does not merge",
			sprint.MergeReq{Note: "the head h1 of s1-2 does not merge: CONFLICT (content): Merge conflict in internal/x.go", ConflictKind: "file", ConflictPaths: []string{"internal/x.go"}},
			sprint.MergeReq{Note: "the head h2 of s1-2 does not merge: CONFLICT (content): Merge conflict in internal/x.go", ConflictKind: "file", ConflictPaths: []string{"internal/x.go"}}},
		{"outside its PATHS",
			sprint.MergeReq{Note: "the head h1 of s1-2 fails the lander's checks: it changes files outside its PATHS (E12): internal/y.go"},
			sprint.MergeReq{Note: "the head h2 of s1-2 fails the lander's checks: it changes files outside its PATHS (E12): internal/z.go"}},
		{"fails the tree gate",
			sprint.MergeReq{Note: "the head h1 of s1-2 fails the tree gate: go vet ./...: exit status 1: x.go:5:26: fmt.Printf format %d has arg s of wrong type string"},
			sprint.MergeReq{Note: "the head h2 of s1-2 fails the tree gate: go test ./internal/x/: exit status 1: "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newConflictRig(t)
			for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
				r.toMerging(id)
			}
			// the lander merged s1-1, pushed it, and the next head was refused
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-1"}}))
			refuse := func(f sprint.MergeReq) {
				t.Helper()
				f.Stream, f.Batch, f.Conflict = "s1", 1, "s1-2"
				r.must(store.MergeStep(f))
				require.Equal(t, sprint.StreamStopped, r.snap().StreamCtl("s1").F("state"), "the merge step records the refusal")
			}
			refuse(tc.fact)
			r.tick()

			s := r.snap()
			assert.NotEqual(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"), "the stream goes on in the same tick")
			assert.Empty(t, r.open(sprint.NConflict), "the refusal is answered by rule, no judgment left")
			assert.NotEmpty(t, r.answeredBy(sprint.RuleConflict))
			pr := s.Work.Card("s1-2")
			require.Equal(t, 2, pr.Int("attempt"), "returned and redealt as a new attempt")
			assert.Contains(t, pr.F("fix"), sprint.RuleConflictFix, "redone on the base's tip")
			if tc.fact.ConflictKind == "" {
				assert.Contains(t, pr.F("fix"), tc.fact.Note, "the refusal is its fix")
			} else {
				assert.Equal(t, sprint.RuleConflictFix, pr.F("fix"), "a file conflict is the tip's to answer")
			}
			assert.Equal(t, "flash", pr.F(sprint.FieldTierNow))
			wc := s.Fleet.Card(pr.F("work"))
			require.NotNil(t, wc)
			assert.Equal(t, "flash-a", wc.F(sprint.FieldRoute), "the redo is dealt at flash")

			// the stream lands the rest of its batch while the refused card is redone
			r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"s1-3"}}))
			assert.Equal(t, sprint.Landed, r.snap().Work.Card("s1-3").Col)
			r.clean("redone on the tip")

			// the same card refused the same way again: a brief defect, the stream stops
			r.toMerging("s1-2")
			refuse(tc.again)
			r.tick()
			r.tick()
			s = r.snap()
			assert.Equal(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"), "a repeat stops the stream")
			open := r.open(sprint.NConflict)
			require.Len(t, open, 1, "one judgment, raised once")
			assert.True(t, strings.HasPrefix(open[0].Note.What, "brief defect: "), open[0].Note.What)
			pr = s.Work.Card("s1-2")
			assert.NotEmpty(t, pr.F(sprint.FieldBriefDefect), "the card is marked a brief defect")
			assert.Equal(t, 2, pr.Int("attempt"), "not redone a third time")
			r.clean("refused twice")
		})
	}
}

// dealStep cuts and deals work cards by hand, the step the tick's deal replaced
// (sprint.TickDeal): a test that needs exact queues deals with it.
func dealStep(r sprint.DealReq) store.Step {
	return store.Step{Args: store.ArgsOf(r), Verb: "deal", Load: []string{sprint.Work, sprint.Fleet, sprint.Merge}, Mirrors: true, Routes: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Deal(s, r) }}
}
