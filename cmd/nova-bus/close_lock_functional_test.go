//go:build functional

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/require"
)

// The checkout exclusion follows docs/SPEC-BUS-REPLY.md, refresh step 1: a holder
// prevents writes, and releasing it lets close proceed. The child owns the zero wait
// budget so this parallel test changes no package variable in the parent test process.
func TestCloseRefusesAHeldCheckoutLockThenSucceeds(t *testing.T) {
	t.Parallel()
	const childEnv = "NOVA_BUS_CLOSE_LOCK_TEST_CHILD"
	if os.Getenv(childEnv) != "1" {
		program, err := os.Executable()
		require.NoError(t, err)
		cmd, cancel := subproc.CommandFor(t.Context(), time.Minute, program, "-test.run=^TestCloseRefusesAHeldCheckoutLockThenSucceeds$", "-test.count=1")
		defer cancel()
		cmd.Env = append(os.Environ(), childEnv+"=1")
		output, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "isolated close lock witness: %s", output)
		return
	}

	hermetic(t)
	checkout, _ := busDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(checkout, "from-ada"), 0o755))
	firstBefore := treeBytes(t, filepath.Join(checkout, "from-ada"))
	otherBefore := treeBytes(t, filepath.Join(checkout, "from-bo"))
	oldWait := checkoutLockWait
	checkoutLockWait = 0 // immediate refusal: no polling or wall-clock wait
	defer func() { checkoutLockWait = oldWait }()
	release, err := bus.LockCheckout(checkout, 0)
	require.NoError(t, err)
	defer release()
	args := []string{"close", "--bus", checkout, "--as", "Ada", "--before", "2026-09-08T00:00:00Z", "--remote", "origin", "--branch", "main", "--no-push"}

	refused := invoke(t, "", args...)
	require.Equal(t, 1, refused.code, "close wrote beside a held lock: %s\n%s", refused.stdout, refused.stderr)
	require.Contains(t, refused.stderr, "CLOSE REFUSED:")
	require.Contains(t, refused.stderr, "another nova-bus is already running on this checkout")
	require.NotContains(t, refused.stdout+refused.stderr, "CLOSE OK")
	require.Equal(t, firstBefore, treeBytes(t, filepath.Join(checkout, "from-ada")))
	require.Equal(t, otherBefore, treeBytes(t, filepath.Join(checkout, "from-bo")))
	probeRelease, probeErr := bus.LockCheckout(checkout, 0)
	if probeRelease != nil {
		probeRelease()
	}
	require.ErrorContains(t, probeErr, "another nova-bus is already running on this checkout", "the refused close released the original holder")

	release()
	done := invoke(t, "", args...)
	require.Equal(t, 0, done.code, "close refused after release: %s\n%s", done.stdout, done.stderr)
	require.Contains(t, done.stdout, "CLOSE OK closed=2 kept=0 receipts=1")
	require.NotEqual(t, firstBefore, treeBytes(t, filepath.Join(checkout, "from-ada")))
	require.Equal(t, otherBefore, treeBytes(t, filepath.Join(checkout, "from-bo")))
}
