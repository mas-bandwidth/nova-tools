package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// TestStreamsListsEveryStreamByRepoInOneCall is the one listing
// (docs/SPEC-SPRINT.md section 11, streams): three streams over two
// repositories, one of them naming both, read by one Load of the work and
// merge tables. A stream keeps every repository and base its cards' briefs
// name, and a mix is a finding. drop and hold by repository refuse unless
// --expect is that count.
func TestStreamsListsEveryStreamByRepoInOneCall(t *testing.T) {
	t.Parallel()

	r := newStreamListRig(t)
	r.must(store.AddStep(sprint.AddReq{Stream: "alpha", Cards: []sprint.CardAdd{
		{ID: "alpha-1", Brief: streamListBrief("mas-bandwidth/nova", "sprint/nova", "List nova. Then stop."), Needs: []string{"alpha-2"}},
		{ID: "alpha-2", Brief: streamListBrief("https://git.example.test/mas-bandwidth/nova.git", "sprint/nova", "Stage the clone.")},
	}}))
	r.must(store.AddStep(sprint.AddReq{Stream: "beta", Cards: []sprint.CardAdd{
		{ID: "beta-1", Brief: streamListBrief("mas-bandwidth/nova-tools", "sprint/tools", "Count tools.")},
	}}))
	r.must(store.AddStep(sprint.AddReq{Stream: "both", Cards: []sprint.CardAdd{
		{ID: "both-1", Brief: streamListBrief("mas-bandwidth/nova", "sprint/nova", "Share the nova card.")},
		{ID: "both-2", Brief: streamListBrief("mas-bandwidth/nova-tools", "sprint/tools", "Share the tools card.")},
	}}))
	r.must(store.SetStep(sprint.SetReq{Streams: []string{"alpha", "beta"}, Release: "v11", Who: "coordinator"}))

	before := callCounts(r.m)
	started := time.Now()
	s := r.loadWorkMerge()
	rows := sprint.StreamsList(s, sprint.StreamListFilter{Cards: true})
	elapsed := time.Since(started)
	delta := callDelta(before, r.m)
	require.Equal(t, 1, delta["shapes"], "the listing is one Load of the tables, not one per stream (shapes exchanges %d, took %s)", delta["shapes"], elapsed)
	require.Len(t, rows, 3)

	byName := map[string]sprint.StreamView{}
	for _, row := range rows {
		byName[row.Stream] = row
	}
	alpha := byName["alpha"]
	assert.Equal(t, []string{"mas-bandwidth/nova"}, alpha.Repos)
	assert.Equal(t, []string{"sprint/nova"}, alpha.Bases)
	assert.Equal(t, "v11", alpha.Release)
	assert.Equal(t, 2, alpha.Open)
	assert.Equal(t, 0, alpha.Landed)
	assert.Empty(t, alpha.Finding)
	require.Len(t, alpha.Cards, 2)
	assert.Equal(t, "alpha-1", alpha.Cards[0].ID)
	assert.Equal(t, sprint.Waiting, alpha.Cards[0].State)
	assert.Equal(t, "flash", alpha.Cards[0].Tier)
	assert.Equal(t, "List nova.", alpha.Cards[0].Title)
	assert.Equal(t, []string{"alpha-2"}, alpha.Cards[0].Needs)
	assert.Equal(t, sprint.Ready, alpha.Cards[1].State)
	assert.Equal(t, "Stage the clone.", alpha.Cards[1].Title)

	beta := byName["beta"]
	assert.Equal(t, []string{"mas-bandwidth/nova-tools"}, beta.Repos)
	assert.Equal(t, []string{"sprint/tools"}, beta.Bases)
	assert.Equal(t, "v11", beta.Release)
	assert.Equal(t, 1, beta.Open)
	assert.Equal(t, 0, beta.Landed)
	assert.Empty(t, beta.Finding)

	both := byName["both"]
	assert.Equal(t, []string{"mas-bandwidth/nova", "mas-bandwidth/nova-tools"}, both.Repos)
	assert.Equal(t, []string{"sprint/nova", "sprint/tools"}, both.Bases)
	assert.Empty(t, both.Release)
	assert.Equal(t, 2, both.Open)
	assert.Contains(t, both.Finding, "more than one repository")
	assert.Contains(t, both.Finding, "mas-bandwidth/nova-tools")
	assert.Contains(t, both.Finding, "more than one base")

	released := sprint.StreamsList(s, sprint.StreamListFilter{Release: "v11"})
	require.Len(t, released, 2)
	assert.Equal(t, "alpha", released[0].Stream)
	assert.Equal(t, "beta", released[1].Stream)

	novaOnly := sprint.StreamsList(s, sprint.StreamListFilter{Repo: "mas-bandwidth/nova", Release: "v11"})
	require.Len(t, novaOnly, 1)
	assert.Equal(t, "alpha", novaOnly[0].Stream)

	// Landed is the work column. One card is moved there on this snapshot so
	// the count is read from the column the listing sees.
	betaCard := s.Work.Card("beta-1")
	require.NotNil(t, betaCard)
	betaCard.Col = sprint.Landed
	landed := sprint.StreamsList(s, sprint.StreamListFilter{Repo: "mas-bandwidth/nova-tools"})
	require.Len(t, landed, 2, "both still names nova-tools")
	assert.Equal(t, 0, landed[0].Open)
	assert.Equal(t, 1, landed[0].Landed)
	assert.Equal(t, "beta", landed[0].Stream)

	miss := r.plan("hold", func(s *sprint.Snapshot) sprint.Plan {
		return sprint.HoldByRepo(s, sprint.RepoAct{Repo: "mas-bandwidth/nova-tools", Expect: 9, Reason: "pause", Who: "coordinator"})
	})
	require.NotEmpty(t, miss.Refused)
	assert.Contains(t, miss.Refused[0].Why, "2")
	assert.False(t, freshHeld(r, "beta"))

	held := r.plan("hold", func(s *sprint.Snapshot) sprint.Plan {
		return sprint.HoldByRepo(s, sprint.RepoAct{Repo: "mas-bandwidth/nova-tools", Expect: 2, Reason: "pause", Who: "coordinator"})
	})
	require.Empty(t, held.Refused, "%v", held.Refused)
	assert.True(t, freshHeld(r, "beta"))
	assert.True(t, freshHeld(r, "both"), "a mixed stream is held whole")
	assert.False(t, freshHeld(r, "alpha"))

	refused := r.plan("drop", func(s *sprint.Snapshot) sprint.Plan {
		return sprint.DropByRepo(s, sprint.RepoAct{Repo: "mas-bandwidth/nova", Expect: 1, Reason: "left", Who: "coordinator"})
	})
	require.NotEmpty(t, refused.Refused)
	assert.Contains(t, refused.Refused[0].Why, "3")
	require.True(t, r.loadWorkMerge().Work.Placed("alpha-1") != nil, "a mismatched --expect writes nothing")

	none := r.plan("drop", func(s *sprint.Snapshot) sprint.Plan {
		return sprint.DropByRepo(s, sprint.RepoAct{Repo: "mas-bandwidth/nova", Reason: "left", Who: "coordinator"})
	})
	require.NotEmpty(t, none.Refused)
	assert.Contains(t, none.Refused[0].Why, "without --expect")

	dropped := r.plan("drop", func(s *sprint.Snapshot) sprint.Plan {
		return sprint.DropByRepo(s, sprint.RepoAct{Repo: "mas-bandwidth/nova", Streams: []string{"alpha", "both"}, Expect: 3, Reason: "left", Who: "coordinator"})
	})
	require.Empty(t, dropped.Refused, "%v", dropped.Refused)
	after := r.loadWorkMerge()
	assert.Nil(t, after.Work.Placed("alpha-1"))
	assert.Nil(t, after.Work.Placed("alpha-2"))
	assert.Nil(t, after.Work.Placed("both-1"))
	require.NotNil(t, after.Work.Placed("both-2"), "the other repository stays")
	left := sprint.StreamsList(after, sprint.StreamListFilter{})
	for _, row := range left {
		if row.Stream == "both" {
			assert.Equal(t, []string{"mas-bandwidth/nova-tools"}, row.Repos)
			assert.Empty(t, row.Finding)
		}
	}

	r.measure(3000)
}

