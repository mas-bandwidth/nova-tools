//go:build functional

package ci

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// The COST line lands on a real ci:cost stream: one entry per run, the
// receipt's identity and every job on it, and a second run of the same head
// is a second entry, never an overwrite. A throwaway redis-server, the way
// every functional control here starts one; never the fleet store.
func TestCostLineLandsOnTheStream(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	c := CostFromJobs(costFixture(t))
	r := costReceipt()
	first, err := WriteCost(ctx, client, &r, c)
	if err != nil {
		t.Fatal(err)
	}
	rerun := costReceipt()
	rerun.RunID = "778"
	second, err := WriteCost(ctx, client, &rerun, c)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two runs wrote one entry %s", first)
	}
	entries, err := client.XRange(ctx, CostStream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%s holds %d entries, want 2", CostStream, len(entries))
	}
	got := entries[0].Values
	for k, want := range map[string]string{"repo": "mas-bandwidth/nova-tools", "sha": costFixtureSHA, "run_id": "777",
		"spin": "75", "total": "150", "jobs": "5", "job:e2e:2": "e2e:33:success:2:rerun"} {
		if got[k] != want {
			t.Errorf("entry %s: %s = %v, want %q", entries[0].ID, k, got[k], want)
		}
	}
	if entries[1].Values["run_id"] != "778" {
		t.Errorf("the second entry names run %v, want 778", entries[1].Values["run_id"])
	}
	keys, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != CostStream {
		t.Errorf("the write touched %v, want only %s", keys, CostStream)
	}
}
