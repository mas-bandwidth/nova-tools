package land_test

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintci"
)

// TestL1 verifies control L1 (Issue #3139 rev 7 §11):
// Two concurrent plans naming one unit: one refused;
// property test over 1,000 plans, no unit in two batches.
func TestL1(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	// 1. Two concurrent plans naming one unit: one refused.
	head1 := "aaaa111122223333444455556666777788889999"
	unit1 := "gh/mas-bandwidth/nova-tools/101"
	fromTip := "1111111111111111111111111111111111111111"

	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit1, Repo: f.repo, Base: f.base,
		Branch: "card-101", Head: head1, BaseSHA: fromTip,
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit1, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}

	membersCSV := fmt.Sprintf("%s@%s", unit1, head1)
	var wg sync.WaitGroup
	var successes int32
	var refused int32

	for i := 0; i < 2; i++ {
		wg.Add(1)
		bID := fmt.Sprintf("batch-l1-%d", i)
		go func(batchID string) {
			defer wg.Done()
			_, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, membersCSV, "", "go", fromTip, "in-1")
			if err == nil {
				atomic.AddInt32(&successes, 1)
			} else if strings.Contains(err.Error(), "REFUSED") {
				atomic.AddInt32(&refused, 1)
			}
		}(bID)
	}
	wg.Wait()

	if successes != 1 || refused != 1 {
		t.Fatalf("concurrent plans: got successes=%d refused=%d, want 1 success and 1 refused", successes, refused)
	}

	// 2. Property test over 1,000 plans: no unit in two batches.
	const numUnits = 12
	unitNames := make([]string, numUnits)
	for i := 0; i < numUnits; i++ {
		u := fmt.Sprintf("gh/mas-bandwidth/nova-tools/%d", 200+i)
		h := fmt.Sprintf("%040d", 200+i)
		unitNames[i] = u
		_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint, Unit: u, Repo: f.repo, Base: f.base,
			Branch: fmt.Sprintf("card-%d", 200+i), Head: h, BaseSHA: fromTip,
		})
		if err != nil {
			t.Fatalf("unit head %s: %v", u, err)
		}
		if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, u, f.repo, f.base, 0); err != nil {
			t.Fatalf("unit eval %s: %v", u, err)
		}
	}

	const totalPlans = 1000
	workers := 8
	plansPerWorker := totalPlans / workers
	var planWG sync.WaitGroup

	for w := 0; w < workers; w++ {
		planWG.Add(1)
		go func(workerID int) {
			defer planWG.Done()
			r := rand.New(rand.NewSource(int64(workerID + 100)))
			for p := 0; p < plansPerWorker; p++ {
				idx1 := r.Intn(numUnits)
				idx2 := (idx1 + 1 + r.Intn(numUnits-1)) % numUnits
				u1 := unitNames[idx1]
				u2 := unitNames[idx2]
				mCSV := fmt.Sprintf("%s@%040d,%s@%040d", u1, 200+idx1, u2, 200+idx2)
				bID := fmt.Sprintf("b-prop-%d-%d", workerID, p)

				_, _, planErr := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, bID, f.lease, mCSV, "", "go", fromTip, "in-prop")
				if planErr == nil {
					// Randomly void some batches to keep chain churning
					if r.Intn(3) == 0 {
						_ = land.CallBatchVoid(f.ctx, f.client, f.sprint, f.repo, f.base, bID, "prop-churn")
					}
				}
			}
		}(w)
	}
	planWG.Wait()

	// Invariant check: verify that across the entire chain, no unit is duplicated
	chain, err := f.client.ZRange(f.ctx, land.ChainKey(f.repo, f.base), 0, -1).Result()
	if err != nil {
		t.Fatalf("zrange chain: %v", err)
	}

	claimed := make(map[string]string)
	for _, bID := range chain {
		bData, err := f.client.HGetAll(f.ctx, land.BatchKey(f.repo, f.base, bID)).Result()
		if err != nil {
			t.Fatalf("hgetall batch %s: %v", bID, err)
		}
		for _, m := range strings.Split(bData["members"], ",") {
			parts := strings.Split(m, "@")
			u := parts[0]
			if prev, ok := claimed[u]; ok {
				t.Fatalf("property violation: unit %s is in multiple batches (%s and %s)", u, prev, bID)
			}
			claimed[u] = bID
			uBatch, err := f.client.HGet(f.ctx, land.UnitKey(f.sprint, u), "batch").Result()
			if err != nil {
				t.Fatalf("hget unit batch: %v", err)
			}
			if uBatch != bID {
				t.Fatalf("unit %s batch field %s != expected batch %s", u, uBatch, bID)
			}
		}
	}
}

// TestL2 verifies control L2 (Issue #3139 rev 7 §11):
// A redelivered entry read by two slots is gated once; the second gets STALE.
func TestL2(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	unit := "gh/mas-bandwidth/nova-tools/102"
	head := "2222111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"

	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-102", Head: head, BaseSHA: fromTip,
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}

	token, entryID, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l2", f.lease, unit+"@"+head, "", "go", fromTip, "in-l2")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if entryID == "" {
		t.Fatalf("missing entryID")
	}

	// Slot 1 claims attempt 1
	res1, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l2", 1, token, "bench-1", "slot-1")
	if err != nil || res1 != "OK" {
		t.Fatalf("slot 1 claim: got %s, err %v, want OK", res1, err)
	}

	// Slot 2 reads the same redelivered attempt 1 and tries to claim
	res2, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l2", 1, token, "bench-2", "slot-2")
	if err != nil || res2 != "STALE" {
		t.Fatalf("slot 2 claim: got %s, err %v, want STALE", res2, err)
	}

	// Slot 1 writes receipt
	resReceipt1, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l2", 1, token, "GREEN", "bench-1", "worker-1", "train-1", "tree-1", "in-l2", "", "", "", "", "10")
	if err != nil || resReceipt1 != "OK" {
		t.Fatalf("slot 1 receipt: got %s, err %v, want OK", resReceipt1, err)
	}

	// Slot 2 attempts to write receipt for attempt 1
	resReceipt2, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l2", 1, token, "GREEN", "bench-2", "worker-2", "train-1", "tree-1", "in-l2", "", "", "", "", "10")
	if err != nil || resReceipt2 != "ALREADY" {
		t.Fatalf("slot 2 receipt: got %s, err %v, want ALREADY", resReceipt2, err)
	}
}

