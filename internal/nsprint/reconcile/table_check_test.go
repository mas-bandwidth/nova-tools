package reconcile_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func TestTableCheckDutyValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var d *reconcile.TableCheckDuty
	if _, err := d.Run(ctx, &reconcile.Lease{}); err == nil {
		t.Error("nil duty did not return error")
	}

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	duty := reconcile.NewTableCheckDuty(nil)
	if _, err := duty.Run(ctx, &reconcile.Lease{}); err == nil {
		t.Error("nil client did not return error")
	}

	duty = reconcile.NewTableCheckDuty(c)
	if _, err := duty.Run(ctx, nil); err == nil {
		t.Error("nil lease did not return error")
	}
}

func TestTableCheckDutyCadenceAndDrift(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	t0 := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	nowVal := t0

	duty := reconcile.NewTableCheckDuty(c)
	duty.Sprint = "fix"
	duty.Now = func() time.Time { return nowVal }

	// 1. Initial run on clean check: clears drift
	if err := c.HSet(ctx, "s:fix", "drift", "DRIFT").Err(); err != nil {
		t.Fatal(err)
	}

	hasDriftMock := false
	checkCalls := 0
	duty.Check = func(ctx context.Context, client redis.UniversalClient, sprint string, now time.Time) (*table.CheckResult, error) {
		checkCalls++
		res := &table.CheckResult{Sprint: sprint}
		if hasDriftMock {
			res.DriftCount = 1
			res.Cells = []table.CellCheck{{Cell: "ws:swarm:ready", Table: 0, Sets: 1, Drift: true}}
		}
		return res, nil
	}

	// First run at t0: should execute and clear drift
	cnt, err := duty.Run(ctx, &reconcile.Lease{})
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	if cnt.Refused != 0 || checkCalls != 1 {
		t.Fatalf("unexpected counts: cnt=%+v calls=%d", cnt, checkCalls)
	}
	if v, err := c.HGet(ctx, "s:fix", "drift").Result(); err != redis.Nil {
		t.Fatalf("expected drift to be cleared, got %q (err=%v)", v, err)
	}

	// 2. Second run 30s later (within 60s window): should be skipped
	nowVal = t0.Add(30 * time.Second)
	cnt, err = duty.Run(ctx, &reconcile.Lease{})
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if checkCalls != 1 {
		t.Fatalf("second run was not skipped: checkCalls=%d", checkCalls)
	}

	// 3. Third run 60s later: should execute. Now with drift: should write DRIFT to s:fix.
	nowVal = t0.Add(60 * time.Second)
	hasDriftMock = true
	cnt, err = duty.Run(ctx, &reconcile.Lease{})
	if err != nil {
		t.Fatalf("third run failed: %v", err)
	}
	if checkCalls != 2 {
		t.Fatalf("third run did not execute: checkCalls=%d", checkCalls)
	}
	v, err := c.HGet(ctx, "s:fix", "drift").Result()
	if err != nil {
		t.Fatalf("failed to read drift: %v", err)
	}
	if v != "DRIFT" {
		t.Fatalf("expected drift %q, got %q", "DRIFT", v)
	}

	// Verify that ws.Counts and table headline show DRIFT
	if err := c.HSet(ctx, "s:fix", "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "fix"}).Err(); err != nil {
		t.Fatal(err)
	}
	snapCounts, err := (&ws.CountsReader{}).Read(ctx, c, nowVal)
	if err != nil {
		t.Fatalf("counts.Read failed: %v", err)
	}
	if snapCounts.Drift != "DRIFT" {
		t.Errorf("snapCounts.Drift = %q, want DRIFT", snapCounts.Drift)
	}
	if !strings.HasSuffix(snapCounts.Header(), " DRIFT") {
		t.Errorf("snapCounts.Header() = %q, want suffix ' DRIFT'", snapCounts.Header())
	}
}

func TestTableCheckDutyRefusal(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	duty := reconcile.NewTableCheckDuty(c)
	duty.Sprint = "fix"
	duty.Check = func(ctx context.Context, client redis.UniversalClient, sprint string, now time.Time) (*table.CheckResult, error) {
		return nil, errors.New("simulated error")
	}

	cnt, err := duty.Run(context.Background(), &reconcile.Lease{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if cnt.Refused != 1 {
		t.Fatalf("expected cnt.Refused = 1, got %d", cnt.Refused)
	}
}

func TestTableCheckDutyLiveCleanCheck(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	for _, cmd := range table.SprintFixture() {
		args := make([]any, len(cmd))
		for i, v := range cmd {
			args[i] = v
		}
		if err := c.Do(ctx, args...).Err(); err != nil {
			t.Fatal(err)
		}
	}

	// Set drift field to DRIFT before running
	if err := c.HSet(ctx, "s:fix", "drift", "DRIFT").Err(); err != nil {
		t.Fatal(err)
	}

	// Run TableCheckDuty with default nil Check (invokes real table.CheckSets)
	duty := reconcile.NewTableCheckDuty(c)
	duty.Sprint = "fix"
	duty.Now = table.SprintFixtureNow

	cnt, err := duty.Run(ctx, &reconcile.Lease{})
	if err != nil {
		t.Fatalf("duty.Run failed: %v", err)
	}
	if cnt.Refused != 0 {
		t.Fatalf("unexpected cnt.Refused: %d", cnt.Refused)
	}

	// Since fixture is clean, drift should be deleted
	if v, err := c.HGet(ctx, "s:fix", "drift").Result(); err != redis.Nil {
		t.Fatalf("expected drift to be cleared, got %q (err=%v)", v, err)
	}
}
