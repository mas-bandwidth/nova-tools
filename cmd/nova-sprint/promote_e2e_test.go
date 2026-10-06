package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// fakeForge is the promoteForge a test hands the step: it records each call and
// answers from fields the test sets.
type fakeForge struct {
	calls    []string
	opened   struct{ base, head string }
	checks   promoteChecks
	failure  promoteFailure
	mergeSHA string // set, the pull request reads as merged once queued
	queued   bool
}

func (f *fakeForge) Open(_ context.Context, base, head, _, _ string) (string, error) {
	f.calls = append(f.calls, "open")
	f.opened.base, f.opened.head = base, head
	return "9", nil
}

func (f *fakeForge) View(_ context.Context, _ string) (prJSON, error) {
	f.calls = append(f.calls, "view")
	v := prJSON{ID: "PR_1", State: "OPEN"}
	if f.queued && f.mergeSHA != "" && f.failure.Name == "" {
		v.State = "MERGED"
		v.Merge = &struct {
			OID string `json:"oid"`
		}{OID: f.mergeSHA}
	}
	return v, nil
}

func (f *fakeForge) Checks(_ context.Context, _ string) (promoteChecks, error) {
	f.calls = append(f.calls, "checks")
	return f.checks, nil
}

func (f *fakeForge) Enqueue(_ context.Context, _ string) (string, error) {
	f.calls = append(f.calls, "enqueue")
	f.queued = true
	return "MQE_1", nil
}

func (f *fakeForge) Entry(_ context.Context, _ string) (string, error) {
	if !f.queued {
		return "", nil
	}
	return "MQE_1", nil
}

func (f *fakeForge) GroupFailure(_ context.Context, _ string) (promoteFailure, error) {
	f.calls = append(f.calls, "group")
	return f.failure, nil
}

func (f *fakeForge) did(name string) bool {
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

// promoteTwin is a work clone with a bare origin: dev, and a sprint branch that
// forked from it. The clone's local sprint ref is left stale on purpose.
type promoteTwin struct {
	t         *testing.T
	dir, bare string
	env       []string
	extra     []string // the hosted runner's git config, over the shared identity
}

func newPromoteTwin(t *testing.T) *promoteTwin {
	t.Helper()
	root := t.TempDir()
	w := &promoteTwin{t: t, dir: filepath.Join(root, "work"), bare: filepath.Join(root, "origin.git")}
	require.NoError(t, os.Mkdir(w.dir, 0o755))
	require.NoError(t, os.Mkdir(w.bare, 0o755))
	w.env = testgit.Environ(testgit.NoGlobalConfig(t)...)
	w.extra = w.env[len(w.env)-2:]
	w.gitIn(w.bare, "init", "-q", "--bare", "-b", "dev")
	w.gitIn(w.dir, "init", "-q", "-b", "dev")
	w.gitIn(w.dir, "remote", "add", "origin", w.bare)
	require.NoError(t, os.WriteFile(filepath.Join(w.dir, "shared.txt"), []byte("base\n"), 0o644))
	w.gitIn(w.dir, "add", "shared.txt")
	w.gitIn(w.dir, "commit", "-q", "-m", "base")
	w.gitIn(w.dir, "push", "-q", "origin", "dev")
	w.gitIn(w.dir, "checkout", "-q", "-b", "sprint/live")
	w.commit("sprint/live", "a.txt", "a\n", "land card-a (sprint stream s1)")
	w.gitIn(w.dir, "push", "-q", "origin", "sprint/live")
	// the local ref goes stale: origin's sprint branch moves on past it
	w.commit("sprint/live", "b.txt", "b\n", "land card-b (sprint stream s1)")
	w.gitIn(w.dir, "push", "-q", "origin", "sprint/live")
	w.gitIn(w.dir, "reset", "-q", "--hard", "HEAD~1")
	return w
}

func (w *promoteTwin) gitIn(where string, args ...string) string {
	w.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: testgit.Environ(w.extra...), OwnRepo: true}, args...)
	require.NoError(w.t, err, "git %v\n%s\n%s", args, res.Stderr, res.Stdout)
	return strings.TrimSpace(string(res.Stdout))
}

