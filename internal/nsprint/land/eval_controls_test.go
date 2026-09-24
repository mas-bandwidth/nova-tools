package land_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/redis/go-redis/v9"
)

// TestL13: an order-only HOLD record is a note with stack_parent;
// a CI-only HOLD is a note; a substantive one stays a hold (§3.4, L13).
func TestL13(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	// 1. Setup a unit
	head := "1111111111111111111111111111111111111111"
	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint,
		Unit:   "task-13-order",
		Repo:   f.repo,
		Base:   f.base,
		Branch: "order-branch",
		Head:   head,
	})
	if err != nil {
		t.Fatalf("init unit: %v", err)
	}

	// 2. Order-only hold reason: "after #123"
	seq, isHold, err := land.RecordHoldOrNote(f.ctx, f.client, f.sprint, "task-13-order", "johnny", head, "objection", "after #123", "", "", "", "test")
	if err != nil {
		t.Fatalf("record order note: %v", err)
	}
	if isHold {
		t.Fatalf("expected order-only reason to be a NOTE, but got isHold=true")
	}
	if seq <= 0 {
		t.Fatalf("expected positive sequence number, got %d", seq)
	}

	// Verify holds_open is still 0
	holdsOpen, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "task-13-order"), "holds_open").Int()
	if holdsOpen != 0 {
		t.Fatalf("expected holds_open=0 for order note, got %d", holdsOpen)
	}

	// Verify stack_parent was set on unit
	stackParent, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "task-13-order"), "stack_parent").Result()
	if stackParent != "#123" {
		t.Fatalf("expected stack_parent=#123, got %s", stackParent)
	}

	// 3. CI-only hold reason: "ci failed on macos"
	_, isHoldCI, err := land.RecordHoldOrNote(f.ctx, f.client, f.sprint, "task-13-order", "stella", head, "objection", "ci failed on macos", "", "", "", "test")
	if err != nil {
		t.Fatalf("record ci note: %v", err)
	}
	if isHoldCI {
		t.Fatalf("expected CI-only reason to be a NOTE, but got isHold=true")
	}
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "task-13-order"), "holds_open").Int()
	if holdsOpen != 0 {
		t.Fatalf("expected holds_open=0 after ci note, got %d", holdsOpen)
	}

	// 4. Substantive hold: "leak in buffer: free before return"
	_, isHoldSub, err := land.RecordHoldOrNote(f.ctx, f.client, f.sprint, "task-13-order", "emma", head, "objection", "leak in buffer: free before return", "", "", "", "test")
	if err != nil {
		t.Fatalf("record substantive hold: %v", err)
	}
	if !isHoldSub {
		t.Fatalf("expected substantive reason to be a HOLD, got isHold=false")
	}
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "task-13-order"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("expected holds_open=1 after substantive hold, got %d", holdsOpen)
	}
}

// TestL25: replaying the 12-lane mix (15 holds, 13 missing reads, 12 pending reads)
// under a fixture policy readers: 1: zero gates run on those units, and why names each condition (§3.3, L25).
func TestL25(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	pol := &land.BasePolicy{
		Base:          f.base,
		LandBar:       10,
		Readers:       1, // L25 fixture policy: readers: 1
		RequiredSteps: []string{"test"},
	}
	if err := land.SyncPolicyToRedis(f.ctx, f.client, f.repo, pol); err != nil {
		t.Fatalf("sync policy: %v", err)
	}

	baseSHA := "1111111111111111111111111111111111111111"
	gid := land.GID("single", f.base, baseSHA, pol.RequiredSetID(), pol.PolicyID(), land.RunnerID())

	// Setup helper to create unit
	makeUnit := func(id, head string, cardDone bool) {
		_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint:  f.sprint,
			Unit:    id,
			Repo:    f.repo,
			Base:    f.base,
			Head:    head,
			BaseSHA: baseSHA,
		})
		if err != nil {
			t.Fatalf("unit head %s: %v", id, err)
		}
		if cardDone {
			_ = f.client.HSet(f.ctx, land.UnitKey(f.sprint, id), "card_done", head).Err()
		}
		// Write valid CI receipt so CI check passes
		_, _ = land.CallGateReceiptWrite(f.ctx, f.client, f.repo, head, gid, "OK", "single", f.base, baseSHA, pol.RequiredSetID(), pol.PolicyID(), land.RunnerID(), "batch-1:1", "bench-1", "pkg", "Test")
	}

	// 1. 15 units with open holds (with approved read, but blocked by hold)
	for i := 1; i <= 15; i++ {
		uid := fmt.Sprintf("unit-held-%d", i)
		h := fmt.Sprintf("heldsha%032d", i)
		makeUnit(uid, h, true)
		_, _ = land.CallRead(f.ctx, f.client, f.sprint, uid, "reader-1", h, "APPROVE", "10", "substance", "", "")
		_, _, err := land.CallHold(f.ctx, f.client, f.sprint, uid, "reviewer-1", h, "objection", "hold reason", "", "", "", "test")
		if err != nil {
			t.Fatalf("call hold %s: %v", uid, err)
		}
	}

	// 2. 13 units with missing reads (0 reads at head)
	for i := 1; i <= 13; i++ {
		uid := fmt.Sprintf("unit-unread-%d", i)
		h := fmt.Sprintf("unreadsha%030d", i)
		makeUnit(uid, h, true)
	}

	// 3. 12 units with pending / low score reads (score 8 < bar 10)
	for i := 1; i <= 12; i++ {
		uid := fmt.Sprintf("unit-lowscore-%d", i)
		h := fmt.Sprintf("lowscoresha%028d", i)
		makeUnit(uid, h, true)
		_, err := land.CallRead(f.ctx, f.client, f.sprint, uid, "reader-1", h, "APPROVE", "8", "substance", "", "")
		if err != nil {
			t.Fatalf("call read %s: %v", uid, err)
		}
	}

	// Run evaluation over all units
	evaluated, landable, err := land.EvalPass(f.ctx, f.client, f.sprint, f.repo, &land.RepoPolicy{
		Repo:  f.repo,
		Bases: map[string]*land.BasePolicy{f.base: pol},
	})
	if err != nil {
		t.Fatalf("eval pass: %v", err)
	}

	if evaluated != 40 {
		t.Fatalf("expected 40 units evaluated, got %d", evaluated)
	}
	// Zero of these 40 units must become landable!
	if landable != 0 {
		t.Fatalf("expected 0 units landable, got %d", landable)
	}

	// Verify landable zset is empty
	landableCount, _ := f.client.ZCard(f.ctx, land.LandableKey(f.sprint, f.repo, f.base)).Result()
	if landableCount != 0 {
		t.Fatalf("expected 0 units in landable zset, got %d", landableCount)
	}

	// Check that EvaluateUnit names each condition
	for i := 1; i <= 15; i++ {
		uid := fmt.Sprintf("unit-held-%d", i)
		res, _ := land.EvaluateUnit(f.ctx, f.client, f.sprint, uid, pol)
		if !strings.Contains(res.Reason, "holds") {
			t.Fatalf("held unit %s reason want 'holds', got %s", uid, res.Reason)
		}
	}
	for i := 1; i <= 13; i++ {
		uid := fmt.Sprintf("unit-unread-%d", i)
		res, _ := land.EvaluateUnit(f.ctx, f.client, f.sprint, uid, pol)
		if !strings.Contains(res.Reason, "reads 0/1") {
			t.Fatalf("unread unit %s reason want 'reads 0/1', got %s", uid, res.Reason)
		}
	}
	for i := 1; i <= 12; i++ {
		uid := fmt.Sprintf("unit-lowscore-%d", i)
		res, _ := land.EvaluateUnit(f.ctx, f.client, f.sprint, uid, pol)
		if !strings.Contains(res.Reason, "reads 0/1") {
			t.Fatalf("low score unit %s reason want 'reads 0/1', got %s", uid, res.Reason)
		}
	}
}

