package sprint_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A twin repository for the tests that land on a base (land_cure_test.go): a bare origin
// with main, dev and the base; a developer's clone that commits to dev and the base; and
// the land round's own clone.

const syncBase = "sprint/mechanical-2026-10-02"

type syncRepo struct {
	t                 *testing.T
	origin, dev, land string
	env               []string
}

func newSyncRepo(t *testing.T) *syncRepo {
	t.Helper()
	root := t.TempDir()
	r := &syncRepo{t: t, origin: filepath.Join(root, "origin.git"), dev: filepath.Join(root, "dev"), land: filepath.Join(root, "land"),
		env: append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=sync-test", "GIT_AUTHOR_EMAIL=sync-test@example.com",
			"GIT_COMMITTER_NAME=sync-test", "GIT_COMMITTER_EMAIL=sync-test@example.com")}
	r.git(root, "init", "-q", "--bare", "-b", "main", r.origin)
	r.git(root, "clone", "-q", r.origin, r.dev)
	require.NoError(t, os.WriteFile(filepath.Join(r.dev, "README.md"), []byte("# repo\n"), 0o644))
	r.git(r.dev, "add", "README.md")
	r.git(r.dev, "commit", "-q", "-m", "initial commit")
	r.git(r.dev, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/dev", "HEAD:refs/heads/"+syncBase)
	r.git(root, "clone", "-q", r.origin, r.land)
	return r
}

func (r *syncRepo) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.CommandContext(r.t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// commit is a developer's commit of file on branch, pushed.
func (r *syncRepo) commit(branch, file, content, msg string) {
	r.t.Helper()
	r.git(r.dev, "fetch", "-q", "origin")
	r.git(r.dev, "checkout", "-q", "-B", branch, "origin/"+branch)
	require.NoError(r.t, os.WriteFile(filepath.Join(r.dev, file), []byte(content), 0o644))
	r.git(r.dev, "add", file)
	r.git(r.dev, "commit", "-q", "-m", msg)
	r.git(r.dev, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
}

func (r *syncRepo) tip(branch string) string { return r.git(r.origin, "rev-parse", branch) }

func (r *syncRepo) has(branch, rev string) bool {
	cmd := exec.CommandContext(r.t.Context(), "git", "merge-base", "--is-ancestor", rev, branch)
	cmd.Dir, cmd.Env = r.origin, r.env
	return cmd.Run() == nil
}

// syncRig is the twin store with streams s1 and s2, and the twin repository.