// TestL6 verifies control L6 (Issue #3139 rev 7 §11):
// ns_land twice for one batch writes nothing the second time.
func TestL6(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	unit := "gh/mas-bandwidth/nova-tools/106"
	head := "6666111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"
	trainHead := "7777111122223333444455556666777788889999"
	mergeSha := "8888111122223333444455556666777788889999"

	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-106", Head: head, BaseSHA: fromTip, Author: "emma",
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}

	token, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l6", f.lease, unit+"@"+head, "", "go", fromTip, "in-l6")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l6", 1, token, "bench-1", "slot-1")
	_, err = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l6", 1, token, "GREEN", "bench-1", "worker-1", trainHead, "tree-l6", "in-l6", "", "", "", "", "10")
	if err != nil {
		t.Fatalf("gate receipt: %v", err)
	}

	// Linearization point: ns_land_intent
	if _, err := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l6", f.lease); err != nil {
		t.Fatalf("land intent: %v", err)
	}

	// First call to ns_land: lands the batch
	res1, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l6", f.lease, trainHead, mergeSha, "1")
	if err != nil || res1 != "OK" {
		t.Fatalf("ns_land first: got %s, err %v, want OK", res1, err)
	}

	// Verify state after first landing
	landedVal, err := f.client.Get(f.ctx, land.LandedKey(f.repo, unit, head)).Result()
	if err != nil {
		t.Fatalf("landed key: %v", err)
	}
	if !strings.HasPrefix(landedVal, mergeSha) {
		t.Fatalf("landed val %s does not start with merge sha %s", landedVal, mergeSha)
	}

	tipSha, err := f.client.HGet(f.ctx, land.TipKey(f.repo, f.base), "sha").Result()
	if err != nil || tipSha != trainHead {
		t.Fatalf("tip sha: got %s, want %s", tipSha, trainHead)
	}

	events1, err := f.client.XRange(f.ctx, land.EventsStream(f.repo), "-", "+").Result()
	if err != nil {
		t.Fatalf("events range: %v", err)
	}
	var landedEventsCount int
	for _, e := range events1 {
		if fmt.Sprint(e.Values["event"]) == "LANDED" {
			landedEventsCount++
		}
	}
	if landedEventsCount != 1 {
		t.Fatalf("expected 1 LANDED event, got %d", landedEventsCount)
	}

	// Second call to ns_land: writes NOTHING
	res2, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l6", f.lease, trainHead, mergeSha, "1")
	if err != nil || res2 != "ALREADY" {
		t.Fatalf("ns_land second: got %s, err %v, want ALREADY", res2, err)
	}

	// Verify events count has not changed
	events2, err := f.client.XRange(f.ctx, land.EventsStream(f.repo), "-", "+").Result()
	if err != nil {
		t.Fatalf("events range: %v", err)
	}
	var landedEventsCount2 int
	for _, e := range events2 {
		if fmt.Sprint(e.Values["event"]) == "LANDED" {
			landedEventsCount2++
		}
	}
	if landedEventsCount2 != 1 {
		t.Fatalf("second call wrote a second LANDED event: got %d", landedEventsCount2)
	}
}

// TestL7 verifies control L7 (Issue #3139 rev 7 §11):
// A landed unit never re-enters landable; a plan naming a landed head is refused.
func TestL7(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	unit := "gh/mas-bandwidth/nova-tools/107"
	head := "7777111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"
	trainHead := "8888111122223333444455556666777788889999"

	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base,
		Branch: "card-107", Head: head, BaseSHA: fromTip,
	})
	if err != nil {
		t.Fatalf("unit head: %v", err)
	}
	if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
		t.Fatalf("unit eval: %v", err)
	}

	token, _, _ := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l7", f.lease, unit+"@"+head, "", "go", fromTip, "in-l7")
	_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l7", 1, token, "bench-1", "slot-1")
	_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l7", 1, token, "GREEN", "bench-1", "worker-1", trainHead, "tree-l7", "in-l7", "", "", "", "", "10")
	_, _ = land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l7", f.lease)
	_, err = land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l7", f.lease, trainHead, trainHead, "1")
	if err != nil {
		t.Fatalf("land: %v", err)
	}

	// 1. Attempt to re-enter landable: must be refused
	_, evalErr := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0)
	if evalErr == nil || !strings.Contains(evalErr.Error(), "REFUSED") {
		t.Fatalf("unit eval on landed unit: got %v, want REFUSED", evalErr)
	}

	score, err := f.client.ZScore(f.ctx, land.LandableKey(f.sprint, f.repo, f.base), unit).Result()
	if err == nil || score != 0 {
		t.Fatalf("landed unit present in landable zset: score=%f", score)
	}

	// 2. A plan naming the landed head is refused
	_, _, planErr := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l7-again", f.lease, unit+"@"+head, "", "go", trainHead, "in-l7-2")
	if planErr == nil || !strings.Contains(planErr.Error(), "REFUSED") {
		t.Fatalf("plan naming landed head: got %v, want REFUSED", planErr)
	}
}

