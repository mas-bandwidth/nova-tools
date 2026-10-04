package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// normRepo makes one repository's spellings equal and keeps two apart.
func TestLandNamesARepositoryOneWay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"https://forge.test/owner/name.git", "git@forge.test:owner/name", true},
		{"https://forge.test/owner/name", "ssh://git@forge.test/owner/name.git/", true},
		{"https://forge.test/owner/name.git", "https://forge.test/owner/other.git", false},
		{"/srv/git/name.git", "/srv/git/name", true},
		{"/srv/git/Repo.git", "/srv/git/repo.git", false},
		{"https://Forge.TEST/owner/name.git", "https://forge.test/owner/name", true},
		{"https://forge.test/Owner/name.git", "https://forge.test/owner/name.git", false},
	} {
		assert.Equal(t, tc.same, sameRepo(tc.a, tc.b), "%s %s", tc.a, tc.b)
	}
	assert.Regexp(t, `^forge\.test-owner-name-[0-9a-f]{16}$`, repoDirName("https://forge.test/owner/name.git"))
	assert.Equal(t, repoDirName("https://forge.test/owner/name.git"), repoDirName("git@forge.test:owner/name"))
}
