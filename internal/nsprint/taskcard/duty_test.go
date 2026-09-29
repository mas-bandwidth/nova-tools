//go:build functional

package taskcard_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

func TestDutyLifecycleStreamLand(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan")

	d, err := taskcard.StartDuty(ctx, c, taskcard.DutyOpts{
		Stream: "dev",
		Actor:  "rowan",
	})
	if err != nil {
		t.Fatalf("StartDuty: %v", err)
	}
	if d.ID != "dev:land" {
		t.Fatalf("duty ID = %q, want dev:land", d.ID)
	}
	if d.Stream != "dev" || d.Friend != "rowan" || d.EST != taskcard.DefaultLandEST {
		t.Fatalf("unexpected duty: %+v", d)
	}

	// Verify duty:working set
	members := c.SMembers(ctx, taskcard.DutyWorkingKey).Val()
	if len(members) != 1 || members[0] != "dev:land" {
		t.Fatalf("duty:working = %v, want [dev:land]", members)
	}

	// Verify task record in Redis
	h := c.HGetAll(ctx, "task:dev:land").Val()
	if h["where"] != "working" || h["kind"] != "duty" || h["stream"] != "dev" || h["friend"] != "rowan" {
		t.Fatalf("task:dev:land = %v", h)
	}

	// Verify starting while working refuses
	_, err = taskcard.StartDuty(ctx, c, taskcard.DutyOpts{
		Stream: "dev",
		Actor:  "rowan",
	})
	if err == nil || !strings.Contains(err.Error(), "already working") {
		t.Fatalf("expected already working error, got %v", err)
	}

	// Land the duty
	mergeSHA := "0123456789abcdef0123456789abcdef01234567"
	err = taskcard.LandDuty(ctx, c, d.ID, "rowan", mergeSHA, "merge receipt", 42*time.Second)
	if err != nil {
		t.Fatalf("LandDuty: %v", err)
	}

	// Verify duty:working is now empty
	members = c.SMembers(ctx, taskcard.DutyWorkingKey).Val()
	if len(members) != 0 {
		t.Fatalf("duty:working after land = %v, want empty", members)
	}

	h = c.HGetAll(ctx, "task:dev:land").Val()
	if h["where"] != "landed" || h["merge_sha"] != mergeSHA || h["wall"] != "42s" {
		t.Fatalf("task:dev:land after land = %v", h)
	}

	// Starting again now generates suffixed ID since dev:land already exists
	d2, err := taskcard.StartDuty(ctx, c, taskcard.DutyOpts{
		Stream: "dev",
		Actor:  "rowan",
	})
	if err != nil {
		t.Fatalf("StartDuty second run: %v", err)
	}
	if !strings.HasPrefix(d2.ID, "dev:land-") {
		t.Fatalf("duty ID second run = %q, want prefix dev:land-", d2.ID)
	}
}

func TestDutyLifecycleOps(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan")

	d, err := taskcard.StartDuty(ctx, c, taskcard.DutyOpts{
		Stream: "ops",
		Op:     "fn-deploy",
		Actor:  "rowan",
	})
	if err != nil {
		t.Fatalf("StartDuty ops: %v", err)
	}
	if !strings.HasPrefix(d.ID, "ops:fn-deploy-") {
		t.Fatalf("duty ID = %q, want prefix ops:fn-deploy-", d.ID)
	}
	if d.Stream != "ops" || d.EST != taskcard.DefaultOpsEST {
		t.Fatalf("unexpected ops duty: %+v", d)
	}

	// Land ops duty
	err = taskcard.LandDuty(ctx, c, d.ID, "rowan", "-", "FN RECEIPT", 3*time.Second)
	if err != nil {
		t.Fatalf("LandDuty ops: %v", err)
	}

	h := c.HGetAll(ctx, "task:"+d.ID).Val()
	if h["where"] != "landed" || h["stream"] != "ops" || h["wall"] != "3s" {
		t.Fatalf("task record after land = %v", h)
	}
}

func TestDutyCancel(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan")

	d, err := taskcard.StartDuty(ctx, c, taskcard.DutyOpts{
		Stream: "ops",
		Op:     "sprint-clear",
		Actor:  "rowan",
	})
	if err != nil {
		t.Fatalf("StartDuty: %v", err)
	}

	err = taskcard.CancelDuty(ctx, c, d.ID, "rowan", "operator abort")
	if err != nil {
		t.Fatalf("CancelDuty: %v", err)
	}

	members := c.SMembers(ctx, taskcard.DutyWorkingKey).Val()
	if len(members) != 0 {
		t.Fatalf("duty:working after cancel = %v, want empty", members)
	}

	h := c.HGetAll(ctx, "task:"+d.ID).Val()
	if h["where"] != "done" || h["where_ok"] != "fail" {
		t.Fatalf("task record after cancel = %v", h)
	}
}
