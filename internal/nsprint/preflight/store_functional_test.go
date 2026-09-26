//go:build functional

package preflight

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// Check 7.1 needs INFO and FUNCTION, which miniredis does not serve; it runs
// against a throwaway redis-server as the store package's controls do.
func TestPreflightRedisAndLibrary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, aof := range []string{"no", "yes"} {
		// The helper starts with --appendonly no; a later argument wins.
		addr := testutil.Start(t, "--appendonly", aof)
		c := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = c.Close() })
		if aof == "no" {
			if err := fn.Load(ctx, c); err != nil {
				t.Fatal(err)
			}
			if l := checkRedis(ctx, c); !l.Red || !strings.Contains(l.Why, "AOF off") {
				t.Fatalf("aof off: %s", l)
			}
			continue
		}
		if l := checkRedis(ctx, c); !l.Red || !strings.Contains(l.Why, "nova_sprint not loaded") {
			t.Fatalf("no library: %s", l)
		}
		if err := c.FunctionLoadReplace(ctx, "#!lua name=nova_sprint\nredis.register_function('ns_ping', function() return 'OLD' end)\n").Err(); err != nil {
			t.Fatal(err)
		}
		if l := checkRedis(ctx, c); !l.Red || !strings.Contains(l.Why, "not the binary's version") {
			t.Fatalf("old library: %s", l)
		}
		if err := fn.Load(ctx, c); err != nil {
			t.Fatal(err)
		}
		if l := checkRedis(ctx, c); l.Red {
			t.Fatalf("aof on, library at version: %s", l)
		}
	}
}
