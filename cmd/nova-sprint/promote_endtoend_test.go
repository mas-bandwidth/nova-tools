package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// fakeForge is the forge the verb talks to in a test: it records the calls and
// answers from fields, so a promotion runs end to end with no network. Merging is
// the test's to say: merged is the merge commit View reports.
type fakeForge struct {
	calls   []string
	checks  prChecks
	failed  fakeRun
	queued  bool
	merged  string
	headRef string
}

func (f *fakeForge) OpenPR(_ context.Context, base, head, _, _ string) (string, error) {
	f.calls = append(f.calls, "open "+base+" "+head)
	f.headRef = head
	return "9", nil
}

func (f *fakeForge) View(context.Context, string) (prView, error) {
	f.calls = append(f.calls, "view")
	return prView{ID: "PR_9", Merged: f.merged}, nil
}

func (f *fakeForge) Checks(context.Context, string) (prChecks, error) {
	f.calls = append(f.calls, "checks")
	if f.checks.State == "" {
		return prChecks{State: checksPass}, nil
	}
	return f.checks, nil
}

func (f *fakeForge) Enqueue(context.Context, string) (string, error) {
	f.calls = append(f.calls, "enqueue")
	f.queued = true
	return "MQE_9", nil
}

func (f *fakeForge) Confirm(context.Context, string) (string, error) {
	f.calls = append(f.calls, "confirm")
	if !f.queued {
		return "", nil
	}
	return "MQE_9", nil
}

// fakeRun is a failed run the fake reports: its check name and log.
type fakeRun struct {
	Name, Log string
}

func (f *fakeForge) FailedRuns(context.Context, ...string) ([]ghRunRow, error) {
	f.calls = append(f.calls, "runs")
	if f.failed.Name == "" {
		return nil, nil
	}
	return []ghRunRow{{DatabaseID: 1, Conclusion: "failure", Status: "completed", Name: f.failed.Name}}, nil
}

func (f *fakeForge) RunLog(context.Context, int) (string, error) {
	f.calls = append(f.calls, "log")
	return f.failed.Log, nil
}

func (f *fakeForge) Repo(context.Context) (string, error) {
	return "mas-bandwidth/nova-tools", nil
}

func (f *fakeForge) did(call string) bool {
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

// promoTwin is a bare origin with dev and a sprint branch, and a clone of it.
type promoTwin struct {
	t         *testing.T
	root, dir string
	bare      string
	env       []string
}

func newPromoTwin(t *testing.T) *promoTwin {
	t.Helper()
	w := &promoTwin{t: t, root: t.TempDir()}
	w.dir = filepath.Join(w.root, "work")
	w.bare = filepath.Join(w.root, "origin.git")
	require.NoError(t, os.Mkdir(w.dir, 0o755))
	require.NoError(t, os.Mkdir(w.bare, 0o755))
	w.env = testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	w.promoGit(w.bare, "init", "-q", "--bare", "-b", "dev")
	w.promoGit(w.dir, "init", "-q", "-b", "dev")
	w.promoGit(w.dir, "remote", "add", "origin", w.bare)
	w.write("tests.txt", "base\n")
	w.commit("base")
	w.promoGit(w.dir, "push", "-q", "origin", "dev")
	w.promoGit(w.dir, "checkout", "-q", "-b", "sprint/live")
	return w
}

func (w *promoTwin) promoGit(where string, args ...string) string {
	w.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: testgit.Environ(w.env[len(w.env)-2:]...), OwnRepo: true}, args...)
	require.NoError(w.t, err, "git %v\n%s\n%s", args, res.Stderr, res.Stdout)
	return strings.TrimSpace(string(res.Stdout))
}

func (w *promoTwin) write(name, text string) {
	w.t.Helper()
	require.NoError(w.t, os.WriteFile(filepath.Join(w.dir, name), []byte(text), 0o644))
}

func (w *promoTwin) commit(msg string) {
	w.t.Helper()
	w.promoGit(w.dir, "add", "-A")
	w.promoGit(w.dir, "commit", "-q", "-m", msg)
}

// land commits on the sprint branch as a land does and pushes it.
func (w *promoTwin) land(file, text string) {
	w.t.Helper()
	w.promoGit(w.dir, "checkout", "-q", "sprint/live")
	w.write(file, text)
	w.commit("land " + strings.TrimSuffix(file, ".txt") + " (sprint stream s1)")
	w.promoGit(w.dir, "push", "-q", "origin", "sprint/live")
}

