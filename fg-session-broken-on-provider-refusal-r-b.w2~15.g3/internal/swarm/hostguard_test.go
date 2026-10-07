package swarm

import (
	"os"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
	"github.com/stretchr/testify/require"
)

// 47d81e9c put testguard.RefuseHosts on the swarm ssh/scp/rsync seams. Reverting
// bench.go and benchpull.go kept this package green: the class test that
// landed with it lives in internal/ci, not here.

func armHostGuard(t *testing.T) {
	t.Helper()
	// Reload after t.Setenv restores the incoming value. Cleanups are LIFO:
	// register this first so it runs last. Do not Unsetenv: this helper did
	// not own the incoming NOVA_TEST_NO_HOST=1 that make test sets.
	t.Cleanup(testguard.Reload)
	t.Setenv(testguard.EnvNoHost, "1")
	testguard.Reload()
}

// TestHostGuardCleanupRestoresCachedState is the isolation defect on #2152:
// with incoming NOVA_TEST_NO_HOST=1, armHostGuard's Unsetenv+Reload ran
// before t.Setenv restored the variable, so later tests saw env=1 while
// testguard.Refusing() was false. No child, no network.
func TestHostGuardCleanupRestoresCachedState(t *testing.T) {
	t.Cleanup(testguard.Reload)
	t.Setenv(testguard.EnvNoHost, "1")
	testguard.Reload()
	t.Run("arm", func(t *testing.T) {
		armHostGuard(t)
		require.True(t, testguard.Refusing(), "the armed subtest must refuse")
	})
	require.Equal(t, "1", os.Getenv(testguard.EnvNoHost), "environment was %q, want 1", os.Getenv(testguard.EnvNoHost))
	require.True(t, testguard.Refusing(), "environment was restored to 1 but cached host guard remained disabled")
}