// TestL23 verifies control L23 (Issue #3139 rev 7 §11):
// A HOLD record written before the intent's cut refuses it;
// a HOLD after the cut (even before the physical push) lands with post_land=1 and its follow-up task;
// no intent is cut with an open hold.
func TestL23(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	// Case 1: HOLD before cut refuses intent
	unit1 := "gh/mas-bandwidth/nova-tools/123-1"
	head1 := "1231111122223333444455556666777788889999"
	fromTip := "1111111111111111111111111111111111111111"

	_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit1, Repo: f.repo, Base: f.base,
		Branch: "card-123-1", Head: head1, BaseSHA: fromTip, Author: "alice",
	})
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit1, f.repo, f.base, 0)
	tok1, _, _ := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l23-1", f.lease, unit1+"@"+head1, "", "go", fromTip, "in-1")
	_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l23-1", 1, tok1, "bench-1", "slot-1")
	_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l23-1", 1, tok1, "GREEN", "bench-1", "w-1", "train-1", "tree-1", "in-1", "", "", "", "", "10")

	// HOLD written before cut
	_, postLand1, err := land.CallHold(f.ctx, f.client, f.sprint, unit1, "bob", head1, "substantive", "needs review", "", "", "", "manual")
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if postLand1 {
		t.Fatalf("post_land should be false before cut")
	}

	// Intent cut must be refused
	_, intentErr := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l23-1", f.lease)
	if intentErr == nil || !strings.Contains(intentErr.Error(), "REFUSED") {
		t.Fatalf("land intent with open hold: got %v, want REFUSED", intentErr)
	}

	// Case 2: HOLD after cut lands with post_land=1 and follow-up task
	unit2 := "gh/mas-bandwidth/nova-tools/123-2"
	head2 := "1232111122223333444455556666777788889999"

	_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit2, Repo: f.repo, Base: f.base,
		Branch: "card-123-2", Head: head2, BaseSHA: fromTip, Author: "carol",
	})
	_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, unit2, f.repo, f.base, 0)
	tok2, _, _ := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l23-2", f.lease, unit2+"@"+head2, "", "go", fromTip, "in-2")
	_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-l23-2", 1, tok2, "bench-1", "slot-1")
	_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-l23-2", 1, tok2, "GREEN", "bench-1", "w-1", "train-2", "tree-2", "in-2", "", "", "", "", "10")

	// Intent cut succeeds
	seqCut, err := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l23-2", f.lease)
	if err != nil {
		t.Fatalf("land intent: %v", err)
	}
	if seqCut <= 0 {
		t.Fatalf("invalid seqCut: %d", seqCut)
	}

	// HOLD arrives after cut, before physical push/landing
	_, postLand2, err := land.CallHold(f.ctx, f.client, f.sprint, unit2, "dave", head2, "substantive", "late hold", "", "", "", "manual")
	if err != nil {
		t.Fatalf("hold after cut: %v", err)
	}
	if !postLand2 {
		t.Fatalf("post_land must be true after cut")
	}

	// Check follow-up task queued in q:carol and q:dave
	tasksCarol, err := f.client.XRange(f.ctx, "q:carol", "-", "+").Result()
	if err != nil || len(tasksCarol) == 0 {
		t.Fatalf("expected follow-up task in q:carol, got err=%v len=%d", err, len(tasksCarol))
	}
	if fmt.Sprint(tasksCarol[0].Values["post_land"]) != "1" {
		t.Fatalf("task post_land != 1: %v", tasksCarol[0].Values)
	}

	tasksDave, err := f.client.XRange(f.ctx, "q:dave", "-", "+").Result()
	if err != nil || len(tasksDave) == 0 {
		t.Fatalf("expected follow-up task in q:dave, got err=%v len=%d", err, len(tasksDave))
	}

	// Batch lands
	resLand, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-l23-2", f.lease, "train-2", "merge-2", "1")
	if err != nil || resLand != "OK" {
		t.Fatalf("land: got %s, err %v, want OK", resLand, err)
	}

	// Hold remains post_land=1
	postLandStored, err := f.client.HGet(f.ctx, land.HoldKey(f.sprint, unit2, "dave"), "post_land").Result()
	if err != nil || postLandStored != "1" {
		t.Fatalf("stored hold post_land: got %s, want 1", postLandStored)
	}
}

