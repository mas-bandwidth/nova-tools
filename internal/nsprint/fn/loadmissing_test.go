//go:build functional

package fn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

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

	if err := fn.LoadMissing(ctx, c); err != nil {
		t.Fatalf("load into an empty store: %v", err)
	}
	if st, err := fn.Check(ctx, c); err != nil || !st.OK() {
		t.Fatalf("after LoadMissing on an empty store: %+v %v, want the embedded library", st, err)
	}

	held := "#!lua name=" + fn.Library + "\nredis.register_function('ns_ping', function() return 'PONG' end)\n" +
		"redis.register_function('ns_held_only', function() return 1 end)\n"
	if err := c.FunctionLoadReplace(ctx, held).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fn.LoadMissing(ctx, c); err != nil {
		t.Fatalf("LoadMissing over a held library: %v", err)
	}
	code, found, err := fn.Loaded(ctx, c)
	if err != nil || !found || code != held {
		t.Fatalf("LoadMissing replaced the held library (found %v, err %v):\n%s", found, err, code)
	}

	// A caller refused FUNCTION LIST is not the deployer: nothing, no error.
	if err := c.Do(ctx, "ACL", "SETUSER", "ns-seat", "reset", "on", ">pw", "~*", "+@all", "-function").Err(); err != nil {
		t.Fatal(err)
	}
	seat := redis.NewClient(&redis.Options{Addr: addr, Username: "ns-seat", Password: "pw"})
	t.Cleanup(func() { _ = seat.Close() })
	if err := fn.LoadMissing(ctx, seat); err != nil {
		t.Fatalf("LoadMissing as a seat without FUNCTION: %v", err)
	}
	if code, _, _ := fn.Loaded(ctx, c); !strings.Contains(code, "ns_held_only") {
		t.Fatal("the seat's LoadMissing changed the library")
	}
}
