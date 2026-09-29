//go:build functional

package audit

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestAuditAndPurgeFunctionalRealRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Seed real redis keys
	if err := client.Set(ctx, "ws:order", "streams", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "ws:names", "streams", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "task:t1", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "task:t2", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "card:c1", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "s:sprint-1:pool", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "unowned_orphan_one", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "unowned_orphan_two", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}

	reg := DefaultRegistry()
	report, err := reg.Audit(ctx, client)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}

	if report.TotalKeys != 8 {
		t.Fatalf("TotalKeys = %d; want 8", report.TotalKeys)
	}
	if report.FamilyCounts["ws"] != 2 {
		t.Errorf("ws = %d; want 2", report.FamilyCounts["ws"])
	}
	if report.FamilyCounts["task"] != 2 {
		t.Errorf("task = %d; want 2", report.FamilyCounts["task"])
	}
	if report.FamilyCounts["card"] != 1 {
		t.Errorf("card = %d; want 1", report.FamilyCounts["card"])
	}
	if report.FamilyCounts["s"] != 1 {
		t.Errorf("s = %d; want 1", report.FamilyCounts["s"])
	}
	if len(report.OrphanKeys) != 2 {
		t.Fatalf("orphan keys len = %d; want 2", len(report.OrphanKeys))
	}
	sort.Strings(report.OrphanKeys)
	if report.OrphanKeys[0] != "unowned_orphan_one" || report.OrphanKeys[1] != "unowned_orphan_two" {
		t.Errorf("orphans = %v; want [unowned_orphan_one, unowned_orphan_two]", report.OrphanKeys)
	}

	// Purge family "task"
	deleted, err := reg.Purge(ctx, client, "task")
	if err != nil {
		t.Fatalf("Purge(task): %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d; want 2", deleted)
	}

	// Purge orphans
	delOrphans, err := reg.Purge(ctx, client, "orphans")
	if err != nil {
		t.Fatalf("Purge(orphans): %v", err)
	}
	if delOrphans != 2 {
		t.Errorf("deleted orphans = %d; want 2", delOrphans)
	}

	// Check final state
	finalReport, err := reg.Audit(ctx, client)
	if err != nil {
		t.Fatalf("final Audit: %v", err)
	}
	if finalReport.TotalKeys != 4 {
		t.Errorf("final TotalKeys = %d; want 4", finalReport.TotalKeys)
	}
	if len(finalReport.OrphanKeys) != 0 {
		t.Errorf("final OrphanKeys = %v; want []", finalReport.OrphanKeys)
	}
}