// commit writes a file on a branch of the work clone, in a throwaway checkout of
// that branch, and leaves the clone on sprint/live.
func (w *promoteTwin) commit(branch, file, body, msg string) {
	w.t.Helper()
	w.gitIn(w.dir, "checkout", "-q", branch)
	require.NoError(w.t, os.WriteFile(filepath.Join(w.dir, file), []byte(body), 0o644))
	w.gitIn(w.dir, "add", file)
	w.gitIn(w.dir, "commit", "-q", "-m", msg)
}

// devMoves adds a commit to origin's dev, as a pull request landing there does.
func (w *promoteTwin) devMoves(file, body string) string {
	w.t.Helper()
	other := w.t.TempDir()
	w.gitIn(filepath.Dir(w.dir), "clone", "-q", w.bare, other)
	require.NoError(w.t, os.WriteFile(filepath.Join(other, file), []byte(body), 0o644))
	w.gitIn(other, "add", file)
	w.gitIn(other, "commit", "-q", "-m", "dev moves "+file)
	w.gitIn(other, "push", "-q", "origin", "dev")
	return w.gitIn(other, "rev-parse", "HEAD")
}

func (w *promoteTwin) promoter(f *fakeForge) *promoter {
	return &promoter{
		dir: w.dir, live: "sprint/live", base: "dev", env: w.env,
		now:   time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC),
		forge: f,
	}
}

