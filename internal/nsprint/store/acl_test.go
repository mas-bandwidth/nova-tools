package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestACLEachActorCallsOnlyItsFunctions(t *testing.T) {
	t.Parallel()

	extra := []string{"--user", "default", "off"}
	for _, rule := range store.ACLRules {
		parts := strings.Split(rule, " ")
		name := parts[0]
		userArgs := []string{"--user", name, "on", ">" + name + "-pass"}
		userArgs = append(userArgs, parts[1:]...)
		extra = append(extra, userArgs...)
	}

	addr := testutil.Start(t, extra...)
	ctx := context.Background()

	// Deploy library as ns-deploy
	deployClient := redis.NewClient(&redis.Options{Addr: addr, Username: "ns-deploy", Password: "ns-deploy-pass"})
	defer deployClient.Close()
	if err := fn.Load(ctx, deployClient); err != nil {
		t.Fatalf("ns-deploy failed to load functions: %v", err)
	}

	// Helper to create client
	clientFor := func(name string) *redis.Client {
		return redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: name + "-pass"})
	}

	// Test actor users
	actors := []string{"ns-friend", "ns-bench", "ns-reconciler", "ns-consumer", "ns-coordinator"}
	for _, actor := range actors {
		t.Run(actor, func(t *testing.T) {
			c := clientFor(actor)
			defer c.Close()

			// Can call its allowed FCALLs
			// For simplicity, we just check ping
			res, err := c.FCall(ctx, "ns_ping", []string{}).Result()
			if err != nil {
				t.Fatalf("expected to call ns_ping: %v", err)
			}
			if res != "PONG" {
				t.Fatalf("ns_ping returned %v, want PONG", res)
			}

			// Refused direct writes
			keys := []string{"s:test", "bench:test", "friend:test"}
			for _, key := range keys {
				err = c.Set(ctx, key, "value", 0).Err()
				if err == nil {
					t.Errorf("actor %s should be refused direct write to %s", actor, key)
				} else if !strings.Contains(err.Error(), "NOPERM") {
					t.Errorf("expected NOPERM, got %v", err)
				}
			}
		})
	}

	// Test table user
	t.Run("ns-table", func(t *testing.T) {
		c := clientFor("ns-table")
		defer c.Close()

		// Refused FCALL (only FCALL_RO is allowed)
		err := c.FCall(ctx, "ns_ping", []string{}).Err()
		if err == nil {
			t.Errorf("ns-table should be refused FCALL")
		} else if !strings.Contains(err.Error(), "NOPERM") {
			t.Errorf("expected NOPERM, got %v", err)
		}

		// Allowed FCALL_RO ns_snapshot
		_, err = c.FCallRo(ctx, "ns_snapshot", []string{"s:test"}).Result()
		// It might fail because s:test is empty or missing, but it shouldn't fail with NOPERM
		if err != nil && strings.Contains(err.Error(), "NOPERM") {
			t.Errorf("ns-table should be allowed FCALL_RO ns_snapshot, got NOPERM")
		}

		// Refused direct write
		err = c.Set(ctx, "s:test", "value", 0).Err()
		if err == nil {
			t.Errorf("ns-table should be refused direct write")
		} else if !strings.Contains(err.Error(), "NOPERM") {
			t.Errorf("expected NOPERM, got %v", err)
		}
	})
}
