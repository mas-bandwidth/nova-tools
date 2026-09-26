//go:build functional

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// A sentinel can return to waiting when new work is added to its stream.
// Every claim must agree with ready --why about the currently unmet edge.
func TestReopenedSentinelBlocksQueueClaim_Stella(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"already-released", "another-edge-releases-later"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			c, st := sdStore(t)
			ctx := context.Background()
			const stop = "alpha:sentinel"
			if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
				t.Fatal(err)
			}
			deps := stop
			if mode == "another-edge-releases-later" {
				if r := sdQueue(t, st, "T", ""); r.Status != task.PushCreated {
					t.Fatalf("push T: %+v", r)
				}
				deps += ";task:T"
			}
			if r := sdQueue(t, st, "B", deps); r.Status != task.PushCreated || r.Waiting == 0 {
				t.Fatalf("push B: %+v", r)
			}
			sdLand(t, c, "A1")
			if w := sdField(t, c, stop, "where"); w != "landed" {
				t.Fatalf("stop after first landing: %s", w)
			}
			if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
				t.Fatal(err)
			}
			if w := sdField(t, c, stop, "where"); w != "waiting" {
				t.Fatalf("stop after new work: %s", w)
			}
			if mode == "another-edge-releases-later" {
				claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "T", As: "f1"})
				if err != nil || !ok {
					t.Fatalf("take T: %v %v", ok, err)
				}
				if got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sdSprint, ID: "T", Token: claim.Token, Evidence: "https://example.test/T"}); err != nil || got != task.DoneClosed {
					t.Fatalf("done T: %s %v", got, err)
				}
			}
			why, code := sdWhy(t, c, "B")
			if code != 1 || !strings.Contains(why, "WAIT task:"+stop+" waiting") {
				t.Fatalf("fixture does not expose blocked B: exit %d %s", code, why)
			}
			claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "B", As: "f1"})
			if ok {
				t.Fatalf("ready --why says %q but task.Take CLAIMED %s at attempt %d while %s is waiting (err=%v)", why, claim.ID, claim.Attempt, stop, err)
			}
		})
	}
}

// The same invariant applies to the unified consumer flow, both at dealing
// a ready primary and at starting a copy dealt before the stream reopened.
func TestReopenedSentinelBlocksUnifiedCopies_Stella(t *testing.T) {
	t.Parallel()
	for _, consumer := range []string{"friend:f1", "bench:b1"} {
		for _, phase := range []string{"deal", "work"} {
			t.Run(consumer+"/"+phase, func(t *testing.T) {
				t.Parallel()
				c, st := sdStore(t)
				ctx := context.Background()
				if err := c.HSet(ctx, consumer+":desired", "slots", 4, "paused", "0").Err(); err != nil {
					t.Fatal(err)
				}
				if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
					t.Fatal(err)
				}
				if err := sdCard(t, c, "C", "beta", "alpha:sentinel"); err != nil {
					t.Fatal(err)
				}
				sdLand(t, c, "A1")
				lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test"})
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				if counts, err := (&reconcile.WaitingResolve{Client: c, Out: &out}).Run(ctx, lease); err != nil || counts.Routed != 1 {
					t.Fatalf("resolve: %+v %v %s", counts, err, out.String())
				}
				if w := sdField(t, c, "C", "where"); w != "ready" {
					t.Fatalf("C was not released: %s", w)
				}
				as, err := taskcard.ParseConsumer(consumer)
				if err != nil {
					t.Fatal(err)
				}
				deal := func() ([]taskcard.Dealt, error) {
					return taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"C"}, By: "test"})
				}
				if phase == "work" {
					if copies, err := deal(); err != nil || len(copies) != 1 {
						t.Fatalf("initial deal: %+v %v", copies, err)
					}
				}
				if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
					t.Fatal(err)
				}
				if w := sdField(t, c, "alpha:sentinel", "where"); w != "waiting" {
					t.Fatalf("stop after new work: %s", w)
				}
				if phase == "deal" {
					copies, err := deal()
					if len(copies) > 0 {
						t.Fatalf("%s dealt %+v while C's alpha:sentinel dependency is waiting (err=%v)", consumer, copies, err)
					}
				} else {
					worked, err := taskcard.Work(ctx, c, as, "test", 1, false)
					if len(worked.IDs) > 0 {
						t.Fatalf("%s started %+v while C's alpha:sentinel dependency is waiting (err=%v)", consumer, worked.IDs, err)
					}
				}
			})
		}
	}
}
