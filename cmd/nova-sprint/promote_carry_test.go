package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// carryRepo is a local twin: a bare origin and a clone. The forge is never
// this repository's network. originSprint is the commit origin's live branch
// holds; stale is the clone's local branch when that ref has been left behind.
type carryRepo struct {
	dir, bare                      string
	env                            []string
	live, base                     string
	originSprint, originDev, stale string
}

func (r carryRepo) git(t *testing.T, args ...string) string {
	t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: r.dir, Env: r.env, OwnRepo: true}, args...)
	require.NoError(t, err, "git %v\n%s\n%s", args, res.Stderr, res.Stdout)
	return strings.TrimSpace(string(res.Stdout))
}

func (r carryRepo) gitBare(t *testing.T, args ...string) (string, error) {
	t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: r.bare, Env: r.env, OwnRepo: true}, args...)
	if err != nil {
		return strings.TrimSpace(string(res.Stderr)), err
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// newCarryRepo builds the twin. mode is clean (diverged, local ref stale),
// conflict (the same file changed on both sides), or not-ahead (origin's
// cut is level with the target while the local ref has a commit origin does not).
func newCarryRepo(t *testing.T, mode string) carryRepo {
	t.Helper()
	root := t.TempDir()
	r := carryRepo{
		dir:  filepath.Join(root, "work"),
		bare: filepath.Join(root, "origin.git"),
		live: "sprint/live",
		base: "dev",
		env: append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=promote",
			"GIT_AUTHOR_EMAIL=promote@example.invalid",
			"GIT_COMMITTER_NAME=promote",
			"GIT_COMMITTER_EMAIL=promote@example.invalid",
			"GIT_PAGER=cat",
			"GIT_TERMINAL_PROMPT=0",
		),
	}
	require.NoError(t, os.Mkdir(r.dir, 0o755))
	require.NoError(t, os.Mkdir(r.bare, 0o755))
	r.git(t, "init", "-q", "--bare", "-b", "dev", r.bare)
	r.git(t, "init", "-q", "-b", "dev")
	r.git(t, "remote", "add", "origin", r.bare)
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, "README"), []byte("base\n"), 0o644))
	r.git(t, "add", "README")
	r.git(t, "commit", "-q", "-m", "base")
	baseSHA := r.git(t, "rev-parse", "HEAD")
	r.git(t, "push", "-q", "origin", "HEAD:refs/heads/dev")
	r.git(t, "checkout", "-q", "-b", r.live)

	switch mode {
	case "not-ahead":
		r.git(t, "push", "-q", "origin", "HEAD:refs/heads/"+r.live)
		require.NoError(t, os.WriteFile(filepath.Join(r.dir, "local.txt"), []byte("only-local\n"), 0o644))
		r.git(t, "add", "local.txt")
		r.git(t, "commit", "-q", "-m", "land card-local (sprint stream s1)")
		r.stale = r.git(t, "rev-parse", "HEAD")
		r.originSprint = r.git(t, "rev-parse", "refs/remotes/origin/"+r.live)
		r.originDev = r.git(t, "rev-parse", "refs/remotes/origin/dev")
		require.Equal(t, r.originSprint, r.originDev, "origin's cut is level with the target")
		require.NotEqual(t, r.stale, r.originSprint, "the local ref is the trap")
		return r
	case "conflict":
		require.NoError(t, os.WriteFile(filepath.Join(r.dir, "f.txt"), []byte("sprint\n"), 0o644))
		r.git(t, "add", "f.txt")
		r.git(t, "commit", "-q", "-m", "land card-s (sprint stream s1)")
	default:
		require.NoError(t, os.WriteFile(filepath.Join(r.dir, "sprint.txt"), []byte("from-sprint\n"), 0o644))
		r.git(t, "add", "sprint.txt")
		r.git(t, "commit", "-q", "-m", "land card-s (sprint stream s1)")
	}
	r.git(t, "push", "-q", "origin", "HEAD:refs/heads/"+r.live)
	r.originSprint = r.git(t, "rev-parse", "HEAD")
	r.git(t, "checkout", "-q", "dev")
	if mode == "conflict" {
		require.NoError(t, os.WriteFile(filepath.Join(r.dir, "f.txt"), []byte("target\n"), 0o644))
		r.git(t, "add", "f.txt")
		r.git(t, "commit", "-q", "-m", "target move")
	} else {
		require.NoError(t, os.WriteFile(filepath.Join(r.dir, "target.txt"), []byte("from-target\n"), 0o644))
		r.git(t, "add", "target.txt")
		r.git(t, "commit", "-q", "-m", "target move")
	}
	r.git(t, "push", "-q", "origin", "HEAD:refs/heads/dev")
	r.originDev = r.git(t, "rev-parse", "HEAD")
	r.git(t, "checkout", "-q", r.live)
	if mode == "conflict" {
		r.stale = r.git(t, "rev-parse", "HEAD")
		return r
	}
	r.git(t, "reset", "--hard", baseSHA)
	r.stale = r.git(t, "rev-parse", "HEAD")
	require.NotEqual(t, r.stale, r.originSprint)
	return r
}

