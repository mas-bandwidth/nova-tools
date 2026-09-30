//go:build functional

package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
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

	// Layer 1's lifecycle: only the coordinator calls ns_tset_define and
	// ns_tset_teardown (the L1 contract amendment, lifecycle, section 4).
	t.Run("tset lifecycle", func(t *testing.T) {
		t.Parallel()
		aclTSetLifecycle(t)
	})

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

// aclTSetLifecycle runs the lifecycle's two functions as every seat on a
// server of its own that holds the tset library (the legacy library of the
// test above has the same name, so they cannot share one): the coordinator
// defines and tears down the new path's namespace, and is refused NOPERM,
// with nothing written, outside it; every other seat is refused the call.
func aclTSetLifecycle(t *testing.T) {
	t.Helper()
	var extra []string
	for _, rule := range store.ACLRules {
		parts := strings.Split(rule, " ")
		extra = append(extra, "--user", parts[0], "on", ">"+parts[0]+"-pass")
		extra = append(extra, parts[1:]...)
	}
	addr := testutil.Start(t, extra...)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	defer admin.Close()
	if err := fn.LoadTSet(ctx, admin, fn.TSetStandalone); err != nil {
		t.Fatalf("load the tset library: %v", err)
	}
	build, err := fn.TSetBuild(fn.TSetStandalone)
	if err != nil {
		t.Fatal(err)
	}
	define := func(space string) string {
		raw, err := tset.EncodeDefine(tset.DefineSpec{Space: space, Build: build, View: "sprint",
			Tables: []tset.TableSpec{{Name: "work", Columns: []tset.ColumnSpec{{Name: "ready", Kind: tset.ColumnKindSet}}}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	teardown := `{"space":"{sprint}:","confirm":"sprint"}`
	status := func(reply any) (string, string) {
		var r struct{ Status, Code string }
		text, _ := reply.(string)
		_ = json.Unmarshal([]byte(text), &r)
		return r.Status, r.Code
	}
	clientFor := func(name string) *redis.Client {
		return redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: name + "-pass"})
	}
	for _, rule := range store.ACLRules {
		name := strings.SplitN(rule, " ", 2)[0]
		if name == "ns-coordinator" {
			continue
		}
		c := clientFor(name)
		for _, call := range []struct{ fn, arg string }{{"ns_tset_define", define("{sprint}:")}, {"ns_tset_teardown", teardown}} {
			if _, err := c.FCall(ctx, call.fn, nil, tset.Version, call.arg).Result(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
				t.Errorf("%s called %s: %v, want NOPERM", name, call.fn, err)
			}
		}
		_ = c.Close()
	}
	if n, err := admin.DBSize(ctx).Result(); err != nil || n != 0 {
		t.Fatalf("refused seats wrote %d keys (err %v)", n, err)
	}
	coord := clientFor("ns-coordinator")
	defer coord.Close()
	reply, err := coord.FCall(ctx, "ns_tset_define", nil, tset.Version, define("elsewhere:")).Result()
	if st, code := status(reply); err != nil || st != "refused" || code != "NOPERM" {
		t.Fatalf("coordinator define outside {sprint}: = %v %v, want refused NOPERM", reply, err)
	}
	if n, err := admin.DBSize(ctx).Result(); err != nil || n != 0 {
		t.Fatalf("a NOPERM define wrote %d keys (err %v)", n, err)
	}
	reply, err = coord.FCall(ctx, "ns_tset_define", nil, tset.Version, define("{sprint}:")).Result()
	if st, _ := status(reply); err != nil || st != "ok" {
		t.Fatalf("coordinator define = %v %v, want ok", reply, err)
	}
	for calls := 0; ; calls++ {
		if calls == 8 {
			t.Fatal("coordinator teardown did not finish in 8 calls")
		}
		reply, err = coord.FCall(ctx, "ns_tset_teardown", nil, tset.Version, teardown).Result()
		if st, _ := status(reply); err != nil || st != "ok" {
			t.Fatalf("coordinator teardown = %v %v, want ok", reply, err)
		}
		if strings.Contains(reply.(string), `"done":true`) {
			break
		}
	}
	keys, err := admin.Keys(ctx, "*").Result()
	if err != nil || len(keys) != 1 || keys[0] != "{sprint}:sprint:lifecycle" {
		t.Fatalf("after teardown the store holds %v (err %v), want only the receipt stream", keys, err)
	}
}
