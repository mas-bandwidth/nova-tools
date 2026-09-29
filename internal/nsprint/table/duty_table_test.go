package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func TestTableShowsCurrentDutyAndAlarm(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 18, 0, 0, 0, time.UTC)

	c.SAdd(ctx, "friends", "rowan")
	c.HSet(ctx, "friend:rowan:beat", "at", strconv.FormatInt(now.UnixMilli(), 10), "cpu", "5.0")
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: "dev"}, redis.Z{Score: 2, Member: "ops"})
	c.ZAdd(ctx, ws.KeyAt(0, "dev", ws.Working), redis.Z{Score: 1, Member: "c1"})
	c.ZAdd(ctx, ws.KeyAt(0, "ops", ws.Landed), redis.Z{Score: 1, Member: "ops:fn-deploy-1"})

	// Duty created 20 minutes ago with 15m EST -> Overdue!
	createdAt := now.Add(-20 * time.Minute)
	c.SAdd(ctx, "duty:working", "dev:land")
	c.HSet(ctx, "task:dev:land", map[string]any{
		"where":      "working",
		"kind":       "duty",
		"stream":     "dev",
		"friend":     "rowan",
		"est":        "15m",
		"created_at": strconv.FormatInt(createdAt.UnixMilli(), 10),
	})

	reader := table.NewSprintReader(c, table.SprintConfig{Friends: []string{"rowan"}})
	snap, err := reader.Read(ctx, now)
	if err != nil {
		t.Fatalf("reader.Read: %v", err)
	}

	// Verify friend row displays duty
	if len(snap.Consumers) != 1 {
		t.Fatalf("len(Consumers) = %d, want 1", len(snap.Consumers))
	}
	friendRow := snap.Consumers[0]
	if friendRow.Duty != "dev:land" {
		t.Fatalf("friendRow.Duty = %q, want dev:land", friendRow.Duty)
	}
	if friendRow.Status() != "duty" {
		t.Fatalf("friendRow.Status() = %q, want duty", friendRow.Status())
	}

	rendered := snap.Render(now)

	// Friend row in table should show "rowan dev:land" and "duty"
	if !strings.Contains(rendered, "rowan dev:land") {
		t.Errorf("rendered table should contain 'rowan dev:land':\n%s", rendered)
	}
	if !strings.Contains(rendered, "duty") {
		t.Errorf("rendered table should contain status 'duty':\n%s", rendered)
	}

	// Stream ops row should be rendered because it has landed cards
	if !strings.Contains(rendered, "ops") {
		t.Errorf("rendered table should contain stream 'ops':\n%s", rendered)
	}

	// Header should contain land: dev 20m0s
	if !strings.Contains(rendered, "land: dev 20m0s") {
		t.Errorf("rendered table should contain 'land: dev 20m0s':\n%s", rendered)
	}

	// Overdue duty should raise ALARM line
	wantAlarm := "ALARM duty=dev:land age=20m0s est=15m0s"
	if !strings.Contains(rendered, wantAlarm) {
		t.Errorf("rendered table should contain %q:\n%s", wantAlarm, rendered)
	}
}
