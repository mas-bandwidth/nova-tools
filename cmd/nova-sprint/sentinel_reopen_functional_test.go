//go:build functional

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
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

// TestReopenRaceClaimNeverSeesUnmetEdge is probe (d), both orders of a take
// of a released row and the push that reopens its stream's sentinel. Each
// is one FCALL, and Redis runs a function to its end before the next
// command, so a take reads the sentinel wholly before or wholly after the
// reopen:
//   - claim-first: the take claims (the edge is met at the claim); after the
//     reopen that row stays claimed (the NOT DONE boundary: running work is
//     the dialogue/checkpoint's), and a later take of another row on the
//     same edge is refused with ready --why's line.
//   - reopen-first: the take is refused with ready --why's line.
//   - interleave: 20 trials started together; ws:log, one stream both calls
//     append to, gives the execution order: a CLAIMED row comes before the
//     reopen, a refused one after it.
func TestReopenRaceClaimNeverSeesUnmetEdge(t *testing.T) {
	t.Parallel()
	// setup: stream alpha with card A1 landed, rows (each DEPENDS-ON the
	// stream's sentinel) released to open
	setup := func(t *testing.T, stream string, rows ...string) (*redis.Client, *store.Store, string) {
		c, st := sdStore(t)
		if err := c.HSet(context.Background(), "friend:f1:desired", "slots", 100).Err(); err != nil {
			t.Fatal(err)
		}
		stop := ws.SentinelID(stream)
		if err := sdCard(t, c, stream+"-A1", stream, ""); err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if res := sdQueue(t, st, r, stop); res.Status != task.PushCreated {
				t.Fatalf("push %s: %+v", r, res)
			}
		}
		sdLand(t, c, stream+"-A1")
		for _, r := range rows {
			if s := sdField(t, c, r, "state"); s != "open" {
				t.Fatalf("%s not released: %s", r, s)
			}
		}
		return c, st, stop
	}
	take := func(st *store.Store, id string) (bool, error) {
		_, ok, err := task.Take(context.Background(), st, task.TakeRequest{Sprint: sdSprint, ID: id, As: "f1"})
		return ok, err
	}
	refusedWith := func(t *testing.T, err error, want string) {
		t.Helper()
		var blocked *task.BlockedError
		if !errors.As(err, &blocked) || blocked.Wait != want {
			t.Fatalf("take refused with %v; want BlockedError.Wait %q", err, want)
		}
	}

	t.Run("claim-first", func(t *testing.T) {
		t.Parallel()
		c, st, stop := setup(t, "alpha", "B", "C")
		if ok, err := take(st, "B"); err != nil || !ok {
			t.Fatalf("take B with alpha landed: %v %v", ok, err)
		}
		if err := sdCard(t, c, "alpha-A2", "alpha", ""); err != nil {
			t.Fatal(err)
		}
		if w := sdField(t, c, stop, "where"); w != "waiting" {
			t.Fatalf("stop after the reopen: %s", w)
		}
		// the NOT DONE boundary, asserted: the row claimed before the reopen
		// stays claimed
		if s := sdField(t, c, "B", "state"); s != "claimed" {
			t.Fatalf("B after the reopen: %s, want claimed (running work is not recalled)", s)
		}
		why, code := sdWhy(t, c, "C")
		if code != 1 || why != "WAIT task:"+stop+" waiting" {
			t.Fatalf("ready --why C: exit %d %q", code, why)
		}
		ok, err := take(st, "C")
		if ok {
			t.Fatalf("take C after the reopen claimed")
		}
		refusedWith(t, err, why)
		t.Logf("claim-first: B claimed before the reopen stays %s; C after it: %s", sdField(t, c, "B", "state"), why)
	})

	t.Run("reopen-first", func(t *testing.T) {
		t.Parallel()
		c, st, stop := setup(t, "alpha", "B")
		if err := sdCard(t, c, "alpha-A2", "alpha", ""); err != nil {
			t.Fatal(err)
		}
		ok, err := take(st, "B")
		if ok {
			t.Fatalf("take B after the reopen claimed")
		}
		refusedWith(t, err, "WAIT task:"+stop+" waiting")
		if s := sdField(t, c, "B", "state"); s != "open" {
			t.Fatalf("B after the refused take: %s, want open (nothing written)", s)
		}
		t.Logf("reopen-first: B refused, WAIT task:%s waiting", stop)
	})

	t.Run("interleave", func(t *testing.T) {
		t.Parallel()
		const trials = 20
		c, st := sdStore(t)
		ctx := context.Background()
		if err := c.HSet(ctx, "friend:f1:desired", "slots", 100).Err(); err != nil {
			t.Fatal(err)
		}
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
			var wg sync.WaitGroup
			var ok bool
			var takeErr, pushErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				ok, takeErr = take(st, b)
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
			refusedWith(t, takeErr, "WAIT task:"+stop+" waiting")
			if claimAt >= 0 {
				t.Fatalf("trial %d: refused take left a claim entry #%d", i, claimAt)
			}
			reopenedFirst++
		}
		t.Logf("interleave: %d trials, %d claimed before the reopen, %d refused after it, none claimed on an unmet edge", trials, claimedFirst, reopenedFirst)
	})
}

