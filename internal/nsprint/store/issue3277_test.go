package store_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/stretchr/testify/require"
)

// #3277: Open sends nothing; the caller's first batch is the
// probe, so a one-operation verb pays one round trip, not two.
func TestOpenSendsNoCommand3277(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr, count := testutil.CommandCounter(t)
	for name, open := range map[string]func(context.Context, string) (*store.Store, error){"Open": store.Open} {
		s, err := open(context.Background(), addr)
		if err != nil {
			require.NoError(t, err, "%s: %v", name, err)
		}
		if n := count(); n != 0 {
			require.Equal(t, int64(0), n, "%s sent %d commands before the caller's first batch; want 0", name, n)
		}
		_ = s.Close()
	}
}