// TestPromoteCarriesACutToARecordedPromotion is the hand sequence of 2026-10-05
// as one verb: the cut is taken from origin's sprint branch (the local ref is
// stale), origin's dev is merged into it, the pull request is opened, queued once
// its checks pass, and the merge is recorded. A conflicting dev stops with one
// judgment naming the files, and a failed merge-group run raises one judgment.
func TestPromoteCarriesACutToARecordedPromotion(t *testing.T) {
	t.Parallel()
	t.Run("clean", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		w.devMoves("dev-only.txt", "d\n")
		originTip := w.gitIn(w.bare, "rev-parse", "refs/heads/sprint/live")
		f := &fakeForge{checks: promoteChecks{State: promoteChecksPending, Pending: []string{"ci"}}}
		p := w.promoter(f)
		var out bytes.Buffer

		// checks pending: the pull request is open, not queued, and the line says on what
		o, code := p.step(context.Background(), &out, &out)
		require.Zero(t, code, out.String())
		require.True(t, o.Cut)
		require.True(t, o.Waiting)
		require.False(t, f.did("enqueue"), "not queued before its checks pass")
		require.Contains(t, out.String(), "on=checks")
		require.Equal(t, []string{"card-a", "card-b"}, o.Cards, "cut from origin, not the stale local ref")
		require.Equal(t, "promo/2026-10-05-1", f.opened.head)
		require.Equal(t, "dev", f.opened.base)
		cut := w.gitIn(w.bare, "rev-parse", "refs/heads/promo/2026-10-05-1")
		require.Equal(t, o.CutSHA, cut)
		require.Equal(t, originTip, w.gitIn(w.bare, "rev-parse", cut+"^1"), "first parent is origin's sprint tip")
		require.Equal(t, w.gitIn(w.bare, "rev-parse", "refs/heads/dev"), w.gitIn(w.bare, "rev-parse", cut+"^2"), "second parent is origin's dev")
		require.Equal(t, "d", w.gitIn(w.bare, "show", cut+":dev-only.txt"), "dev's work is in the cut")
		require.Equal(t, "b", w.gitIn(w.bare, "show", cut+":b.txt"), "the sprint's newest card is in the cut")
		require.Equal(t, "sprint/live", w.gitIn(w.dir, "symbolic-ref", "--short", "HEAD"), "no checkout was touched")

		// checks pass: queued, then still waiting on the queue
		f.checks = promoteChecks{State: promoteChecksPass}
		out.Reset()
		o, code = p.step(context.Background(), &out, &out)
		require.Zero(t, code, out.String())
		require.True(t, f.did("enqueue"))
		require.True(t, o.Waiting)
		require.Contains(t, out.String(), "on=queue")
		require.Empty(t, o.Promoted)

		// the queue merges it: recorded
		f.mergeSHA = w.devMoves("merged.txt", "m\n") // the queue lands it on dev
		out.Reset()
		o, code = p.step(context.Background(), &out, &out)
		require.Zero(t, code, out.String())
		require.Equal(t, f.mergeSHA, o.Promoted)
		require.Contains(t, out.String(), "promoted --sha "+f.mergeSHA)
		_, err := p.git(context.Background(), "config", "--local", "--get", "promote.pr")
		require.Error(t, err, "the open promotion is closed")
	})

	t.Run("conflict", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		w.devMoves("a.txt", "dev's a\n")
		f := &fakeForge{}
		p := w.promoter(f)
		var out bytes.Buffer
		o, code := p.step(context.Background(), &out, &out)
		require.Equal(t, 1, code)
		require.NotNil(t, o.Judgment)
		require.Equal(t, []string{"a.txt"}, o.Judgment.Files)
		require.Contains(t, o.Judgment.What, "a.txt")
		require.Equal(t, "", w.gitIn(w.bare, "branch", "--list", "promo/*"), "nothing was pushed")
		require.Empty(t, f.calls, "no pull request was opened")
		o, code = p.step(context.Background(), &out, &out)
		require.Equal(t, 1, code)
		require.Nil(t, o.Judgment, "one judgment, not two")
	})

	t.Run("failed queue run", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		f := &fakeForge{checks: promoteChecks{State: promoteChecksPass}, failure: promoteFailure{Name: "functional", Log: "--- FAIL: TestX"}}
		p := w.promoter(f)
		var out bytes.Buffer
		o, code := p.step(context.Background(), &out, &out)
		require.Equal(t, 1, code, out.String())
		require.NotNil(t, o.Judgment)
		require.Contains(t, o.Judgment.What, "functional")
		require.Contains(t, o.Judgment.Tail, "FAIL: TestX")
		require.Empty(t, o.Promoted)
		o, _ = p.step(context.Background(), &out, &out)
		require.Nil(t, o.Judgment, "one judgment, not two")
	})

	t.Run("failed check", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		f := &fakeForge{checks: promoteChecks{State: promoteChecksFail, Failing: []string{"vet"}}}
		o, code := w.promoter(f).step(context.Background(), &bytes.Buffer{}, &bytes.Buffer{})
		require.Equal(t, 1, code)
		require.NotNil(t, o.Judgment)
		require.Contains(t, o.Judgment.What, "vet")
		require.False(t, f.did("enqueue"), "a red pull request is not queued")
	})

	t.Run("not ahead", func(t *testing.T) {
		t.Parallel()
		w := newPromoteTwin(t)
		// dev takes the whole sprint branch: the cut would carry nothing
		w.gitIn(w.dir, "fetch", "-q", "origin")
		w.gitIn(w.dir, "push", "-q", "-f", "origin", "refs/remotes/origin/sprint/live:refs/heads/dev")
		// an earlier promotion is recorded, so the landed list is not empty
		w.gitIn(w.dir, "update-ref", "refs/promoted/last", w.gitIn(w.dir, "rev-list", "--max-parents=0", "HEAD"))
		var errb bytes.Buffer
		_, code := w.promoter(&fakeForge{}).step(context.Background(), &bytes.Buffer{}, &errb)
		require.Equal(t, 1, code)
		require.Contains(t, errb.String(), "not ahead")
	})
}
