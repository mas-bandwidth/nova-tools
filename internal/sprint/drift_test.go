package sprint_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
)

// twinRepo is a repository standing for origin as the binding fetches it: dev, and the
// sprint base cut from it, each commit stamped on the rig's fake clock.
type twinRepo struct {
	t   *testing.T
	dir string
}

func newTwinRepo(t *testing.T) *twinRepo {
	t.Helper()
	g := &twinRepo{t: t, dir: t.TempDir()}
	g.gitAt(time.Time{}, "init", "--quiet", "--initial-branch=dev")
	g.gitAt(time.Time{}, "config", "commit.gpgsign", "false")
	return g
}

// gitAt runs git in the repository, its commits dated at (zero: now, for a command that
// writes no commit).
func (g *twinRepo) gitAt(at time.Time, args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", g.dir}, args...)...)
	extra := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}
	if !at.IsZero() {
		d := at.UTC().Format(time.RFC3339)
		extra = append(extra, "GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d)
	}
	cmd.Env = testgit.Environ(extra...)
	out, err := cmd.CombinedOutput()
	require.NoError(g.t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// commit writes one commit on the branch checked out, at the time.
func (g *twinRepo) commit(at time.Time, name string) string {
	g.t.Helper()
	require.NoError(g.t, os.WriteFile(filepath.Join(g.dir, name), []byte(name+"\n"), 0o644))
	g.gitAt(at, "add", name)
	g.gitAt(at, "commit", "--quiet", "-m", name)
	return g.gitAt(time.Time{}, "rev-parse", "HEAD")
}

// driftRig is the alarm rig's twin store and fake clock with a twin repository: the
// drift part planned on a fresh read of the store, with the facts the git reader reads
// from the twin repository, applied as the machine's step.
type driftRig struct {
	*alarmRig
	repo   *twinRepo
	server string            // the running server's build commit
	gate   *sprint.DriftGate // the last whole-tree gate at the base's tip
}

func (r *driftRig) clock(d time.Duration) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
	return r.now
}

func (r *driftRig) at() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

// tick reads the drift facts from the twin repository and applies the drift part.
func (r *driftRig) tick() {
	r.t.Helper()
	facts, err := sprint.ReadDrift(context.Background(), sprint.RunGit, r.repo.dir, "sprint/base", "dev", r.server)
	require.NoError(r.t, err)
	facts.Gate = r.gate
	r.tickOn(&facts)
}

// tickOn applies the drift part on the facts given (nil: none read this tick).
func (r *driftRig) tickOn(facts *sprint.DriftFacts) {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, store.Step{Verb: "tick drift", Actor: sprint.MachineActor, Load: store.All, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p, _ := sprint.TickDrift(s, sprint.TickReq{}, facts)
		return p
	}})
	require.NoError(r.t, err)
	require.Empty(r.t, res.Refused)
}

// raised is, for each drift type, the judgments of it the sprint wrote, the raises
// again pushed of it, and the ones of it open now.
func (r *driftRig) raised() map[string][3]int {
	r.t.Helper()
	notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	ids := map[string]string{}
	out := map[string][3]int{}
	for _, n := range notes {
		if n.Kind == sprint.Judgment && slices.Contains(sprint.DriftTypes, n.Type) {
			ids[n.ID] = n.Type
			e := out[n.Type]
			e[0]++
			out[n.Type] = e
		}
	}
	for _, n := range notes {
		if n.Kind == sprint.Happened && n.Type == sprint.NRaisedAgain {
			for id, typ := range ids {
				if strings.HasPrefix(n.What, id+" ") {
					assert.Equal(r.t, "coordinator", n.To, "a raise again is the coordinator's: %+v", n)
					e := out[typ]
					e[1]++
					out[typ] = e
				}
			}
		}
	}
	for _, o := range r.snap().Open {
		if o.Note.Kind == sprint.Judgment && slices.Contains(sprint.DriftTypes, o.Note.Type) {
			e := out[o.Note.Type]
			e[2]++
			out[o.Note.Type] = e
		}
	}
	return out
}

