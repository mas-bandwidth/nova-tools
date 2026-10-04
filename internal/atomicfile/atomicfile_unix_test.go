//go:build !windows

package atomicfile

import (
	"syscall"
	"testing"
)

// The umask and ExactMode permission tests each run their scenario in a fresh
// subprocess (the rig's rerun) so a process-wide umask touches no parallel
// test; the rig in rig_test.go owns that seam, the write and the permission
// check, and the lines below are the scenarios.

func TestUmaskHonored(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.rerun("GO_TEST_SUBPROCESS_UMASK=077", func() {
		syscall.Umask(0o077)
		r.permIs(r.write("umask_077_test.txt", "private content\n", 0o644), 0o600)
	})
}

func TestUmaskGroupWritableHonored(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.rerun("GO_TEST_SUBPROCESS_UMASK=002", func() {
		syscall.Umask(0o002)
		r.permIs(r.write("umask_002_test.txt", "group content\n", 0o666), 0o664)
	})
}

func TestExactModeBypassesUmask(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.rerun("GO_TEST_SUBPROCESS_EXACT_MODE=077", func() {
		syscall.Umask(0o077)
		r.permIs(r.write("exact_mode_077_test.txt", "exact content\n", 0o644, ExactMode()), 0o644)
	})
}
