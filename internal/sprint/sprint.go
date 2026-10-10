// Package sprint manages server-side enforcement of friend row working set.
// It ensures that a friend's row working set equals her live lanes.
package sprint

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Judgment represents a violation of the row=lanes contract.
type Judgment struct {
	CardID   string
	LaneID   string
	Reason   string
	Severity string // warning, error
}

// Type aliases for types from friend package
type (
	LiveLane   = friend.LiveLane
	CardStatus = friend.CardStatus
)

// WorkingSetValidator checks and enforces the row=lanes contract.
type WorkingSetValidator struct {
	st *store.Store
}

// NewWorkingSetValidator creates a new validator.
func NewWorkingSetValidator(st *store.Store) *WorkingSetValidator {
	return &WorkingSetValidator{st: st}
}

// ValidateRowEqualsLanes checks if friend's row working set equals live lanes.
// Returns judgments for violations (live lane on card not taken) and persists them.
func (v *WorkingSetValidator) ValidateRowEqualsLanes(ctx context.Context, f string) ([]Judgment, error) {
	if v.st == nil || f == "" {
		return nil, fmt.Errorf("sprint: store and friend are required")
	}

	// Get live lanes from friend beat
	liveLanes, err := getLiveLanesInternal(ctx, v.st, f)
	if err != nil {
		return nil, err
	}

	// Get taken cards from friend's row
	takenCards, err := getTakenCardsInternal(ctx, v.st, f)
	if err != nil {
		return nil, err
	}

	var judgments []Judgment

	// Check: live lane on a card not taken on her row is a judgment
	for _, lane := range liveLanes {
		if _, ok := takenCards[lane.ID]; !ok {
			judgment := Judgment{
				CardID: lane.ID,
				LaneID: lane.ID, // lane id is the card id per spec
				Reason: fmt.Sprintf("live lane on card not taken on row: %s", lane.ID),
				Severity: "error",
			}

			// Persist the judgment
			if err := friend.RecordJudgment(ctx, v.st, friend.Judgment{
				Friend:   f,
				CardID:   judgment.CardID,
				LaneID:   judgment.LaneID,
				Reason:   judgment.Reason,
				Severity: judgment.Severity,
			}); err != nil {
				return nil, fmt.Errorf("sprint: failed to record judgment: %w", err)
			}

			judgments = append(judgments, judgment)
		}
	}

	return judgments, nil
}

// ExpireStaleCards checks for taken cards with no live lane past their deadline.
// Moves them back to ready by clearing their take record.
func (v *WorkingSetValidator) ExpireStaleCards(ctx context.Context, f string, now time.Time) ([]string, error) {
	if v.st == nil || f == "" {
		return nil, fmt.Errorf("sprint: store and friend are required")
	}

	// Get live lanes
	liveLanes, err := getLiveLanesInternal(ctx, v.st, f)
	if err != nil {
		return nil, err
	}

	// Build set of live lane card IDs
	liveCardIDs := make(map[string]bool)
	for _, lane := range liveLanes {
		liveCardIDs[lane.ID] = true
	}

	// Get taken cards
	takenCards, err := getTakenCardsInternal(ctx, v.st, f)
	if err != nil {
		return nil, err
	}

	var stale []string
	for cardID, status := range takenCards {
		// Check if card has a live lane
		if liveCardIDs[cardID] {
			continue
		}

		// Check if past deadline
		if !status.TakeDeadline.IsZero() && status.TakeDeadline.Before(now) {
			// Clear the take record to move card back to ready
			if err := friend.ClearTake(ctx, v.st, f, cardID); err != nil {
				return nil, fmt.Errorf("sprint: failed to clear take for %s: %w", cardID, err)
			}
			stale = append(stale, cardID)
		}
	}

	return stale, nil
}

// ApplyRowRestriction enforces the row working set to match live lanes.
// This combines expire stale and judgment generation.
func (v *WorkingSetValidator) ApplyRowRestriction(ctx context.Context, f string, now time.Time) ([]string, []Judgment, error) {
	if v.st == nil || f == "" {
		return nil, nil, fmt.Errorf("sprint: store and friend are required")
	}

	stale, err := v.ExpireStaleCards(ctx, f, now)
	if err != nil {
		return nil, nil, err
	}

	judgments, err := v.ValidateRowEqualsLanes(ctx, f)
	if err != nil {
		return nil, nil, err
	}

	return stale, judgments, nil
}

// internal helpers that call Redis via the friend package
func getLiveLanesInternal(ctx context.Context, st *store.Store, f string) ([]LiveLane, error) {
	return friend.GetLiveLanes(ctx, st, f)
}

func getTakenCardsInternal(ctx context.Context, st *store.Store, f string) (map[string]CardStatus, error) {
	return friend.GetRowWorkingSet(ctx, st, f)
}

// RecordJudgment logs a judgment to the system.
func (v *WorkingSetValidator) RecordJudgment(ctx context.Context, judgment Judgment) error {
	if v.st == nil {
		return fmt.Errorf("sprint: store is required")
	}

	// In real implementation, this would write to audit log
	// For now, just validate
	if judgment.CardID == "" || judgment.Reason == "" {
		return fmt.Errorf("sprint: judgment must have card_id and reason")
	}

	return nil
}

// BuildJudgmentMessage creates a human-readable judgment message.
func BuildJudgmentMessage(judgment Judgment) string {
	return fmt.Sprintf("JUDGMENT: card=%s lane=%s reason=%s severity=%s",
		judgment.CardID, judgment.LaneID, judgment.Reason, judgment.Severity)
}

// SplitJudgmentMessage parses a judgment message back to struct.
func SplitJudgmentMessage(msg string) (Judgment, error) {
	j := Judgment{}

	parts := strings.Split(msg, " ")
	for _, part := range parts {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "card":
			j.CardID = val
		case "lane":
			j.LaneID = val
		case "reason":
			j.Reason = val
		case "severity":
			j.Severity = val
		}
	}

	return j, nil
}
