package redisq_test

// The tests nova-tools #2277 names, for the four Layer 1 uses of
// docs/SPEC-REDIS.md (L42-50) and the round trip the spec demands of each
// (L52-53): "write through the instance, kill the instance, read the
// fallback, and assert the same value. Nothing in the package may make Redis
// the authority; the record is." A miniredis fake stands in for the instance
// (rule 6: no test opens a network socket), the kill is the fake's Close,
// and every fallback lands inside its own t.TempDir(). The fallbacks are read
// back through the tools that already own them -- nova-wake's report source,
// the record's own usage reader, nova-work's plan parser -- so a pass proves
// the uses degrade to the files those tools already take, never to a second
// design of them.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/redisq"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// killInstance kills the fake instance and disposes the dead connection, so
// a call against it fails at once instead of paying the client's redial
// backoff. The fallback contract is the same either way -- an unreachable
// instance is an unreachable instance -- and the test pays no wall-clock wait
// for the transport's noise.
func killInstance(mr *miniredis.Miniredis, q *redisq.Queue) {
	mr.Close()
	_ = q.Close()
}

// TestIssue2277 is the anchor the card names: it runs the five tests the
// issue lists, each under the issue's own name, so the one command the card
// gates on exercises every use.
func TestIssue2277(t *testing.T) {
	t.Run("TestWakeDoorbellFallsBackToReportAndEntryFiles", testWakeDoorbellFallsBackToReportAndEntryFiles)
	t.Run("TestSwarmSlotsAndLocksFallBackToLockFiles", testSwarmSlotsAndLocksFallBackToLockFiles)
	t.Run("TestBudgetsFallBackToUsageRows", testBudgetsFallBackToUsageRows)
	t.Run("TestPlanStateFallsBackToPlanFile", testPlanStateFallsBackToPlanFile)
	t.Run("TestFallbackRoundTripKillsInstanceAndReadsSameValue", testFallbackRoundTripKillsInstanceAndReadsSameValue)
}

func TestWakeDoorbellFallsBackToReportAndEntryFiles(t *testing.T) {
	testWakeDoorbellFallsBackToReportAndEntryFiles(t)
}

func TestSwarmSlotsAndLocksFallBackToLockFiles(t *testing.T) {
	testSwarmSlotsAndLocksFallBackToLockFiles(t)
}

func TestBudgetsFallBackToUsageRows(t *testing.T) {
	testBudgetsFallBackToUsageRows(t)
}

func TestPlanStateFallsBackToPlanFile(t *testing.T) {
	testPlanStateFallsBackToPlanFile(t)
}

func TestFallbackRoundTripKillsInstanceAndReadsSameValue(t *testing.T) {
	testFallbackRoundTripKillsInstanceAndReadsSameValue(t)
}

// a-wake-signal-written-through-the-instance-is-readable-from-the-report-files-after-the-instance-dies:
// the doorbell's ring lands in the one report file nova-wake's report source
// watches, under the reports directory its --reports flag polls, so a
// watcher already polling that directory sees the ring with no second design
// of the file. (The entry half of that source is a forge poll, not a file;
// the report file is the file half the doorbell rides.)
func testWakeDoorbellFallsBackToReportAndEntryFiles(t *testing.T) {
	ctx := context.Background()
	mr, q := newQueue(t)
	reports := t.TempDir()
	door := redisq.NewDoorbell(q, reports)
	signal := "card 9014 landed on bench-hulk; the red lane is one shorter"

	if err := door.Ring(ctx, "rowan", "night", signal, time.Minute); err != nil {
		t.Fatalf("ring the doorbell: %s", err)
	}
	// Through the living instance the signal reads back.
	got, err := door.Read(ctx, "rowan", "night")
	if err != nil || got != signal {
		t.Fatalf("read through the instance = %q err=%v, want the signal %q", got, err, signal)
	}
	// The fallback is the report file nova-wake watches: its one file name,
	// at depth, under the reports directory.
	path := door.ReportPath("rowan", "night")
	if base := filepath.Base(path); base != wake.ReportName {
		t.Fatalf("the doorbell's fallback is %q, want the one report file nova-wake watches (%q)", base, wake.ReportName)
	}
	// Kill the instance: the signal is readable from the report files.
	killInstance(mr, q)
	got, err = door.Read(ctx, "rowan", "night")
	if err != nil || got != signal {
		t.Fatalf("read the report files after the kill = %q err=%v, want the same signal %q", got, err, signal)
	}
	// And nova-wake's own report source sees the file, which is the proof the
	// ring rides the watcher that already polls the reports directory.
	res, err := (&wake.Reports{Dirs: []string{reports}}).Poll(ctx, time.Now().UTC())
	if err != nil {
		t.Fatalf("poll nova-wake's report source: %s", err)
	}
	found := false
	for _, item := range res.Items {
		if item.Kind == wake.KindReport && item.Key == "report:"+path {
			found = true
		}
	}
	if !found {
		t.Fatalf("nova-wake's report source does not see the doorbell's fallback file %s among %d items", path, len(res.Items))
	}
}

