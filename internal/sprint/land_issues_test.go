package sprint_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/github"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// fakeGitHub is GitHub's issues as the closer asks them: each issue open or closed, the
// comments each close left, every close asked, and down, when set, refusing every close.
type fakeGitHub struct {
	closed   map[string]bool
	comments map[string][]string
	asked    []string
	down     error
}

func (f *fakeGitHub) Close(_ context.Context, i github.Issue, comment string) (bool, error) {
	f.asked = append(f.asked, i.String())
	if f.down != nil {
		return false, f.down
	}
	if f.closed[i.String()] {
		return true, nil
	}
	f.closed[i.String()] = true
	f.comments[i.String()] = append(f.comments[i.String()], comment)
	return false, nil
}

// closePass is one pass of the closer as the lander and the tick run it: every request
// recorded through IssuesClosed, then a tick, which pumps what the step queued.
func (r *alarmRig) closePass(f github.Closer, cards ...string) []sprint.IssuesReq {
	r.t.Helper()
	reqs := sprint.CloseLandedIssues(r.ctx, r.snap(), f, r.st.Now(), cards, sprint.MachineActor)
	for _, q := range reqs {
		r.must(store.Step{Verb: "issues", Args: store.ArgsOf(q), Load: []string{sprint.Work},
			Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.IssuesClosed(s, q) }})
	}
	r.must(store.Step{Verb: "issues", Load: []string{sprint.Work}, Plan: sprint.IssuesShown})
	r.ticks(1)
	return reqs
}

// shown is the pending closes as where shows them (sprint.PropIssuesPending).
func (r *alarmRig) shown() string {
	r.t.Helper()
	v, _ := r.snap().Work.Prop(sprint.PropIssuesPending)
	return v
}