// TestSprintCardDealRefusesReopenedSentinel: the sprint-card deal FCALL
// (ns_card_deal, deal.lua) judges a card's task and sentinel edges itself
// (NS.dep.first_blocker, edges only), whatever the Go gate read before it:
// a pooled card on alpha:sentinel is not dealt while alpha is reopened and
// is dealt once alpha lands again; a card whose bare label names a sprint
// card is left to the Go gate and dealt.
func TestSprintCardDealRefusesReopenedSentinel(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	ctx := context.Background()
	const bench, fence = "b1", "fence-4414"
	for _, cmd := range [][]any{
		{"SADD", "benches", bench},
		{"HSET", "bench:" + bench + ":desired", "slots", "4"},
		{"HSET", "bench:" + bench + ":state", "state", "UP"},
		{"HSET", "lease:reconciler", "token", fence},
	} {
		if err := c.Do(ctx, cmd...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	sdSprintCard(t, c, "Q", "task:alpha:sentinel")
	sdSprintCard(t, c, "L", "Q9")
	sdLand(t, c, "A1")
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	fns := &reconcile.DealFunctions{Client: c, Actor: "reconciler"}
	res, err := fns.Reserve(ctx, fence, bench, []deal.Card{{Sprint: sdSprint, Label: "Q"}, {Sprint: sdSprint, Label: "L"}})
	if err != nil {
		t.Fatal(err)
	}
	var dealt []string
	for _, r := range res {
		dealt = append(dealt, r.Card.Label)
	}
	if len(dealt) != 1 || dealt[0] != "L" {
		t.Fatalf("ns_card_deal with alpha reopened dealt %v; want [L] (Q waits on task:alpha:sentinel)", dealt)
	}
	if s := c.HGet(ctx, "s:"+sdSprint+":card:Q", "state").Val(); s != "queued" {
		t.Fatalf("Q after the refused deal: %s, want queued", s)
	}
	sdLand(t, c, "A2")
	res, err = fns.Reserve(ctx, fence, bench, []deal.Card{{Sprint: sdSprint, Label: "Q"}})
	if err != nil || len(res) != 1 {
		t.Fatalf("ns_card_deal with alpha landed again: %+v %v", res, err)
	}
	t.Logf("sprint-card deal: reopened alpha dealt %v, relanded alpha dealt Q", dealt)
}

// TestReopenedSentinelEveryDoorPrintsReaderLine: one spelling of the edge
// on every line (#4414 round 3). With alpha reopened, ready --why prints
// WAIT task:alpha:sentinel waiting for a queue row (DEPENDS-ON
// task:alpha:sentinel) and for stream cards (blocked_on alpha:sentinel)
// alike, and every claim door's refusal carries that line byte for byte:
// task take (the whole line), card take, card deal and named card work
// (DEPENDS task:<id> then the line).
func TestReopenedSentinelEveryDoorPrintsReaderLine(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	const want = "WAIT task:alpha:sentinel waiting"
	if err := sdCard(t, c, "A1", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"D", "E", "F"} {
		if err := sdCard(t, c, id, "beta-"+strings.ToLower(id), "alpha:sentinel"); err != nil {
			t.Fatal(err)
		}
	}
	sdQueue(t, st, "B", "alpha:sentinel")
	sdLand(t, c, "A1")
	if _, _, err := sdResolve(c, sdLease(t, st)); err != nil {
		t.Fatal(err)
	}
	as, err := taskcard.ParseConsumer("friend:f1")
	if err != nil {
		t.Fatal(err)
	}
	dd, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"D"}, By: "test"})
	if err != nil || len(dd) != 1 {
		t.Fatalf("deal D while landed: %+v %v", dd, err)
	}
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"B", "E", "F"} {
		if why, code := sdWhy(t, c, id); code != 1 || why != want {
			t.Fatalf("ready --why %s: exit %d %q, want %q", id, code, why, want)
		}
	}
	door := func(name, id string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s %s: let through with alpha reopened", name, id)
		}
		_, line, ok := strings.Cut(err.Error(), "DEPENDS task:"+id+" ")
		if !ok || line != want {
			t.Fatalf("%s %s: %q; want DEPENDS task:%s then %q byte for byte", name, id, err.Error(), id, want)
		}
		t.Logf("%s %s: DEPENDS task:%s %s", name, id, id, line)
	}
	_, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sdSprint, ID: "B", As: "f1"})
	var blocked *task.BlockedError
	if ok || !errors.As(err, &blocked) || blocked.Wait != want {
		t.Fatalf("task take B: ok %v err %v; want %q", ok, err, want)
	}
	_, err = taskcard.Take(ctx, c, "f1", 1, "test", "E")
	door("card take", "E", err)
	_, err = taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"F"}, By: "test"})
	door("card deal", "F", err)
	_, err = taskcard.Work(ctx, c, as, "test", 1, false, dd[0].Copy)
	door("card work", dd[0].Copy, err)
}