// Every drift is a fact the machine raises (docs/SPEC-SPRINT.md section 8, "Drift
// alarms"): the base ahead of dev past drift_commits or drift_hours, an open card cut on
// another base, the live server's commit not on the base, the base red at its tip by the
// whole-tree gate with the functional class: each one judgment, raised once when it
// starts, raised again every 10 minutes while it holds (the judgment rewritten, and a push
// to the coordinator), never one per tick, and closed when it stops.
func TestEveryDriftIsRaisedAgainEveryTenMinutesWhileItHolds(t *testing.T) {
	t.Parallel()
	r := &driftRig{alarmRig: newAlarmRig(t), repo: newTwinRepo(t)}
	t0 := r.at()

	// dev at one commit; the base cut from it, 3 commits ahead, the oldest 3 hours old
	r.repo.commit(t0.Add(-5*time.Hour), "root")
	r.repo.gitAt(time.Time{}, "branch", "sprint/base")
	r.repo.gitAt(time.Time{}, "checkout", "--quiet", "sprint/base")
	r.repo.commit(t0.Add(-3*time.Hour), "a")
	r.repo.commit(t0.Add(-2*time.Hour), "b")
	tip := r.repo.commit(t0.Add(-1*time.Hour), "c")
	// the server built from a side branch off dev, never merged into the base
	r.repo.gitAt(time.Time{}, "checkout", "--quiet", "-b", "side", "dev")
	r.server = r.repo.commit(t0.Add(-4*time.Hour), "side")
	r.repo.gitAt(time.Time{}, "checkout", "--quiet", "sprint/base")
	// the base red at its tip by the whole tree's gate, the functional class run
	r.gate = &sprint.DriftGate{Tip: tip, Whole: true, Functional: true, Red: true, Failed: []string{"internal/ci TestHarnessWatchIsWired"}}
	// a card cut on a temporary branch, and one on the base
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stray"}, Brief: "REPO: mas-bandwidth/nova-tools\nBASE: tmp/rescue\n"}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"good"}, Brief: "REPO: mas-bandwidth/nova-tools\nBASE: sprint/base\n"}))
	// the thresholds: 2 commits, or 2 hours
	r.must(store.SetStep(sprint.SetReq{DriftCommits: "2", DriftHours: "2", Who: "coordinator"}))

	want := func(when string, e [3]int) {
		t.Helper()
		got := r.raised()
		for _, typ := range sprint.DriftTypes {
			assert.Equal(t, e, got[typ], "%s: %s (judgments written, raises again, open)", when, typ)
		}
	}

	r.tick()
	want("each drift starts", [3]int{1, 0, 1})
	s := r.snap()
	for _, o := range s.Open {
		switch o.Note.Type {
		case sprint.NDriftCardBase:
			assert.Equal(t, "stray", o.Subject(), "the card cut off the base is named")
			assert.Contains(t, o.Note.What, "tmp/rescue", "and the branch it names")
		case sprint.NDriftAhead:
			assert.Contains(t, o.Note.What, "3 commits")
		case sprint.NDriftServer:
			assert.Contains(t, o.Note.What, r.server[:12])
		case sprint.NDriftBaseRed:
			assert.Contains(t, o.Note.What, "TestHarnessWatchIsWired")
		}
	}

	// every minute for nine: the same judgments, nothing pushed again
	for range 9 {
		r.clock(time.Minute)
		r.tick()
	}
	want("nine minutes on", [3]int{1, 0, 1})

	// ten minutes after it was written, each is raised again once, and only once a tick
	r.clock(time.Minute)
	r.tick()
	r.tick()
	want("ten minutes on", [3]int{1, 1, 1})
	r.clock(10 * time.Minute)
	r.tick()
	want("twenty minutes on", [3]int{1, 2, 1})

	// the base ahead grows: still the one judgment, its facts the latest
	r.repo.commit(r.at(), "d")
	r.tick()
	want("a fourth commit on the base", [3]int{1, 2, 1})

	// each drift ends: dev takes the base, the card leaves, the server is rebuilt on the
	// base, the base's gate is green at its new tip
	r.repo.gitAt(time.Time{}, "checkout", "--quiet", "dev")
	r.repo.gitAt(r.at(), "merge", "--quiet", "--ff-only", "sprint/base")
	r.repo.gitAt(time.Time{}, "checkout", "--quiet", "sprint/base")
	r.must(store.DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"stray"}}, Reason: "cut on tmp/rescue", Who: "coordinator"}))
	r.server = r.repo.gitAt(time.Time{}, "rev-parse", "sprint/base")
	r.gate = &sprint.DriftGate{Tip: r.server, Whole: true, Functional: true}
	r.clock(time.Minute)
	r.tick()
	want("every drift ended", [3]int{1, 2, 0})

	// ten minutes on with nothing drifting: nothing raised
	r.clock(10 * time.Minute)
	r.tick()
	want("quiet", [3]int{1, 2, 0})

	// a narrow gate alone judges nothing: red by the lander's gate is not the base red
	r.gate = &sprint.DriftGate{Tip: r.server, Red: true, Failed: []string{"internal/sprint"}}
	r.tick()
	want("a narrow gate", [3]int{1, 2, 0})

	// under the thresholds the base ahead is no drift: one commit, an hour old
	r.repo.commit(r.at().Add(-time.Hour), "e")
	r.tick()
	assert.Equal(t, 0, r.raised()[sprint.NDriftAhead][2], "1 commit an hour old is under 2 commits and 2 hours")

	// the coordinator's wait quiets a drift until its time, and only until then: a card cut
	// on a dead branch, judged, then waited for 30 minutes
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"dead"}, Brief: "REPO: mas-bandwidth/nova-tools\nBASE: old/dead\n"}))
	r.clock(time.Minute)
	r.tick()
	assert.Equal(t, [3]int{2, 2, 1}, r.raised()[sprint.NDriftCardBase], "a second card off the base: judged once")
	id := r.open(sprint.NDriftCardBase)
	require.NotEmpty(t, id)
	r.must(store.WaitStep(sprint.WaitReq{Note: id, Until: r.at().Add(30 * time.Minute), Who: "coordinator"}))
	assert.Equal(t, [3]int{2, 2, 0}, r.raised()[sprint.NDriftCardBase], "the wait closes the judgment and holds the drift")
	held := func(when string) {
		t.Helper()
		n := 0
		for _, o := range r.snap().Acked {
			if o.Note.Type == sprint.NDriftCardBase && !o.Note.Review.IsZero() {
				n++
			}
		}
		assert.Equal(t, 1, n, "%s: the wait on the card off the base stands", when)
	}
	held("waited")
	for range 2 {
		r.clock(10 * time.Minute)
		r.tick()
	}
	assert.Equal(t, [3]int{2, 2, 0}, r.raised()[sprint.NDriftCardBase], "twenty minutes into the wait: no raise, no judgment")
	held("twenty minutes into the wait")
	// a tick that read no facts closes nothing, the wait included
	r.tickOn(nil)
	held("a tick with no facts")
	// the wait runs out with the drift holding: judged again, and raised again ten minutes on
	r.clock(10 * time.Minute)
	r.tick()
	assert.Equal(t, [3]int{3, 2, 1}, r.raised()[sprint.NDriftCardBase], "the wait run out: the drift judged again")
	r.clock(10 * time.Minute)
	r.tick()
	assert.Equal(t, [3]int{3, 3, 1}, r.raised()[sprint.NDriftCardBase], "and raised again ten minutes on")
	r.must(store.DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"dead"}}, Reason: "cut on old/dead", Who: "coordinator"}))
	r.clock(time.Minute)
	r.tick()
	assert.Equal(t, [3]int{3, 3, 0}, r.raised()[sprint.NDriftCardBase], "the card gone: closed")
}

