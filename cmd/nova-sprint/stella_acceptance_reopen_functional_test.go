//go:build functional

package main

// Stella's combined behavior test (stella-7555eac2ed06, 2026-09-26 21:24 UTC),
// copied unchanged: #4412's acceptance with #4414's release and reopen.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// #4412 acceptance plus #4414 release/reopen: only acceptance releases
// downstream work, and reopening prevents previously queued work starting.
func TestStellaAcceptanceReleasesThenReopenBlocksBothQueues(t *testing.T) {
	t.Parallel()
	for _, consumer := range []string{"friend:f1", "bench:b1"} {
		t.Run(consumer, func(t *testing.T) {
			t.Parallel()
			c, st := sdStore(t)
			ctx := context.Background()
			if err := c.HSet(ctx, "friend:rowan:roles", "roles", "coordinator").Err(); err != nil {
				t.Fatal(err)
			}
			if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
				t.Fatal(err)
			}
			if err := sdCard(t, c, "C", "beta", "alpha:sentinel"); err != nil {
				t.Fatal(err)
			}
			if r := sdQueue(t, st, "B", "alpha:sentinel"); r.Status != task.PushCreated || r.Waiting != 1 {
				t.Fatalf("queue B: %+v", r)
			}
			sdLand(t, c, "A1")
			if sdField(t, c, "alpha:sentinel", "where") != "waiting" || sdField(t, c, "B", "waits_on") == "" {
				t.Fatal("last member released the stream before acceptance")
			}
			if _, err := taskcard.Land(ctx, c, "alpha:sentinel", "f1", sdSHA, "not coordinator"); err == nil {
				t.Fatal("non-coordinator accepted stream")
			}
			lease := sdLease(t, st)
			if _, routed, err := sdResolve(c, lease); err != nil || routed != 0 {
				t.Fatalf("resolve before acceptance: %d %v", routed, err)
			}
			if _, err := taskcard.Land(ctx, c, "alpha:sentinel", "rowan", sdSHA, "accepted integrated stream"); err != nil {
				t.Fatal(err)
			}
			if sdField(t, c, "B", "state") != "open" || sdField(t, c, "B", "waits_on") != "" {
				t.Fatal("acceptance failed to release task-queue B in the landing call")
			}
			if _, routed, err := sdResolve(c, lease); err != nil || routed != 1 {
				t.Fatalf("resolve after acceptance: %d %v", routed, err)
			}
			as, err := taskcard.ParseConsumer(consumer)
			if err != nil {
				t.Fatal(err)
			}
			if err := taskcard.Enroll(ctx, c, as, true); err != nil {
				t.Fatal(err)
			}
			if err := c.HSet(ctx, as.DesiredKey(), "slots", 1).Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.HSet(ctx, as.BeatKey(), "at", time.Now().UnixMilli()).Err(); err != nil {
				t.Fatal(err)
			}
			copies, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"C"}, By: "rowan"})
			if err != nil || len(copies) != 1 {
				t.Fatalf("deal C: %+v %v", copies, err)
			}
			if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
				t.Fatal(err)
			}
			if sdField(t, c, "alpha:sentinel", "where") != "waiting" {
				t.Fatal("new member failed to reopen stream")
			}
			if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "B", As: "f1"}); ok || err == nil || !strings.Contains(err.Error(), "alpha:sentinel") {
				t.Fatalf("queue B started after reopen: ok=%v err=%v", ok, err)
			}
			if _, err := taskcard.Work(ctx, c, as, "rowan", 1, false, copies[0].Copy); err == nil || !strings.Contains(err.Error(), "alpha:sentinel") {
				t.Fatalf("consumer C started after reopen: %v", err)
			}
		})
	}
}