// TestL29: the #1589 case: Emma's newer APPROVE releases her older HOLD,
// an older APPROVE never releases a newer HOLD (§3.4, L29).
func TestL29(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	h1 := "1111111111111111111111111111111111111111"
	h2 := "2222222222222222222222222222222222222222"

	// Unit 1
	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint,
		Unit:   "unit-l29",
		Repo:   f.repo,
		Base:   f.base,
		Head:   h1,
	})
	if err != nil {
		t.Fatalf("init unit: %v", err)
	}

	// Emma puts a HOLD on unit-l29 at H1
	_, _, err = land.CallHold(f.ctx, f.client, f.sprint, "unit-l29", "emma", h1, "objection", "hold text", "", "", "", "test")
	if err != nil {
		t.Fatalf("emma hold: %v", err)
	}
	holdsOpen, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("expected holds_open=1, got %d", holdsOpen)
	}

	// Head moves to H2
	_ = f.client.HSet(f.ctx, land.UnitKey(f.sprint, "unit-l29"), "head", h2).Err()

	// Emma posts an APPROVE at H2
	_, err = land.CallRead(f.ctx, f.client, f.sprint, "unit-l29", "emma", h2, "APPROVE", "10", "substance", "", "")
	if err != nil {
		t.Fatalf("emma approve: %v", err)
	}

	// Supersede check runs
	isNewer := func(newer, older string) bool {
		return newer == h2 && older == h1
	}
	released, err := land.SupersedeHoldsOnApprove(f.ctx, f.client, f.sprint, "unit-l29", "emma", h2, isNewer)
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if len(released) != 1 || released[0] != "emma" {
		t.Fatalf("expected emma's hold released, got %v", released)
	}

	// Verify holds_open dropped to 0
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29"), "holds_open").Int()
	if holdsOpen != 0 {
		t.Fatalf("expected holds_open=0 after supersede, got %d", holdsOpen)
	}

	// Verify hold record has release_kind=superseded
	hkey := land.HoldKey(f.sprint, "unit-l29", "emma")
	hRec, _ := f.client.HGetAll(f.ctx, hkey).Result()
	if hRec["release_kind"] != "superseded" || hRec["released_by"] != "emma" {
		t.Fatalf("hold record want release_kind=superseded released_by=emma, got %v", hRec)
	}

	// Subcase: older APPROVE never releases newer HOLD
	_, err = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint,
		Unit:   "unit-l29-neg",
		Repo:   f.repo,
		Base:   f.base,
		Head:   h2,
	})
	if err != nil {
		t.Fatalf("init neg unit: %v", err)
	}
	// Rowan approves at H1
	_, _ = land.CallRead(f.ctx, f.client, f.sprint, "unit-l29-neg", "rowan", h1, "APPROVE", "10", "substance", "", "")
	// Rowan holds at H2
	_, _, _ = land.CallHold(f.ctx, f.client, f.sprint, "unit-l29-neg", "rowan", h2, "objection", "hold newer", "", "", "", "test")

	// Attempting to supersede from H1 against H2
	isNewerFalse := func(newer, older string) bool {
		return newer == h2 && older == h1 // H1 is older, not newer
	}
	relNeg, err := land.SupersedeHoldsOnApprove(f.ctx, f.client, f.sprint, "unit-l29-neg", "rowan", h1, isNewerFalse)
	if err != nil {
		t.Fatalf("supersede neg: %v", err)
	}
	if len(relNeg) != 0 {
		t.Fatalf("expected older approve NOT to release newer hold, got %v", relNeg)
	}
	holdsOpenNeg, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29-neg"), "holds_open").Int()
	if holdsOpenNeg != 1 {
		t.Fatalf("expected holds_open=1, got %d", holdsOpenNeg)
	}
}

