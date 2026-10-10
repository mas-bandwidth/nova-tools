//go:build functional

package swarm

import (
	"os"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/testguard"
	"github.com/stretchr/testify/require"
)

// 47d81e9c put testguard.RefuseHosts on the swarm ssh/scp/rsync seams. Reverting
// bench.go and benchpull.go kept this package green: the class test that
// landed with it lives in internal/ci, not here.
//
// The value under test below is the process-wide guard's cached state against the
// process's environment, which has no per-test seam, so this file is the functional
// tier's (the serial-tests ledger: "The way off this list is a per-test seam"; a test
// that needs a real process-wide resource and cannot be seamed goes to the functional
// tier instead).

// armHostGuard arms the process-wide guard for one test and puts the environment back.
// The Reload is registered FIRST so it runs LAST -- cleanups are LIFO, and the guard must
// re-read the environment AFTER the restore puts it back, so the cached value always
// matches what the environment says (#2152). The test only ever sets the variable to the
// value the suite already runs under (the Makefile sets NOVA_TEST_NO_HOST=1 for every
// tier), so a parallel sibling sees no change. Do not Unsetenv: this helper did not own
// the incoming value.
func armHostGuard(t *testing.T) {
	t.Helper()
	prev, had := os.LookupEnv(testguard.EnvNoHost)
	t.Cleanup(testguard.Reload)
	t.Cleanup(func() {
		if had {
			os.Setenv(testguard.EnvNoHost, prev) // ignored: the restore of the set below; nothing can act on a failure to put the test's own value back
			return
		}
		os.Unsetenv(testguard.EnvNoHost) // ignored: the restore of the set below; the guard's Reload beside this cleanup is what the later tests read
	})
	os.Setenv(testguard.EnvNoHost, "1") // ignored: the value the Makefile sets for every tier; the guard's own Reload below is what this test reads back
	testguard.Reload()
}

// TestHostGuardCleanupRestoresCachedState is the isolation defect on #2152:
// with incoming NOVA_TEST_NO_HOST=1, an arming helper's Unsetenv+Reload ran
// before the environment was restored, so later tests saw env=1 while the
// guard stayed disabled. The guard's armed state is not exported, so the test
// reads the environment, which is what Reload reads the armed state from. No
// child, no network.
func TestHostGuardCleanupRestoresCachedState(t *testing.T) {
	t.Parallel()

	armHostGuard(t)
	t.Run("arm", func(t *testing.T) {
		armHostGuard(t)
		require.Equal(t, "1", os.Getenv(testguard.EnvNoHost), "the armed subtest must run under %s=1", testguard.EnvNoHost)
	})
	require.Equal(t, "1", os.Getenv(testguard.EnvNoHost), "environment was %q, want 1: the armed subtest's cleanups must leave the guard armed for every later test", os.Getenv(testguard.EnvNoHost))
}
