package sprint_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// memAdoptStore is the in-memory sprint.AdoptStore.
type memAdoptStore struct{ R sprint.AdoptRecord }

func (m *memAdoptStore) Load(context.Context) (sprint.AdoptRecord, error) { return m.R, nil }
func (m *memAdoptStore) Save(_ context.Context, r sprint.AdoptRecord) error {
	m.R = r
	return nil
}

// fakeAdopt is every stage of an adoption, faked: the base tip and the live
// build are fields, every call is logged in order, and a stage fails when its
// name is in fail.
type fakeAdopt struct {
	tip, live string
	read      sprint.AdoptRead
	machines  []string
	versions  map[string]string // what each machine reads back
	lastTick  time.Time
	fail      map[string]error
	calls     []string
	asked     []sprint.AdoptJudgment
	server    string // the build the server binary is
}

func newFakeAdopt() *fakeAdopt {
	return &fakeAdopt{
		tip: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", live: "bbbbbbbbbbbb",
		machines: []string{"m1", "m2", "m3-unfunded"},
		versions: map[string]string{}, fail: map[string]error{}, server: "live",
	}
}

func (f *fakeAdopt) call(name string) error {
	f.calls = append(f.calls, name)
	return f.fail[name]
}

func (f *fakeAdopt) BaseTip(context.Context) (string, error)   { return f.tip, nil }
func (f *fakeAdopt) LiveBuild(context.Context) (string, error) { return f.live, nil }
func (f *fakeAdopt) Build(_ context.Context, tip string) (sprint.AdoptBuild, error) {
	return sprint.AdoptBuild{Tip: tip, Version: "v-" + tip[:7], Dir: "/bench/out/" + tip[:7]}, f.call("build")
}
func (f *fakeAdopt) Canary(context.Context, sprint.AdoptBuild) (string, error) {
	return "version ok", f.call("canary")
}
func (f *fakeAdopt) Shadow(context.Context, sprint.AdoptBuild) (string, error) {
	return "parts=4 took=20ms", f.call("shadow")
}
func (f *fakeAdopt) DealColdRead(_ context.Context, b sprint.AdoptBuild) (string, error) {
	return "adopt-read-" + b.Tip[:7], f.call("deal")
}
func (f *fakeAdopt) ColdRead(context.Context, string) (sprint.AdoptRead, error) {
	return f.read, f.call("read")
}
func (f *fakeAdopt) Ask(_ context.Context, j sprint.AdoptJudgment) error {
	f.asked = append(f.asked, j)
	return f.call("ask")
}
func (f *fakeAdopt) KeepRollback(context.Context) ([]string, error) {
	return []string{"/srv/nova-sprint.adopt-prev"}, f.call("keep")
}
func (f *fakeAdopt) Switch(_ context.Context, b sprint.AdoptBuild) error {
	if err := f.call("switch"); err != nil {
		return err
	}
	f.server = b.Version
	return nil
}
func (f *fakeAdopt) Machines(context.Context) ([]string, error) {
	return f.machines, f.call("machines")
}
func (f *fakeAdopt) Push(_ context.Context, m string, b sprint.AdoptBuild) error {
	if err := f.call("push " + m); err != nil {
		return err
	}
	f.versions[m] = b.Version
	return nil
}
func (f *fakeAdopt) Version(_ context.Context, m string) (string, error) {
	return f.versions[m], f.call("version " + m)
}
func (f *fakeAdopt) LastTick(context.Context) (time.Time, error) { return f.lastTick, f.call("tick") }
func (f *fakeAdopt) Rollback(context.Context, []string) error {
	if err := f.call("rollback"); err != nil {
		return err
	}
	f.server = "live"
	return nil
}

// adoptRig is a pipeline on fakes and the in-memory record, its clock a
// field the test moves: no test here sleeps on the wall clock.
type adoptRig struct {
	t   *testing.T
	ctx context.Context
	f   *fakeAdopt
	st  *memAdoptStore
	now time.Time
	a   *sprint.Adoption
}