// TestL29b: an untyped inbound hold keyed login:x is released only by x
// or a --releases record (§3.4, L29b).
func TestL29b(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	head := "1111111111111111111111111111111111111111"
	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint,
		Unit:   "unit-l29b",
		Repo:   f.repo,
		Base:   f.base,
		Head:   head,
	})
	if err != nil {
		t.Fatalf("init unit: %v", err)
	}

	// 1. Inbound comment starting with HOLD from login alice
	isObj, reason := land.ParseInboundObjection("issue_comment", "alice", "HOLD: bounds check missing on slice index")
	if !isObj {
		t.Fatalf("expected inbound objection to be detected")
	}

	_, isHold, err := land.RecordInboundObjection(f.ctx, f.client, f.sprint, "unit-l29b", head, "alice", reason)
	if err != nil || !isHold {
		t.Fatalf("record inbound objection: isHold=%v, err=%v", isHold, err)
	}

	holdsOpen, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29b"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("expected holds_open=1, got %d", holdsOpen)
	}

	// 2. An untyped inbound comment starting with APPROVE: cannot approve or release (#1375)
	isObjApprove, _ := land.ParseInboundObjection("issue_comment", "bob", "APPROVE: LGTM!")
	if isObjApprove {
		t.Fatalf("expected approve comment not to be an objection")
	}
	// holds_open remains 1
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29b"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("holds_open changed unexpectedly: %d", holdsOpen)
	}

	// 3. Inbound release comment from login alice
	_, err = land.ReleaseInboundObjection(f.ctx, f.client, f.sprint, "unit-l29b", "alice", "alice", "fixed now")
	if err != nil {
		t.Fatalf("release by alice: %v", err)
	}
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29b"), "holds_open").Int()
	if holdsOpen != 0 {
		t.Fatalf("expected holds_open=0 after alice release, got %d", holdsOpen)
	}

	// 4. Release by friend via --releases record
	// Setup second hold from carol
	_, _, _ = land.RecordInboundObjection(f.ctx, f.client, f.sprint, "unit-l29b", head, "carol", "HOLD: needs doc")
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29b"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("expected holds_open=1 after carol hold, got %d", holdsOpen)
	}

	// Friend stella releases carol's inbound hold via --releases
	_, err = land.ReleaseInboundObjection(f.ctx, f.client, f.sprint, "unit-l29b", "carol", "stella", "overridden by friend stella")
	if err != nil {
		t.Fatalf("release carol by stella: %v", err)
	}
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29b"), "holds_open").Int()
	if holdsOpen != 0 {
		t.Fatalf("expected holds_open=0 after stella release, got %d", holdsOpen)
	}
}