// TestDealPassNamesTheHeldCopy: with alpha reopened, friend:f1 (one slot)
// holds C~1 in ready, which the rule holds; the deal pass names it with the
// line ready --why prints (DEAL skipped=C~1 why=WAIT task:alpha:sentinel
// waiting), counts no slot for it and deals the eligible A2 instead, and
// card work starts A2's copy past C~1.
func TestDealPassNamesTheHeldCopy(t *testing.T) {
	t.Parallel()
	c, st := sdStore(t)
	ctx := context.Background()
	as, err := taskcard.ParseConsumer("friend:f1")
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
	if _, _, err := sdResolve(c, sdLease(t, st)); err != nil {
		t.Fatal(err)
	}
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: as, N: 1, IDs: []string{"C"}, By: "test"}); err != nil || len(d) != 1 {
		t.Fatalf("deal C: %+v %v", d, err)
	}
	if err := sdCard(t, c, "A2", "alpha", ""); err != nil {
		t.Fatal(err)
	}
	pass, err := taskcard.DealPass(ctx, c, "test", now)
	if err != nil {
		t.Fatal(err)
	}
	const want = "DEAL skipped=C~1 why=WAIT task:alpha:sentinel waiting"
	found := false
	for _, l := range pass.Lines {
		found = found || l == want
	}
	if !found || pass.Dealt != 1 {
		t.Fatalf("deal pass: dealt %d lines %q; want %q and A2 dealt", pass.Dealt, pass.Lines, want)
	}
	work, err := taskcard.Work(ctx, c, as, "test", 1, false)
	if err != nil || len(work.IDs) != 1 || sdField(t, c, work.IDs[0], "primary") != "A2" {
		t.Fatalf("work: %+v %v; want A2's copy", work, err)
	}
	t.Logf("deal pass: %q; worked %s", pass.Lines, work.IDs[0])
}
