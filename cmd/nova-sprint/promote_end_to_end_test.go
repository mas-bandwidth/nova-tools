package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// fakeForge is the forge a promotion talks to, in memory: the pull request it
// opens, the checks it reports (pending on the first look, then the given
// buckets), the merge queue, and the merge. merged, when set, is what the queue
// does once the pull request is in it: the merge commit's sha, "" to stay queued.
type fakeForge struct {
	mu        sync.Mutex
	calls     []string
	head      string
	base      string
	looks     int
	checks    []promoteCheck
	entry     string
	queueRun  *promoteRun
	queueLog  string
	merged    func(head string) string
	mergedSHA string
}

func (f *fakeForge) call(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeForge) OpenPR(_ context.Context, base, head, _, _ string) (string, error) {
	f.call("open " + base + " " + head)
	f.head, f.base = head, base
	return "5351", nil
}

func (f *fakeForge) PR(_ context.Context, number string) (prJSON, error) {
	f.call("view " + number)
	v := prJSON{ID: "PR_5351", State: "OPEN"}
	if f.mergedSHA != "" {
		v.State = "MERGED"
		v.Merge = &struct {
			OID string `json:"oid"`
		}{OID: f.mergedSHA}
	}
	return v, nil
}

func (f *fakeForge) Checks(_ context.Context, number string) ([]promoteCheck, error) {
	f.call("checks " + number)
	f.looks++
	if f.looks == 1 {
		return []promoteCheck{{Name: "functional", Bucket: "pending"}}, nil
	}
	return f.checks, nil
}

func (f *fakeForge) QueueEntry(_ context.Context, id string) (string, error) {
	f.call("entry " + id)
	return f.entry, nil
}

func (f *fakeForge) Enqueue(_ context.Context, id string) (string, error) {
	f.call("enqueue " + id)
	f.entry = "MQE_1"
	if f.merged != nil {
		f.mergedSHA = f.merged(f.head)
	}
	return f.entry, nil
}

func (f *fakeForge) QueueRuns(_ context.Context, base, number string) ([]promoteRun, error) {
	f.call("runs " + base + " " + number)
	if f.queueRun != nil && f.entry != "" {
		return []promoteRun{*f.queueRun}, nil
	}
	return nil, nil
}

func (f *fakeForge) RunLog(_ context.Context, id int) (string, error) {
	f.call("log")
	return f.queueLog, nil
}

func (f *fakeForge) did(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// promoteTwin is the twin repository: a bare origin with dev and the sprint
// branch, and the clone promote runs in, whose local sprint ref is left stale
// (the coordinator's 2026-10-05 promotion cut from it).
type promoteTwin struct {
	t               *testing.T
	env             []string
	bare, dir, peer string
}

func newPromoteTwin(t *testing.T) *promoteTwin {
	root := t.TempDir()
	w := &promoteTwin{t: t, bare: filepath.Join(root, "origin.git"), dir: filepath.Join(root, "work"), peer: filepath.Join(root, "peer")}
	w.env = promoteTwinEnv()
	for _, d := range []string{w.bare, w.dir} {
		require.NoError(t, os.Mkdir(d, 0o755))
	}
	w.git(w.bare, "init", "-q", "--bare", "-b", "dev")
	w.git(w.dir, "init", "-q", "-b", "dev")
	w.git(w.dir, "remote", "add", "origin", w.bare)
	w.commit(w.dir, "README", "base\n", "base")
	w.git(w.dir, "push", "-q", "origin", "HEAD:refs/heads/dev")
	w.git(w.dir, "checkout", "-q", "-b", "sprint/live")
	w.commit(w.dir, "a", "a\n", "land card-a (sprint stream s1)")
	w.git(w.dir, "push", "-q", "origin", "HEAD:refs/heads/sprint/live")
	// the peer lands more on origin; the clone's local sprint ref stays behind
	w.git(root, "clone", "-q", "-b", "sprint/live", w.bare, w.peer)
	w.commit(w.peer, "b", "b\n", "land card-b (sprint stream s1)")
	w.git(w.peer, "push", "-q", "origin", "HEAD:refs/heads/sprint/live")
	return w
}

// promoteTwinEnv is the twin's git environment: the shared test identity, and
// no global or system config.
func promoteTwinEnv() []string {
	return testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
}

func (w *promoteTwin) git(where string, args ...string) string {
	w.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: true}, args...)
	require.NoError(w.t, err, "git %v\n%s\n%s", args, res.Stderr, res.Stdout)
	return strings.TrimSpace(string(res.Stdout))
}

