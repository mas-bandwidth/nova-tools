// Package sprint manages server-side enforcement of friend row working set.
// It ensures that a friend's row working set equals her live lanes.
package sprint

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// LaneJudgment represents a violation of the row=lanes contract.
type LaneJudgment struct {
	Friend   string
	CardID   string
	LaneID   string
	Reason   string
	Severity string // warning, error
}

// RowLiveLane represents a live lane with its card ID.
type RowLiveLane struct {
	ID     string
	Target string
}

// CardStatus represents the status of a card on a row.
type CardStatus struct {
	State        string // taken, ready, done
	TakeTime     time.Time
	TakeDeadline time.Time
}

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
func (v *WorkingSetValidator) ValidateRowEqualsLanes(ctx context.Context, f string) ([]LaneJudgment, error) {
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

	var judgments []LaneJudgment

	// Check: live lane on a card not taken on her row is a judgment
	for _, lane := range liveLanes {
		if _, ok := takenCards[lane.ID]; !ok {
			judgment := LaneJudgment{
				Friend:   f,
				CardID:   lane.ID,
				LaneID:   lane.ID, // lane id is the card id per spec
				Reason:   fmt.Sprintf("live lane on card not taken on row: %s", lane.ID),
				Severity: "error",
			}

			// Persist the judgment
			if err := v.RecordJudgment(ctx, judgment); err != nil {
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
			if err := clearTakeInternal(ctx, v.st, f, cardID); err != nil {
				return nil, fmt.Errorf("sprint: failed to clear take for %s: %w", cardID, err)
			}
			stale = append(stale, cardID)
		}
	}

	return stale, nil
}

// ApplyRowRestriction enforces the row working set to match live lanes.
// This combines expire stale and judgment generation.
func (v *WorkingSetValidator) ApplyRowRestriction(ctx context.Context, f string, now time.Time) ([]string, []LaneJudgment, error) {
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

// internal helpers that call Redis
func getLiveLanesInternal(ctx context.Context, st *store.Store, f string) ([]RowLiveLane, error) {
	key := fmt.Sprintf("friend:%s:live_lanes", f)
	val, err := st.Client().Get(ctx, key).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			return []RowLiveLane{}, nil
		}
		return nil, err
	}
	if val == "" {
		return []RowLiveLane{}, nil
	}
	var lanes []RowLiveLane
	parts := strings.Split(val, ",")
	for _, part := range parts {
		colonIdx := strings.Index(part, ":")
		if colonIdx > 0 {
			lanes = append(lanes, RowLiveLane{
				ID:     part[:colonIdx],
				Target: part[colonIdx+1:],
			})
		}
	}
	return lanes, nil
}

func getTakenCardsInternal(ctx context.Context, st *store.Store, f string) (map[string]CardStatus, error) {
	pattern := fmt.Sprintf("friend:%s:taken:*", f)
	iter := st.Client().Scan(ctx, 0, pattern, 0).Iterator()

	cards := make(map[string]CardStatus)
	for iter.Next(ctx) {
		key := iter.Val()
		cardID := key[strings.LastIndex(key, ":")+1:]
		val, err := st.Client().Get(ctx, key).Result()
		if err != nil {
			continue
		}
		takenAt, deadline, ok := strings.Cut(val, ":")
		if !ok {
			continue
		}
		takenAtMs, _ := strconv.ParseInt(takenAt, 10, 64)
		deadlineMs, _ := strconv.ParseInt(deadline, 10, 64)
		cards[cardID] = CardStatus{
			State:        "taken",
			TakeTime:     time.UnixMilli(takenAtMs),
			TakeDeadline: time.UnixMilli(deadlineMs),
		}
	}
	return cards, iter.Err()
}

func clearTakeInternal(ctx context.Context, st *store.Store, f, cardID string) error {
	key := fmt.Sprintf("friend:%s:taken:%s", f, cardID)
	_, err := st.Client().Del(ctx, key).Result()
	return err
}

// RecordJudgment logs a judgment to the system and persists to Redis.
func (v *WorkingSetValidator) RecordJudgment(ctx context.Context, judgment LaneJudgment) error {
	if v.st == nil {
		return fmt.Errorf("sprint: store is required")
	}
	if judgment.CardID == "" || judgment.Reason == "" {
		return fmt.Errorf("sprint: judgment must have card_id and reason")
	}
	if judgment.Friend == "" {
		return fmt.Errorf("sprint: judgment must have friend")
	}

	key := fmt.Sprintf("friend:%s:judgments", judgment.Friend)
	record := fmt.Sprintf("%s:%s:%s:%s", judgment.CardID, judgment.LaneID, judgment.Reason, judgment.Severity)
	_, err := v.st.Client().LPush(ctx, key, record).Result()
	if err != nil {
		return err
	}
	_, err = v.st.Client().Expire(ctx, key, 24*time.Hour).Result()
	return err
}

// BuildJudgmentMessage creates a human-readable judgment message.
func BuildJudgmentMessage(judgment LaneJudgment) string {
	return fmt.Sprintf("JUDGMENT: card=%s lane=%s reason=%s severity=%s",
		judgment.CardID, judgment.LaneID, judgment.Reason, judgment.Severity)
}

// SplitJudgmentMessage parses a judgment message back to struct.
func SplitJudgmentMessage(msg string) (LaneJudgment, error) {
	j := LaneJudgment{}
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
