package land_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// hasLine fails unless some line starts with prefix and contains every part.
func hasLine(t *testing.T, lines []string, prefix string, parts ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		ok := true
		for _, p := range parts {
			if !strings.Contains(l, p) {
				ok = false
			}
		}
		if ok {
			return
		}
	}
	t.Fatalf("want a line %q containing %q in:\n%s", prefix, parts, strings.Join(lines, "\n"))
}

// TestL17 is control L17 (nova-tools#3139 rev 7 section 11, build B14):
// `why <unit>`, `why <repo>#<n>` and `land status` answer from Redis alone,
// with the GitHub client nil: the loaders take only a Redis client, and every
// record here is written by the nova_sprint functions that own it (2.2), so
// the unit keys are the ones the lander writes, not a hand-made shape. The
// retired s:<S>:pr:<repo>:<n> record is never written, and why still answers.
func TestL17(t *testing.T) {
	t.Parallel()

	f := newLandFixture(t, "nova-tools", "dev")
	ctx, c := f.ctx, f.client
	const (
		tip   = "1111111111111111111111111111111111111111"
		head  = "4139b79f0a1b2c3d4e5f60718293a4b5c6d7e8f9"
		head2 = "5222b79f0a1b2c3d4e5f60718293a4b5c6d7e8f9"
		unit  = "gh/mas-bandwidth/nova-tools/3200"
		unit2 = "gh/mas-bandwidth/nova-tools/3201"
	)
	now := time.Now()
	if err := land.CallPolicySet(ctx, c, f.repo, f.base, "pol1", "req1", "run1"); err != nil {
		t.Fatalf("policy: %v", err)
	}
	for _, u := range []struct{ unit, head, pr string }{{unit, head, "3200"}, {unit2, head2, "3201"}} {
		if _, err := land.CallUnitHead(ctx, c, land.UnitHeadParams{
			Sprint: f.sprint, Unit: u.unit, Repo: f.repo, Base: f.base, Branch: "card-" + u.pr,
			Head: u.head, BaseSHA: tip, StackParent: "none", PR: u.pr, Author: "rowan",
		}); err != nil {
			t.Fatalf("unit head %s: %v", u.unit, err)
		}
	}
	gid, err := civerdict.ExpectedFrom(f.base, tip, "pol1", "req1", "run1")
	if err != nil {
		t.Fatalf("gid: %v", err)
	}
	if _, err := land.CallGateReceiptWrite(ctx, c, f.repo, head, gid, "OK", "single", f.base, tip,
		"req1", "pol1", "run1", f.sprint+"/ci:1", "bench-1", "", ""); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	c.SAdd(ctx, "friends", "emma", "stella")
	c.HSet(ctx, "friend:emma:state", "state", "up", "since", now.Add(-5*time.Minute).Unix())
	if _, err := land.CallRead(ctx, c, f.sprint, unit, "stella", head, "APPROVE", "9", "substance", "", ""); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, _, err := land.CallHold(ctx, c, f.sprint, unit, "emma", head, "substance", "scope", "u1", "", "", "verb"); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if n, _ := c.Exists(ctx, "s:"+f.sprint+":pr:"+f.repo+":3200").Result(); n != 0 {
		t.Fatal("the retired PR record exists; L17 must answer without it")
	}

	// why <repo>#<n>: resolves through s:<S>:prunit to the unit.
	p, err := land.LoadPR(ctx, c, f.sprint, land.ID{Repo: f.repo, N: 3200})
	if err != nil {
		t.Fatalf("why nova-tools#3200: %v", err)
	}
	lines := land.Why(p, now)
	t.Logf("why nova-tools#3200 held:\n%s", strings.Join(lines, "\n"))
	hasLine(t, lines, "ci OK@4139b79f")
	hasLine(t, lines, "reads 1", "stella 9 @4139b79f")
	hasLine(t, lines, "holds 1 open (emma HOLD", "@4139b79f", "emma up 5m", "releasable by emma only: hold at head")
	hasLine(t, lines, "stack parent none")
	hasLine(t, lines, "drop none")
	hasLine(t, lines, "state reading")

	// The holder releases; the evaluator moves the unit into landable.
	if _, err := land.CallRelease(ctx, c, f.sprint, unit, "emma", "emma", "typed", "fixed", "u2"); err != nil {
		t.Fatalf("release: %v", err)
	}
	for _, u := range []string{unit, unit2} {
		if _, err := land.CallUnitEval(ctx, c, f.sprint, u, f.repo, f.base, 0); err != nil {
			t.Fatalf("eval %s: %v", u, err)
		}
	}
	// why <unit>: the unit form.
	u, err := land.LoadUnit(ctx, c, f.sprint, unit)
	if err != nil {
		t.Fatalf("why %s: %v", unit, err)
	}
	lines = land.Why(u, now)
	t.Logf("why %s released:\n%s", unit, strings.Join(lines, "\n"))
	hasLine(t, lines, "holds 0 open", "released by emma typed")
	hasLine(t, lines, "landable #1 of 2")

	// A unit not known to the sprint is no record, for either form.
	if _, err := land.LoadUnit(ctx, c, f.sprint, "gh/mas-bandwidth/nova-tools/9999"); err == nil || !strings.Contains(err.Error(), "MISSING s:"+f.sprint+":u:gh/mas-bandwidth/nova-tools/9999") {
		t.Fatalf("unknown unit: err=%v, want no record naming the unit key", err)
	}
	if _, err := land.LoadPR(ctx, c, f.sprint, land.ID{Repo: f.repo, N: 9999}); err == nil || !strings.Contains(err.Error(), "MISSING s:"+f.sprint+":prunit:nova-tools:9999") {
		t.Fatalf("unknown PR: err=%v, want no record naming the prunit key", err)
	}

	// land status: units, the chain with its batches, gates per bench with
	// the worker's liveness, landable, freezes.
	token, _, err := land.CallBatchPlan(ctx, c, f.sprint, f.repo, f.base, "b-l17", f.lease, unit2+"@"+head2, "", "go", tip, "in-l17")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if res, err := land.CallGateClaim(ctx, c, f.repo, f.base, "b-l17", 1, token, "bench-1", "0"); err != nil || res != "OK" {
		t.Fatalf("claim: %q %v", res, err)
	}
	c.HSet(ctx, land.FreezeKey(f.repo, f.base), "reason", "shadow day", "remedy", "land thaw nova-tools dev", "at", "1")
	snap, err := land.LoadStatus(ctx, c, f.sprint)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	lines = land.Status(snap, now)
	t.Logf("land status:\n%s", strings.Join(lines, "\n"))
	hasLine(t, lines, "units 2:", "batched 1", "landable 1")
	hasLine(t, lines, "chain nova-tools/dev 1:", "b-l17 gating attempt 1", unit2)
	hasLine(t, lines, "gates 1:", "bench-1/0 b-l17 attempt 1", "worker live")
	hasLine(t, lines, "landable nova-tools/dev 1, head "+unit)
	hasLine(t, lines, "dropped 0")
	hasLine(t, lines, "freezes 1:", "nova-tools/dev shadow day")
	hasLine(t, lines, "landed 0 in the last hour")

	// The worker's heartbeat lapses: the gate says so.
	c.Del(ctx, "worker:bench-1:0")
	snap, err = land.LoadStatus(ctx, c, f.sprint)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	hasLine(t, land.Status(snap, now), "gates 1:", "bench-1/0 b-l17", "worker MISSING worker:bench-1:0")
}
