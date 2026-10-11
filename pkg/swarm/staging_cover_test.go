package swarm

// The unit tier of staging.go covers what the launcher exports around every job:
// StagingGitEnv puts the pool's identity row in the child's environment as author
// and committer and blanks the bench's own git config. StageCloneIdentity drives
// the job clone's git through gitrun with no injection seam (the gitrun.Options
// with the child's directory and environment is built inside the function), so
// like the git-evidence half of lintbase.go it runs only in the functional tier
// (staging_functional_test.go).

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStagingCoverGitEnvFullIdentity pins StagingGitEnv's main path: a full pool
// identity row is exported as both author and committer, ahead of the two
// assignments that blank the bench's config, in that order.
func TestStagingCoverGitEnvFullIdentity(t *testing.T) {
	t.Parallel()

	got := StagingGitEnv(StagingIdentity{Owner: "pool", Name: "Staging Pool", Email: "staging@example.com"})
	assert.Equal(t, []string{
		"GIT_AUTHOR_NAME=Staging Pool",
		"GIT_AUTHOR_EMAIL=staging@example.com",
		"GIT_COMMITTER_NAME=Staging Pool",
		"GIT_COMMITTER_EMAIL=staging@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	}, got, "the launcher exports the pool identity as author and committer, then blanks the bench config")
}

// TestStagingCoverGitEnvOmitsEmptyFields pins StagingGitEnv's refusal rows: an
// identity field the pool row does not carry exports no entry for it, the
// function invents nothing, and the two assignments that blank the bench's
// config are exported for every identity, an empty one included.
func TestStagingCoverGitEnvOmitsEmptyFields(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		id   StagingIdentity
		want []string
	}{
		{"no name", StagingIdentity{Email: "staging@example.com"}, []string{
			"GIT_AUTHOR_EMAIL=staging@example.com",
			"GIT_COMMITTER_EMAIL=staging@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
		}},
		{"no email", StagingIdentity{Name: "Staging Pool"}, []string{
			"GIT_AUTHOR_NAME=Staging Pool",
			"GIT_COMMITTER_NAME=Staging Pool",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
		}},
		{"empty identity", StagingIdentity{}, []string{
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, StagingGitEnv(tc.id), "identity %+v exports the wrong environment", tc.id)
		})
	}
}
