package land_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/redis/go-redis/v9"
)

// TestUnitReapLander runs the #3091 reap fields through the real land.lua
// functions on the per-base lander keys: the reap writes never touch state or
// the landable zset, ns_land stamps merged_at with state=landed, and landed
// stays terminal. The retired PR keys (s:<S>:landable, s:<S>:pr:<repo>:<n>)
// never appear.
//
// The fixture satisfies ns_unit_eval as the base implements it (no input
// checks). It also writes the expected gid receipt and the card done record
// at head that #3139 B2's evaluator requires, so the test holds whichever of
// this build and B2 lands first.
func TestUnitReapLander(t *testing.T) {
	t.Parallel()

	f := newLandFixture(t, "nova-tools", "dev")

	const (
		unit      = "gh/mas-bandwidth/nova-tools/3091"
		cardLabel = "card-3091"
		head      = "3091111122223333444455556666777788889999"
		fromTip   = "1111111111111111111111111111111111111111"
		trainHead = "3091aaaa22223333444455556666777788889999"
		mergeSHA  = "3091bbbb22223333444455556666777788889999"
	)
	ukey := land.UnitKey(f.sprint, unit)
	zkey := land.LandableKey(f.sprint, f.repo, f.base)

	cardKeys := []string{
		"s:" + f.sprint + ":card:" + cardLabel, "s:" + f.sprint + ":pool", "s:" + f.sprint + ":waiting",
		"s:" + f.sprint + ":log", "s:" + f.sprint + ":idx:card:queued",
	}
	if reply, err := f.client.FCall(f.ctx, "ns_card_push", cardKeys,
		cardLabel, "payload-3091", "0", f.base, fromTip, "internal/nsprint/pr", "mas-bandwidth/nova-tools", "model", "", "code").Text(); err != nil || !strings.HasPrefix(reply, "OK place=") {
		t.Fatalf("ns_card_push: %q %v", reply, err)
	}
	params := land.UnitHeadParams{
		Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base, Branch: cardLabel,
		Head: head, BaseSHA: fromTip, PR: "42", Author: "emma", Card: cardLabel,
	}
	if _, err := land.CallUnitHead(f.ctx, f.client, params); err != nil {
		t.Fatalf("unit head: %v", err)
	}
	gid := land.GID("single", f.base, fromTip, "req-1", "pol-1", "runner-1")
	if _, err := land.CallGateReceiptWrite(f.ctx, f.client, f.repo, head, gid, "OK", "single", f.base, fromTip, "req-1", "pol-1", "runner-1", "fixture:1", "bench-1", "", ""); err != nil {
		t.Fatalf("gid receipt: %v", err)
	}
	if err := f.client.HSet(f.ctx, ukey, "card_done", head).Err(); err != nil {
		t.Fatalf("done record: %v", err)
	}
	if _, err := land.CallRead(f.ctx, f.client, f.sprint, unit, "stella", head, "APPROVE", "10", "read", "", ""); err != nil {
		t.Fatalf("counted read: %v", err)
	}

	zscore := func() (float64, bool) {
		t.Helper()
		s, err := f.client.ZScore(f.ctx, zkey, unit).Result()
		if errors.Is(err, redis.Nil) {
			return 0, false
		}
		if err != nil {
			t.Fatalf("zscore: %v", err)
		}
		return s, true
	}
	field := func(name string) string {
		t.Helper()
		return f.client.HGet(f.ctx, ukey, name).Val()
	}
	retiredAbsent := func(t *testing.T) {
		t.Helper()
		for _, k := range []string{"s:" + f.sprint + ":landable", "s:" + f.sprint + ":pr:nova-tools:42"} {
			if n, _ := f.client.Exists(f.ctx, k).Result(); n != 0 {
				t.Fatalf("retired PR key %s exists", k)
			}
		}
	}

	t.Run("eval_inserts_per_base", func(t *testing.T) {
		if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err != nil {
			t.Fatalf("unit eval: %v", err)
		}
		if _, ok := zscore(); !ok {
			t.Fatalf("ZSCORE %s %s is nil after ns_unit_eval", zkey, unit)
		}
		retiredAbsent(t)
	})

	t.Run("reap_writes_leave_state", func(t *testing.T) {
		state0 := field("state")
		score0, _ := zscore()
		if _, err := land.CallRead(f.ctx, f.client, f.sprint, unit, "emma", head, "APPROVE", "9", "read", "", ""); err != nil {
			t.Fatalf("counted read: %v", err)
		}
		if _, err := strconv.ParseInt(field("last_read_at"), 10, 64); err != nil || field("approve_head") != head {
			t.Fatalf("reap fields not written: last_read_at=%q approve_head=%q", field("last_read_at"), field("approve_head"))
		}
		score1, ok := zscore()
		if field("state") != state0 || !ok || score1 != score0 {
			t.Fatalf("a reap write moved state %q -> %q or score %v -> %v", state0, field("state"), score0, score1)
		}
	})

	t.Run("land_removes_and_stamps", func(t *testing.T) {
		token, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-3091", f.lease, unit+"@"+head, "", "go", fromTip, "in-3091")
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if res, err := land.CallGateClaim(f.ctx, f.client, f.repo, f.base, "batch-3091", 1, token, "bench-1", "slot-1"); err != nil || res != "OK" {
			t.Fatalf("claim: %q %v", res, err)
		}
		if res, err := land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, "batch-3091", 1, token, "GREEN", "bench-1", "worker-1", trainHead, "tree-3091", "in-3091", "", "", "", "", "1"); err != nil || res != "OK" {
			t.Fatalf("gate receipt: %q %v", res, err)
		}
		if _, err := land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-3091", f.lease); err != nil {
			t.Fatalf("intent: %v", err)
		}
		if res, err := land.CallLand(f.ctx, f.client, f.sprint, f.repo, f.base, "batch-3091", f.lease, trainHead, mergeSHA, "1"); err != nil || res != "OK" {
			t.Fatalf("ns_land: %q %v", res, err)
		}
		if field("state") != "landed" || field("merge_sha") != mergeSHA || field("landed_head") != head {
			t.Fatalf("after ns_land: state=%q merge_sha=%q landed_head=%q", field("state"), field("merge_sha"), field("landed_head"))
		}
		if _, err := strconv.ParseInt(field("merged_at"), 10, 64); err != nil {
			t.Fatalf("merged_at = %q, want seconds", field("merged_at"))
		}
		if _, ok := zscore(); ok {
			t.Fatalf("landed unit still in %s", zkey)
		}
		retiredAbsent(t)
	})

	t.Run("landed_terminal", func(t *testing.T) {
		merged := field("merged_at")
		if _, err := land.CallRead(f.ctx, f.client, f.sprint, unit, "stella", head, "APPROVE", "10", "read", "", ""); err != nil {
			t.Fatalf("read after land: %v", err)
		}
		if _, err := land.CallUnitHead(f.ctx, f.client, params); err != nil {
			t.Fatalf("unit head replay: %v", err)
		}
		if _, err := land.CallUnitEval(f.ctx, f.client, f.sprint, unit, f.repo, f.base, 0); err == nil || !strings.Contains(err.Error(), "REFUSED landed") {
			t.Fatalf("eval of a landed unit: %v, want REFUSED landed", err)
		}
		if field("state") != "landed" || field("merged_at") != merged {
			t.Fatalf("landed unit moved: state=%q merged_at %q -> %q", field("state"), merged, field("merged_at"))
		}
		if _, ok := zscore(); ok {
			t.Fatalf("landed unit re-entered %s", zkey)
		}
		retiredAbsent(t)
	})
}