// a-slot-or-lock-claim-degrades-to-the-swarm's-lock-files-with-the-same-take-renew-release-contract:
// one fencing token, minted by the machinery already settled in this package,
// held by the instance key and by the lock file the swarm's directory mode
// takes, so killing the instance leaves the claim standing in the file and a
// stale token is a no-op in either store.
func testSwarmSlotsAndLocksFallBackToLockFiles(t *testing.T) {
	ctx := context.Background()
	mr, q := newQueue(t)
	root := t.TempDir()
	locks := redisq.NewSwarmLocks(q, root)

	claim, ok, err := locks.Take(ctx, "space", "slot-3", 10*time.Minute)
	if err != nil || !ok || claim == nil {
		t.Fatalf("take slot-3: claim=%+v ok=%v err=%v", claim, ok, err)
	}
	// The claim is the settled lease key, and the token the settled shape.
	if claim.Key != q.LeaseKey("space", "slot-3") || claim.Key != "swarm:lease:space:slot-3" {
		t.Fatalf("the claim's key is %q, want the settled lease key %q", claim.Key, q.LeaseKey("space", "slot-3"))
	}
	if len(claim.Token) != 32 {
		t.Fatalf("the fencing token %q is not the settled 32-hex-character shape", claim.Token)
	}
	// The lock file the swarm's directory mode takes holds the same token.
	file := locks.LockPath("space", "slot-3")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the lock file: %s", err)
	}
	if held := strings.TrimSpace(string(raw)); held != claim.Token {
		t.Fatalf("the lock file holds %q, want the claim's fencing token %q", held, claim.Token)
	}
	// Through the living instance the holder reads.
	holder, err := locks.Holder(ctx, "space", "slot-3")
	if err != nil || holder != claim.Token {
		t.Fatalf("holder through the instance = %q err=%v, want the claim's fencing token", holder, err)
	}
	// A stale token can neither renew nor release: the record's token check
	// refuses it, and the settled compare-and-release script would refuse it
	// in the instance for the same reason -- the same contract, both stores.
	stale := &redisq.Claim{Lease: &redisq.Lease{Key: claim.Key, Token: "not-the-holder"}, File: file}
	if renewed, err := locks.Renew(ctx, stale, time.Minute); err != nil || renewed {
		t.Fatalf("a stale token renewed: renewed=%v err=%v", renewed, err)
	}
	if released, err := locks.Release(ctx, stale); err != nil || released {
		t.Fatalf("a stale token released: released=%v err=%v", released, err)
	}
	// The holder renews through the living instance.
	if renewed, err := locks.Renew(ctx, claim, 10*time.Minute); err != nil || !renewed {
		t.Fatalf("the holder renews: renewed=%v err=%v", renewed, err)
	}
	// Kill the instance: the claim degrades to the lock file, same token.
	killInstance(mr, q)
	holder, err = locks.Holder(ctx, "space", "slot-3")
	if err != nil || holder != claim.Token {
		t.Fatalf("holder from the lock file after the kill = %q err=%v, want the same fencing token", holder, err)
	}
	// The same take/renew/release contract holds in the file alone.
	if renewed, err := locks.Renew(ctx, claim, 10*time.Minute); err != nil || !renewed {
		t.Fatalf("the holder renews through the lock file: renewed=%v err=%v", renewed, err)
	}
	if renewed, err := locks.Renew(ctx, stale, time.Minute); err != nil || renewed {
		t.Fatalf("a stale token renewed through the lock file: renewed=%v err=%v", renewed, err)
	}
	if released, err := locks.Release(ctx, stale); err != nil || released {
		t.Fatalf("a stale token released through the lock file: released=%v err=%v", released, err)
	}
	if released, err := locks.Release(ctx, claim); err != nil || !released {
		t.Fatalf("the holder releases through the lock file: released=%v err=%v", released, err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatalf("a released claim's lock file is gone; %s still stands", file)
	}
	// A lane holds a lock by the same claim, degraded or not.
	lane, ok, err := locks.Take(ctx, "lanes", "dev-merge", time.Minute)
	if err != nil || !ok || lane == nil {
		t.Fatalf("take the dev-merge lane lock: claim=%+v ok=%v err=%v", lane, ok, err)
	}
	if holder, err := locks.Holder(ctx, "lanes", "dev-merge"); err != nil || holder != lane.Token {
		t.Fatalf("the lane lock's holder after the kill = %q err=%v, want its fencing token", holder, err)
	}
}

