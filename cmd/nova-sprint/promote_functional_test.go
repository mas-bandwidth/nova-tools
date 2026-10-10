//go:build functional

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
)

// funcGH records gh and answers a pull request that is admitted and not yet
// merged, so the pass cuts the branch and stops at the queue.
type funcGH struct {
	calls [][]string
}

func (f *funcGH) call(_ context.Context, _ string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	switch {
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create":
		return "https://example.invalid/nova/pull/7", nil
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		return `{"id":"PR_func","state":"OPEN"}`, nil
	case strings.Contains(joined, "enqueuePullRequest"):
		return `{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"MQ_func"}}}}`, nil
	case len(args) > 0 && args[0] == "api":
		return `{"data":{"node":{"mergeQueueEntry":{"id":"MQ_func","state":"AWAITING_CHECKS"}}}}`, nil
	case len(args) >= 2 && args[0] == "run" && args[1] == "list":
		return `[]`, nil
	default:
		return "", nil
	}
}

func (f *funcGH) body() string {
	for _, c := range f.calls {
		if len(c) < 2 || c[0] != "pr" || c[1] != "create" {
			continue
		}
		for i, a := range c {
			if a == "--body" && i+1 < len(c) {
				return c[i+1]
			}
		}
	}
	return ""
}

func (f *funcGH) head() string {
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

// TestPromoteFunctionalCutsABranchFromTheTip is a real git repository: the
// promo branch is cut from the sprint tip, and the pull request body lists the
// cards landed on it.
func TestPromoteFunctionalCutsABranchFromTheTip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "work")
	bare := filepath.Join(root, "origin.git")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.Mkdir(bare, 0o755))
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=promote",
		"GIT_AUTHOR_EMAIL=promote@example.invalid",
		"GIT_COMMITTER_NAME=promote",
		"GIT_COMMITTER_EMAIL=promote@example.invalid",
	)
	git := func(where string, args ...string) string {
		t.Helper()
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: env, OwnRepo: true}, args...)
		require.NoError(t, err, "git %v\n%s\n%s", args, res.Stderr, res.Stdout)
		return strings.TrimSpace(string(res.Stdout))
	}
	git(bare, "init", "-q", "--bare", "-b", "dev")
	git(dir, "init", "-q", "-b", "dev")
	git(dir, "remote", "add", "origin", bare)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("base\n"), 0o644))
	git(dir, "add", "README")
	git(dir, "commit", "-q", "-m", "base")
	git(dir, "push", "-q", "origin", "HEAD:refs/heads/dev")
	git(dir, "checkout", "-q", "-b", "sprint/live")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), []byte("a\n"), 0o644))
	git(dir, "add", "a")
	git(dir, "commit", "-q", "-m", "land card-a (sprint stream s1)")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b"), []byte("b\n"), 0o644))
	git(dir, "add", "b")
	git(dir, "commit", "-q", "-m", "land card-b (sprint stream s1)")
	git(dir, "push", "-q", "origin", "HEAD:refs/heads/sprint/live")
	tip := git(dir, "rev-parse", "HEAD")

	gh := &funcGH{}
	p := &promoter{
		dir: dir, live: "sprint/live", base: "dev", env: env,
		now:   time.Date(2026, 10, 4, 18, 0, 0, 0, time.UTC),
		ghRun: gh.call,
	}
	out, code := p.step(context.Background(), io.Discard, io.Discard)
	require.Zero(t, code, "pass: %+v", out)
	require.Equal(t, "promo/2026-10-04-1", out.Branch)
	require.Equal(t, "promo/2026-10-04-1", out.Head)
	require.NotEqual(t, "sprint/live", out.Head)
	require.Equal(t, []string{"card-a", "card-b"}, out.Cards)
	require.Equal(t, tip, git(dir, "rev-parse", "refs/heads/promo/2026-10-04-1"), "the promo branch is the sprint tip")
	require.Equal(t, tip, git(bare, "rev-parse", "refs/heads/promo/2026-10-04-1"), "the frozen branch is what was pushed")
	require.Equal(t, "sprint/live", git(dir, "symbolic-ref", "--short", "HEAD"), "the live branch stays checked out")
	require.Equal(t, "promo/2026-10-04-1", gh.head())
	body := gh.body()
	require.Contains(t, body, "card-a")
	require.Contains(t, body, "card-b")
	require.Contains(t, out.Body, "card-a")
	require.Contains(t, out.Body, "card-b")
}