func (r *streamListRig) measure(n int) {
	r.t.Helper()
	r.must(store.AddStep(sprint.AddReq{
		Stream: "bulk",
		Count:  n,
		Brief:  streamListBrief("mas-bandwidth/nova-tools", "sprint/mechanical-2026-10-02", "Fill the stream."),
	}))
	before := callCounts(r.m)
	started := time.Now()
	s := r.loadWorkMerge()
	rows := sprint.StreamsList(s, sprint.StreamListFilter{Repo: "mas-bandwidth/nova-tools"})
	elapsed := time.Since(started)
	delta := callDelta(before, r.m)
	var bulk sprint.StreamView
	for _, row := range rows {
		if row.Stream == "bulk" {
			bulk = row
		}
	}
	require.Equal(r.t, n, bulk.Open)
	require.Equal(r.t, []string{"mas-bandwidth/nova-tools"}, bulk.Repos)
	require.Less(r.t, elapsed, time.Second, "one listing of %d cards took %s", n, elapsed) // wall-ok: the card's bound is one second for 3000 cards on the twin, and the line prints the elapsed time
	r.t.Logf("MEASURE streams-list cards=%d elapsed=%s shapes=%d cells=%d readset=%d", n, elapsed, delta["shapes"], delta["cells"], delta["readset"])
	fmt.Printf("MEASURE streams-list cards=%d elapsed=%s shapes=%d cells=%d readset=%d\n", n, elapsed, delta["shapes"], delta["cells"], delta["readset"])
}

func streamListBrief(repo, base, task string) string {
	return "tier: flash\nREPO: " + repo + "\nBASE: " + base + "\n\nTHE TASK. " + task + "\n"
}

type streamListRig struct {
	t   *testing.T
	m   *store.Mem
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	n   int
}

func newStreamListRig(t *testing.T) *streamListRig {
	t.Helper()
	r := &streamListRig{t: t, m: store.NewMem(), ctx: context.Background()}
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); r.n++; return fmt.Sprint(r.n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	return r
}

func (r *streamListRig) must(step store.Step) store.Result {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused: %v", step.Verb, res.Refused)
	return res
}

func (r *streamListRig) plan(verb string, plan func(*sprint.Snapshot) sprint.Plan) store.Result {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, store.Step{
		Verb:    verb,
		Load:    []string{sprint.Work, sprint.Merge, sprint.Fleet, sprint.Readers},
		Mirrors: true,
		Named:   true,
		Plan:    plan,
	})
	require.NoError(r.t, err, verb)
	return res
}

func (r *streamListRig) loadWorkMerge() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, []string{sprint.Work, sprint.Merge}, nil)
	require.NoError(r.t, err)
	return s
}

func freshHeld(r *streamListRig, stream string) bool {
	r.t.Helper()
	return sprint.StreamHeld(r.loadWorkMerge(), stream)
}

func callCounts(m *store.Mem) map[string]int {
	out := map[string]int{}
	for k, v := range m.Calls {
		out[k] = v
	}
	return out
}

func callDelta(before map[string]int, m *store.Mem) map[string]int {
	out := map[string]int{}
	for k, v := range m.Calls {
		out[k] = v - before[k]
	}
	return out
}