// TestL28 verifies control L28 (Issue #3139 rev 7 §11):
// The paused publisher:
// (i) paused after the intent, a HOLD arrives: lands, the hold is post_land with its follow-up task;
// (ii) paused before the intent, a HOLD arrives: the intent is refused, nothing pushed;
// (iii) paused after the intent, lease expires, the new publisher completes it, the old one resumes:
//
//	its push is refused or identical and its ns_land is STALE, one LANDED event;
//
// (iv) paused after the intent, the base moved by hand: both pushes refused, intent dead, chain re-planned.
func TestL28(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	fromTip := "1111111111111111111111111111111111111111"

	// Subcase (i): paused after intent, HOLD arrives: lands, hold is post_land with follow-up task
	t.Run("Subcase1_PausedAfterIntent_HoldArrives", func(t *testing.T) {
		u := "gh/mas-bandwidth/nova-tools/28-1"
		h := "2801111122223333444455556666777788889999"
		_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint, Unit: u, Repo: f.repo, Base: f.base,
			Branch: "card-28-1", Head: h, BaseSHA: fromTip, Author: "author-1",
		})
		_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, u, f.repo, f.base, 0)
		tok, _, _ := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-1", f.lease, u+"@"+h, "", "go", fromTip, "in")
		_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "b28-1", 1, tok, "bench-1", "s-1")
		_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "b28-1", 1, tok, "GREEN", "bench-1", "w-1", "train-28-1", "tree-28-1", "in", "", "", "", "", "10")

		// Cut intent
		_, err := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-1", f.lease)
		if err != nil {
			t.Fatalf("intent: %v", err)
		}

		// Paused after intent: HOLD arrives
		_, postLand, err := land.CallHold(f.ctx, f.client, f.sprint, u, "holder-1", h, "substantive", "hold-subcase-1", "", "", "", "manual")
		if err != nil || !postLand {
			t.Fatalf("expected post_land=true, got postLand=%v err=%v", postLand, err)
		}

		// Resumes and lands
		res, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-1", f.lease, "train-28-1", "merge-28-1", "1")
		if err != nil || res != "OK" {
			t.Fatalf("land: got %s, err %v", res, err)
		}
	})

	// Subcase (ii): paused before intent, a HOLD arrives: intent refused, nothing pushed
	t.Run("Subcase2_PausedBeforeIntent_HoldArrives", func(t *testing.T) {
		u := "gh/mas-bandwidth/nova-tools/28-2"
		h := "2802111122223333444455556666777788889999"
		curTip, _ := f.client.HGet(f.ctx, land.TipKey(f.repo, f.base), "sha").Result()

		_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint, Unit: u, Repo: f.repo, Base: f.base,
			Branch: "card-28-2", Head: h, BaseSHA: curTip, Author: "author-2",
		})
		_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, u, f.repo, f.base, 0)
		tok, _, _ := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-2", f.lease, u+"@"+h, "", "go", curTip, "in")
		_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "b28-2", 1, tok, "bench-1", "s-1")
		_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "b28-2", 1, tok, "GREEN", "bench-1", "w-1", "train-28-2", "tree-28-2", "in", "", "", "", "", "10")

		// Paused before intent: HOLD arrives
		_, postLand, err := land.CallHold(f.ctx, f.client, f.sprint, u, "holder-2", h, "substantive", "hold-subcase-2", "", "", "", "manual")
		if err != nil || postLand {
			t.Fatalf("expected postLand=false, got %v", postLand)
		}

		// Resumes: intent refused
		_, intentErr := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-2", f.lease)
		if intentErr == nil || !strings.Contains(intentErr.Error(), "REFUSED") {
			t.Fatalf("intent cut: got %v, want REFUSED", intentErr)
		}
	})

	// Subcase (iii): paused after intent, lease expires, new publisher completes it, old resumes:
	// old ns_land is STALE, one LANDED event
	t.Run("Subcase3_LeaseExpires_Takeover", func(t *testing.T) {
		u := "gh/mas-bandwidth/nova-tools/28-3"
		h := "2803111122223333444455556666777788889999"
		curTip, _ := f.client.HGet(f.ctx, land.TipKey(f.repo, f.base), "sha").Result()

		_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint, Unit: u, Repo: f.repo, Base: f.base,
			Branch: "card-28-3", Head: h, BaseSHA: curTip, Author: "author-3",
		})
		_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, u, f.repo, f.base, 0)
		tok, _, _ := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-3", f.lease, u+"@"+h, "", "go", curTip, "in")
		_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "b28-3", 1, tok, "bench-1", "s-1")
		_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "b28-3", 1, tok, "GREEN", "bench-1", "w-1", "train-28-3", "tree-28-3", "in", "", "", "", "", "10")

		oldLease := f.lease
		_, err := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-3", oldLease)
		if err != nil {
			t.Fatalf("intent: %v", err)
		}

		// Publisher 1 pauses. New publisher takes lease
		newLease := fmt.Sprintf("%d:lease-token-new", f.gen)
		f.client.Set(f.ctx, land.LeaseKey(f.repo, f.base), newLease, 60*time.Second)

		// Record events count before landing
		eventsBefore, _ := f.client.XRange(f.ctx, land.EventsStream(f.repo), "-", "+").Result()
		var landedBefore int
		for _, e := range eventsBefore {
			if fmt.Sprint(e.Values["event"]) == "LANDED" {
				landedBefore++
			}
		}

		// New publisher completes landing
		resNew, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-3", newLease, "train-28-3", "merge-28-3", "1")
		if err != nil || resNew != "OK" {
			t.Fatalf("new publisher land: got %s, err %v", resNew, err)
		}

		// Old publisher resumes and calls ns_land with old lease
		resOld, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-3", oldLease, "train-28-3", "merge-28-3", "1")
		if err != nil || resOld != "STALE" {
			t.Fatalf("old publisher land: got %s, err %v, want STALE", resOld, err)
		}

		// Assert exactly one LANDED event added
		eventsAfter, _ := f.client.XRange(f.ctx, land.EventsStream(f.repo), "-", "+").Result()
		var landedAfter int
		for _, e := range eventsAfter {
			if fmt.Sprint(e.Values["event"]) == "LANDED" {
				landedAfter++
			}
		}
		if landedAfter != landedBefore+1 {
			t.Fatalf("expected exactly 1 new LANDED event, got before=%d after=%d", landedBefore, landedAfter)
		}

		// Reset lease for fixture
		f.client.Set(f.ctx, land.LeaseKey(f.repo, f.base), f.lease, 60*time.Second)
	})

	// Subcase (iv): paused after intent, base moved by hand: both pushes refused, intent dead, chain re-planned
	t.Run("Subcase4_BaseMoved_IntentDead", func(t *testing.T) {
		u := "gh/mas-bandwidth/nova-tools/28-4"
		h := "2804111122223333444455556666777788889999"
		curTip, _ := f.client.HGet(f.ctx, land.TipKey(f.repo, f.base), "sha").Result()

		_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint, Unit: u, Repo: f.repo, Base: f.base,
			Branch: "card-28-4", Head: h, BaseSHA: curTip, Author: "author-4",
		})
		_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, u, f.repo, f.base, 0)
		tok, _, _ := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-4", f.lease, u+"@"+h, "", "go", curTip, "in")
		_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "b28-4", 1, tok, "bench-1", "s-1")
		_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "b28-4", 1, tok, "GREEN", "bench-1", "w-1", "train-28-4", "tree-28-4", "in", "", "", "", "", "10")

		_, err := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-4", f.lease)
		if err != nil {
			t.Fatalf("intent: %v", err)
		}

		// Base moves by hand to newTip
		newTip := "9999999999999999999999999999999999999999"
		f.client.HSet(f.ctx, land.TipKey(f.repo, f.base), "sha", newTip)

		// Intent marked dead, batch voided
		if err := land.CallPubState(f.ctx, f.client, f.repo, f.base, "b28-4", "dead"); err != nil {
			t.Fatalf("pub state dead: %v", err)
		}
		if err := land.CallBatchVoid(f.ctx, f.client, f.sprint, f.repo, f.base, "b28-4", "base-moved"); err != nil {
			t.Fatalf("batch void: %v", err)
		}

		// Member returns to landable
		st, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, u), "state").Result()
		if st != "landable" {
			t.Fatalf("expected unit state landable after void, got %s", st)
		}
		chain, _ := f.client.ZRange(f.ctx, land.ChainKey(f.repo, f.base), 0, -1).Result()
		for _, bID := range chain {
			if bID == "b28-4" {
				t.Fatalf("batch b28-4 still in chain after void")
			}
		}
	})
}

