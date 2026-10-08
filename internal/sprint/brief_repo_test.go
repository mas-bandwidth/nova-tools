package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A REPO: value names one repository, exactly owner/name: the shape the friend's staging
// holds it to (internal/friend/stage.go), so a value the admission lint and recut/rework
// accept is one staging can take. A word, a URL, a local path, "-" and "none" are all no
// owner/name (brief_repo.go, cardhdr.IsRepoValue).
func TestOnlyAnOwnerNameIsARepoValue(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"mas-bandwidth/nova-tools", "o/r", "a_b/c.d", "Owner1/Name-2", "_o/_r"} {
		assert.True(t, cardhdr.IsRepoValue(v), v)
		assert.Empty(t, RepoLineWhy("REPO: "+v+"\n"), v)
	}
	for _, v := range []string{
		"garbage", "-", "none", "mas-bandwidth/nova-tools tier: frontier",
		"https://example.com/mas-bandwidth/nova-tools.git",
		"git@example.com:mas-bandwidth/nova-tools.git",
		"/Users/glenn/nova-tools", "mas-bandwidth/nova-tools/extra",
		"mas-bandwidth//nova-tools", "owner/../name", "owner/name..", "../name",
		"owner/", "/name", ".hidden/name", "owner/.hidden",
	} {
		assert.False(t, cardhdr.IsRepoValue(v), v)
		assert.NotEmpty(t, RepoLineWhy("REPO: "+v+"\n"), v)
	}
}

// A brief that names no REPO line reads as one, and a brief whose REPO line is an owner/name
// is not refused by recut and rework (brief_repo.go).
func TestRepoLineWhyPassesABriefWithNoOrAOneRepo(t *testing.T) {
	t.Parallel()
	assert.Empty(t, RepoLineWhy("RESULT: c sha=0123456789ab tier: pro\nBASE: dev\n\nThe task."))
	assert.Empty(t, RepoLineWhy("RESULT: c sha=0123456789ab tier: pro\nREPO: mas-bandwidth/nova-tools\nBASE: dev\n\nThe task."))
}