func newAdoptRig(t *testing.T) *adoptRig {
	r := &adoptRig{t: t, ctx: context.Background(), f: newFakeAdopt(), st: &memAdoptStore{}, now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	r.a = &sprint.Adoption{Steps: r.f, Store: r.st, Now: func() time.Time { return r.now }, TickEvery: time.Minute, Missed: 3, Watch: 5}
	return r
}

func (r *adoptRig) pass() sprint.AdoptPass {
	r.t.Helper()
	p, err := r.a.Pass(r.ctx)
	require.NoError(r.t, err)
	return p
}

// toJudgment runs the pipeline to its one judgment: started, built, checked,
// the cold read dealt and still out on the first pass, done on the second.
func (r *adoptRig) toJudgment() sprint.AdoptJudgment {
	r.t.Helper()
	p := r.pass()
	require.Equal(r.t, sprint.AdoptReading, p.Stage, p.Lines)
	assert.Contains(r.t, strings.Join(p.Lines, "\n"), "ADOPT START tip=aaaaaaaaaaaa live=bbbbbbbbbbbb")
	assert.Contains(r.t, strings.Join(p.Lines, "\n"), "ADOPT WAIT")
	assert.Empty(r.t, r.f.asked, "no judgment while the cold read is out")
	r.f.read = sprint.AdoptRead{Done: true, OK: true, Finding: "the build reads clean"}
	p = r.pass()
	require.Equal(r.t, sprint.AdoptAsked, p.Stage, p.Lines)
	require.NotNil(r.t, p.Judgment)
	require.Len(r.t, r.f.asked, 1)
	return r.f.asked[0]
}

func TestAdoptionRunsWhenTheBaseMovesAndAsksOneJudgment(t *testing.T) {
	t.Parallel()
	t.Run("a live build at the base tip starts nothing", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.f.live = r.f.tip[:12]
		p := r.pass()
		assert.Equal(t, sprint.AdoptIdle, p.Stage)
		assert.Equal(t, []string{"ADOPT CURRENT live=aaaaaaaaaaaa base=aaaaaaaaaaaa"}, p.Lines)
		assert.Empty(t, r.f.calls, "no stage runs when the base has not moved")
	})

	t.Run("a base ahead of the live build starts the pipeline and asks one judgment with the evidence", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		j := r.toJudgment()
		assert.Equal(t, []string{"build", "canary", "shadow", "deal", "read", "read", "ask"}, r.f.calls)
		assert.Equal(t, "aaaaaaaaaaaa", j.ID)
		assert.Equal(t, "version ok", j.Canary)
		assert.Equal(t, "parts=4 took=20ms", j.Shadow)
		assert.Equal(t, "adopt-read-aaaaaaa", j.Card)
		assert.True(t, j.Read.OK)
		assert.Contains(t, j.Question, "nova-sprint adopt --answer yes|no --judgment aaaaaaaaaaaa")
		// unanswered, a pass asks nothing again and switches nothing
		for range 3 {
			p := r.pass()
			assert.Equal(t, sprint.AdoptAsked, p.Stage)
		}
		assert.Len(t, r.f.asked, 1, "one judgment, however many passes")
		assert.Equal(t, "live", r.f.server)
	})

	t.Run("a yes keeps the copies, switches, pushes to every machine row and reads each back", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.toJudgment()
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "yes", "canary, shadow and read green")
		require.NoError(t, err)
		r.f.calls = nil
		p := r.pass()
		require.Equal(t, sprint.AdoptWatching, p.Stage, p.Lines)
		assert.Equal(t, []string{"keep", "switch", "machines",
			"push m1", "version m1", "push m2", "version m2", "push m3-unfunded", "version m3-unfunded"}, r.f.calls)
		assert.Equal(t, "v-aaaaaaa", r.f.server)
		for _, m := range r.f.machines {
			assert.Equal(t, "v-aaaaaaa", r.f.versions[m], m)
		}
		assert.Contains(t, p.Lines, "ADOPT FLEET version=v-aaaaaaa machines=3 read_back=3")
		assert.Equal(t, []string{"/srv/nova-sprint.adopt-prev"}, r.st.R.Kept)

		// the server ticks through the watch: adopted, never rolled back
		for range 5 {
			r.now = r.now.Add(time.Minute)
			r.f.lastTick = r.now
			p = r.pass()
		}
		assert.Equal(t, sprint.AdoptAdopted, p.Stage, p.Lines)
		assert.NotContains(t, r.f.calls, "rollback")
		r.f.live = r.f.tip
		assert.Equal(t, sprint.AdoptAdopted, r.pass().Stage, "the adopted record stays while the live build is the tip")
	})

	t.Run("missed ticks after the switch roll back by themselves", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.toJudgment()
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "yes", "")
		require.NoError(t, err)
		r.pass()
		r.now = r.now.Add(time.Minute)
		r.f.lastTick = r.now
		assert.Equal(t, sprint.AdoptWatching, r.pass().Stage)
		r.now = r.now.Add(2 * time.Minute)
		assert.Equal(t, sprint.AdoptWatching, r.pass().Stage, "two missed is not three")
		r.now = r.now.Add(time.Minute)
		p := r.pass()
		assert.Equal(t, sprint.AdoptRolledBack, p.Stage, p.Lines)
		assert.Equal(t, "live", r.f.server)
		assert.Contains(t, strings.Join(p.Lines, "\n"), "ADOPT ROLLED BACK tip=aaaaaaaaaaaa: no tick for 3m0s")
		// the same tip is never switched again by itself; a pass names why
		r.f.calls = nil
		p = r.pass()
		assert.Equal(t, sprint.AdoptRolledBack, p.Stage)
		assert.Empty(t, r.f.calls)
		assert.Contains(t, p.Lines[0], "ADOPT ROLLED-BACK tip=aaaaaaaaaaaa")
	})

	t.Run("a partial switch rolls the kept copies back before it blocks", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.toJudgment()
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "yes", "")
		require.NoError(t, err)
		r.f.fail["switch"] = errors.New("friend daemon did not replace")
		r.f.calls = nil
		p := r.pass()
		assert.Equal(t, sprint.AdoptBlocked, p.Stage, p.Lines)
		assert.Equal(t, []string{"keep", "switch", "rollback"}, r.f.calls)
		assert.Equal(t, "live", r.f.server, "a failed switch leaves the prior server build live")
		assert.Contains(t, strings.Join(p.Lines, "\n"), "ADOPT BLOCKED tip=aaaaaaaaaaaa at switch: friend daemon did not replace")
	})

	t.Run("a no keeps the live build", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.toJudgment()
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "no", "the read found a refusal")
		require.NoError(t, err)
		r.f.calls = nil
		p := r.pass()
		assert.Equal(t, sprint.AdoptDeclined, p.Stage)
		assert.Empty(t, r.f.calls, "a no switches, keeps and pushes nothing")
		assert.Equal(t, "live", r.f.server)
		// the next tip starts again
		r.f.tip = "cccccccccccccccccccccccccccccccccccccccc"
		r.f.read = sprint.AdoptRead{}
		p = r.pass()
		assert.Equal(t, sprint.AdoptReading, p.Stage)
		assert.Contains(t, strings.Join(p.Lines, "\n"), "ADOPT START tip=cccccccccccc")
	})

	t.Run("a failed stage blocks and names what blocks it", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.f.fail["shadow"] = errors.New("the shadow tick printed no plan")
		p := r.pass()
		assert.Equal(t, sprint.AdoptBlocked, p.Stage)
		assert.Contains(t, strings.Join(p.Lines, "\n"), "ADOPT BLOCKED tip=aaaaaaaaaaaa at shadow: the shadow tick printed no plan")
		assert.NotContains(t, r.f.calls, "deal")
		assert.Empty(t, r.f.asked)
		p = r.pass()
		assert.Contains(t, p.Lines[0], "ADOPT BLOCKED tip=aaaaaaaaaaaa live=bbbbbbbbbbbb: shadow: the shadow tick printed no plan")
	})

	t.Run("a machine the push missed is named and pushed again while watched", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.f.fail["push m1"] = errors.New("ssh: connect timed out")
		r.toJudgment()
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "yes", "")
		require.NoError(t, err)
		p := r.pass()
		assert.Contains(t, p.Lines, "ADOPT FLEET MISSED machine=m1: push: ssh: connect timed out")
		assert.Contains(t, p.Lines, "ADOPT FLEET version=v-aaaaaaa machines=3 read_back=2")
		delete(r.f.fail, "push m1")
		r.f.calls = nil
		r.now = r.now.Add(time.Minute)
		r.f.lastTick = r.now
		p = r.pass()
		assert.Equal(t, []string{"tick", "machines", "push m1", "version m1"}, r.f.calls)
		assert.Contains(t, p.Lines, "ADOPT FLEET version=v-aaaaaaa machines=3 read_back=3")
	})

	t.Run("a machine still missing at adoption is pushed after it, and across the next tip, until it reads the build", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.f.fail["push m1"] = errors.New("ssh: connect timed out")
		r.toJudgment()
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "yes", "")
		require.NoError(t, err)
		r.pass()
		for range 5 {
			r.now = r.now.Add(time.Minute)
			r.f.lastTick = r.now
			r.pass()
		}
		require.Equal(t, sprint.AdoptAdopted, r.st.R.Stage)
		// adopted, the live build is the tip, and the missed row is still pushed
		r.f.live = r.f.tip
		r.f.calls = nil
		p := r.pass()
		assert.Equal(t, []string{"machines", "push m1"}, r.f.calls, p.Lines)
		assert.Contains(t, p.Lines, "ADOPT FLEET MISSED machine=m1: push: ssh: connect timed out")
		// the base moves on and the new tip blocks: the debt is carried, not dropped
		r.f.tip = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		r.f.fail["build"] = errors.New("the bench is down")
		p = r.pass()
		assert.Equal(t, sprint.AdoptBlocked, p.Stage, p.Lines)
		require.NotNil(t, r.st.R.Owed)
		assert.Equal(t, "v-aaaaaaa", r.st.R.Owed.Build.Version)
		delete(r.f.fail, "push m1")
		r.f.calls = nil
		p = r.pass()
		assert.Equal(t, []string{"machines", "push m1", "version m1"}, r.f.calls, p.Lines)
		assert.Equal(t, "v-aaaaaaa", r.f.versions["m1"])
		assert.Nil(t, r.st.R.Owed, "a debt every row has read back is dropped")
		r.f.calls = nil
		r.pass()
		assert.Empty(t, r.f.calls, "nothing owed, nothing pushed")
	})

	t.Run("a rolled-back build is not owed to the fleet", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.f.fail["push m1"] = errors.New("ssh: connect timed out")
		r.toJudgment()
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "yes", "")
		require.NoError(t, err)
		r.pass()
		r.now = r.now.Add(3 * time.Minute)
		require.Equal(t, sprint.AdoptRolledBack, r.pass().Stage)
		r.f.tip = "ffffffffffffffffffffffffffffffffffffffff"
		r.f.calls = nil
		r.pass()
		assert.Nil(t, r.st.R.Owed)
		assert.NotContains(t, r.f.calls, "push m1")
	})

	t.Run("the base moving before the switch restarts on the new tip; an answer to the old one is refused", func(t *testing.T) {
		t.Parallel()
		r := newAdoptRig(t)
		r.toJudgment()
		r.f.tip = "dddddddddddddddddddddddddddddddddddddddd"
		r.f.read = sprint.AdoptRead{}
		p := r.pass()
		assert.Contains(t, strings.Join(p.Lines, "\n"), "ADOPT RESTARTED tip=aaaaaaaaaaaa stage=asked")
		_, err := sprint.AnswerAdoption(r.ctx, r.st, "aaaaaaaaaaaa", "yes", "")
		require.Error(t, err)
		assert.Equal(t, "live", r.f.server)
	})

	t.Run("the record survives a pass on a file", func(t *testing.T) {
		t.Parallel()
		st := sprint.FileAdoptStore{Path: filepath.Join(t.TempDir(), "adopt.json")}
		r := newAdoptRig(t)
		r.a.Store = st
		r.f.read = sprint.AdoptRead{Done: true, OK: true}
		assert.Equal(t, sprint.AdoptAsked, r.pass().Stage)
		got, err := st.Load(r.ctx)
		require.NoError(t, err)
		assert.Equal(t, sprint.AdoptAsked, got.Stage)
		require.NotNil(t, got.Judgment)
		_, err = sprint.AnswerAdoption(r.ctx, st, "aaaaaaaaaaaa", "maybe", "")
		require.Error(t, err)
	})
}