// TestL31c verifies control L31c (Issue #3139 rev 7 §11):
// One head H gated on base A (base_sha a, required set RA) and on base B (b, RB),
// in both write orders: two keys, both units landable from their own receipt;
// a re-gate on A returns ALREADY; a RED on B never blocks A;
// a policy change on A turns only A stale and leaves both receipts readable.
func TestL31c(t *testing.T) {
	orders := []struct {
		name       string
		firstBase  string
		secondBase string
	}{
		{"Order_A_then_B", "baseA", "baseB"},
		{"Order_B_then_A", "baseB", "baseA"},
	}

	for _, tc := range orders {
		t.Run(tc.name, func(t *testing.T) {
			f := newLandFixture(t, "nova-tools", "dev")

			headH := "hhhh111122223333444455556666777788889999"
			baseA := "baseA"
			shaA := "aaaa111122223333444455556666777788889999"
			reqA := "RA"
			polA := "polA"

			baseB := "baseB"
			shaB := "bbbb111122223333444455556666777788889999"
			reqB := "RB"
			polB := "polB"

			runnerID := "runner-v1"

			gidA := land.GID("single", baseA, shaA, reqA, polA, runnerID)
			gidB := land.GID("single", baseB, shaB, reqB, polB, runnerID)

			if gidA == gidB {
				t.Fatalf("gidA and gidB must differ: both %s", gidA)
			}

			write := func(base string) {
				if base == "baseA" {
					resA, err := land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gidA, "OK", "single", baseA, shaA, reqA, polA, runnerID, "batch-a:1", "bench-1", "pkgA", "TestA")
					if err != nil || resA != "OK" {
						t.Fatalf("receipt write A: got %s, err %v", resA, err)
					}
				} else {
					// Base B gets RED
					resB, err := land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gidB, "FAIL", "single", baseB, shaB, reqB, polB, runnerID, "batch-b:1", "bench-2", "pkgB", "TestB")
					if err != nil || resB != "OK" {
						t.Fatalf("receipt write B: got %s, err %v", resB, err)
					}
				}
			}

			write(tc.firstBase)
			write(tc.secondBase)

			// 1. Two keys exist
			keyA := land.CIKey(f.repo, headH, gidA)
			keyB := land.CIKey(f.repo, headH, gidB)

			verdictA, err := f.client.HGet(f.ctx, keyA, "verdict").Result()
			if err != nil || verdictA != "OK" {
				t.Fatalf("keyA verdict: got %s, err %v, want OK", verdictA, err)
			}

			verdictB, err := f.client.HGet(f.ctx, keyB, "verdict").Result()
			if err != nil || verdictB != "FAIL" {
				t.Fatalf("keyB verdict: got %s, err %v, want FAIL", verdictB, err)
			}

			// 2. Both GIDs are recorded in ci:<repo>:<head>:gids
			gids, err := f.client.SMembers(f.ctx, land.CIGIDsKey(f.repo, headH)).Result()
			if err != nil {
				t.Fatalf("smembers gids: %v", err)
			}
			var hasA, hasB bool
			for _, g := range gids {
				if g == gidA {
					hasA = true
				}
				if g == gidB {
					hasB = true
				}
			}
			if !hasA || !hasB {
				t.Fatalf("gids set missing expected gids: %v", gids)
			}

			// 3. A re-gate on A returns ALREADY
			reGateA, err := land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gidA, "OK", "single", baseA, shaA, reqA, polA, runnerID, "batch-a:1", "bench-1", "pkgA", "TestA")
			if err != nil || reGateA != "ALREADY" {
				t.Fatalf("re-gate on A: got %s, err %v, want ALREADY", reGateA, err)
			}

			// 4. A RED on B never blocks A
			verdictACheck, err := f.client.HGet(f.ctx, keyA, "verdict").Result()
			if err != nil || verdictACheck != "OK" {
				t.Fatalf("verdict A altered: got %s, want OK", verdictACheck)
			}

			// 5. A policy change on A turns only A stale and leaves both receipts readable
			polAChanged := "polA-v2"
			gidANew := land.GID("single", baseA, shaA, reqA, polAChanged, runnerID)

			// Expected gid on A is now gidANew. The key for gidANew is absent.
			existsNew, err := f.client.Exists(f.ctx, land.CIKey(f.repo, headH, gidANew)).Result()
			if err != nil || existsNew != 0 {
				t.Fatalf("expected gidANew key to be absent, got exists=%d", existsNew)
			}
			// But ci:<repo>:<head>:gids is non-empty -> head H is stale for base A, not missing.
			gidsLen, _ := f.client.SCard(f.ctx, land.CIGIDsKey(f.repo, headH)).Result()
			if gidsLen < 2 {
				t.Fatalf("expected at least 2 gids, got %d", gidsLen)
			}

			// Both original receipts remain readable
			resARead, err := f.client.HGetAll(f.ctx, keyA).Result()
			if err != nil || resARead["verdict"] != "OK" {
				t.Fatalf("receipt A unreadable after policy change: %v", resARead)
			}
			resBRead, err := f.client.HGetAll(f.ctx, keyB).Result()
			if err != nil || resBRead["verdict"] != "FAIL" {
				t.Fatalf("receipt B unreadable after policy change: %v", resBRead)
			}
		})
	}
}

