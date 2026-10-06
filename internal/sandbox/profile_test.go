//go:build darwin

package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDarwinProfileAllowsOpenDirectoryMembershipLookup verifies that the darwin
// profile template includes the com.apple.system.opendirectoryd.membership service
// in the mach-lookup allow list. This prevents sqlite3 and git launches from
// stalling on Open Directory lookups when running inside the sandbox.
func TestDarwinProfileAllowsOpenDirectoryMembershipLookup(t *testing.T) {
	t.Parallel()

	write := t.TempDir()
	home := t.TempDir()
	tmp := t.TempDir()

	p, bad := Build(in(t, write, tmp, home, "/bin/echo"))
	require.Empty(t, bad, "refused at build")

	text, _, err := DarwinProfile(p)
	require.NoError(t, err, "DarwinProfile")
	require.Contains(t, text, `com.apple.system.opendirectoryd.membership`, "profile includes membership service in mach-lookup allow list")
}
