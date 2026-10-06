//go:build darwin

package sandbox

import (
	"strings"
	"testing"
)

// TestDarwinProfileAllowsOpenDirectoryMembershipLookup verifies that the darwin
// profile template includes the com.apple.system.opendirectoryd.membership service
// in the mach-lookup allow list. This prevents sqlite3 and git launches from
// stalling on Open Directory lookups when running inside the sandbox.
func TestDarwinProfileAllowsOpenDirectoryMembershipLookup(t *testing.T) {
	write := t.TempDir()
	home := t.TempDir()
	tmp := t.TempDir()

	p, bad := Build(in(t, write, tmp, home, "/bin/echo"))
	if len(bad) > 0 {
		t.Fatalf("refused at build: %v", bad)
	}

	text, _, err := DarwinProfile(p)
	if err != nil {
		t.Fatalf("DarwinProfile: %v", err)
	}

	if !strings.Contains(text, `com.apple.system.opendirectoryd.membership`) {
		t.Error("profile does not include com.apple.system.opendirectoryd.membership in mach-lookup allow list")
	}
}