// TestL29c: a HOLD by a friend with friend:<x>:down is released by a may-hold reader's --releases;
// without down the same release is refused (§3.4, L29c).
func TestL29c(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	head := "1111111111111111111111111111111111111111"
	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint,
		Unit:   "unit-l29c",
		Repo:   f.repo,
		Base:   f.base,
		Head:   head,
	})
	if err != nil {
		t.Fatalf("init unit: %v", err)
	}

	// Stella places a HOLD
	_, _, err = land.CallHold(f.ctx, f.client, f.sprint, "unit-l29c", "stella", head, "objection", "stella hold", "", "", "", "test")
	if err != nil {
		t.Fatalf("stella hold: %v", err)
	}
	holdsOpen, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29c"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("expected holds_open=1, got %d", holdsOpen)
	}

	// 1. Without down: Emma tries to release Stella's hold -> REFUSED
	_, err = land.CallRelease(f.ctx, f.client, f.sprint, "unit-l29c", "stella", "emma", "override", "releasing stella hold", "")
	if err == nil || !strings.Contains(err.Error(), "holder not down") {
		t.Fatalf("expected REFUSED holder not down, got err=%v", err)
	}

	// Stella's hold is still open
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29c"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("expected holds_open=1 after refused release, got %d", holdsOpen)
	}

	// 2. Mark Stella as down
	if err := f.client.Set(f.ctx, "friend:stella:down", "1", 0).Err(); err != nil {
		t.Fatalf("set stella down: %v", err)
	}

	// 3. With down, a releaser who is not a may-hold reader is refused (3.4)
	_, err = land.CallReleaseAs(f.ctx, f.client, f.sprint, "unit-l29c", "stella", "mallory", "down-friend", "stella is down", "", []string{"emma", "johnny"})
	if err == nil || !strings.Contains(err.Error(), "releaser not may-hold") {
		t.Fatalf("expected REFUSED releaser not may-hold, got err=%v", err)
	}
	_, err = land.CallRelease(f.ctx, f.client, f.sprint, "unit-l29c", "stella", "emma", "down-friend", "stella is down", "")
	if err == nil || !strings.Contains(err.Error(), "releaser not may-hold") {
		t.Fatalf("expected REFUSED with no roster, got err=%v", err)
	}

	// 4. With down: Emma, a may-hold reader, releases Stella's hold -> SUCCESS
	seq, err := land.CallReleaseAs(f.ctx, f.client, f.sprint, "unit-l29c", "stella", "emma", "down-friend", "stella is down", "", []string{"emma", "johnny"})
	if err != nil {
		t.Fatalf("release down friend: %v", err)
	}
	if seq <= 0 {
		t.Fatalf("expected positive release seq, got %d", seq)
	}

	// Holds open is now 0
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29c"), "holds_open").Int()
	if holdsOpen != 0 {
		t.Fatalf("expected holds_open=0 after down friend release, got %d", holdsOpen)
	}

	// Check hold record names both
	hRec, _ := f.client.HGetAll(f.ctx, land.HoldKey(f.sprint, "unit-l29c", "stella")).Result()
	if hRec["released_by"] != "emma" || hRec["release_kind"] != "down-friend" {
		t.Fatalf("expected released_by=emma release_kind=down-friend, got %v", hRec)
	}

	// 5. Repair-scoped (3.5) is not refused for a non-holder: it releases a
	// hold that names a done_when test, and only such a hold.
	_, _, _ = land.CallHold(f.ctx, f.client, f.sprint, "unit-l29c", "johnny", head, "objection", "TestX red", "", "a.go", "TestX", "test")
	_, _, _ = land.CallHold(f.ctx, f.client, f.sprint, "unit-l29c", "carol", head, "objection", "no test named", "", "", "", "test")
	if _, err := land.CallRelease(f.ctx, f.client, f.sprint, "unit-l29c", "johnny", "rowan", "repair-scoped", "TestX green at H2", ""); err != nil {
		t.Fatalf("repair-scoped release: %v", err)
	}
	if _, err := land.CallRelease(f.ctx, f.client, f.sprint, "unit-l29c", "carol", "rowan", "repair-scoped", "no done_when", ""); err == nil || !strings.Contains(err.Error(), "repair-scoped needs done_when") {
		t.Fatalf("expected repair-scoped refused without done_when, got %v", err)
	}
	if _, err := land.CallRelease(f.ctx, f.client, f.sprint, "unit-l29c", "carol", "rowan", "superseded", "not rowan's", ""); err == nil || !strings.Contains(err.Error(), "superseded only by the holder") {
		t.Fatalf("expected superseded by a non-holder refused, got %v", err)
	}
	holdsOpen, _ = f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-l29c"), "holds_open").Int()
	if holdsOpen != 1 {
		t.Fatalf("expected carol's hold alone open, holds_open=%d", holdsOpen)
	}
}

// TestL31b: against the gid lookup: a receipt for another base, base_sha,
// policy_id or runner_id is ci stale, never OK (§3.3, L31b).
func TestL31b(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	pol := &land.BasePolicy{
		Base:          f.base,
		LandBar:       10,
		Readers:       0,
		RequiredSteps: []string{"test"},
	}
	if err := land.SyncPolicyToRedis(f.ctx, f.client, f.repo, pol); err != nil {
		t.Fatalf("sync policy: %v", err)
	}

	headH := "1111111111111111111111111111111111111111"
	baseSHAExpected := "2222222222222222222222222222222222222222"

	_, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint:  f.sprint,
		Unit:    "unit-l31b",
		Repo:    f.repo,
		Base:    f.base,
		Head:    headH,
		BaseSHA: baseSHAExpected,
	})
	if err != nil {
		t.Fatalf("init unit: %v", err)
	}
	_ = f.client.HSet(f.ctx, land.UnitKey(f.sprint, "unit-l31b"), "card_done", headH).Err()

	// Subcase 1: No receipts at all -> ci missing
	resMissing, _ := land.EvaluateUnit(f.ctx, f.client, f.sprint, "unit-l31b", pol)
	if resMissing.CIStatus != "missing" || resMissing.Landable {
		t.Fatalf("expected CIStatus=missing landable=false, got %v", resMissing)
	}

	// Subcase 2: Receipt for another base_sha -> ci stale
	baseSHAOld := "3333333333333333333333333333333333333333"
	gidOld := land.GID("single", f.base, baseSHAOld, pol.RequiredSetID(), pol.PolicyID(), land.RunnerID())
	_, err = land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gidOld, "OK", "single", f.base, baseSHAOld, pol.RequiredSetID(), pol.PolicyID(), land.RunnerID(), "batch-old:1", "bench-1", "pkg", "Test")
	if err != nil {
		t.Fatalf("write old receipt: %v", err)
	}

	resStaleBaseSHA, _ := land.EvaluateUnit(f.ctx, f.client, f.sprint, "unit-l31b", pol)
	if resStaleBaseSHA.CIStatus != "stale" || resStaleBaseSHA.Landable {
		t.Fatalf("expected CIStatus=stale landable=false for older base_sha, got %v", resStaleBaseSHA)
	}
	if !strings.HasPrefix(resStaleBaseSHA.Reason, "ci stale") {
		t.Fatalf("expected reason starting with 'ci stale', got %s", resStaleBaseSHA.Reason)
	}

	// Subcase 3: Receipt for another policy_id -> ci stale
	gidOldPol := land.GID("single", f.base, baseSHAExpected, pol.RequiredSetID(), "policy-v999", land.RunnerID())
	_, _ = land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, gidOldPol, "OK", "single", f.base, baseSHAExpected, pol.RequiredSetID(), "policy-v999", land.RunnerID(), "batch-pol:1", "bench-1", "pkg", "Test")

	resStalePol, _ := land.EvaluateUnit(f.ctx, f.client, f.sprint, "unit-l31b", pol)
	if resStalePol.CIStatus != "stale" || resStalePol.Landable {
		t.Fatalf("expected CIStatus=stale landable=false for older policy_id, got %v", resStalePol)
	}

	// Subcase 4: Expected receipt matches exact gid -> OK and landable!
	expectedGID := land.GID("single", f.base, baseSHAExpected, pol.RequiredSetID(), pol.PolicyID(), land.RunnerID())
	_, err = land.CallGateReceiptWrite(f.ctx, f.client, f.repo, headH, expectedGID, "OK", "single", f.base, baseSHAExpected, pol.RequiredSetID(), pol.PolicyID(), land.RunnerID(), "batch-exact:1", "bench-1", "pkg", "Test")
	if err != nil {
		t.Fatalf("write exact receipt: %v", err)
	}

	resOK, err := land.EvaluateUnit(f.ctx, f.client, f.sprint, "unit-l31b", pol)
	if err != nil || resOK.CIStatus != "OK" || !resOK.Landable {
		t.Fatalf("expected CIStatus=OK landable=true, got res=%v, err=%v", resOK, err)
	}
}

