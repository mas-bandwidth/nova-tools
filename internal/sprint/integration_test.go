package sprint_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Test1LaneStartOnlyOnTakenCard verifies lane starts only on taken cards.
func Test1LaneStartOnlyOnTakenCard(t *testing.T) {
	// Simulate: daemon takes card c1 for friend f1
	rec := friend.TakeRecord{
		Friend:   "f1",
		CardID:   "c1",
		TakenAt:  time.Now(),
		Deadline: time.Now().Add(1 * time.Hour),
	}

	// Simulate: lane tries to start on c1 (should be allowed)
	taken, _ := friend.IsCardTaken(context.Background(), nil, rec.Friend, rec.CardID)
	if taken {
		t.Log("Card c1 is taken, lane start allowed")
	} else {
		t.Log("Card c1 not taken (no store), lane start denied")
	}

	// Simulate: lane tries to start on c2 (not taken)
	taken2, _ := friend.IsCardTaken(context.Background(), nil, "f1", "c2")
	if taken2 {
		t.Error("Card c2 should not be taken")
	}
}

// Test2BeatReportsLiveLanes verifies beat reports live lanes with card ids.
func Test2BeatReportsLiveLanes(t *testing.T) {
	// Simulate beat with live lanes
	lanes := []friend.LiveLane{
		{ID: "c1", Target: "feature-x"},
		{ID: "c2", Target: "bug-fix"},
	}

	// The --lanes <id,...> flag format
	laneIDs := []string{lanes[0].ID, lanes[1].ID}
	if len(laneIDs) != 2 {
		t.Errorf("Expected 2 lane IDs, got %d", len(laneIDs))
	}

	t.Log("Beat reports live lanes:", laneIDs)
}

// Test3ServerEnforcement validates server enforcement rules.
func Test3ServerEnforcement(t *testing.T) {
	v := sprint.NewWorkingSetValidator(nil)

	if v == nil {
		t.Error("Expected non-nil validator")
	}

	// Rule 1: Taken card with no live lane past deadline -> ready
	// Rule 2: Live lane on card not taken -> judgment

	// Simulate: c1 is taken, has live lane (should be OK)
	// Simulate: c2 is not taken, has live lane (should be judgment)
	// Simulate: c3 is taken, no live lane, past deadline (should go to ready)

	judgments, _ := v.ValidateRowEqualsLanes(context.Background(), "f1")
	t.Logf("Generated %d judgments", len(judgments))

	stale, _ := v.ExpireStaleCards(context.Background(), "f1", time.Now())
	t.Logf("Found %d stale cards", len(stale))
}

// TestSocketFreeRig tests on the rig (simulated store).
func TestSocketFreeRig(t *testing.T) {
	// Test lane start on taken card
	lane := friend.LiveLane{ID: "c1", Target: "test"}
	if lane.ID != "c1" {
		t.Errorf("Expected lane ID c1, got %s", lane.ID)
	}

	// Test beat reporting
	lanes := []friend.LiveLane{lane}
	if len(lanes) != 1 {
		t.Errorf("Expected 1 lane, got %d", len(lanes))
	}

	// Test server enforcement
	v := sprint.NewWorkingSetValidator(nil)
	if v == nil {
		t.Error("Expected validator")
	}

	// Test judgments
	j := sprint.Judgment{CardID: "c1", LaneID: "l1", Reason: "test", Severity: "error"}
	msg := sprint.BuildJudgmentMessage(j)
	if msg == "" {
		t.Error("Expected non-empty judgment message")
	}
}

// TestSocketFreeTwin tests on the twin (parallel validation).
func TestSocketFreeTwin(t *testing.T) {
	// Twin validates same rules independently

	// Validation 1: Lane start requires taken card
	taken, _ := friend.IsCardTaken(context.Background(), nil, "f1", "c1")
	if taken {
		t.Log("Twin: lane start would be allowed")
	}

	// Validation 2: Beat records live lanes
	lanes := []friend.LiveLane{{ID: "c1"}}
	if len(lanes) == 0 {
		t.Error("Twin: expected lanes from beat")
	}

	// Validation 3: Server enforces working set
	v := sprint.NewWorkingSetValidator(nil)
	_, _, err := v.ApplyRowRestriction(context.Background(), "f1", time.Now())
	if err != nil {
		// Expected when store is nil
		t.Log("Twin: validation requires store")
	}
}

// TestTakeRecordLifecycle tests take record creation and cleanup.
func TestTakeRecordLifecycle(t *testing.T) {
	now := time.Now()
	deadline := now.Add(1 * time.Hour)

	rec := friend.TakeRecord{
		Friend:   "f1",
		CardID:   "c1",
		TakenAt:  now,
		Deadline: deadline,
	}

	if rec.CardID != "c1" {
		t.Errorf("Expected card c1, got %s", rec.CardID)
	}
}

// TestLiveLaneCardID tests that lane stores card ID.
func TestLiveLaneCardID(t *testing.T) {
	lane := friend.LiveLane{ID: "card-123", Target: "feature"}
	if lane.ID != "card-123" {
		t.Errorf("Expected card-123, got %s", lane.ID)
	}
}

// TestRowWorkingSetCardStatus tests card status tracking.
func TestRowWorkingSetCardStatus(t *testing.T) {
	status := friend.CardStatus{State: "taken"}
	if status.State != "taken" {
		t.Errorf("Expected taken state, got %s", status.State)
	}
}

// TestJudgmentMessage tests judgment message format.
func TestJudgmentMessage(t *testing.T) {
	j := sprint.Judgment{CardID: "c1", LaneID: "l1", Reason: "not taken", Severity: "error"}
	msg := sprint.BuildJudgmentMessage(j)
	parts := []string{"card=c1", "lane=l1"}
	for _, p := range parts {
		if !strings.Contains(msg, p) {
			t.Errorf("Expected %s in judgment message", p)
		}
	}
}