// a-spend-counter-degrades-to-the-usage-rows-the-record-carries: each observed
// spend lands as a row of the record's own sixteen columns under the pool's
// usage/, and after the kill the counter reports the same spend by folding
// those rows with the record's own arithmetic.
func testBudgetsFallBackToUsageRows(t *testing.T) {
	ctx := context.Background()
	mr, q := newQueue(t)
	poolRoot := t.TempDir()
	budgets := redisq.NewBudgets(q, poolRoot)

	// Two observed spends, rule 13's three counted columns.
	if err := budgets.Spend(ctx, "rowan", "card-9014", 1000, 200, 50, time.Minute); err != nil {
		t.Fatalf("spend one: %s", err)
	}
	if err := budgets.Spend(ctx, "rowan", "card-9014", 500, 100, 25, time.Minute); err != nil {
		t.Fatalf("spend two: %s", err)
	}
	const want = 1000 + 200 + 50 + 500 + 100 + 25
	// Through the living instance the counter reports the spend.
	spent, err := budgets.Report(ctx, "rowan", "card-9014")
	if err != nil || spent != want {
		t.Fatalf("report through the instance = %d err=%v, want the observed spend %d", spent, err, want)
	}
	// The fallback is the usage row the record carries: the record's own
	// reader folds the two rows the way it folds a retried native card's.
	p, err := swarm.OpenPool(poolRoot)
	if err != nil {
		t.Fatalf("open the pool the rows live in: %s", err)
	}
	rows, err := p.ReadUsage()
	if err != nil || len(rows) != 1 {
		t.Fatalf("the record's usage reader read %d rows err=%v, want the one folded job row", len(rows), err)
	}
	for col, wantCol := range map[string]int{"tokens_in": 1500, "tokens_out": 300, "reasoning": 75} {
		got, ok := rows[0].Int(col)
		if !ok || got != wantCol {
			t.Fatalf("the folded row's %s = %d ok=%v, want %d", col, got, ok, wantCol)
		}
	}
	// The row's columns are the record's own sixteen, in its order.
	raw, err := os.ReadFile(budgets.UsagePath("card-9014"))
	if err != nil {
		t.Fatalf("read the usage file: %s", err)
	}
	if head := strings.SplitN(string(raw), "\n", 2)[0]; head != strings.Join(swarm.UsageColumns, "\t") {
		t.Fatalf("the usage row's columns are %q, want the record's own %q", head, strings.Join(swarm.UsageColumns, "\t"))
	}
	// Kill the instance: the spend reports the same from the rows.
	killInstance(mr, q)
	spent, err = budgets.Report(ctx, "rowan", "card-9014")
	if err != nil || spent != want {
		t.Fatalf("report from the usage rows after the kill = %d err=%v, want the same spend %d", spent, err, want)
	}
}

// plan-position-and-deed-degrade-to-the-plan-file-and-read-back-equal: the
// fallback is the .work plan file nova-work reads, one (:plan ...) whose
// current node carries the position, the deed and the owner, and after the
// kill the position reads back from it byte for byte.
func testPlanStateFallsBackToPlanFile(t *testing.T) {
	ctx := context.Background()
	mr, q := newQueue(t)
	planFile := filepath.Join(t.TempDir(), "plan.work")
	plans := redisq.NewPlanState(q, planFile)

	if err := plans.At(ctx, "rowan", "node-3", "live", time.Minute); err != nil {
		t.Fatalf("record where the plan is: %s", err)
	}
	// Through the living instance the position reads back.
	got, err := plans.Read(ctx, "rowan")
	if err != nil || got != "node-3\tlive" {
		t.Fatalf("read through the instance = %q err=%v, want node-3 live", got, err)
	}
	// The fallback is the plan file on disk, readable by nova-work's own
	// parser: a (:plan ...) whose current node carries the position, the
	// deed and the owner.
	data, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatalf("read the plan file: %s", err)
	}
	plan, err := worklang.ParsePlan(planFile, data, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("the fallback is not a plan file nova-work reads: %s", err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0].ID() != "node-3" {
		t.Fatalf("the plan file's nodes = %d, want the one node node-3", len(plan.Nodes))
	}
	if f, ok := plan.Nodes[0].Fields["state"]; !ok || f.Value != "live" {
		t.Fatalf("the plan file's node :state = %+v, want the deed live", plan.Nodes[0].Fields["state"])
	}
	if f, ok := plan.Nodes[0].Fields["owner"]; !ok || f.Value != "rowan" {
		t.Fatalf("the plan file's node :owner = %+v, want rowan", plan.Nodes[0].Fields["owner"])
	}
	// Every state the plan grammar admits round-trips, and a deed it does
	// not admit is refused, so the mirror and the grammar cannot drift.
	for _, deed := range worklang.KnownStates() {
		if err := plans.At(ctx, "rowan", "node-"+deed, deed, time.Minute); err != nil {
			t.Fatalf("the deed %q was refused: %s", deed, err)
		}
		body, err := os.ReadFile(planFile)
		if err != nil {
			t.Fatalf("read the plan file after the deed %q: %s", deed, err)
		}
		if _, err := worklang.ParsePlan(planFile, body, worklang.DefaultLimits()); err != nil {
			t.Fatalf("the deed %q did not land in a plan file nova-work reads: %s", deed, err)
		}
	}
	if err := plans.At(ctx, "rowan", "node-9", "nearly", time.Minute); err == nil {
		t.Fatalf("a deed the plan grammar does not admit was accepted")
	}
	// Kill the instance: the position degrades to the plan file, writes and
	// reads alike.
	killInstance(mr, q)
	if err := plans.At(ctx, "rowan", "node-4", "blocked", time.Minute); err != nil {
		t.Fatalf("record where the plan is through the fallback: %s", err)
	}
	got, err = plans.Read(ctx, "rowan")
	if err != nil || got != "node-4\tblocked" {
		t.Fatalf("read the plan file after the kill = %q err=%v, want node-4 blocked", got, err)
	}
}