// TestL32: a stale inbound consumer refuses the intent (inbound-stale);
// a ref moved without a delivery writes INBOUND MISSED and the head from git (§3.2, L32).
func TestL32(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")

	// Subcase 1: inbound consumer stale refuses intent
	// Setup batch
	batchID := "batch-l32-1"
	token, _, err := land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, "unit-l32@1111111111111111111111111111111111111111", "file.go", "go", "1111111111111111111111111111111111111111", "input-1")
	if err != nil {
		// Create unit first
		_, _ = land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
			Sprint: f.sprint,
			Unit:   "unit-l32",
			Repo:   f.repo,
			Base:   f.base,
			Head:   "1111111111111111111111111111111111111111",
		})
		_, _ = land.CallUnitEval(f.ctx, f.client, f.sprint, "unit-l32", f.repo, f.base, 0)
		token, _, err = land.CallBatchPlan(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease, "unit-l32@1111111111111111111111111111111111111111", "file.go", "go", "1111111111111111111111111111111111111111", "input-1")
		if err != nil {
			t.Fatalf("batch plan: %v", err)
		}
	}
	_, _ = land.CallGateClaim(f.ctx, f.client, f.repo, f.base, batchID, 1, token, "bench-1", "slot-1")
	_, _ = land.CallGateReceipt(f.ctx, f.client, f.repo, f.base, batchID, 1, token, "GREEN", "bench-1", "worker-1", "train-head", "train-tree", "input-1", "", "", "", "", "10")

	// Set inbound consumer stale: pending=2
	_ = f.client.HSet(f.ctx, "ev:github:consumer:land", "pending", "2", "at", strconv.FormatInt(time.Now().Unix(), 10)).Err()
	fresh, reason, _ := land.CheckInboundFreshness(f.ctx, f.client, time.Now())
	if fresh || !strings.Contains(reason, "inbound-stale") {
		t.Fatalf("expected inbound fresh=false, reason inbound-stale, got fresh=%v, reason=%s", fresh, reason)
	}

	// ns_land_intent must return REFUSED inbound-stale
	_, err = land.CallLandIntent(f.ctx, f.client, f.sprint, f.repo, f.base, batchID, f.lease)
	if err == nil || !strings.Contains(err.Error(), "inbound-stale") {
		t.Fatalf("expected intent refused inbound-stale, got err=%v", err)
	}

	// Set inbound consumer beat older than 10s
	_ = f.client.HSet(f.ctx, "ev:github:consumer:land", "pending", "0", "at", strconv.FormatInt(time.Now().Unix()-20, 10)).Err()
	freshOld, _, _ := land.CheckInboundFreshness(f.ctx, f.client, time.Now())
	if freshOld {
		t.Fatalf("expected inbound fresh=false when age > 10s")
	}

	// A missing beat is no evidence: it refuses too (3.2)
	_ = f.client.Del(f.ctx, land.InboundConsumerKey).Err()
	if freshNone, reasonNone, _ := land.CheckInboundFreshness(f.ctx, f.client, time.Now()); freshNone || !strings.Contains(reasonNone, "no beat") {
		t.Fatalf("expected a missing beat to refuse, got fresh=%v reason=%s", freshNone, reasonNone)
	}
	if _, _, err := land.EvalPass(f.ctx, f.client, f.sprint, "other-repo", nil); err != nil {
		t.Fatalf("eval pass after the consumer drained: %v", err)
	}
	if fresh, reason, _ := land.CheckInboundFreshness(f.ctx, f.client, time.Now()); !fresh {
		t.Fatalf("expected the pass's own drain to beat fresh, got %s", reason)
	}

	// Subcase 2: git reconcile discovers moved ref with no webhook delivery
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: "unit-l32-card", Repo: f.repo, Base: f.base, Branch: "card-3139",
		Head: "9999999999999999999999999999999999999999", BaseSHA: "1111111111111111111111111111111111111111", Files: "a.go",
	}); err != nil {
		t.Fatalf("unit on card-3139: %v", err)
	}
	gitRefs := map[string]string{
		"refs/heads/card-3139": "9999999999999999999999999999999999999999",
	}
	// Initial state
	_, _ = land.ReconcileMirror(f.ctx, f.client, f.sprint, f.repo, "", gitRefs)

	// A delivered move (webhook named it, mirror fetched) is not missed
	if v, err := land.CallRefSeen(f.ctx, f.client, f.repo, "refs/heads/card-3139", "8888888888888888888888888888888888888888", "delivery"); err != nil || v != "MOVED" {
		t.Fatalf("delivery ref seen: %s %v", v, err)
	}
	gitRefs["refs/heads/card-3139"] = "8888888888888888888888888888888888888888"
	if missed, err := land.ReconcileMirror(f.ctx, f.client, f.sprint, f.repo, "", gitRefs); err != nil || len(missed) != 0 {
		t.Fatalf("delivered move must not be missed: %v %v", missed, err)
	}

	// Ref moves in git with no delivery
	gitRefs["refs/heads/card-3139"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	missed, err := land.ReconcileMirror(f.ctx, f.client, f.sprint, f.repo, "", gitRefs)
	if err != nil {
		t.Fatalf("reconcile mirror: %v", err)
	}
	if len(missed) != 1 || !strings.Contains(missed[0], "INBOUND MISSED ref=refs/heads/card-3139 sha=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("expected INBOUND MISSED for moved ref, got %v", missed)
	}

	// Check event written to stream
	events, _ := f.client.XRange(f.ctx, land.EventsStream(f.repo), "-", "+").Result()
	hasMissedEvent := false
	for _, ev := range events {
		if ev.Values["event"] == "INBOUND MISSED" && ev.Values["ref"] == "refs/heads/card-3139" {
			hasMissedEvent = true
			break
		}
	}
	if !hasMissedEvent {
		t.Fatalf("expected INBOUND MISSED event in stream, found: %v", events)
	}

	// ...and the head from git (L32 second clause)
	u, _ := f.client.HGetAll(f.ctx, land.UnitKey(f.sprint, "unit-l32-card")).Result()
	if u["head"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || u["branch"] != "card-3139" || u["files"] != "a.go" {
		t.Fatalf("expected the unit head written from git, got %v", u)
	}
	if n, _ := f.client.HGet(f.ctx, "land:"+f.repo+":inbound", "missed").Int(); n != 1 {
		t.Fatalf("expected missed count 1, got %d", n)
	}
}

// TestNoStateAPI verifies that land eval makes zero REST or GraphQL calls to GitHub,
// and fails if any such call is attempted (§0.1, L18).
func TestNoStateAPI(t *testing.T) {
	// 1. Dynamic verification: Install custom HTTP Transport that detects any HTTP calls
	callAttempted := false
	originalTransport := http.DefaultTransport
	http.DefaultTransport = &failingTransport{
		onCall: func(req *http.Request) {
			callAttempted = true
		},
	}
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})

	f := newLandFixture(t, "nova-tools", "dev")

	// Run EvalPass
	pol := &land.BasePolicy{
		Base:          f.base,
		LandBar:       10,
		Readers:       0,
		RequiredSteps: []string{"test"},
	}
	_, _, err := land.EvalPass(f.ctx, f.client, f.sprint, f.repo, &land.RepoPolicy{
		Repo:  f.repo,
		Bases: map[string]*land.BasePolicy{f.base: pol},
	})
	if err != nil {
		t.Fatalf("eval pass: %v", err)
	}

	if callAttempted {
		t.Fatalf("TestNoStateAPI: land eval attempted an HTTP call to external service!")
	}

	// 2. Static AST check: ensure no github API imports in land eval files
	files := []string{
		"eval.go",
		"eval_policy.go",
		"eval_records.go",
		"eval_freshness.go",
		"eval_unit.go",
	}
	fset := token.NewFileSet()
	for _, fn := range files {
		node, err := parser.ParseFile(fset, fn, nil, parser.ImportsOnly)
		if err != nil {
			continue // If file is elsewhere in tests
		}
		for _, imp := range node.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(path, "go-github") || strings.Contains(path, "graphql") {
				t.Fatalf("TestNoStateAPI: %s imports forbidden GitHub state API package: %s", fn, path)
			}
		}
	}
}

