package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/stretchr/testify/require"
)

// storeChildEnv marks the child a store test re-enters itself with: the
// environment under test belongs to that child alone, passed as cmd.Env, so
// no t.Setenv touches this process beside the other tests (the serial
// allowlist's per-test seam: cmd.Env for a child).
const storeChildEnv = "NOVA_NSPRINT_STORE_TEST_CHILD"

// storeChildAddrEnv tells the child which store address its parent prepared:
// the parent owns the listener or the throwaway Redis, the child dials it.
const storeChildAddrEnv = "NOVA_NSPRINT_STORE_TEST_ADDR"

// inStoreTestChild reports whether this run is the child a store test
// re-entered itself with.
func inStoreTestChild(t *testing.T) bool {
	t.Helper()
	return os.Getenv(storeChildEnv) == t.Name()
}

// runStoreTestChild re-enters the test in a child whose whole environment is
// the caller's list, the per-test seam for the process environment: the child
// answers the marker, runs the seam's assertions there, and a failing child
// fails here with its output.
func runStoreTestChild(t *testing.T, env ...string) {
	t.Helper()
	cmd, cancel := subproc.Command(t.Context(), subproc.Tool, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	defer cancel()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "NOVA_TEST_NO_HOST=1", storeChildEnv + "=" + t.Name()}, env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "store test child failed:\n%s", out)
}

// #3277: Open sends nothing; the caller's first batch is the
// probe, so a one-operation verb pays one round trip, not two.
func TestOpenSendsNoCommandBeforeTheCallersFirstBatch(t *testing.T) {
	t.Parallel()
	if inStoreTestChild(t) {
		s, err := store.Open(context.Background(), os.Getenv(storeChildAddrEnv))
		require.NoError(t, err)
		require.NoError(t, s.Close())
		return
	}
	addr, count := testutil.CommandCounter(t)
	runStoreTestChild(t, storeChildAddrEnv+"="+addr, store.UserEnv+"=")
	require.Equal(t, int64(0), count(), "Open sent a command before the caller's first batch")
}
