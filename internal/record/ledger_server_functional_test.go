//go:build functional

package record_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func ledgerServer(t *testing.T) (*redis.Client, *record.RedisLedger) {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testredis.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	return c, record.NewRedisLedger(c)
}

func ledgerServerImage(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	image := make(map[string]string, len(keys))
	for _, key := range keys {
		kind, err := c.Type(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if kind == "hash" {
			// Rebuilding a large hash can change its DUMP iteration order.
			// Compare every field/value, preserving the full key set as well.
			fields, err := c.HGetAll(ctx, key).Result()
			if err != nil {
				t.Fatal(err)
			}
			value, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			image[key] = "hash:" + string(value)
			continue
		}
		value, err := c.Dump(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		image[key] = kind + ":" + value
	}
	return image
}

// Permission checks run on a real Redis server: miniredis does not implement
// acl_check_cmd or command/key selectors. Every user belongs to this owned server.
func TestLedgerServerPermissionRefusalPreservesWholeBatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		rules []string
		clear bool
	}{
		{name: "hset", rules: []string{"-hset"}},
		{name: "del", rules: []string{"-del"}},
		{name: "later-key-hset", rules: []string{"-hset", "(+hset ~tokens:ledger:2026-09-01 ~tokens:ledger:2026-09-02)"}},
		{name: "clear-needs-restore", rules: []string{"-hset"}, clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			admin, store := ledgerServer(t)
			days := []record.LedgerDay{}
			for n := 1; n <= 3; n++ {
				day := fmt.Sprintf("2026-09-%02d", n)
				days = append(days, record.LedgerDay{Day: day, Entries: []record.LedgerEntry{{Day: day, Card: "before", Model: "model", Repo: "repo"}}})
			}
			if err := store.ReplaceLedgerDays(ctx, days); err != nil {
				t.Fatal(err)
			}
			before := ledgerServerImage(t, admin)
			rules := []string{"on", ">test-only-password", "~*", "&*", "+@all"}
			rules = append(rules, tc.rules...)
			if err := admin.ACLSetUser(ctx, "fixture", rules...).Err(); err != nil {
				t.Fatal(err)
			}
			client := redis.NewClient(&redis.Options{Addr: admin.Options().Addr, Username: "fixture", Password: "test-only-password"})
			t.Cleanup(func() { _ = client.Close() })
			who, err := client.Do(ctx, "ACL", "WHOAMI").Text()
			if err != nil || who != "fixture" {
				t.Fatalf("fixture user=%q (%v)", who, err)
			}
			for i := range days {
				if tc.clear {
					days[i].Entries = nil
				} else {
					days[i].Entries[0].Card = "after"
				}
			}
			err = record.NewRedisLedger(client).ReplaceLedgerDays(ctx, days)
			if err == nil || !strings.Contains(err.Error(), "NOPERM") {
				t.Fatalf("want permission refusal, got %v", err)
			}
			t.Logf("refused before mutation: %v", err)
			if after := ledgerServerImage(t, admin); !reflect.DeepEqual(after, before) {
				t.Fatalf("refused batch changed store: before=%v after=%v", before, after)
			}
		})
	}
}

func largeLedgerDay(day string) record.LedgerDay {
	entries := make([]record.LedgerEntry, 5000)
	for i := range entries {
		entries[i] = record.LedgerEntry{Day: day, Card: fmt.Sprintf("card-%d", i), Model: "model", Repo: "repo", Tokens: [5]int64{int64(i + 1)}, Known: [5]bool{true}}
	}
	return record.LedgerDay{Day: day, Entries: entries}
}

func TestLedgerServerLargeDayWritesEveryRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, store := ledgerServer(t)
	day := largeLedgerDay("2026-09-01")
	if err := record.CheckLedgerDay(day.Day, day.Entries); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceLedgerDays(ctx, []record.LedgerDay{day}); err != nil {
		t.Fatal(err)
	}
	if count, err := c.HLen(ctx, record.LedgerKey(day.Day)).Result(); err != nil || count != 5000 {
		t.Fatalf("rows=%d (%v), want5000", count, err)
	}
	totals, indexed, _, err := store.LedgerReport(ctx, "2026-09", "model")
	if err != nil || indexed != 1 || len(totals) != 1 {
		t.Fatalf("report=%v indexed=%d (%v)", totals, indexed, err)
	}
	if totals[0].Rows != 5000 || totals[0].Tokens[0] != 5000*5001/2 {
		t.Fatalf("large day totals=%+v", totals[0])
	}
}

func TestLedgerServerRecoverableErrorRestoresLargeDay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, store := ledgerServer(t)
	days := []record.LedgerDay{largeLedgerDay("2026-09-01"), {Day: "2026-09-02", Entries: []record.LedgerEntry{{Day: "2026-09-02", Card: "before", Model: "model", Repo: "repo"}}}, {Day: "2026-09-03"}}
	if err := store.ReplaceLedgerDays(ctx, days); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "not-a-hash", "fixture", 0).Err(); err != nil {
		t.Fatal(err)
	}
	before := ledgerServerImage(t, c)
	c.AddHook(&injectEvalMidScriptFailHook{})
	days[0].Entries = days[0].Entries[:1]
	days[0].Entries[0].Card = "after"
	err := store.ReplaceLedgerDays(ctx, days)
	if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Fatalf("want injected recoverable error, got %v", err)
	}
	if after := ledgerServerImage(t, c); !reflect.DeepEqual(after, before) {
		t.Fatal("recoverable error did not restore large prior hash and absent day")
	}
}
