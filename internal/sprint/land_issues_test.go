package sprint_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
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

// closePass is one pass of the closer as the lander and the tick run it: the closer's
// lease taken, every request recorded through IssuesClosed, the lease given back with the
// pending closes shown, then a tick, which pumps what the steps queued. Nil when the lease
// was not taken.
func (r *alarmRig) closePass(f github.Closer, cards ...string) []sprint.IssuesReq {
	r.t.Helper()
	token := "pass" + strconv.Itoa(len(cards)) + r.st.Now().Format("150405")
	view, ok := r.takeLease(token)
	if !ok {
		r.ticks(1)
		return nil
	}
	reqs := sprint.CloseLandedIssues(r.ctx, view, f, r.st.Now(), cards, sprint.MachineActor)
	r.record(reqs)
	r.giveLease(token)
	r.ticks(1)
	return reqs
}

// takeLease runs the closer's lease step as token, and returns the work table it planned on
// (its queue applied) and whether the pass holds the lease.
func (r *alarmRig) takeLease(token string) (*sprint.Snapshot, bool) {
	r.t.Helper()
	var view *sprint.Snapshot
	won := false
	res, err := r.st.Run(r.ctx, store.Step{Verb: "issues", Load: []string{sprint.Work, sprint.Merge}, Actor: sprint.MachineActor,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			p, ok := sprint.IssuesLeaseTake(s, token, r.st.Now())
			view, won = s, ok
			return p
		}})
	require.NoError(r.t, err)
	return view, won && !res.Lost && len(res.Refused) == 0
}

// record writes one pass's requests (IssuesClosed).
func (r *alarmRig) record(reqs []sprint.IssuesReq) {
	r.t.Helper()
	for _, q := range reqs {
		r.must(store.Step{Verb: "issues", Args: store.ArgsOf(q), Load: []string{sprint.Work},
			Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.IssuesClosed(s, q) }})
	}
}

// giveLease ends a pass: the pending closes shown and token's lease given back.
func (r *alarmRig) giveLease(token string) {
	r.t.Helper()
	r.must(store.Step{Verb: "issues", Load: []string{sprint.Work}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.IssuesShown(s)
		p.Props = append(p.Props, sprint.IssuesLeaseGive(s, token).Props...)
		return p
	}})
}

// merging adds the cards to stream s1, released as v1.2.0, and takes them through work and
// two reads to merging, the machine running.
func (r *alarmRig) merging(cards ...sprint.CardAdd) {
	r.t.Helper()
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: cards}))
	r.must(store.SetStep(sprint.SetReq{Streams: []string{"s1"}, Release: "v1.2.0", Who: "coordinator"}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(r.t, err)
	r.ticks(3)
	r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	r.ticks(1)
	for _, c := range cards {
		r.finish(c.ID)
	}
	r.ticks(3)
	for _, rd := range []string{"reader-a", "reader-b"} {
		res, err := r.st.Run(r.ctx, store.ReadStep(sprint.ReadReq{As: rd, Verdict: "ok", Sel: sprint.Sel{Limit: 100}, Who: rd}))
		require.NoError(r.t, err, "read as %s: %+v", rd, res)
	}
	r.ticks(3)
	require.Len(r.t, r.snap().Work.Column(sprint.Merging), len(cards), "the fixture: every card merging")
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
	r.merging(sprint.CardAdd{ID: "a", Brief: brief}, sprint.CardAdd{ID: "a2", Brief: brief}, sprint.CardAdd{ID: "b", Brief: other})

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

// The lander's closer and the server loop's run in two processes, and each reads an issue
// open before it closes it: with nothing between them both comment
// (tla/LandingIssues.tla, MCLandingIssuesBrokenNoLease). One pass at a time holds the
// closer's lease; a second is refused it and asks GitHub nothing; a pass after it plans on
// the work table as its queue leaves it, so a close recorded and not yet pumped is not
// asked again; and a pass that died holding the lease holds the closes up only until it
// lapses (IssuesLease). One comment per issue.
func TestTheTwoClosersCommentOnceOnAnIssue(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	r.merging(sprint.CardAdd{ID: "a", Brief: "c: closes two\nREPO: mas-bandwidth/nova-tools\nISSUES: #31 #32\n\nThe task."},
		sprint.CardAdd{ID: "b", Brief: "c: closes one\nREPO: mas-bandwidth/nova-tools\nISSUES: #33\n\nThe task."})
	f := &fakeGitHub{closed: map[string]bool{}, comments: map[string][]string{}}
	_, ok := r.takeLease("idle")
	assert.False(t, ok, "nothing pending: no lease taken, nothing written")
	_, had := r.snap().Work.Prop(sprint.PropIssuesCloser)
	assert.False(t, had)

	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"a"}, Commit: "c0ffee4", Who: "coordinator"}))
	r.ticks(1)

	// the lander's pass takes the lease; the loop's, between its read and its close, is
	// refused it and asks GitHub nothing
	view, ok := r.takeLease("lander")
	require.True(t, ok)
	_, ok = r.takeLease("loop")
	assert.False(t, ok, "one pass at a time")
	r.ticks(1)
	assert.Equal(t, "lander", sprint.IssuesLeaseHolder(r.snap(), r.st.Now()), "the lease, pumped")
	reqs := sprint.CloseLandedIssues(r.ctx, view, f, r.st.Now(), []string{"a"}, sprint.MachineActor)
	require.Len(t, reqs, 1)
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#31", "mas-bandwidth/nova-tools#32"}, reqs[0].Closed)
	r.record(reqs)
	r.giveLease("lander")

	// no tick yet: the record is queued, the work table not pumped; the loop's pass reads
	// the table as its queue leaves it, finds nothing pending, and asks GitHub nothing
	assert.NotEmpty(t, sprint.PendingCloses(r.snap()), "the fixture: the stored table, unpumped, still shows a pending")
	asked := len(f.asked)
	_, ok = r.takeLease("loop")
	assert.False(t, ok, "the queued record is read: nothing is pending")
	assert.Equal(t, asked, len(f.asked))
	r.ticks(1)
	assert.Empty(t, sprint.PendingCloses(r.snap()))

	// b lands; a pass takes the lease and dies before it closes anything: the loop waits
	// while the lease is live, and takes it once it lapses
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{"b"}, Commit: "c0ffee5", Who: "coordinator"}))
	r.ticks(1)
	_, ok = r.takeLease("dead")
	require.True(t, ok)
	assert.Nil(t, r.closePass(f), "the dead pass's lease is live")
	assert.NotContains(t, f.asked, "mas-bandwidth/nova-tools#33")
	r.mu.Lock()
	r.now = r.now.Add(sprint.IssuesLease)
	r.mu.Unlock()
	reqs = r.closePass(f)
	require.Len(t, reqs, 1, "the lease lapsed: the loop's pass closes b's")
	assert.Equal(t, []string{"mas-bandwidth/nova-tools#33"}, reqs[0].Closed)
	r.giveLease("dead") // the dead pass's give, late: the lease is not its own any more
	assert.Empty(t, sprint.PendingCloses(r.snap()))
	for i, comments := range f.comments {
		assert.Len(t, comments, 1, "%s: one comment", i)
	}
	assert.Len(t, f.comments, 3)
	v, _ := r.snap().Work.Prop(sprint.PropIssuesCloser)
	assert.Equal(t, "free", v, "the lease given back as the pass ended")
}
