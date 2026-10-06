// Package testgit is the one git identity for a test's own scratch repositories.
//
// A test that runs `git commit` (or `git commit-tree`, `git merge`, `git tag -a`) needs an
// author and a committer. A self-hosted bench has one in its global git config and a hosted
// runner has none, so a test that leans on the runner's config passes on every bench and
// fails on ubuntu-latest with `Author identity unknown` (dev CI run 37344601638, shard 6).
// Env sets the identity in the command's own environment and shuts the runner's global and
// system config out, so a test's git behaves the same on every machine. The class rule
// git-identity (internal/ci/git_identity_class_test.go, docs/SPEC-CI.md) refuses a test
// file that commits without it. internal/testkit's GitRig carries the same isolation for a
// bare remote and its clones.
package testgit

import (
	"os"
	"strings"
)

// Name and Email are the fixed author and committer of every commit a test makes.
const (
	Name  = "testgit"
	Email = "testgit@example.com"
)

// Env is EnvFrom(os.Environ()): the environment for a git command a test runs.
func Env() []string { return EnvFrom(os.Environ()) }

// EnvFrom is base without its GIT_ variables, then the fixed identity, the system config
// off and the global config empty, so the command's identity and settings are the test's
// alone. A caller that needs a GIT_ variable of its own (GIT_TERMINAL_PROMPT=0) appends it.
func EnvFrom(base []string) []string {
	return append(withoutGitVars(base),
		"GIT_AUTHOR_NAME="+Name, "GIT_AUTHOR_EMAIL="+Email,
		"GIT_COMMITTER_NAME="+Name, "GIT_COMMITTER_EMAIL="+Email,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
}

// HostedRunnerEnv is base as a hosted CI runner with no git identity sees it: no GIT_
// variables, no global or system config, and user.useConfigOnly set so git never guesses
// an identity from the user and host names. A git commit run in it is refused with
// `Author identity unknown` unless the command carries an identity, so a test hands it to
// its git helper to prove the helper does not lean on the machine.
func HostedRunnerEnv(base []string) []string {
	return append(withoutGitVars(base),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.useConfigOnly", "GIT_CONFIG_VALUE_0=true")
}

// withoutGitVars is env with every GIT_ variable dropped.
func withoutGitVars(env []string) []string {
	out := make([]string, 0, len(env)+6)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}
