//go:build functional

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestSprintAuditOverRealRedis(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Seed keys
	if err := client.Set(ctx, "ws:order", "streams", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "ws:names", "streams", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "task:1", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "task:2", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "s:s1:pool", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "orphan_foo", "val", 0).Err(); err != nil {
		t.Fatal(err)
	}

	// Audit without purge
	code, stdout, stderr := runSprint("sprint", "audit", "--redis", addr)
	if code != 0 {
		t.Fatalf("audit: exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPRINT AUDIT total=6 families=3 orphans=1\n") {
		t.Errorf("stdout missing audit receipt line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "FAMILY s keys=1 pattern=s:*\n") {
		t.Errorf("stdout missing FAMILY s:\n%s", stdout)
	}
	if !strings.Contains(stdout, "FAMILY task keys=2 pattern=task:*\n") {
		t.Errorf("stdout missing FAMILY task:\n%s", stdout)
	}
	if !strings.Contains(stdout, "FAMILY ws keys=2 pattern=ws:*\n") {
		t.Errorf("stdout missing FAMILY ws:\n%s", stdout)
	}
	if !strings.Contains(stdout, "ORPHAN orphan_foo\n") {
		t.Errorf("stdout missing ORPHAN orphan_foo:\n%s", stdout)
	}

	// Purge family task
	code, stdout, stderr = runSprint("sprint", "audit", "--purge", "task", "--redis", addr)
	if code != 0 {
		t.Fatalf("purge task: exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPRINT AUDIT total=4 families=2 orphans=1 purged=2\n") {
		t.Errorf("stdout missing purge receipt:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PURGED family=task keys=2\n") {
		t.Errorf("stdout missing PURGED family line:\n%s", stdout)
	}
	if strings.Contains(stdout, "FAMILY task") {
		t.Errorf("stdout unexpectedly still has FAMILY task after purge:\n%s", stdout)
	}

	// Purge orphans
	code, stdout, stderr = runSprint("sprint", "audit", "--purge", "orphans", "--redis", addr)
	if code != 0 {
		t.Fatalf("purge orphans: exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPRINT AUDIT total=3 families=2 orphans=0 purged=1\n") {
		t.Errorf("stdout missing purge orphans receipt:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PURGED family=orphans keys=1\n") {
		t.Errorf("stdout missing PURGED orphans line:\n%s", stdout)
	}

	// Audit again
	code, stdout, stderr = runSprint("sprint", "audit", "--redis", addr)
	if code != 0 {
		t.Fatalf("final audit: exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "SPRINT AUDIT total=3 families=2 orphans=0\n") {
		t.Errorf("stdout missing final audit receipt:\n%s", stdout)
	}
	if strings.Contains(stdout, "ORPHAN") {
		t.Errorf("stdout unexpectedly contains ORPHAN:\n%s", stdout)
	}
}