type failingTransport struct {
	onCall func(req *http.Request)
}

func (f *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.onCall != nil {
		f.onCall(req)
	}
	return nil, fmt.Errorf("TestNoStateAPI: HTTP call forbidden on land eval path: %s %s", req.Method, req.URL.String())
}

// TestEvalPassInboundHolds drives the verb's path (EvalPass, not the helpers):
// an issue_comment on ev:github whose body opens with HOLD records a
// login:<sender> hold on the PR's unit, a redelivery never counts twice, a
// RELEASE from the same login releases it, and an entry with no body is only
// counted (§3.1, 3.4, L29b).
func TestEvalPassInboundHolds(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	head := "abababababababababababababababababababab"
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{
		Sprint: f.sprint, Unit: "unit-in", Repo: f.repo, Base: f.base, Head: head, PR: "42",
	}); err != nil {
		t.Fatalf("unit: %v", err)
	}
	add := func(kind, sender, body string) {
		t.Helper()
		v := map[string]interface{}{"repo": "mas-bandwidth/nova-tools", "kind": kind, "number": "42",
			"head": head, "action": "created", "at": "1", "sender": sender, "comment_id": "7"}
		if body != "" {
			v["body"] = body
		}
		if err := f.client.XAdd(f.ctx, &redis.XAddArgs{Stream: land.GitHubStream, Values: v}).Err(); err != nil {
			t.Fatalf("xadd: %v", err)
		}
	}
	pass := func() {
		t.Helper()
		if _, err := land.RunEval(f.ctx, f.client, land.EvalConfig{Sprint: f.sprint, Repo: f.repo}); err != nil {
			t.Fatalf("run eval: %v", err)
		}
	}

	add("issue_comment", "alice", "HOLD this breaks the table")
	add("issue_comment", "alice", "HOLD still broken")
	add("issue_comment", "bob", "")
	add("issue_comment", "carol", "looks fine to me")
	pass()
	hold, _ := f.client.HGetAll(f.ctx, land.HoldKey(f.sprint, "unit-in", "login:alice")).Result()
	if hold["origin"] != "inbound" || hold["head"] != head || hold["released_by"] != "" {
		t.Fatalf("expected an open inbound hold for login:alice at head, got %v", hold)
	}
	if n, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-in"), "holds_open").Int(); n != 1 {
		t.Fatalf("expected holds_open=1, got %d", n)
	}
	if n, _ := f.client.XPending(f.ctx, land.GitHubStream, land.InboundGroup).Result(); n.Count != 0 {
		t.Fatalf("expected every entry acknowledged, pending %d", n.Count)
	}
	if fresh, reason, _ := land.CheckInboundFreshness(f.ctx, f.client, time.Now()); !fresh {
		t.Fatalf("expected the drain to beat fresh: %s", reason)
	}

	add("issue_comment", "carol", "RELEASE not mine to release")
	pass()
	if n, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-in"), "holds_open").Int(); n != 1 {
		t.Fatalf("another login's RELEASE must not release alice's hold, holds_open=%d", n)
	}
	add("issue_comment", "alice", "RELEASE fixed at head")
	pass()
	hold, _ = f.client.HGetAll(f.ctx, land.HoldKey(f.sprint, "unit-in", "login:alice")).Result()
	if hold["released_by"] != "login:alice" {
		t.Fatalf("expected alice's own RELEASE to release, got %v", hold)
	}
	if n, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-in"), "holds_open").Int(); n != 0 {
		t.Fatalf("expected holds_open=0, got %d", n)
	}
}

