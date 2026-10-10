//go:build functional

package fn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
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

	if err := fn.LoadMissing(ctx, c); err != nil {
		require.NoError(t, err, "load into an empty store: %v", err)
	}
	source, err := fn.Source()
	if err != nil {
		require.NoError(t, err, err)
	}
	loaded, found, err := fn.Loaded(ctx, c)
	if err != nil || !found || loaded != source {
		require.Failf(t, "assertion failed", "after LoadMissing on an empty store: found %v, err %v, want the embedded library", found, err)
	}

	held := "#!lua name=" + fn.Library + "\nredis.register_function('ns_ping', function() return 'PONG' end)\n" +
		"redis.register_function('ns_held_only', function() return 1 end)\n"
	if err := c.FunctionLoadReplace(ctx, held).Err(); err != nil {
		require.NoError(t, err, err)
	}
	if err := fn.LoadMissing(ctx, c); err != nil {
		require.NoError(t, err, "LoadMissing over a held library: %v", err)
	}
	code, found, err := fn.Loaded(ctx, c)
	if err != nil || !found || code != held {
		require.Failf(t, "assertion failed", "LoadMissing replaced the held library (found %v, err %v):\n%s", found, err, code)
	}

	// A caller refused FUNCTION LIST is not the deployer: nothing, no error.
	if err := c.Do(ctx, "ACL", "SETUSER", "ns-seat", "reset", "on", ">pw", "~*", "+@all", "-function").Err(); err != nil {
		require.NoError(t, err, err)
	}
	seat := redis.NewClient(&redis.Options{Addr: addr, Username: "ns-seat", Password: "pw"})
	t.Cleanup(func() { _ = seat.Close() })
	if err := fn.LoadMissing(ctx, seat); err != nil {
		require.NoError(t, err, "LoadMissing as a seat without FUNCTION: %v", err)
	}
	if code, _, _ := fn.Loaded(ctx, c); !strings.Contains(code, "ns_held_only") {
		require.Contains(t, code, "ns_held_only", "the seat's LoadMissing changed the library")
	}
}
