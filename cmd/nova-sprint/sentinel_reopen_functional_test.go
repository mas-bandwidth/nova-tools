//go:build functional

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// TestReopenedEdgeStaysWaitingUntilReland is the resolver half of #4414's
// fix round: B waits on alpha:sentinel and task:T; alpha lands (B still
// waits on T), A2 reopens alpha, T closes. DEP.resolve re-evaluates every
// edge of B, not only T: B stays waiting, its waits_on names the sentinel,
// ready --why and a take refused for the edge print the same line; alpha's
// second landing releases B and the take claims it.
func TestReopenedEdgeStaysWaitingUntilReland(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	const stop = "alpha:sentinel"
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	if r := sdQueue(t, st, "T", ""); r.Status != task.PushCreated {
		t.Fatalf("push T: %+v", r)
	}
	if r := sdQueue(t, st, "B", stop+";task:T"); r.Status != task.PushCreated || r.Waiting != 2 {
		t.Fatalf("push B: %+v", r)
	}
	sdLand(t, c, "A1")
	if got := sdField(t, c, "B", "waits_on"); got != "task:T" {
		t.Fatalf("B waits_on after alpha landed: %q, want task:T", got)
	}
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "T", As: "f1"})
	if err != nil || !ok {
		t.Fatalf("take T: %v %v", ok, err)
	}
	if got, err := task.Done(ctx, st, task.DoneRequest{Sprint: sdSprint, ID: "T", Token: claim.Token, Evidence: "https://example.test/T"}); err != nil || got != task.DoneClosed {
		t.Fatalf("done T: %s %v", got, err)
	}
	if w, s := sdField(t, c, "B", "where"), sdField(t, c, "B", "state"); w != "waiting" || s != "waiting" {
		t.Fatalf("B after T closed with alpha reopened: where %q state %q, want waiting", w, s)
	}
	if got := sdField(t, c, "B", "waits_on"); got != "task:"+stop {
		t.Fatalf("B waits_on: %q, want task:%s", got, stop)
	}
	if n := c.SIsMember(ctx, "s:"+sdSprint+":waits:task:"+stop, "B").Val(); !n {
		t.Fatalf("B is not in the release index of task:%s", stop)
	}
	why, code := sdWhy(t, c, "B")
	if code != 1 || why != "WAIT task:"+stop+" waiting" {
		t.Fatalf("ready --why B: exit %d %q", code, why)
	}
	sdLand(t, c, "A2")
	if w := sdField(t, c, stop, "where"); w != "landed" {
		t.Fatalf("stop after A2 landed: %s", w)
	}
	if w := sdField(t, c, "B", "where"); w != "ready" {
		t.Fatalf("B after alpha landed again: %s, want ready", w)
	}
	if _, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "B", As: "f1"}); err != nil || !ok {
		t.Fatalf("take B after the reland: %v %v", ok, err)
	}
}

// TestReopenedSentinelTakePrintsReaderLine: a take refused for a
// DEPENDS-ON edge carries ready --why's own line (BlockedError.Wait, which
// `task take --id` prints verbatim), for the single take and the batch
// (ns_task_take_n through TakeAvailable).
func TestReopenedSentinelTakePrintsReaderLine(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	if r := sdQueue(t, st, "B", "alpha:sentinel"); r.Status != task.PushCreated {
		t.Fatalf("push B: %+v", r)
	}
	sdLand(t, c, "A1")
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	why, code := sdWhy(t, c, "B")
	if code != 1 {
		t.Fatalf("ready --why B: exit %d %q", code, why)
	}
	_, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "B", As: "f1"})
	var blocked *task.BlockedError
	if ok || !errors.As(err, &blocked) || blocked.Wait != why {
		t.Fatalf("take B: ok %v err %v; want BlockedError.Wait %q", ok, err, why)
	}
	claims, err := task.TakeAvailable(ctx, st, "f1", sdSprint, "B", 1, "f1", "")
	if len(claims) != 0 || !errors.As(err, &blocked) || blocked.Wait != why {
		t.Fatalf("take --id B (batch): %+v %v; want BlockedError.Wait %q", claims, err, why)
	}
	claims, err = task.TakeAvailable(ctx, st, "f1", sdSprint, "", 4, "f1", "")
	if len(claims) != 0 || err != nil {
		t.Fatalf("take (no id): %+v %v; want B passed over", claims, err)
	}
}

// TestReopenRaceClaimNeverSeesUnmetEdge is probe (d): 20 trials, each a
// take of a released B and the push that reopens its stream's sentinel,
// started together. Each is one FCALL; Redis runs a function to its end
// before the next command, so the take reads the sentinel either wholly
// before the reopen or wholly after. ws:log is one stream both calls append
// to, so its order is the execution order: a CLAIMED B must come before the
// reopen, a refused one after it with ready --why's line.
func TestReopenRaceClaimNeverSeesUnmetEdge(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	if err := c.HSet(ctx, "friend:f1:desired", "slots", 100).Err(); err != nil {
		t.Fatal(err)
	}
	const trials = 20
	claimedFirst, reopenedFirst := 0, 0
	for i := 0; i < trials; i++ {
		stream := fmt.Sprintf("race r%d", i)
		stop := ws.SentinelID(stream)
		a1, a2, b := fmt.Sprintf("R%dA1", i), fmt.Sprintf("R%dA2", i), fmt.Sprintf("R%dB", i)
		if err := sdCard(t, c, a1, stream, ""); err != nil {
			t.Fatal(err)
		}
		if r := sdQueue(t, st, b, stop); r.Status != task.PushCreated {
			t.Fatalf("trial %d push B: %+v", i, r)
		}
		sdLand(t, c, a1)
		if s := sdField(t, c, b, "state"); s != "open" {
			t.Fatalf("trial %d: B not released: %s", i, s)
		}
		var wg sync.WaitGroup
		var ok bool
		var takeErr, pushErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, ok, takeErr = task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: b, As: "f1"})
		}()
		go func() {
			defer wg.Done()
			pushErr = sdCard(t, c, a2, stream, "")
		}()
		wg.Wait()
		if pushErr != nil {
			t.Fatalf("trial %d push A2: %v", i, pushErr)
		}
		claimAt, reopenAt := -1, -1
		for n, e := range c.XRange(ctx, "ws:log", "-", "+").Val() {
			if e.Values["id"] == b && e.Values["to"] == "working" && claimAt < 0 {
				claimAt = n
			}
			if e.Values["id"] == stop && e.Values["from"] == "landed" && e.Values["to"] == "waiting" && reopenAt < 0 {
				reopenAt = n
			}
		}
		if reopenAt < 0 {
			t.Fatalf("trial %d: no reopen of %s on ws:log", i, stop)
		}
		if ok {
			if claimAt < 0 || claimAt > reopenAt {
				t.Fatalf("trial %d: B CLAIMED at ws:log #%d after the reopen at #%d: the edge was unmet at the claim", i, claimAt, reopenAt)
			}
			claimedFirst++
			continue
		}
		var blocked *task.BlockedError
		if !errors.As(takeErr, &blocked) || blocked.Wait != "WAIT task:"+stop+" waiting" || claimAt >= 0 {
			t.Fatalf("trial %d: take refused with %v (claim entry #%d); want WAIT task:%s waiting and no claim", i, takeErr, claimAt, stop)
		}
		reopenedFirst++
	}
	t.Logf("probe (d): %d trials, %d claimed before the reopen, %d refused after it, none claimed on an unmet edge", trials, claimedFirst, reopenedFirst)
}