func (w *promoteTwin) commit(where, file, text, msg string) {
	w.t.Helper()
	require.NoError(w.t, os.WriteFile(filepath.Join(where, file), []byte(text), 0o644))
	w.git(where, "add", file)
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: true}, "commit", "-q", "-m", msg)
	require.NoError(w.t, err, "git commit\n%s\n%s", res.Stderr, res.Stdout)
}

// onDev lands commits on origin's dev from the peer, as the other promotions do.
func (w *promoteTwin) onDev(files map[string]string) {
	w.t.Helper()
	w.git(w.peer, "fetch", "-q", "origin", "dev")
	w.git(w.peer, "checkout", "-q", "-B", "dev", "origin/dev")
	for f, text := range files {
		w.commit(w.peer, f, text, "dev "+f)
	}
	w.git(w.peer, "push", "-q", "origin", "HEAD:refs/heads/dev")
	w.git(w.peer, "checkout", "-q", "sprint/live")
}

func (w *promoteTwin) remote(ref string) string {
	w.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: w.bare, Env: w.env, OwnRepo: true}, "rev-parse", "--verify", "--quiet", "--end-of-options", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

// useForge makes f the forge of every promote run in the twin's clone.
func useForge(t *testing.T, w *promoteTwin, f promoteForge) {
	promoteForges.Store(w.dir, f)
	t.Cleanup(func() { promoteForges.Delete(w.dir) })
}

// promotedSha is the merge sha the store records for the last promotion, ""
// when none.
func (ta *testApp) promotedSha() string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	s, err := st.Load(context.Background(), store.All, nil)
	require.NoError(ta.t, err)
	_, sha, _ := sprint.Promotion(s)
	return sha
}

// promoteApp is a test app whose promote talks to the fake forge and whose
// clock between looks is immediate.
func promoteApp(t *testing.T, w *promoteTwin) *testApp {
	ta := newTestApp(t)
	ta.a.gitEnv = w.env
	ta.a.after = func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- t0
		return c
	}
	ta.ok("init --readers reader-a,reader-b --members m1")
	return ta
}

