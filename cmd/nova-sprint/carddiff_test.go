package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
)

// cardDiffRig is a bare origin, a worker's clone that commits to it, and the
// coordinator's clone where cardDiff looks (landRoot/repoDirName(origin)). No network.
type cardDiffRig struct {
	t                    *testing.T
	a                    *app
	remote, worker, land string
	env                  []string
}

func newCardDiffRig(t *testing.T) *cardDiffRig {
	t.Helper()
	dir := t.TempDir()
	r := &cardDiffRig{t: t, a: &app{}, remote: filepath.Join(dir, "remote.git"), worker: filepath.Join(dir, "worker")}
	r.env = append(slices.DeleteFunc(os.Environ(), func(e string) bool { return strings.HasPrefix(e, "GOFLAGS=") }), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=w", "GIT_AUTHOR_EMAIL=w@example.invalid", "GIT_COMMITTER_NAME=w", "GIT_COMMITTER_EMAIL=w@example.invalid")
	r.git("", "init", "-q", "--bare", "-b", "main", r.remote)
	r.git("", "clone", "-q", r.remote, r.worker)
	r.commit("README", "base\n")
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	root := filepath.Join(dir, "land")
	r.land = filepath.Join(root, repoDirName(r.remote))
	r.git("", "clone", "-q", r.remote, r.land)
	r.a.gitEnv = r.env
	r.a.landRoot = func() (string, error) { return root, nil }
	return r
}

func (r *cardDiffRig) git(dir string, args ...string) string {
	r.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: dir, Env: r.env, OwnRepo: dir != ""}, args...)
	require.NoError(r.t, err, "git %v: %s", args, res.Stderr)
	return strings.TrimSpace(string(res.Stdout))
}

func (r *cardDiffRig) commit(file, text string) string {
	r.t.Helper()
	require.NoError(r.t, os.WriteFile(filepath.Join(r.worker, file), []byte(text), 0o600))
	r.git(r.worker, "add", file)
	r.git(r.worker, "commit", "-q", "-m", "write "+file)
	return r.git(r.worker, "rev-parse", "HEAD")
}

func (r *cardDiffRig) card(head, branch string) *sprint.Card {
	return &sprint.Card{ID: "c1.w1", Fields: map[string]string{
		"head": head, "branch": branch, "brief": "base-repo: " + r.remote + "\nBASE: main\n",
	}}
}

// TestCardDiffFetchesAHeadTheCloneLacks: a head pushed after the clone was made is
// fetched by its branch, and the diff is the card's work against its base.
func TestCardDiffFetchesAHeadTheCloneLacks(t *testing.T) {
	t.Parallel()
	r := newCardDiffRig(t)
	r.git(r.worker, "switch", "-q", "-c", "sprint/c1")
	head := r.commit("work.txt", "the work\n")
	r.git(r.worker, "push", "-q", "origin", "sprint/c1")

	diff, err := r.a.cardDiff(context.Background(), r.card(head, "sprint/c1"))
	require.NoError(t, err)
	assert.Contains(t, diff, "+the work")
}

// TestCardDiffSaysTheFetchFailed: a head the clone lacks whose branch origin cannot
// give is one line naming the failed fetch and its remedy, never a bare diff error
// with the fetch's failure dropped.
func TestCardDiffSaysTheFetchFailed(t *testing.T) {
	t.Parallel()
	r := newCardDiffRig(t)
	r.git(r.worker, "switch", "-q", "-c", "sprint/c1")
	head := r.commit("work.txt", "the work\n") // committed, never pushed

	_, err := r.a.cardDiff(context.Background(), r.card(head, "sprint/c1"))
	require.Error(t, err)
	msg := err.Error()
	assert.NotContains(t, msg, "\n", "the failure is one line")
	assert.Contains(t, msg, "fetch")
	assert.Contains(t, msg, "sprint/c1")
	assert.Contains(t, msg, head)
	assert.Contains(t, msg, "git -C "+r.land+" fetch origin sprint/c1", "the remedy is a command to run")
}
