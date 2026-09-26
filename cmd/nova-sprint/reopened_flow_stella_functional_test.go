//go:build functional

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// A blocked queued copy is not an active writer. It must not keep the
// consumer idle while also preventing dispatch of the work that unblocks it.
func TestReopenedCopyDoesNotStarveItsPrerequisite_Stella(t *testing.T) {
	t.Parallel()
	for _, consumer := range []string{"friend:f1", "bench:b1"} {
		t.Run(consumer, func(t *testing.T) {
			t.Parallel()
			c, st := sdStore(t)
			ctx := context.Background()
			as, err := taskcard.ParseConsumer(consumer)
			if err != nil {
				t.Fatal(err)
			}
			if err := taskcard.Enroll(ctx, c, as, true); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if err := c.HSet(ctx, as.DesiredKey(), "slots", 1, "paused", 0).Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.HSet(ctx, as.BeatKey(), "at", now.UnixMilli()).Err(); err != nil {
				t.Fatal(err)
			}
			if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
				t.Fatal(err)
			}
			if err := sdCard(t, c, "C", "beta", "alpha:sentinel"); err != nil {
				t.Fatal(err)
			}
			sdLand(t, c, "A1")
			sdAccept(t, c, "alpha")
			lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if counts, err := (&reconcile.WaitingResolve{Client: c, Out: &out}).Run(ctx, lease); err != nil || counts.Routed != 1 {
				t.Fatalf("resolve: %+v %v %s", counts, err, out.String())
			}
			if copies, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"C"}, By: "test"}); err != nil || len(copies) != 1 {
				t.Fatalf("deal C: %+v %v", copies, err)
			}
			if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
				t.Fatal(err)
			}
			if w := sdField(t, c, "alpha:sentinel", "where"); w != "waiting" {
				t.Fatalf("alpha not reopened: %s", w)
			}
			for i := 0; i < 2; i++ {
				pass, err := taskcard.DealPass(ctx, c, "test", now)
				if err != nil {
					t.Fatal(err)
				}
				work, err := taskcard.Work(ctx, c, as, "test", 1, false)
				if err != nil {
					t.Fatal(err)
				}
				if len(work.IDs) > 0 {
					if primary := sdField(t, c, work.IDs[0], "primary"); primary != "A2" {
						t.Fatalf("started %s instead of prerequisite A2", primary)
					}
					return
				}
				t.Logf("pass %d: dealt=%d worked=%d free=%d lines=%v", i, pass.Dealt, len(work.IDs), work.Free, pass.Lines)
			}
			// Positive control: A2 is eligible for this consumer and can run
			// through the ordinary named deal. The automatic dealer stalled.
			copies, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"A2"}, By: "test"})
			if err != nil || len(copies) != 1 {
				t.Fatalf("A2 eligibility control: %+v %v", copies, err)
			}
			work, err := taskcard.Work(ctx, c, as, "test", 1, false)
			if err != nil || len(work.IDs) != 1 || sdField(t, c, work.IDs[0], "primary") != "A2" {
				t.Fatalf("A2 work control: %+v %v", work, err)
			}
			t.Fatal("two automatic passes left the free consumer idle: blocked C~1 occupies its ready count so eligible prerequisite A2 is never dealt; a manual named deal starts A2")
		})
	}
}