// A landing closes the issues the card references (docs/SPEC-SPRINT.md section 7, "A landing
// closes the card's issues"): the issues of its brief's ISSUES: line, read against its REPO:
// or by full URL, and its landed commits' "Closes #N", each closed once with one comment
// naming the card, the stream, the landing commit and the release; a number in the brief's
// prose is no reference. A twin landing after it closes nothing new; an issue GitHub has
// closed already is left alone and noted; GitHub refusing leaves the card landed and
// the close pending, shown, and tried again after IssuesRetry by the tick's closer.
func TestALandingClosesTheIssuesTheCardReferences(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	brief := "c: closes two\nREPO: mas-bandwidth/nova-tools\nISSUES: #12, " + github.Web + "mas-bandwidth/ideas/issues/7\n\nThe task; #4327 deleted the old one."
	other := "c: closes one\nREPO: " + github.Web + "mas-bandwidth/nova-tools.git\nISSUES: #21 #20\n\nThe task."
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "a", Brief: brief}, {ID: "a2", Brief: brief}, {ID: "b", Brief: other}}}))
	r.must(store.SetStep(sprint.SetReq{Streams: []string{"s1"}, Release: "v1.2.0", Who: "coordinator"}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.ticks(3)
	r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	r.ticks(1)
	for _, id := range []string{"a", "a2", "b"} {
		r.finish(id)
	}
	r.ticks(3)
	for _, rd := range []string{"reader-a", "reader-b"} {
		res, err := r.st.Run(r.ctx, store.ReadStep(sprint.ReadReq{As: rd, Verdict: "ok", Sel: sprint.Sel{Limit: 100}, Who: rd}))
		require.NoError(t, err, "read as %s: %+v", rd, res)
	}
	r.ticks(3)
	require.Len(t, r.snap().Work.Column(sprint.Merging), 3, "the fixture: a, a2 and b merging")

	f := &fakeGitHub{closed: map[string]bool{"mas-bandwidth/nova-tools#21": true}, comments: map[string][]string{}}

	// a lands at a commit whose message closes #13 too: its three issues are written on it
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"a"}, Commit: "c0ffee1", Closes: map[string][]string{"a": {"mas-bandwidth/nova-tools#13"}}, Who: "coordinator"}))
	r.ticks(1)
	a := r.snap().Work.Placed("a")
	require.Equal(t, sprint.Landed, a.Col)
	assert.Equal(t, "mas-bandwidth/nova-tools#12,mas-bandwidth/ideas#7,mas-bandwidth/nova-tools#13", a.F(sprint.FieldIssues), "the brief's two, then the commit's; #4327 of the prose is none")
	assert.Equal(t, "c0ffee1", a.F(sprint.FieldLandCommit))
	require.Len(t, sprint.PendingCloses(r.snap()), 1, "a's issues are pending until the closer runs")

	reqs := r.closePass(f, "a")
	require.Len(t, reqs, 1)
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#12", "mas-bandwidth/ideas#7", "mas-bandwidth/nova-tools#13"}, reqs[0].Closed)
	for _, i := range []string{"mas-bandwidth/nova-tools#12", "mas-bandwidth/ideas#7", "mas-bandwidth/nova-tools#13"} {
		require.Len(t, f.comments[i], 1, "%s closed with one comment", i)
		c := f.comments[i][0]
		for _, want := range []string{"card: a", "stream: s1", "landing commit: c0ffee1", "ships in: v1.2.0"} {
			assert.Contains(t, c, want, "%s's comment", i)
		}
	}
	assert.Equal(t, a.F(sprint.FieldIssues), r.snap().Work.Placed("a").F(sprint.FieldIssuesClosed))
	assert.Empty(t, sprint.PendingCloses(r.snap()))
	assert.Empty(t, r.closePass(f), "a second pass closes nothing: every issue of a is closed")

	// a2, a's twin, lands: its issues are a's, closed already, so nothing new is asked of GitHub
	asked := len(f.asked)
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"a2"}, Commit: "c0ffee2", Who: "coordinator"}))
	r.ticks(1)
	reqs = r.closePass(f, "a2")
	require.Len(t, reqs, 1)
	assert.Empty(t, reqs[0].Closed, "the twin closes nothing new")
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#12", "mas-bandwidth/ideas#7"}, reqs[0].Noted)
	assert.Equal(t, asked, len(f.asked), "GitHub was not asked again")
	for _, comments := range f.comments {
		assert.Len(t, comments, 1, "one comment an issue")
	}

	// b lands while GitHub is down: b is landed, its issues stay pending with GitHub's
	// words, and the tick does not ask again inside the retry
	f.down = errors.New("gh: could not connect to api.github.com")
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"b"}, Commit: "c0ffee3", Who: "coordinator"}))
	r.ticks(1)
	reqs = r.closePass(f, "b")
	require.Len(t, reqs, 1)
	assert.Contains(t, reqs[0].Why, "could not connect")
	b := r.snap().Work.Placed("b")
	assert.Equal(t, sprint.Landed, b.Col, "the landing stands whatever GitHub says")
	pending := sprint.PendingCloses(r.snap())
	require.Len(t, pending, 1)
	assert.Equal(t, "b", pending[0].Card)
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#21", "mas-bandwidth/nova-tools#20"}, pending[0].Issues)
	assert.Contains(t, pending[0].Why, "could not connect")
	assert.Equal(t, "2 issues of 1 landed cards: b mas-bandwidth/nova-tools#21,mas-bandwidth/nova-tools#20 ("+pending[0].Why+")", r.shown(), "where shows the pending close")
	asked = len(f.asked)
	assert.Empty(t, r.closePass(f), "inside IssuesRetry the tick's closer waits")
	assert.Equal(t, asked, len(f.asked))

	// GitHub is back and the retry is due: the tick's closer leaves #21, closed on
	// GitHub, alone with no comment, and closes #20
	f.down = nil
	r.mu.Lock()
	r.now = r.now.Add(sprint.IssuesRetry)
	r.mu.Unlock()
	reqs = r.closePass(f)
	require.Len(t, reqs, 1)
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#21"}, reqs[0].Noted, "found closed: left alone")
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#20"}, reqs[0].Closed)
	assert.Empty(t, sprint.PendingCloses(r.snap()))
	assert.Equal(t, "0", r.shown(), "where shows none pending")
	b = r.snap().Work.Placed("b")
	assert.Empty(t, b.F(sprint.FieldIssuesWhy), "the why is cleared once nothing is pending")
	assert.Empty(t, f.comments["mas-bandwidth/nova-tools#21"], "an issue closed already gets no comment")
	assert.True(t, slices.Contains(f.asked, "mas-bandwidth/nova-tools#20"))
}