// TestEvalPassSupersedes: the pass itself releases a reader's HOLD at an older
// head when that reader's APPROVE is at the current head, and never a HOLD at
// the current head for an APPROVE at an older one (§3.4, L29 on the verb path).
func TestEvalPassSupersedes(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	h1 := "1010101010101010101010101010101010101010"
	h2 := "2020202020202020202020202020202020202020"
	unitHead := func(unit, h string) {
		t.Helper()
		if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{Sprint: f.sprint, Unit: unit, Repo: f.repo, Base: f.base, Head: h}); err != nil {
			t.Fatalf("unit head: %v", err)
		}
	}
	// emma held at h1, the unit moved to h2, emma approved at h2
	unitHead("unit-sup", h1)
	_, _, _ = land.CallHold(f.ctx, f.client, f.sprint, "unit-sup", "emma", h1, "objection", "TestX red", "", "", "", "verb")
	unitHead("unit-sup", h2)
	_, _ = land.CallRead(f.ctx, f.client, f.sprint, "unit-sup", "emma", h2, "APPROVE", "10", "substance", "", "")
	// stella held at the current head h2 and has an older APPROVE at h1
	unitHead("unit-keep", h2)
	_, _ = land.CallRead(f.ctx, f.client, f.sprint, "unit-keep", "stella", h1, "APPROVE", "10", "substance", "", "")
	_, _, _ = land.CallHold(f.ctx, f.client, f.sprint, "unit-keep", "stella", h2, "objection", "TestY red", "", "", "", "verb")

	if _, _, err := land.EvalPass(f.ctx, f.client, f.sprint, f.repo, nil); err != nil {
		t.Fatalf("eval pass: %v", err)
	}
	sup, _ := f.client.HGetAll(f.ctx, land.HoldKey(f.sprint, "unit-sup", "emma")).Result()
	if sup["release_kind"] != "superseded" || sup["released_by"] != "emma" {
		t.Fatalf("expected emma's older hold superseded by the pass, got %v", sup)
	}
	if n, _ := f.client.HGet(f.ctx, land.UnitKey(f.sprint, "unit-sup"), "holds_open").Int(); n != 0 {
		t.Fatalf("unit-sup holds_open=%d, want 0", n)
	}
	keep, _ := f.client.HGetAll(f.ctx, land.HoldKey(f.sprint, "unit-keep", "stella")).Result()
	if keep["released_by"] != "" {
		t.Fatalf("an older APPROVE released a newer hold: %v", keep)
	}
	// The readers index replaced the KEYS scan (2.2)
	if who, _ := f.client.SMembers(f.ctx, land.ReadersKey(f.sprint, "unit-sup")).Result(); len(who) != 1 || who[0] != "emma" {
		t.Fatalf("readers index = %v, want [emma]", who)
	}
}

