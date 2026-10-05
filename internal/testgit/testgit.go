// Package testgit gives every test that commits in a scratch repository its git
// identity, so a commit never leans on the runner's global git config: the
// hosted ubuntu-latest runner has no user.name or user.email, and a test that
// ran `git commit` with nothing of its own failed there with `Author identity
// unknown` while every self-hosted bench, which has one, stayed green
// (TestWallCoverWallCommitsCountsPastBaseRef, dev run 37344601638, shard 6).
//
// Env is the one helper. The class test internal/ci/git_identity_class_test.go
// refuses a test file that commits without it (docs/SPEC-CI.md, `git-identity`).
package testgit

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Name and Email are the author and committer identity every scratch commit
// carries, as git reads it from GIT_AUTHOR_* and GIT_COMMITTER_*.
const (
	Name  = "nova-test"
	Email = "nova-test@example.invalid"
)

// globalConfig is the text of the global git config Env points at: git may not
// guess an identity from the user and host names, so a commit with no identity
// of its own fails on every machine as it fails on a runner with none.
const globalConfig = "[user]\n\tuseConfigOnly = true\n"

var globals sync.Map // testing.TB -> the path of that test's global config

// Env returns base without its GIT_ variables, then the fixed identity of Name
// and Email for author and committer, the system config off and the global
// config at an empty-but-for-useConfigOnly file in the test's own
// t.TempDir(). Hand it to cmd.Env of every git command a test runs; a caller
// that needs a different author appends its own GIT_AUTHOR_* after it.
func Env(t testing.TB, base []string) []string {
	t.Helper()
	return append(isolated(t, base),
		"GIT_AUTHOR_NAME="+Name, "GIT_AUTHOR_EMAIL="+Email,
		"GIT_COMMITTER_NAME="+Name, "GIT_COMMITTER_EMAIL="+Email)
}

// isolated is base without its GIT_ variables and with the machine's git config
// out of reach: no identity, so a commit through it fails as on a bare runner.
func isolated(t testing.TB, base []string) []string {
	t.Helper()
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+global(t))
}

// global is the test's own global config file, written once per test.
func global(t testing.TB) string {
	t.Helper()
	if p, ok := globals.Load(t); ok {
		return p.(string)
	}
	p := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(p, []byte(globalConfig), 0o644))
	if prev, loaded := globals.LoadOrStore(t, p); loaded {
		return prev.(string)
	}
	t.Cleanup(func() { globals.Delete(t) })
	return p
}
