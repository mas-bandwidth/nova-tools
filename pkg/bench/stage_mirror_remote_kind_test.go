package bench

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Only a URL or an scp-style remote is fetched on the bench itself. Everything else,
// including file:// and a relative path, is a path on the lander's machine: the bench
// reads the mirror's origin for it, so the data still flows into the studio, never out.
func TestOnlyAURLOrAnScpRemoteIsFetchedOnTheBench(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		remote  string
		network bool
	}{
		{"https URL", "https://" + "github.com/robotgamedevelopers/fixed3d.git", true},
		{"http URL", "http://" + "example.com/repo.git", true},
		{"ssh URL", "ssh://git@example.com/repo.git", true},
		{"git URL", "git://example.com/repo.git", true},
		{"scp with a user", "git@example.com:repo.git", true},
		{"scp host and path", "example.com:repo.git", true},
		{"absolute path", "/srv/git/nova-tools.git", false},
		{"relative path", "nova-tools.git", false},
		{"dot relative path", "./nova-tools.git", false},
		{"parent relative path", "../nova-tools.git", false},
		{"file URL", "file:///srv/git/nova-tools.git", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.network, networkRemote(tc.remote))

			st := MirrorStage{Mirror: MirrorDir("nova-tools"), Remote: tc.remote, Ref: GateRef("sprint/mechanical", testSha), Sha: testSha}
			line := StageLine(st, testRun+"/repo")
			if tc.network {
				assert.Contains(t, line, "fetch --quiet --no-tags --no-write-fetch-head '"+tc.remote+"' 'refs/nova-gate/")
				assert.NotContains(t, line, "remote.origin.url", "a network remote is fetched as-is")
			} else {
				assert.Contains(t, line, "src=$(git -C 'nova-bench/mirror/nova-tools.git' config --get remote.origin.url) || src='"+tc.remote+"'", "a local remote reads the mirror's origin")
				assert.Contains(t, line, `--no-write-fetch-head "$src" 'refs/nova-gate/`)
			}
		})
	}
}