// carryForge is the fake forge. It never runs gh. failCheck, when set, is the
// one check a merge-group run failed. Otherwise the second view is the merge
// of the pull request head, an object the twin already has.
type carryForge struct {
	t         *testing.T
	repo      carryRepo
	failCheck string
	calls     [][]string
	views     int
}

func (f *carryForge) call(_ context.Context, _ string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	switch {
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create":
		return "https://example.invalid/nova/pull/11", nil
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		f.views++
		if f.failCheck != "" || f.views < 2 {
			return `{"id":"PR_carry","state":"OPEN"}`, nil
		}
		return fmt.Sprintf(`{"id":"PR_carry","state":"MERGED","mergeCommit":{"oid":"%s"}}`, f.headSHA()), nil
	case strings.Contains(joined, "enqueuePullRequest"):
		return `{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"MQ_carry"}}}}`, nil
	case len(args) > 0 && args[0] == "api":
		return `{"data":{"node":{"mergeQueueEntry":{"id":"MQ_carry","state":"AWAITING_CHECKS"}}}}`, nil
	case len(args) >= 2 && args[0] == "run" && args[1] == "list":
		if f.failCheck == "" {
			return `[]`, nil
		}
		return fmt.Sprintf(`[{"databaseId":9,"conclusion":"failure","status":"completed","name":%q}]`, f.failCheck), nil
	case len(args) >= 2 && args[0] == "run" && args[1] == "view":
		return "check " + f.failCheck + " failed\n", nil
	default:
		return "", fmt.Errorf("unexpected gh %s", joined)
	}
}

func (f *carryForge) head() string {
	for _, c := range f.calls {
		if len(c) < 2 || c[0] != "pr" || c[1] != "create" {
			continue
		}
		for i, a := range c {
			if a == "--head" && i+1 < len(c) {
				return c[i+1]
			}
		}
	}
	return ""
}

func (f *carryForge) headSHA() string {
	f.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: f.repo.dir, Env: f.repo.env, OwnRepo: true}, "rev-parse", "--verify", "refs/heads/"+f.head())
	require.NoError(f.t, err, "promo head %s\n%s", f.head(), res.Stderr)
	return strings.TrimSpace(string(res.Stdout))
}

func (f *carryForge) has(fragment string) bool {
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), fragment) {
			return true
		}
	}
	return false
}

func requireOrder(t *testing.T, out string, steps ...string) {
	t.Helper()
	pos := 0
	for _, step := range steps {
		i := strings.Index(out[pos:], step)
		require.GreaterOrEqual(t, i, 0, "missing %q in:\n%s", step, out)
		pos += i + 1
	}
}

func carryPromoter(t *testing.T, r carryRepo, forge *carryForge, gate *string) *promoter {
	t.Helper()
	return &promoter{
		dir: r.dir, live: r.live, base: r.base, env: r.env,
		now:   time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC),
		ghRun: forge.call,
		gate: func(_ context.Context, _ string, sha string) (string, error) {
			if gate != nil {
				*gate = sha
			}
			return "", nil
		},
	}
}

