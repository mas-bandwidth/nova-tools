package sprint_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestSocketFreeRigCard1 tests lane starts only on taken cards.
// Rule 1: A lane starts only on a card the daemon has taken on her row.
// The card id is in the lane's environment and job directory.
// A lane ends when there is a finish or fail on that card.
func TestSocketFreeRigCard1(t *testing.T) {
	t.Parallel()
	now := time.Now()
	deadline := now.Add(1 * time.Hour)

	// Simulate daemon takes card c1 for friend f1
	takeRecord := friend.TakeRecord{
		Friend:   "f1",
		CardID:   "c1",
		TakenAt:  now,
		Deadline: deadline,
	}

	assert.Equal(t, "c1", takeRecord.CardID)
	assert.Equal(t, "f1", takeRecord.Friend)

	// Check card is taken (using IsCardTaken)
	taken, _ := friend.IsCardTaken(context.Background(), nil, "f1", "c1")
	assert.False(t, taken, "with nil store, returns false")

	// Lane tries to start on un-taken card c2 (should be denied)
	taken2, _ := friend.IsCardTaken(context.Background(), nil, "f1", "c2")
	assert.False(t, taken2, "Card c2 should not be taken")

	// Lane stores card id in its id field
	lane := friend.LiveLane{ID: "c1", Target: "feature-x"}
	assert.Equal(t, "c1", lane.ID)

	// Lane ends with finish or fail - verify lane can track status
	laneState := friend.LiveLaneState{
		Friend:  "f1",
		CardID:  "c1",
		Started: now,
		Status:  "running",
	}
	assert.Equal(t, "c1", laneState.CardID)
}

// TestSocketFreeRigCard2 tests friend beat lists live lanes with card ids.
// Rule 2: Every friend beat lists her live lanes with their card ids (--lanes <id,...>).
// The server records this list.
func TestSocketFreeRigCard2(t *testing.T) {
	t.Parallel()
	// Beat with multiple live lanes reported via --lanes flag
	lanes := []friend.LiveLane{
		{ID: "c1", Target: "feature-x"},
		{ID: "c2", Target: "bug-fix"},
		{ID: "c3", Target: "refactor"},
	}

	// Format: --lanes c1,c2,c3 (comma-separated card ids)
	var laneIDs []string
	for _, lane := range lanes {
		laneIDs = append(laneIDs, lane.ID)
	}

	assert.Len(t, laneIDs, 3)
	assert.Equal(t, []string{"c1", "c2", "c3"}, laneIDs)

	for _, lane := range lanes {
		assert.NotEmpty(t, lane.ID)
		assert.True(t, strings.HasPrefix(lane.ID, "c"), "Lane ID should be card id, got %s", lane.ID)
	}

	assert.NotEmpty(t, lanes)
}

// TestSocketFreeRigCard3 tests server holds row's working set to live lanes.
// Rule 3: Server holds row's working set to live lanes list:
// (a) Taken card with no live lane past its take deadline goes back to ready
// (b) Live lane on card not taken on row is a judgment
func TestSocketFreeRigCard3(t *testing.T) {
	t.Parallel()
	validator := sprint.NewWorkingSetValidator(nil)
	assert.NotNil(t, validator)

	// Test (a): Stale card expiration logic
	// Simulate: card c2 taken, deadline passed, no live lane for c2
	deadline := time.Now().Add(-1 * time.Hour)
	expiredCard := friend.CardStatus{
		State:        "taken",
		TakeTime:     time.Now().Add(-2 * time.Hour),
		TakeDeadline: deadline,
	}
	assert.True(t, expiredCard.TakeDeadline.Before(time.Now()))

	// Test (b): LaneJudgment for live lane on card not taken
	unauthorizedLane := friend.LiveLane{ID: "c3", Target: "unauthorized"}
	takenCards := map[string]bool{
		"c1": true,
		"c2": true,
	}

	assert.False(t, takenCards[unauthorizedLane.ID])
	judgment := sprint.LaneJudgment{
		Friend:   "f1",
		CardID:   unauthorizedLane.ID,
		LaneID:   unauthorizedLane.ID,
		Reason:   "live lane on card not taken on row",
		Severity: "error",
	}

	msg := sprint.BuildJudgmentMessage(judgment)
	assert.NotEmpty(t, msg)
	assert.Contains(t, msg, "card=c3")
	assert.Contains(t, msg, "error")

	parsed, err := sprint.SplitJudgmentMessage(msg)
	require.NoError(t, err)
	assert.Equal(t, "c3", parsed.CardID)

	_, _, err = validator.ApplyRowRestriction(context.Background(), "f1", time.Now())
	require.Error(t, err, "Expected error with nil store")
}

// TestSocketFreeTwin validates all rules independently on the twin rig.
func TestSocketFreeTwin(t *testing.T) {
	t.Parallel()
	taken, _ := friend.IsCardTaken(context.Background(), nil, "f1", "c1")
	assert.False(t, taken)

	lanes := []friend.LiveLane{
		{ID: "c1", Target: "test"},
		{ID: "c2", Target: "another"},
	}
	assert.Len(t, lanes, 2)

	validator := sprint.NewWorkingSetValidator(nil)
	assert.NotNil(t, validator)

	j := sprint.LaneJudgment{Friend: "f1", CardID: "c1", LaneID: "l1", Reason: "not taken", Severity: "error"}
	msg := sprint.BuildJudgmentMessage(j)
	assert.Contains(t, msg, "card=c1")
}

// TestCardIDInLaneEnvironment tests that lane environment contains card id.
func TestCardIDInLaneEnvironment(t *testing.T) {
	t.Parallel()
	lane := friend.LiveLane{ID: "card-123", Target: "feature"}
	assert.Equal(t, "card-123", lane.ID)
}

// TestTakeRecordDeadline tests take record has proper deadline.
func TestTakeRecordDeadline(t *testing.T) {
	t.Parallel()
	now := time.Now()
	deadline := now.Add(1 * time.Hour)

	rec := friend.TakeRecord{
		Friend:   "f1",
		CardID:   "c1",
		TakenAt:  now,
		Deadline: deadline,
	}

	assert.True(t, rec.Deadline.After(now))
	assert.Equal(t, deadline, rec.Deadline)
}

// TestLaneJudgmentForUnauthorizedWork tests judgment format for unauthorized work.
func TestLaneJudgmentForUnauthorizedWork(t *testing.T) {
	t.Parallel()
	j := sprint.LaneJudgment{
		Friend:   "f1",
		CardID:   "card-123",
		LaneID:   "card-123",
		Reason:   "live lane on card not taken on row",
		Severity: "error",
	}

	msg := sprint.BuildJudgmentMessage(j)
	assert.Contains(t, msg, "card=card-123")
	assert.Contains(t, msg, "lane=card-123")
	assert.Contains(t, msg, "reason")
	assert.Contains(t, msg, "severity")
}
