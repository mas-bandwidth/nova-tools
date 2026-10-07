package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/require"
)

const storeTestChildEnv = "NOVA_NSPRINT_STORE_TEST_CHILD"

// Store tests that need process environment run in a child with cmd.Env, per the
// serial allowlist's per-test-seam rule.
func inStoreTestChild(t *testing.T) bool {
	t.Helper()
	return os.Getenv(storeTestChildEnv) == t.Name()
}

func runStoreTestChild(t *testing.T, env ...string) {
	t.Helper()
	cmd, cancel := subproc.Command(t.Context(), subproc.Tool, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	defer cancel()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "NOVA_TEST_NO_HOST=1", storeTestChildEnv + "=" + t.Name()}, env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "store test child failed:\n%s", out)
}

// #3277: Open sends nothing; the caller's first batch is the
// probe, so a one-operation verb pays one round trip, not two.
func TestOpenSendsNoCommandBeforeTheCallersFirstBatch(t *testing.T) {
	t.Parallel()
	if inStoreTestChild(t) {
		addr := os.Getenv("NOVA_NSPRINT_STORE_TEST_ADDR")
		s, err := store.Open(context.Background(), addr)
		require.NoError(t, err)
		require.NoError(t, s.Close())
		return
	}
	addr, count := testutil.CommandCounter(t)
	runStoreTestChild(t, "NOVA_NSPRINT_STORE_TEST_ADDR="+addr, store.UserEnv+"=")
	require.Equal(t, int64(0), count(), "Open sent a command before the caller's first batch")
}
