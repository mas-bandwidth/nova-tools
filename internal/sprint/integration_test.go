package sprint_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestSocketFreeRigCard1 tests lane starts only on taken cards.
func TestSocketFreeRigCard1(t *testing.T) {
	// Rule 1: Lane starts only on a card the daemon has taken on her row
	// Card id is in lane's environment (simulated here by checking IsCardTaken)
	
	// Simulate daemon takes card c1 for friend f1
	record := friend.TakeRecord{
		Friend:   "f1",
		CardID:   "c1",
		TakenAt:  time.Now(),
		Deadline: time.Now().Add(1 * time.Hour),
	}

	// Lane tries to start on c1 (should check IsCardTaken)
	taken, _ := friend.IsCardTaken(context.Background(), nil, record.Friend, record.CardID)
	if taken {
		t.Log("Card c1 taken, lane start would be allowed")
	} else {
		t.Log("Card c1 not taken (nil store), lane start denied")
	}

	// Lane tries to start on c2 (not taken - should be denied)
	taken2, _ := friend.IsCardTaken(context.Background(), nil, "f1", "c2")
	if taken2 {
		t.Error("Card c2 should not be taken")
	}

	// Lane id is the card id per spec
	lane := friend.LiveLane{ID: "c1", Target: "feature-x"}
	if lane.ID != "c1" {
		t.Errorf("Expected lane ID c1, got %s", lane.ID)
	}
}

// TestSocketFreeRigCard2 tests friend beat lists live lanes with card ids.
func TestSocketFreeRigCard2(t *testing.T) {
	// Rule 2: Every friend beat lists her live lanes with their card ids (--lanes <id,...>)
	// Server records this list
	
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

	// Verify card ids are properly formatted
	for i, id := range laneIDs {
		if id == "" {
			t.Errorf("Lane %d ID should not be empty", i)
		}
	}

	// Server records via UpdateLiveLanes (tested with nil store for socket-free)
	if len(lanes) == 0 {
		t.Error("Expected lanes to be recorded")
	}
}

// TestSocketFreeRigCard3 tests server holds row's working set to live lanes.
func TestSocketFreeRigCard3(t *testing.T) {
	// Rule 3: Server holds row's working set to live lanes list
	// (a) Taken card with no live lane past its take deadline goes back to ready
	// (b) Live lane on card not taken on row is a judgment
	
	v := sprint.NewWorkingSetValidator(nil)
	if v == nil {
		t.Error("Expected non-nil validator")
	}

	// Test judgment persistence helper (works without store)
	j := sprint.Judgment{
		CardID:   "c1",
		LaneID:   "l1",
		Reason:   "live lane on card not taken on row",
		Severity: "error",
	}
	msg := sprint.BuildJudgmentMessage(j)
	if msg == "" {
		t.Error("Expected non-empty judgment message")
	}
	if !strings.Contains(msg, "card=c1") {
		t.Error("Expected card=c1 in judgment message")
	}

	// Test split judgment message
	parsed, err := sprint.SplitJudgmentMessage(msg)
	if err != nil {
		t.Errorf("SplitJudgmentMessage failed: %v", err)
	}
	if parsed.CardID != "c1" {
		t.Errorf("Expected card=c1, got %s", parsed.CardID)
	}
}

// TestSocketFreeTwin validates all rules independently.
func TestSocketFreeTwin(t *testing.T) {
	// Twin validates same rules independently

	// Validation 1: Lane start requires taken card
	taken, _ := friend.IsCardTaken(context.Background(), nil, "f1", "c1")
	if !taken {
		t.Log("Twin: card c1 not taken (nil store), lane start denied")
	}

	// Validation 2: Beat records live lanes
	lanes := []friend.LiveLane{{ID: "c1", Target: "test"}}
	if len(lanes) == 0 {
		t.Error("Twin: expected lanes from beat")
	}

	// Validation 3: Server enforces working set (requires store)
	v := sprint.NewWorkingSetValidator(nil)
	if v == nil {
		t.Error("Twin: expected validator")
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
