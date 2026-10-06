// Package testgit is the one identity a test's git commit runs under. A hosted runner
// (ubuntu-latest) carries no global git identity, and the self-hosted benches do, so a
// test that commits in a scratch repository and leans on the machine's config is green
// on every bench and red on the hosted leg with `Author identity unknown` (run
// 37344601638). Every test that runs `git commit` takes its author and committer from
// here, through the command's environment, never t.Setenv and never the runner's
// config; the class test TestNoTestCommitsWithoutTheSharedGitIdentity
// (internal/ci/git_identity_class_test.go) refuses a commit that does not.
package testgit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Name and Email are the fixed author and committer of every test commit.
const (
	Name  = "nova-test"
	Email = "nova-test@example.invalid"
)

// Env is the author and committer identity as environment variables, for a command
// whose Env the caller builds itself.
func Env() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + Name, "GIT_AUTHOR_EMAIL=" + Email,
		"GIT_COMMITTER_NAME=" + Name, "GIT_COMMITTER_EMAIL=" + Email,
	}
}

// Environ is the caller's environment, then Env, then extra: a later variable wins in
// os/exec, so the identity overrides any the runner's environment carries and extra
// overrides both.
func Environ(extra ...string) []string {
	env := append(os.Environ(), Env()...)
	return append(env, extra...)
}

// NoGlobalConfig is the hosted runner's git on any machine: the system config off and
// the global config at a file under t.TempDir() that sets only user.useConfigOnly, so
// git never guesses an identity from the host name. A command run with it and without
// Env refuses to commit on a bench exactly as it does on ubuntu-latest.
func NoGlobalConfig(t testing.TB) []string {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644))
	return []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + global}
}