// TestPromoteCarriesACutToARecordedPromotion is the hand sequence of
// 2026-10-05 (promotion 5, pull request 5351) as one verb over a twin
// repository and a fake forge: the cut is taken from origin's sprint branch,
// never the clone's stale local ref; origin/dev is merged into it; the gate
// runs, the pull request opens, its checks are waited on, it is queued, and
// when the queue merges it the promotion is recorded in the store. A
// conflicting dev stops with one judgment naming the files, and nothing is
// pushed; a failed queue run raises one judgment naming the check; a cut not
// ahead of dev is refused.
func TestPromoteCarriesACutToARecordedPromotion(t *testing.T) {
	t.Parallel()
	const args = "promote --once --branch sprint/live --poll 1m --repo-dir "

	t.Run("clean", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		w.onDev(map[string]string{"devonly": "dev\n"})
		f := &fakeForge{checks: []promoteCheck{{Name: "functional", Bucket: "pass"}}}
		f.merged = func(head string) string { return w.remote("refs/heads/" + head) }
		useForge(t, w, f)
		ta := promoteApp(t, w)
		stale := w.git(w.dir, "rev-parse", "refs/heads/sprint/live")
		tip := w.remote("refs/heads/sprint/live")
		dev := w.remote("refs/heads/dev")
		require.NotEqual(t, stale, tip, "the twin's local sprint ref is stale")

		code, out, errs := ta.do(args + w.dir)
		require.Zero(t, code, "%s\n%s", out, errs)
		require.Equal(t, "promo/2030-01-02-1", f.head, "the pull request head is the frozen branch: %s", out)
		cut := w.remote("refs/heads/" + f.head)
		require.NotEmpty(t, cut, "the cut was pushed")
		parents := strings.Fields(w.git(w.bare, "rev-list", "--parents", "-n", "1", cut))
		require.Equal(t, []string{cut, tip, dev}, parents, "the cut merges origin/dev into origin's sprint tip, never the stale local ref")
		require.Equal(t, "dev", f.base)
		require.True(t, f.did("checks"), "the checks were waited on")
		require.True(t, f.did("enqueue"), "the pull request was queued")
		for _, step := range []string{"PROMOTE FETCH", "PROMOTE MERGE", "PROMOTE CUT", "PROMOTE WAIT", "checks pending: functional", "PROMOTE QUEUE", "PROMOTE RECORDED sha=" + cut} {
			require.Contains(t, out, step, "each step is printed as it goes")
		}
		require.Equal(t, cut, ta.promotedSha(), "the promotion is recorded in the store")
		ta.clean()
	})

	t.Run("conflict", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		w.onDev(map[string]string{"a": "dev a\n", "b": "dev b\n"})
		f := &fakeForge{}
		useForge(t, w, f)
		ta := promoteApp(t, w)
		code, out, errs := ta.do(args + w.dir)
		require.Equal(t, 1, code, "%s\n%s", out, errs)
		require.Equal(t, 1, strings.Count(out, "JUDGMENT"), "one judgment: %s", out)
		require.Contains(t, out, "JUDGMENT promote conflict")
		require.Contains(t, out, "files=a,b", "the judgment names the conflicted files")
		require.Empty(t, f.calls, "nothing reaches the forge")
		require.Empty(t, w.git(w.bare, "branch", "--list", "promo/*"), "nothing is pushed")
		require.Empty(t, w.git(w.dir, "branch", "--list", "promo/*"), "nothing is cut")
		require.Empty(t, ta.promotedSha(), "a conflict records nothing")
		ta.clean()
	})

	t.Run("queue fails", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		f := &fakeForge{
			checks:   []promoteCheck{{Name: "functional", Bucket: "pass"}},
			queueRun: &promoteRun{ID: 9, Name: "functional", Status: "completed", Conclusion: "failure"},
			queueLog: "--- FAIL: TestTree (0.02s)\n",
		}
		useForge(t, w, f)
		ta := promoteApp(t, w)
		code, out, errs := ta.do(args + w.dir)
		require.Equal(t, 1, code, "%s\n%s", out, errs)
		require.Equal(t, 1, strings.Count(out, "JUDGMENT"), "one judgment: %s", out)
		require.Contains(t, out, "JUDGMENT merge-group failed")
		require.Contains(t, out, "check=functional", "the judgment names the failing check")
		require.Contains(t, out, "FAIL: TestTree")
		require.Empty(t, ta.promotedSha(), "a failed queue records nothing")
		ta.clean()
	})

	t.Run("not ahead", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		// dev already holds the sprint tip: the 2026-10-05 cut that was 0 ahead
		w.git(w.peer, "push", "-q", "origin", "HEAD:refs/heads/dev")
		f := &fakeForge{}
		useForge(t, w, f)
		ta := promoteApp(t, w)
		code, out, errs := ta.do(args + w.dir)
		require.Equal(t, 1, code, "%s\n%s", out, errs)
		require.Contains(t, errs, "not ahead of origin/dev")
		require.Empty(t, f.calls, "nothing reaches the forge")
		require.Empty(t, w.git(w.bare, "branch", "--list", "promo/*"), "nothing is pushed")
		ta.clean()
	})
}