// one-shared-helper-proving-per-use-write-kill-read-equal: the round trip
// SPEC-REDIS L52-53 demands, run once per use through the same helper -- the
// write goes through the instance, the kill is the instance's death, the read
// is the fallback's, and the value is the one written.
func testFallbackRoundTripKillsInstanceAndReadsSameValue(t *testing.T) {
	ctx := context.Background()

	// The wake doorbell.
	mr, q := newQueue(t)
	door := redisq.NewDoorbell(q, t.TempDir())
	fallbackRoundTrip(t, "wake doorbell",
		func() error { return door.Ring(ctx, "rowan", "night", "the wall moved the card to done", time.Minute) },
		func() { killInstance(mr, q) },
		func() (string, error) { return door.Read(ctx, "rowan", "night") },
		func() string { return "the wall moved the card to done" },
	)

	// The swarm's slots and locks.
	mr, q = newQueue(t)
	locks := redisq.NewSwarmLocks(q, t.TempDir())
	var claim *redisq.Claim
	fallbackRoundTrip(t, "swarm slots and locks",
		func() error {
			c, ok, err := locks.Take(ctx, "space", "slot-1", 10*time.Minute)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("the take lost slot-1 to a holder that was not there")
			}
			claim = c
			return nil
		},
		func() { killInstance(mr, q) },
		func() (string, error) { return locks.Holder(ctx, "space", "slot-1") },
		func() string { return claim.Token },
	)

	// The budgets rule 13 keeps.
	mr, q = newQueue(t)
	budgets := redisq.NewBudgets(q, t.TempDir())
	fallbackRoundTrip(t, "budgets",
		func() error { return budgets.Spend(ctx, "rowan", "card-7", 1200, 300, 75, time.Minute) },
		func() { killInstance(mr, q) },
		func() (string, error) {
			n, err := budgets.Report(ctx, "rowan", "card-7")
			return strconv.Itoa(n), err
		},
		func() string { return strconv.Itoa(1200 + 300 + 75) },
	)

	// Plan state.
	mr, q = newQueue(t)
	plans := redisq.NewPlanState(q, filepath.Join(t.TempDir(), "plan.work"))
	fallbackRoundTrip(t, "plan state",
		func() error { return plans.At(ctx, "rowan", "node-2", "ready", time.Minute) },
		func() { killInstance(mr, q) },
		func() (string, error) { return plans.Read(ctx, "rowan") },
		func() string { return "node-2\tready" },
	)
}

// fallbackRoundTrip is the shared helper: write through the instance, kill
// the instance, read the fallback, assert the same value. One shape, every
// use; the want is read after the write because a claim's token does not
// exist until it is minted.
func fallbackRoundTrip(t *testing.T, use string, write func() error, kill func(), read func() (string, error), want func() string) {
	t.Helper()
	if err := write(); err != nil {
		t.Fatalf("%s: write through the instance: %s", use, err)
	}
	expected := want()
	kill()
	got, err := read()
	if err != nil {
		t.Fatalf("%s: read the fallback after the kill: %s", use, err)
	}
	if got != expected {
		t.Fatalf("%s: after the instance died the fallback reads %q, want the same value %q", use, got, expected)
	}
}
