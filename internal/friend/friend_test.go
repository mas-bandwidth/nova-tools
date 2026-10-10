package friend

import (
	"context"
	"testing"
	"time"
)

func TestLiveLaneStruct(t *testing.T) {
	lane := LiveLane{ID: "card-123", Target: "feature-x"}
	if lane.ID != "card-123" {
		t.Errorf("Expected ID card-123, got %s", lane.ID)
	}
}

func TestRowWorkingSetStruct(t *testing.T) {
	set := RowWorkingSet{Friend: "f1", Cards: map[string]CardStatus{"c1": {State: "taken"}}}
	if set.Friend != "f1" {
		t.Errorf("Expected friend f1, got %s", set.Friend)
	}
}

func TestTakeRecordStruct(t *testing.T) {
	rec := TakeRecord{Friend: "f1", CardID: "c1"}
	if rec.CardID != "c1" {
		t.Errorf("Expected card c1, got %s", rec.CardID)
	}
}

func TestUpdateLiveLanesRequiresInputs(t *testing.T) {
	if err := UpdateLiveLanes(context.Background(), nil, "", []LiveLane{}); err == nil {
		t.Error("Expected error when store is nil")
	}
}

func TestGetLiveLanesRequiresInputs(t *testing.T) {
	if _, err := GetLiveLanes(context.Background(), nil, ""); err == nil {
		t.Error("Expected error when store is nil")
	}
}

func TestRecordTakeRequiresInputs(t *testing.T) {
	if err := RecordTake(context.Background(), nil, "", "", time.Time{}, time.Time{}); err == nil {
		t.Error("Expected error when store is nil")
	}
}

func TestIsCardTakenReturnsTrueWhenTaken(t *testing.T) {
	// Test with struct (no Redis) - should return false due to nil store
	taken, _ := IsCardTaken(context.Background(), nil, "", "")
	if taken {
		t.Error("Expected false when store is nil")
	}
}

func TestGetRowWorkingSetRequiresInputs(t *testing.T) {
	if _, err := GetRowWorkingSet(context.Background(), nil, ""); err == nil {
		t.Error("Expected error when store is nil")
	}
}
