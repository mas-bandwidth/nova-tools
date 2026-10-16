//go:build functional

// A real ansible-playbook run against the toolchains fixture, under a wall-clock
// budget: a functional test, not a unit test (the owner, 2026-10-16: a unit test
// tests basic logic and code, calls no program and is not sensitive to timing).
// On hosted runners these timed out and held the v1.4.6 cut (run 38974382538).

package baddigest

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/fleet/internal/toolchainfixture"
)

func TestToolchainsPlayPreservesInstallOnChecksumFailure(t *testing.T) {
	t.Parallel()
	toolchainfixture.Check(t, "../..", "bad-digest")
}