// TestL31d verifies control L31d (Issue #3139 rev 7 §11):
// - ns_ci_cut then ns_ci_end DONE OK on head H for base dev at current policy:
//   ci:<repo>:H:<gid> exists, H is landable with no lander gate queued, and civerdict,
//   ci.LandReady and why all read OK.
// - ns_ci_cut and ns_ci_rerun write no ci: key.
// - A second end for the same gid returns ALREADY.
// - An end on another base_sha leaves H ci stale and queues one single.
// - FAIL then rerun FAIL on same test: one FAIL receipt, none after first end.
// - FAIL then OK: no receipt until disposition rerun, whose OK writes OK.
// - An end with no policy record returns NOPOLICY and writes no ci: key.
// - ci cut without --base-ref exits 2.
// - With ci_reruns 2, FAIL, rerun FAIL writes nothing and second rerun FAIL writes FAIL.
// - A harvested card's CICut carries its branch.
// - Preflight 7.16 is green on a gid source.
// - sprintci.Bench.Run writes no Redis key.
func TestL31d(t *testing.T) {
	t.Run("ns_ci_cut then ns_ci_end DONE OK creates receipt and makes head landable", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		st := store.New(f.client)
		f.client.HSet(f.ctx, "s:"+f.sprint, "status", "open")
		f.client.SAdd(f.ctx, "benches", "bench-1")
		f.client.HSet(f.ctx, "bench:bench-1:desired", "slots", "4", "machine", "bench-1", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-1:beat", "host", "bench-1", "at", "1")

		headH := "aaaa111122223333444455556666777788889999"
		tipSHA := "1111111111111111111111111111111111111111"
		unitID := "gh/mas-bandwidth/nova-tools/101"

		_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint, Unit: unitID, Repo: f.repo, Base: f.base,
			Branch: "feat-101", Head: headH, BaseSHA: tipSHA, PR: "101",
		})
		if err != nil {
			t.Fatalf("unit head: %v", err)
		}

		res, err := ci.Cut(f.ctx, st, ci.CutRequest{
			Sprint: f.sprint, Repo: f.repo, PR: 101, Head: headH, Base: tipSHA, BaseRef: f.base, Actor: "ctl",
		})
		if err != nil || res.Status != "CREATED" {
			t.Fatalf("ci cut: %v, %v", res, err)
		}
		label := ci.Label(101, headH)

		key := "s:" + f.sprint + ":card:" + label
		vals, err := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		if err != nil || vals[0] != "queued" {
			t.Fatalf("deal state: %v, %v", vals, err)
		}
		attempt := vals[1].(string)
		identity := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals[2], attempt)
		token := attempt + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity, "token", token, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)

		endRes, err := ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token, Identity: identity,
			Outcome: "DONE", Reason: "done", Verdict: "OK", Actor: "wrapper",
		})
		if err != nil || endRes.Status != "ENDED" || endRes.Detail != "OK" {
			t.Fatalf("ci end: %v, %v", endRes, err)
		}

		expGID, err := civerdict.Expected(f.ctx, f.client, f.repo, f.base, tipSHA)
		if err != nil {
			t.Fatalf("expected GID: %v", err)
		}
		ckey := civerdict.Key(f.repo, headH, expGID)
		n, err := f.client.Exists(f.ctx, ckey).Result()
		if err != nil || n != 1 {
			t.Fatalf("ci:<repo>:H:<gid> key %s exists=%d, want 1", ckey, n)
		}

		rec, err := civerdict.Read(f.ctx, f.client, f.repo, headH, expGID)
		if err != nil || rec["verdict"] != "OK" {
			t.Fatalf("civerdict.Read: %v, %v", rec, err)
		}
		hRec, err := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if err != nil || hRec["verdict"] != "OK" {
			t.Fatalf("civerdict.ReadHead: %v, %v", hRec, err)
		}
		ready, whyReady, err := ci.LandReady(f.ctx, st, f.repo, headH, f.base)
		if err != nil || !ready || whyReady != "ci: OK" {
			t.Fatalf("ci.LandReady: ready=%v, why=%s, err=%v", ready, whyReady, err)
		}

		if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unitID, f.repo, f.base, 0); err != nil {
			t.Fatalf("unit eval: %v", err)
		}
		uState, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, unitID), "state").Result()
		if uState != "landable" {
			t.Fatalf("unit state=%q, want landable", uState)
		}
		gatesLen, _ := f.client.XLen(f.ctx, "land:"+f.repo+":gates").Result()
		if gatesLen != 0 {
			t.Fatalf("lander gates queued = %d, want 0", gatesLen)
		}

		p := &land.PR{
			ID:     land.ID{Repo: f.repo, N: 101},
			Fields: map[string]string{"head": headH, "draft": "false"},
			CI:     rec,
			CIGIDs: []string{expGID},
		}
		whyLines := land.Why(p, time.Now())
		if len(whyLines) == 0 || !strings.Contains(whyLines[0], "ci OK@") {
			t.Fatalf("why lines: %v; want ci OK@", whyLines)
		}
	})

	t.Run("ns_ci_cut and ns_ci_rerun write no ci: key", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		st := store.New(f.client)
		f.client.HSet(f.ctx, "s:"+f.sprint, "status", "open")
		f.client.SAdd(f.ctx, "benches", "bench-1", "bench-2")
		f.client.HSet(f.ctx, "bench:bench-1:desired", "slots", "4", "machine", "bench-1", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-1:beat", "host", "bench-1", "at", "1")
		f.client.HSet(f.ctx, "bench:bench-2:desired", "slots", "4", "machine", "bench-2", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-2:beat", "host", "bench-2", "at", "1")

		headH2 := "bbbb111122223333444455556666777788889999"
		tipSHA := "1111111111111111111111111111111111111111"

		res, err := ci.Cut(f.ctx, st, ci.CutRequest{
			Sprint: f.sprint, Repo: f.repo, PR: 102, Head: headH2, Base: tipSHA, BaseRef: f.base, Actor: "ctl",
		})
		if err != nil || res.Status != "CREATED" {
			t.Fatalf("cut: %v, %v", res, err)
		}
		rec, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH2)
		if len(rec) != 0 {
			t.Fatalf("cut wrote ci: key: %v", rec)
		}

		label := ci.Label(102, headH2)
		key := "s:" + f.sprint + ":card:" + label
		vals, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		attempt := vals[1].(string)
		identity := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals[2], attempt)
		token := attempt + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity, "token", token, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)

		ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token, Identity: identity,
			Outcome: "DONE", Reason: "done", Verdict: "FAIL", Pkg: "pkg1", Test: "Test1", Actor: "wrapper",
		})

		rRes, err := ci.Rerun(f.ctx, st, f.sprint, label, "actor", "rerun-1")
		if err != nil || rRes.Status != "RERUN" {
			t.Fatalf("rerun: %v, %v", rRes, err)
		}
		rec, _ = civerdict.ReadHead(f.ctx, f.client, f.repo, headH2)
		if len(rec) != 0 {
			t.Fatalf("rerun wrote ci: key: %v", rec)
		}
	})

	t.Run("second end for same gid returns ALREADY", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		headH := "cccc111122223333444455556666777788889999"
		tipSHA := "1111111111111111111111111111111111111111"
		gid, _ := civerdict.Expected(f.ctx, f.client, f.repo, f.base, tipSHA)

		res1, err := land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gid, "OK", "single", f.base, tipSHA, "req-1", "pol-1", "runner-1", "batch:1", "bench-1", "pkg", "Test")
		if err != nil || res1 != "OK" {
			t.Fatalf("first receipt write: %s, %v", res1, err)
		}
		res2, err := land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gid, "OK", "single", f.base, tipSHA, "req-1", "pol-1", "runner-1", "batch:1", "bench-1", "pkg", "Test")
		if err != nil || res2 != "ALREADY" {
			t.Fatalf("second receipt write: %s, %v; want ALREADY", res2, err)
		}
	})

	t.Run("end on another base_sha leaves H ci stale and queues one single", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		headH := "dddd111122223333444455556666777788889999"
		oldTipSHA := "9999999999999999999999999999999999999999"
		currTipSHA := "1111111111111111111111111111111111111111"

		gidOld, _ := civerdict.Expected(f.ctx, f.client, f.repo, f.base, oldTipSHA)
		res, err := land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gidOld, "OK", "single", f.base, oldTipSHA, "req-1", "pol-1", "runner-1", "batch:1", "bench-1", "pkg", "Test")
		if err != nil || res != "OK" {
			t.Fatalf("write receipt: %s, %v", res, err)
		}

		unitID := "gh/mas-bandwidth/nova-tools/104"
		_, err = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint, Unit: unitID, Repo: f.repo, Base: f.base,
			Branch: "feat-104", Head: headH, BaseSHA: currTipSHA, PR: "104",
		})
		if err != nil {
			t.Fatalf("unit head: %v", err)
		}
		if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unitID, f.repo, f.base, 0); err != nil {
			t.Fatalf("unit eval: %v", err)
		}

		gidCurr, _ := civerdict.Expected(f.ctx, f.client, f.repo, f.base, currTipSHA)
		if gidCurr == gidOld {
			t.Fatalf("expected gids to differ: both %s", gidCurr)
		}

		p := &land.PR{
			ID:     land.ID{Repo: f.repo, N: 104},
			Fields: map[string]string{"head": headH, "draft": "false"},
			CI:     nil,
			CIGIDs: []string{gidOld},
		}
		whyLines := land.Why(p, time.Now())
		if len(whyLines) == 0 || !strings.Contains(whyLines[0], "ci stale@") {
			t.Fatalf("why lines: %v; want ci stale@", whyLines)
		}

		batchID := "batch-single-1"
		membersCSV := fmt.Sprintf("%s@%s", unitID, headH)
		_, _, err = land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, membersCSV, "", "single", currTipSHA, "in-single")
		if err != nil {
			t.Fatalf("batch plan single: %v", err)
		}
		bState, _ := f.client.HGet(f.ctx, land.BatchKey(f.repo, f.base, batchID), "class").Result()
		if bState != "single" {
			t.Fatalf("batch class: %s, want single", bState)
		}
	})

	t.Run("FAIL then rerun FAIL on same test: one FAIL receipt, none after first end", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		st := store.New(f.client)
		f.client.HSet(f.ctx, "s:"+f.sprint, "status", "open")
		f.client.SAdd(f.ctx, "benches", "bench-1", "bench-2")
		f.client.HSet(f.ctx, "bench:bench-1:desired", "slots", "4", "machine", "bench-1", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-1:beat", "host", "bench-1", "at", "1")
		f.client.HSet(f.ctx, "bench:bench-2:desired", "slots", "4", "machine", "bench-2", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-2:beat", "host", "bench-2", "at", "1")

		headH := "eeee111122223333444455556666777788889999"
		tipSHA := "1111111111111111111111111111111111111111"
		ci.Cut(f.ctx, st, ci.CutRequest{Sprint: f.sprint, Repo: f.repo, PR: 105, Head: headH, Base: tipSHA, BaseRef: f.base, Actor: "ctl"})
		label := ci.Label(105, headH)

		key := "s:" + f.sprint + ":card:" + label
		vals, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals[2], vals[1])
		token := vals[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity, "token", token, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)

		ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token, Identity: identity,
			Outcome: "DONE", Reason: "done", Verdict: "FAIL", Pkg: "pkgX", Test: "TestX", Actor: "wrapper",
		})
		rec1, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if len(rec1) != 0 {
			t.Fatalf("first FAIL wrote receipt: %v, want none until rerun budget spent", rec1)
		}

		ci.Rerun(f.ctx, st, f.sprint, label, "actor", "rerun-1")
		vals2, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity2 := fmt.Sprintf("%s/%s/%s/bench-2/%s", f.sprint, label, vals2[2], vals2[1])
		token2 := vals2[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-2", "identity", identity2, "token", token2, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)

		ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token2, Identity: identity2,
			Outcome: "DONE", Reason: "done", Verdict: "FAIL", Pkg: "pkgX", Test: "TestX", Actor: "wrapper",
		})

		rec2, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if rec2["verdict"] != "FAIL" {
			t.Fatalf("second FAIL verdict: %s, want FAIL", rec2["verdict"])
		}
		gids, _ := f.client.SMembers(f.ctx, civerdict.GIDsKey(f.repo, headH)).Result()
		if len(gids) != 1 {
			t.Fatalf("expected exactly 1 gid, got %d", len(gids))
		}
	})

	t.Run("FAIL then OK: no receipt until disposition rerun, whose OK writes OK", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		st := store.New(f.client)
		f.client.HSet(f.ctx, "s:"+f.sprint, "status", "open")
		f.client.SAdd(f.ctx, "benches", "bench-1", "bench-2")
		f.client.HSet(f.ctx, "bench:bench-1:desired", "slots", "4", "machine", "bench-1", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-1:beat", "host", "bench-1", "at", "1")
		f.client.HSet(f.ctx, "bench:bench-2:desired", "slots", "4", "machine", "bench-2", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-2:beat", "host", "bench-2", "at", "1")

		headH := "ffff111122223333444455556666777788889999"
		tipSHA := "1111111111111111111111111111111111111111"
		ci.Cut(f.ctx, st, ci.CutRequest{Sprint: f.sprint, Repo: f.repo, PR: 106, Head: headH, Base: tipSHA, BaseRef: f.base, Actor: "ctl"})
		label := ci.Label(106, headH)

		key := "s:" + f.sprint + ":card:" + label
		vals, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals[2], vals[1])
		token := vals[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity, "token", token, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)
		ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token, Identity: identity,
			Outcome: "DONE", Reason: "done", Verdict: "FAIL", Pkg: "pkgA", Test: "TestA", Actor: "wrapper",
		})
		rec1, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if len(rec1) != 0 {
			t.Fatalf("receipt written after first FAIL")
		}

		ci.Rerun(f.ctx, st, f.sprint, label, "actor", "rerun-1")
		vals2, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity2 := fmt.Sprintf("%s/%s/%s/bench-2/%s", f.sprint, label, vals2[2], vals2[1])
		token2 := vals2[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-2", "identity", identity2, "token", token2, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)
		rEnd2, _ := ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token2, Identity: identity2,
			Outcome: "DONE", Reason: "done", Verdict: "OK", Actor: "wrapper",
		})
		if rEnd2.Detail != "FLAKY" {
			t.Fatalf("end2 detail: %s, want FLAKY", rEnd2.Detail)
		}
		rec2, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if len(rec2) != 0 {
			t.Fatalf("receipt written after FLAKY")
		}

		rDisp, err := ci.Dispose(f.ctx, st, f.sprint, f.repo, headH, "APPROVE", "friend1", "https://example.test/disp")
		if err != nil || rDisp.Status != "APPROVE" {
			t.Fatalf("dispose: %v, %v", rDisp, err)
		}

		vals3, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity3 := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals3[2], vals3[1])
		token3 := vals3[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity3, "token", token3, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)
		rEnd3, _ := ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token3, Identity: identity3,
			Outcome: "DONE", Reason: "done", Verdict: "OK", Actor: "wrapper",
		})
		if rEnd3.Detail != "OK" {
			t.Fatalf("disposition end detail: %s, want OK", rEnd3.Detail)
		}
		rec3, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if rec3["verdict"] != "OK" {
			t.Fatalf("receipt after disposition rerun: %v, want OK", rec3)
		}
	})

	t.Run("end with no policy record returns NOPOLICY and writes no ci: key", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		st := store.New(f.client)
		f.client.HSet(f.ctx, "s:"+f.sprint, "status", "open")
		f.client.SAdd(f.ctx, "benches", "bench-1")
		f.client.HSet(f.ctx, "bench:bench-1:desired", "slots", "4", "machine", "bench-1", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-1:beat", "host", "bench-1", "at", "1")

		headH := "1212121212121212121212121212121212121212"
		tipSHA := "1111111111111111111111111111111111111111"
		ci.Cut(f.ctx, st, ci.CutRequest{Sprint: f.sprint, Repo: f.repo, PR: 107, Head: headH, Base: tipSHA, BaseRef: "other-branch", Actor: "ctl"})
		label := ci.Label(107, headH)

		key := "s:" + f.sprint + ":card:" + label
		vals, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals[2], vals[1])
		token := vals[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity, "token", token, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)

		r, err := ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token, Identity: identity,
			Outcome: "DONE", Reason: "done", Verdict: "OK", Actor: "wrapper",
		})
		if err != nil || r.Status != "NOPOLICY" {
			t.Fatalf("end with no policy: %v, %v, want NOPOLICY", r, err)
		}
		rec, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if len(rec) != 0 {
			t.Fatalf("NOPOLICY wrote receipt: %v", rec)
		}
	})

	t.Run("ci cut without base-ref exits 2", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		st := store.New(f.client)
		f.client.HSet(f.ctx, "s:"+f.sprint, "status", "open")
		headH := "3434343434343434343434343434343434343434"
		tipSHA := "1111111111111111111111111111111111111111"
		_, err := ci.Cut(f.ctx, st, ci.CutRequest{
			Sprint: f.sprint, Repo: f.repo, PR: 108, Head: headH, Base: tipSHA, BaseRef: "", Actor: "ctl",
		})
		if err == nil {
			t.Fatal("expected error for missing base-ref")
		}
	})

	t.Run("with ci_reruns 2, FAIL, rerun FAIL writes nothing and second rerun FAIL writes FAIL", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		st := store.New(f.client)
		f.client.HSet(f.ctx, "s:"+f.sprint, "status", "open")
		f.client.HSet(f.ctx, "s:"+f.sprint+":policy", "ci_reruns", "2")
		f.client.SAdd(f.ctx, "benches", "bench-1", "bench-2")
		f.client.HSet(f.ctx, "bench:bench-1:desired", "slots", "4", "machine", "bench-1", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-1:beat", "host", "bench-1", "at", "1")
		f.client.HSet(f.ctx, "bench:bench-2:desired", "slots", "4", "machine", "bench-2", "paused", "0", "legs", "go")
		f.client.HSet(f.ctx, "bench:bench-2:beat", "host", "bench-2", "at", "1")

		headH := "5656565656565656565656565656565656565656"
		tipSHA := "1111111111111111111111111111111111111111"
		ci.Cut(f.ctx, st, ci.CutRequest{Sprint: f.sprint, Repo: f.repo, PR: 109, Head: headH, Base: tipSHA, BaseRef: f.base, Actor: "ctl"})
		label := ci.Label(109, headH)

		key := "s:" + f.sprint + ":card:" + label
		vals, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals[2], vals[1])
		token := vals[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity, "token", token, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)
		ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token, Identity: identity,
			Outcome: "DONE", Reason: "done", Verdict: "FAIL", Pkg: "p", Test: "t", Actor: "wrapper",
		})
		rec1, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if len(rec1) != 0 {
			t.Fatalf("first FAIL wrote receipt with budget 2: %v", rec1)
		}

		ci.Rerun(f.ctx, st, f.sprint, label, "actor", "rerun-1")
		vals2, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity2 := fmt.Sprintf("%s/%s/%s/bench-2/%s", f.sprint, label, vals2[2], vals2[1])
		token2 := vals2[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-2", "identity", identity2, "token", token2, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)
		ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token2, Identity: identity2,
			Outcome: "DONE", Reason: "done", Verdict: "FAIL", Pkg: "p", Test: "t", Actor: "wrapper",
		})
		rec2, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if len(rec2) != 0 {
			t.Fatalf("second FAIL wrote receipt with budget 2: %v", rec2)
		}

		ci.Rerun(f.ctx, st, f.sprint, label, "actor", "rerun-2")
		vals3, _ := f.client.HMGet(f.ctx, key, "state", "attempt", "base_sha", "bench").Result()
		identity3 := fmt.Sprintf("%s/%s/%s/bench-1/%s", f.sprint, label, vals3[2], vals3[1])
		token3 := vals3[1].(string) + ".0123456789abcdef0123456789abcdef"
		f.client.HSet(f.ctx, key, "state", "dealt", "bench", "bench-1", "identity", identity3, "token", token3, "token_sha", "abcdefabcdef")
		f.client.SMove(f.ctx, "s:"+f.sprint+":idx:card:queued", "s:"+f.sprint+":idx:card:dealt", label)
		ci.End(f.ctx, st, ci.EndRecord{
			Sprint: f.sprint, Label: label, Token: token3, Identity: identity3,
			Outcome: "DONE", Reason: "done", Verdict: "FAIL", Pkg: "p", Test: "t", Actor: "wrapper",
		})
		rec3, _ := civerdict.ReadHead(f.ctx, f.client, f.repo, headH)
		if rec3["verdict"] != "FAIL" {
			t.Fatalf("third FAIL verdict: %s, want FAIL", rec3["verdict"])
		}
	})

	t.Run("harvested card CICut carries its branch", func(t *testing.T) {
		c := map[string]string{
			"repo":     "nova-tools",
			"pr":       "201",
			"head":     "7878787878787878787878787878787878787878",
			"base":     "feature/branch-x",
			"base_sha": "1111111111111111111111111111111111111111",
		}
		cutReq := consume.CICut{
			Repo:    c["repo"],
			Head:    c["head"],
			Base:    c["base_sha"],
			BaseRef: c["base"],
		}
		if cutReq.BaseRef != "feature/branch-x" {
			t.Fatalf("CICut.BaseRef: got %q, want feature/branch-x", cutReq.BaseRef)
		}
	})

	t.Run("preflight 7.16 is green on a gid source", func(t *testing.T) {
		in := preflight.FleetInput{
			Loaded: preflight.Loaded{Profiles: true, Workflows: true, ReviewReady: true, LandReady: true},
			Profiles: []preflight.BenchProfile{
				{Bench: "ctl-hulk", OS: "linux", Legs: []string{"go"}},
			},
			ReviewReady: []preflight.HeadRecord{{Repo: "nova-tools", PR: 10, Head: "4567def", RunnerOnly: true}},
			LandReady:   []preflight.LandReceipt{{Repo: "nova-tools", PR: 10, Head: "4567def", Source: "ci:nova-tools:4567def:gid1"}},
		}
		line := preflight.CheckTwoSchedulers(in)
		if line.Red {
			t.Fatalf("preflight 7.16 is red on gid source: %s", line)
		}
	})

	t.Run("sprintci.Bench.Run writes no Redis key", func(t *testing.T) {
		f := newLandFixture(t, "nova-tools", "dev")
		dealer, err := sprintci.New(4)
		if err != nil {
			t.Fatal(err)
		}
		b := &sprintci.Bench{
			Name:   "bench-1",
			Dealer: dealer,
			Redis:  f.client.Options().Addr,
		}
		keysBefore, _ := f.client.Keys(f.ctx, "*").Result()
		card := sprintci.Card{Repo: "nova-tools", PR: 1, SHA: "1111111111111111111111111111111111111111"}
		dealer.Deal("bench-1", card)

		b.Run(f.ctx, "nonexistent-card")
		keysAfter, _ := f.client.Keys(f.ctx, "*").Result()
		if len(keysBefore) != len(keysAfter) {
			t.Fatalf("keys before=%d, after=%d; sprintci.Bench.Run wrote keys", len(keysBefore), len(keysAfter))
		}
	})
}