// The deadlines part keeps the drift alarms on the facts the binding gives the tick
// (TickReq.Drift), and none on none; set takes the thresholds as whole numbers from 1.
func TestTheDeadlinesPartJudgesTheDriftItIsGiven(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"stray"}, Brief: "BASE: old/branch\n"}))
	s := r.snap()
	types := func(p sprint.Plan) []string {
		var out []string
		for _, n := range p.Notes {
			if slices.Contains(sprint.DriftTypes, n.Type) {
				out = append(out, n.Type)
			}
		}
		return out
	}
	p, _ := sprint.TickDeadlines(s, sprint.TickReq{})
	assert.Empty(t, types(p), "no facts, no drift judged")
	f := sprint.DriftFacts{Base: "sprint/base", Ahead: &sprint.DriftAhead{Commits: 40, Oldest: s.Now.Add(-time.Hour), Tip: "abc"},
		Server: &sprint.DriftServer{Commit: "0123456789abcdef", Why: "not an ancestor of sprint/base"}}
	p, _ = sprint.TickDeadlines(s, sprint.TickReq{Drift: &f})
	assert.ElementsMatch(t, []string{sprint.NDriftAhead, sprint.NDriftCardBase, sprint.NDriftServer}, types(p), "40 commits is past the default of %d", sprint.DriftCommitsDefault)

	res, err := r.st.Run(r.ctx, store.SetStep(sprint.SetReq{DriftCommits: "0", Who: "coordinator"}))
	require.NoError(t, err)
	require.Len(t, res.Refused, 1)
	assert.Contains(t, res.Refused[0].Why, "--drift-commits wants a whole number from 1")
	r.must(store.SetStep(sprint.SetReq{DriftCommits: "50", DriftHours: "default", Who: "coordinator"}))
	s = r.snap()
	assert.Equal(t, 50, s.DriftCommits())
	assert.Equal(t, sprint.DriftHoursDefault, s.DriftHours())
	p, _ = sprint.TickDeadlines(s, sprint.TickReq{Drift: &f})
	assert.ElementsMatch(t, []string{sprint.NDriftCardBase, sprint.NDriftServer}, types(p), "40 commits an hour old is under 50 and 2 hours")
}