// TestPromoteCarriesACutToARecordedPromotion is the promote pass on a twin
// repository and a fake forge: a clean cut is merged, opened, queued and
// recorded; a conflict and a failed queue run each stop with one judgment;
// the cut is origin's ref, a cut that is not ahead is refused, and every
// step is printed before the pass blocks on it.
func TestPromoteCarriesACutToARecordedPromotion(t *testing.T) {
	t.Parallel()

	t.Run("clean", func(t *testing.T) {
		t.Parallel()
		r := newCarryRepo(t, "clean")
		forge := &carryForge{t: t, repo: r}
		var gated string
		var out, errb bytes.Buffer
		o, code := carryPromoter(t, r, forge, &gated).step(context.Background(), &out, &errb)
		text := out.String()
		require.Zero(t, code, "stderr: %s\nstdout: %s", errb.String(), text)
		require.Empty(t, errb.String())
		requireOrder(t, text,
			"PROMOTE FETCH",
			"PROMOTE AHEAD",
			"PROMOTE CUT-FROM",
			"PROMOTE MERGE",
			"PROMOTE GATE",
			"PROMOTE PUSH",
			"PROMOTE OPEN",
			"PROMOTE QUEUE",
			"PROMOTE WAITING on merge queue",
			"promoted --sha ",
		)
		var cutFrom string
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "PROMOTE CUT-FROM") {
				cutFrom = line
			}
		}
		require.Contains(t, cutFrom, "ref=origin/"+r.live)
		require.Contains(t, cutFrom, r.originSprint)
		require.NotContains(t, cutFrom, r.stale, "the cut is not the local ref")
		require.Equal(t, "promo/2026-10-05-1", o.Branch)
		require.Equal(t, o.Branch, o.Head)
		require.NotEqual(t, r.live, o.Head)
		require.NotEmpty(t, o.Promoted)
		require.Contains(t, text, "promoted --sha "+o.Promoted)
		require.Equal(t, o.Promoted, r.git(t, "rev-parse", "refs/promoted/last"))
		require.Equal(t, o.Promoted, gated, "the gate runs on the merged cut")
		require.Equal(t, r.originSprint, r.git(t, "rev-parse", o.Branch+"^1"), "first parent is origin's cut, not the local ref")
		require.Equal(t, r.originDev, r.git(t, "rev-parse", o.Branch+"^2"), "the target was merged into the cut")
		require.NotEqual(t, r.stale, r.originSprint)
		require.Equal(t, r.stale, r.git(t, "rev-parse", "HEAD"), "the local branch is left where it was")
		require.Equal(t, r.live, r.git(t, "symbolic-ref", "--short", "HEAD"))
		require.Equal(t, "from-sprint", r.git(t, "cat-file", "blob", o.Branch+":sprint.txt"))
		require.Equal(t, "from-target", r.git(t, "cat-file", "blob", o.Branch+":target.txt"))
		pushed, err := r.gitBare(t, "rev-parse", "refs/heads/"+o.Branch)
		require.NoError(t, err)
		require.Equal(t, o.Promoted, pushed)
		require.Equal(t, o.Branch, forge.head())
		require.True(t, forge.has("enqueuePullRequest"), "admission is the enqueue mutation, calls %v", forge.calls)
		require.Nil(t, o.Judgment)
		for _, c := range forge.calls {
			for _, a := range c {
				require.NotContains(t, []string{"--squash", "--rebase", "--merge"}, a, "no strategy flag: %v", c)
			}
		}
	})

	t.Run("conflict", func(t *testing.T) {
		t.Parallel()
		r := newCarryRepo(t, "conflict")
		forge := &carryForge{t: t, repo: r}
		var gated string
		var out, errb bytes.Buffer
		o, code := carryPromoter(t, r, forge, &gated).step(context.Background(), &out, &errb)
		text := out.String()
		require.Equal(t, 1, code, "stdout: %s\nstderr: %s", text, errb.String())
		require.NotNil(t, o.Judgment, "one judgment, stdout: %s", text)
		require.Contains(t, text, "JUDGMENT")
		require.Contains(t, text, "f.txt", "the judgment names the conflicted file")
		require.Contains(t, o.Judgment.What, "f.txt")
		require.Empty(t, o.Promoted)
		require.NotContains(t, text, "promoted --sha")
		require.NotContains(t, text, "PROMOTE OPEN")
		require.Empty(t, forge.calls, "a conflict opens nothing")
		require.Empty(t, gated, "a conflict does not run the gate")
		require.Equal(t, "sprint\n", string(mustRead(t, filepath.Join(r.dir, "f.txt"))))
		require.NotContains(t, string(mustRead(t, filepath.Join(r.dir, "f.txt"))), "<<<<<<")
		require.Empty(t, r.git(t, "status", "--porcelain"), "nothing was resolved in the worktree")
		_, err := r.gitBare(t, "rev-parse", "--verify", "refs/heads/promo/2026-10-05-1")
		require.Error(t, err, "a conflict pushes nothing")
		res, merr := gitrun.Run(context.Background(), gitrun.Options{C: r.dir, Env: r.env, OwnRepo: true}, "rev-parse", "-q", "--verify", "MERGE_HEAD")
		require.Error(t, merr, "the merge was aborted, stdout %s", res.Stdout)
		require.Equal(t, r.live, r.git(t, "symbolic-ref", "--short", "HEAD"))
	})

	t.Run("queue", func(t *testing.T) {
		t.Parallel()
		r := newCarryRepo(t, "clean")
		forge := &carryForge{t: t, repo: r, failCheck: "tree-gate"}
		var out, errb bytes.Buffer
		o, code := carryPromoter(t, r, forge, nil).step(context.Background(), &out, &errb)
		text := out.String()
		require.Equal(t, 1, code, "stdout: %s\nstderr: %s", text, errb.String())
		require.NotNil(t, o.Judgment)
		require.Contains(t, text, "JUDGMENT")
		require.Contains(t, text, "tree-gate", "the judgment names the failing check")
		require.Contains(t, o.Judgment.What, "tree-gate")
		require.Equal(t, []string{"fix-and-recut", "skip"}, o.Judgment.Decisions)
		require.Empty(t, o.Promoted, "a failed queue run does not record the sha")
		require.NotContains(t, text, "promoted --sha")
		_, err := gitrun.Run(context.Background(), gitrun.Options{C: r.dir, Env: r.env, OwnRepo: true}, "rev-parse", "-q", "--verify", "refs/promoted/last")
		require.Error(t, err, "refs/promoted/last was not written")
		require.True(t, forge.has("enqueuePullRequest"))
		requireOrder(t, text, "PROMOTE FETCH", "PROMOTE MERGE", "PROMOTE OPEN", "PROMOTE QUEUE", "PROMOTE WAITING on merge queue", "JUDGMENT")
	})

	t.Run("not-ahead", func(t *testing.T) {
		t.Parallel()
		r := newCarryRepo(t, "not-ahead")
		forge := &carryForge{t: t, repo: r}
		var gated string
		var out, errb bytes.Buffer
		o, code := carryPromoter(t, r, forge, &gated).step(context.Background(), &out, &errb)
		text := out.String()
		require.Equal(t, 1, code, "stdout: %s\nstderr: %s", text, errb.String())
		require.Contains(t, errb.String(), "not ahead")
		require.Contains(t, text, "PROMOTE FETCH", "a refusal still says it is fetching")
		require.NotContains(t, text, "PROMOTE OPEN")
		require.NotContains(t, text, "promoted --sha")
		require.Empty(t, o.Promoted)
		require.Empty(t, forge.calls, "a cut that is not ahead never reaches the forge, calls %v", forge.calls)
		require.Empty(t, gated)
		require.Empty(t, r.git(t, "branch", "--list", "promo/*"))
		require.Equal(t, r.stale, r.git(t, "rev-parse", "HEAD"), "the local commit was not the cut")
	})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, path)
	return b
}