// TestEvalPassQueuesOneCISingle: a unit whose expected identity has no
// receipt queues exactly one ci single across passes, with batch, attempt
// and token (§3.3, L31b).
func TestEvalPassQueuesOneCISingle(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	head := "3030303030303030303030303030303030303030"
	if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{Sprint: f.sprint, Unit: "unit-ci", Repo: f.repo, Base: f.base, Head: head, BaseSHA: "1111111111111111111111111111111111111111"}); err != nil {
		t.Fatalf("unit head: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := land.EvalPass(f.ctx, f.client, f.sprint, f.repo, nil); err != nil {
			t.Fatalf("eval pass %d: %v", i, err)
		}
	}
	entries, _ := f.client.XRange(f.ctx, land.GatesStream(f.repo), "-", "+").Result()
	if len(entries) != 1 {
		t.Fatalf("expected one ci single over three passes, got %d: %v", len(entries), entries)
	}
	e := entries[0].Values
	if e["kind"] != "single" || e["attempt"] != "1" || e["token"] == "" || e["batch"] == "" || e["unit"] != "unit-ci" {
		t.Fatalf("ci single entry missing identity fields: %v", e)
	}
}

// TestEvalPassMirrorReconcile: with a mirror, the pass fetches refs/heads/*,
// reads each unit branch from git, and for a ref that moved with no delivery
// writes INBOUND MISSED and the head from git with files from the mirror diff;
// a pull_request synchronize delivery fetches the branch and writes the head
// with no INBOUND MISSED (§3.1, 3.2, L32 on the verb path).
func TestEvalPassMirrorReconcile(t *testing.T) {
	f := newLandFixture(t, "nova-tools", "dev")
	dir := t.TempDir()
	up := filepath.Join(dir, "up")
	mirror := filepath.Join(dir, "mirror.git")
	git := func(d string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", d}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(up, 0o755); err != nil {
		t.Fatal(err)
	}
	git(up, "init", "-q", "-b", "dev")
	commit := func(file string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(up, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		git(up, "add", file)
		git(up, "commit", "-q", "-m", file)
		return git(up, "rev-parse", "HEAD")
	}
	base := commit("base.txt")
	git(up, "checkout", "-q", "-b", "card-a")
	h1 := commit("a.go")
	git(up, "checkout", "-q", "dev")
	git(up, "checkout", "-q", "-b", "card-b")
	b1 := commit("b.go")
	git(dir, "clone", "-q", "--bare", up, mirror)

	for _, u := range []struct{ id, branch, head, pr string }{{"unit-a", "card-a", h1, "5"}, {"unit-b", "card-b", b1, "6"}} {
		if _, err := land.CallUnitHead(f.ctx, f.client, land.UnitHeadParams{Sprint: f.sprint, Unit: u.id, Repo: f.repo, Base: f.base,
			Branch: u.branch, Head: u.head, BaseSHA: base, PR: u.pr}); err != nil {
			t.Fatalf("unit %s: %v", u.id, err)
		}
	}
	cfg := land.EvalConfig{Sprint: f.sprint, Repo: f.repo, MirrorDir: mirror, FetchEach: time.Nanosecond}
	if rep, err := land.RunEval(f.ctx, f.client, cfg); err != nil || len(rep.Missed) != 0 {
		t.Fatalf("first pass: missed=%v err=%v", rep.Missed, err)
	}

	// card-a moves with no delivery; card-b moves and its delivery arrives
	git(up, "checkout", "-q", "card-a")
	h2 := commit("a2.go")
	git(up, "checkout", "-q", "card-b")
	b2 := commit("b2.go")
	if err := f.client.XAdd(f.ctx, &redis.XAddArgs{Stream: land.GitHubStream, Values: map[string]interface{}{
		"repo": "mas-bandwidth/nova-tools", "kind": "pull_request", "number": "6", "head": b2, "action": "synchronize", "at": "1", "sender": "rowan", "comment_id": "",
	}}).Err(); err != nil {
		t.Fatal(err)
	}
	rep, err := land.RunEval(f.ctx, f.client, cfg)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if rep.Inbound.Heads != 1 {
		t.Fatalf("expected one head from the delivery, got %+v", rep.Inbound)
	}
	if len(rep.Missed) != 1 || !strings.Contains(rep.Missed[0], "INBOUND MISSED ref=refs/heads/card-a sha="+h2) {
		t.Fatalf("expected card-a missed only, got %v", rep.Missed)
	}
	ua, _ := f.client.HGetAll(f.ctx, land.UnitKey(f.sprint, "unit-a")).Result()
	if ua["head"] != h2 || ua["files"] != "a.go,a2.go" {
		t.Fatalf("unit-a head/files from git: %v", ua)
	}
	ub, _ := f.client.HGetAll(f.ctx, land.UnitKey(f.sprint, "unit-b")).Result()
	if ub["head"] != b2 || ub["files"] != "b.go,b2.go" {
		t.Fatalf("unit-b head/files from the delivery: %v", ub)
	}
}

// TestRunnerIDTracksBuild: runner_id names the stamped build, not a constant.
func TestRunnerIDTracksBuild(t *testing.T) {
	old := land.ToolsVersion
	t.Cleanup(func() { land.ToolsVersion = old })
	land.ToolsVersion = "v9.9.9-test"
	if id := land.RunnerID(); !strings.HasPrefix(id, "nova-tools-v9.9.9-test/go-") {
		t.Fatalf("runner id %q does not track the stamped build", id)
	}
	land.ToolsVersion = ""
	if id := land.RunnerID(); strings.Contains(id, "v0.12.0") {
		t.Fatalf("runner id %q is the old hard-coded version", id)
	}
}