// advanceDev moves origin's dev with a commit made from a second clone, the way a
// merge from another stream does, leaving the first clone's local dev stale.
func (w *promoTwin) advanceDev(file, text string) {
	w.t.Helper()
	other := filepath.Join(w.root, "other")
	if _, err := os.Stat(other); err != nil {
		w.promoGit(w.root, "clone", "-q", w.bare, other)
	} else {
		w.promoGit(other, "pull", "-q", "--ff-only", "origin", "dev")
	}
	require.NoError(w.t, os.WriteFile(filepath.Join(other, file), []byte(text), 0o644))
	w.promoGit(other, "add", "-A")
	w.promoGit(other, "commit", "-q", "-m", "dev moves")
	w.promoGit(other, "push", "-q", "origin", "dev")
}

func (w *promoTwin) promoter(f *fakeForge) *promoter {
	return &promoter{
		dir: w.dir, live: "sprint/live", base: "dev", env: w.env,
		now:   time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC),
		forge: f,
	}
}

// TestPromoteCarriesACutToARecordedPromotion is promote from cut to recorded on a
// twin repository and a fake forge: a clean cut is merged with the target, opened,
// queued once its checks pass, and recorded when the queue merges it; a target that
// conflicts stops with one judgment naming the files; a failed queue run raises one
// judgment naming the failing check.
func TestPromoteCarriesACutToARecordedPromotion(t *testing.T) {
	t.Parallel()

	t.Run("a clean cut is merged with the target, queued and recorded", func(t *testing.T) {
		t.Parallel()
		w := newPromoTwin(t)
		w.land("card-a.txt", "a\n")
		w.advanceDev("other.txt", "dev\n")
		// the local dev is stale on purpose: the cut is from origin
		f := &fakeForge{checks: prChecks{State: checksPending, Name: "ci"}}
		p := w.promoter(f)
		// the sprint's store, as the verb opens it: no --actor, so the step acts
		// as the coordinator seat
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		p.recorder = appRecorder{a: ta.a, c: common{verb: "promote", redis: "mem:0", epoch: -1}}
		var out bytes.Buffer

		o, code := p.step(context.Background(), &out, &out)
		require.Zero(t, code, out.String())
		require.False(t, f.did("enqueue"), "not queued while checks are pending")
		require.Contains(t, out.String(), "checks=pending")

		f.checks = prChecks{State: checksPass}
		o, code = p.step(context.Background(), &out, &out)
		require.Zero(t, code, out.String())
		require.True(t, f.did("enqueue"), "queued once the checks pass")
		require.Equal(t, "MQE_9", o.Entry)
		require.Equal(t, "promo/2026-10-05-1", f.headRef)

		cut := w.promoGit(w.dir, "rev-parse", "promo/2026-10-05-1")
		require.Len(t, strings.Fields(w.promoGit(w.dir, "rev-list", "--parents", "-n1", cut)), 3, "the cut is a merge of two parents")
		require.NoError(t, errors.Join(
			gitErr(w, "merge-base", "--is-ancestor", "origin/dev", cut),
			gitErr(w, "merge-base", "--is-ancestor", "origin/sprint/live", cut),
		), "the cut holds the target and the sprint tip")
		require.Equal(t, cut, w.promoGit(w.bare, "rev-parse", "refs/heads/promo/2026-10-05-1"), "the cut is what was pushed")
		require.FileExists(t, filepath.Join(w.dir, "card-a.txt"), "the live checkout is untouched")

		// the queue merges it: the next pass records the merge commit
		w.advanceDev("merged.txt", "the queue's merge commit\n")
		f.merged = w.promoGit(w.bare, "rev-parse", "refs/heads/dev")
		o, code = p.step(context.Background(), &out, &out)
		require.Zero(t, code, out.String())
		require.Equal(t, f.merged, o.Promoted)
		require.Contains(t, out.String(), "promoted --sha "+f.merged)
		require.Equal(t, f.merged, w.promoGit(w.dir, "rev-parse", "refs/promoted/last"))
		require.Equal(t, f.merged, storedPromotion(t, ta), "the store holds the promotion: no hand step")
		require.Contains(t, out.String(), "PROMOTE RECORD sha="+f.merged+" recorded: promoted")

		// a pass that died after the store write and before the clone's record:
		// the next pass finds the pull request pending, and the store records once
		w.promoGit(w.dir, "update-ref", "-d", "refs/promoted/last")
		w.promoGit(w.dir, "config", "--local", "promote.branch", "promo/2026-10-05-1")
		w.promoGit(w.dir, "config", "--local", "promote.pr", "9")
		applies := ta.applies()
		out.Reset()
		o, code = p.step(context.Background(), &out, &out)
		require.Zero(t, code, out.String())
		require.Equal(t, f.merged, o.Promoted)
		require.Contains(t, out.String(), "recorded already")
		require.Equal(t, applies, ta.applies(), "the store was not written twice")
		require.Equal(t, f.merged, w.promoGit(w.dir, "rev-parse", "refs/promoted/last"))
		ta.clean()
	})

	t.Run("a store that refuses the record leaves the promotion pending", func(t *testing.T) {
		t.Parallel()
		w := newPromoTwin(t)
		w.land("card-a.txt", "a\n")
		f := &fakeForge{}
		p := w.promoter(f)
		// a store with no coordinator seat: there is no one whose word it is
		ta := newTestApp(t)
		p.recorder = appRecorder{a: ta.a, c: common{verb: "promote", redis: "mem:0", epoch: -1}}
		var out, errb bytes.Buffer
		_, code := p.step(context.Background(), &out, &errb)
		require.Zero(t, code, errb.String())
		require.True(t, f.did("enqueue"))

		f.merged = w.promoGit(w.dir, "rev-parse", "origin/sprint/live")
		o, code := p.step(context.Background(), &out, &errb)
		require.Equal(t, 1, code)
		require.Empty(t, o.Promoted)
		require.Contains(t, errb.String(), "not recorded in the sprint's store")
		require.NotContains(t, out.String(), "promoted --sha")
		require.Error(t, gitErr(w, "rev-parse", "--verify", "--quiet", "refs/promoted/last"), "the clone records nothing the store did not")
		require.Equal(t, "promo/2026-10-05-1", w.promoGit(w.dir, "config", "--local", "--get", "promote.branch"), "the next pass looks again")
	})

	t.Run("a conflicting target stops with one judgment naming the files", func(t *testing.T) {
		t.Parallel()
		w := newPromoTwin(t)
		w.land("tests.txt", "sprint side\n")
		w.advanceDev("tests.txt", "dev side\n")
		f := &fakeForge{}
		p := w.promoter(f)
		var out bytes.Buffer

		o, code := p.step(context.Background(), &out, &out)
		require.Equal(t, 1, code)
		require.NotNil(t, o.Judgment)
		require.Equal(t, []string{"tests.txt"}, o.Judgment.Files)
		require.Equal(t, []string{"resolve-and-recut", "skip"}, o.Judgment.Decisions)
		require.Contains(t, out.String(), "files=tests.txt")
		require.Empty(t, f.calls, "nothing reached the forge")
		require.Empty(t, w.promoGit(w.dir, "branch", "--list", "promo/*"), "no branch was cut")
		require.Empty(t, w.promoGit(w.bare, "branch", "--list", "promo/*"), "nothing was pushed")

		again, code := p.step(context.Background(), &out, &out)
		require.Equal(t, 1, code)
		require.Nil(t, again.Judgment, "one judgment, not one a pass")
	})

	t.Run("a failed queue run raises one judgment naming the check", func(t *testing.T) {
		t.Parallel()
		w := newPromoTwin(t)
		w.land("card-a.txt", "a\n")
		f := &fakeForge{failed: fakeRun{Name: "tree-gate", Log: "--- FAIL: TestTree\n"}}
		p := w.promoter(f)
		var out bytes.Buffer

		o, code := p.step(context.Background(), &out, &out)
		require.Equal(t, 1, code)
		require.NotNil(t, o.Judgment)
		require.Contains(t, o.Judgment.What, "tree-gate")
		require.Contains(t, o.Judgment.Tail, "FAIL: TestTree")
		require.Empty(t, o.Promoted)

		again, _ := p.step(context.Background(), &out, &out)
		require.Nil(t, again.Judgment)
	})

	t.Run("a sprint branch not ahead of the target is refused", func(t *testing.T) {
		t.Parallel()
		w := newPromoTwin(t)
		first := w.promoGit(w.dir, "rev-parse", "dev")
		w.land("card-a.txt", "a\n")
		w.promoGit(w.dir, "push", "-q", "origin", "sprint/live:dev") // dev now holds the tip
		w.promoGit(w.dir, "update-ref", "refs/promoted/last", first) // a stale record lists the card again
		f := &fakeForge{}
		var out, errb bytes.Buffer
		_, code := w.promoter(f).step(context.Background(), &out, &errb)
		require.Equal(t, 1, code)
		require.Contains(t, errb.String(), "not ahead")
		// the base's runs at the last promotion are read (devRed); nothing is opened
		require.Equal(t, []string{"runs"}, f.calls)
	})
}

// storedPromotion is the merge sha the sprint's store records as the last promotion.
func storedPromotion(t *testing.T, ta *testApp) string {
	t.Helper()
	st, err := ta.a.store(common{verb: "where", redis: "mem:0", actor: "coordinator", epoch: -1})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Work}, nil)
	require.NoError(t, err)
	_, sha, ok := sprint.Promotion(s)
	require.True(t, ok, "the store records no promotion")
	return sha
}

func gitErr(w *promoTwin, args ...string) error {
	_, err := gitrun.Run(context.Background(), gitrun.Options{C: w.dir, Env: w.env, OwnRepo: true}, args...)
	return err
}
