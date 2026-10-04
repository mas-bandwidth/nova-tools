//go:build functional

package fn_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestLoadMissingNeverReplaces is nova-tools #3620: a verb's load on the way
// to its FCALL (card push, the reconciler) ran FUNCTION LOAD REPLACE with its
// own binary's library, so an older binary took functions the deployed one had
// (ns_fleet_step: "ERR Function not found"). LoadMissing loads an empty store
// and leaves a held library, older or newer, exactly as it is.
func TestLoadMissingNeverReplaces(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })

	require.NoError(t, fn.LoadMissing(ctx, c), "load into an empty store")
	source, err := fn.Source()
	require.NoError(t, err)
	loaded, found, err := fn.Loaded(ctx, c)
	require.NoError(t, err)
	require.True(t, found, "after LoadMissing on an empty store: want the embedded library")
	require.Equal(t, source, loaded, "after LoadMissing on an empty store: want the embedded library")

	held := "#!lua name=" + fn.Library + "\nredis.register_function('ns_ping', function() return 'PONG' end)\n" +
		"redis.register_function('ns_held_only', function() return 1 end)\n"
	require.NoError(t, c.FunctionLoadReplace(ctx, held).Err())
	require.NoError(t, fn.LoadMissing(ctx, c), "LoadMissing over a held library")

	code, found, err := fn.Loaded(ctx, c)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, held, code, "LoadMissing replaced the held library")

	// A caller refused FUNCTION LIST is not the deployer: nothing, no error.
	require.NoError(t, c.Do(ctx, "ACL", "SETUSER", "ns-seat", "reset", "on", ">pw", "~*", "+@all", "-function").Err())
	seat := redis.NewClient(&redis.Options{Addr: addr, Username: "ns-seat", Password: "pw"})
	t.Cleanup(func() { _ = seat.Close() })
	require.NoError(t, fn.LoadMissing(ctx, seat), "LoadMissing as a seat without FUNCTION")

	code, _, _ = fn.Loaded(ctx, c)
	require.Contains(t, code, "ns_held_only", "the seat's LoadMissing changed the library")
}
