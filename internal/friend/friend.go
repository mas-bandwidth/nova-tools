// Package friend manages a friend's live lanes and row working set.
// It implements the contract that a friend's row working set equals her live lanes.
package friend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// LiveLane represents a live lane with its card ID.
type LiveLane struct {
	ID     string // card ID this lane is working on
	Target string // what the lane is working on (may differ from card)
}

// RowWorkingSet represents the set of cards on a friend's row.
type RowWorkingSet struct {
	Friend string
	Cards  map[string]CardStatus
}

// CardStatus represents the status of a card on a row.
type CardStatus struct {
	State        string // taken, ready, done
	TakeTime     time.Time
	TakeDeadline time.Time
}

// TakeRecord holds information about a taken card.
type TakeRecord struct {
	Friend string
	CardID string
	TakenAt time.Time
	Deadline time.Time
}

// LaneState tracks the state of a friend's lane.
type LiveLaneState struct {
	Friend  string
	CardID  string
	Started time.Time
	Ended   *time.Time
	Status  string // running, finished, failed
}

// UpdateLiveLanes records a friend's live lanes from a beat.
func UpdateLiveLanes(ctx context.Context, st *store.Store, friend string, lanes []LiveLane) error {
	if st == nil || friend == "" {
		return fmt.Errorf("friend: store and friend are required")
	}

	// Build the live lanes string for Redis
	var laneStrs []string
	for _, lane := range lanes {
		laneStrs = append(laneStrs, fmt.Sprintf("%s:%s", lane.ID, lane.Target))
	}

	key := fmt.Sprintf("friend:%s:live_lanes", friend)
	if len(laneStrs) == 0 {
		// Clear the key if no live lanes
		_, err := st.Client().Del(ctx, key).Result()
		return err
	}

	_, err := st.Client().Set(ctx, key, strings.Join(laneStrs, ","), 0).Result()
	return err
}

// GetLiveLanes returns the current live lanes for a friend.
func GetLiveLanes(ctx context.Context, st *store.Store, friend string) ([]LiveLane, error) {
	if st == nil || friend == "" {
		return nil, fmt.Errorf("friend: store and friend are required")
	}

	key := fmt.Sprintf("friend:%s:live_lanes", friend)
	val, err := st.Client().Get(ctx, key).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			return []LiveLane{}, nil
		}
		return nil, err
	}

	if val == "" {
		return []LiveLane{}, nil
	}

	var lanes []LiveLane
	parts := strings.Split(val, ",")
	for _, part := range parts {
		colonIdx := strings.Index(part, ":")
		if colonIdx > 0 {
			lanes = append(lanes, LiveLane{
				ID:     part[:colonIdx],
				Target: part[colonIdx+1:],
			})
		}
	}

	return lanes, nil
}

// RecordTake records when a friend takes a card.
func RecordTake(ctx context.Context, st *store.Store, friend, cardID string, takeTime, deadline time.Time) error {
	if st == nil || friend == "" || cardID == "" {
		return fmt.Errorf("friend: store, friend, and card_id are required")
	}

	key := fmt.Sprintf("friend:%s:taken:%s", friend, cardID)
	record := fmt.Sprintf("%d:%d", takeTime.UnixMilli(), deadline.UnixMilli())

	_, err := st.Client().Set(ctx, key, record, 0).Result()
	return err
}

// GetTakenCard returns the take record for a card on a friend's row.
func GetTakenCard(ctx context.Context, st *store.Store, friend, cardID string) (*TakeRecord, error) {
	if st == nil || friend == "" || cardID == "" {
		return nil, fmt.Errorf("friend: store, friend, and card_id are required")
	}

	key := fmt.Sprintf("friend:%s:taken:%s", friend, cardID)
	val, err := st.Client().Get(ctx, key).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			return nil, nil
		}
		return nil, err
	}

	takenAt, deadline, ok := strings.Cut(val, ":")
	if !ok {
		return nil, fmt.Errorf("friend: invalid take record format")
	}

	takenAtMs, err := strconv.ParseInt(takenAt, 10, 64)
	if err != nil {
		return nil, err
	}
	deadlineMs, err := strconv.ParseInt(deadline, 10, 64)
	if err != nil {
		return nil, err
	}

	return &TakeRecord{
		Friend:   friend,
		CardID:   cardID,
		TakenAt:  time.UnixMilli(takenAtMs),
		Deadline: time.UnixMilli(deadlineMs),
	}, nil
}

// ClearTake removes a take record when a card is completed.
func ClearTake(ctx context.Context, st *store.Store, friend, cardID string) error {
	if st == nil || friend == "" || cardID == "" {
		return fmt.Errorf("friend: store, friend, and card_id are required")
	}

	key := fmt.Sprintf("friend:%s:taken:%s", friend, cardID)
	_, err := st.Client().Del(ctx, key).Result()
	return err
}

// GetRowWorkingSet returns all cards on a friend's row.
func GetRowWorkingSet(ctx context.Context, st *store.Store, friend string) (map[string]CardStatus, error) {
	if st == nil || friend == "" {
		return nil, fmt.Errorf("friend: store and friend are required")
	}

	pattern := fmt.Sprintf("friend:%s:taken:*", friend)
	iter := st.Client().Scan(ctx, 0, pattern, 0).Iterator()

	cards := make(map[string]CardStatus)
	for iter.Next(ctx) {
		key := iter.Val()
		// Extract cardID from key: friend:f:taken:cardID
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

// IsCardTaken checks if a specific card is taken by a friend.
func IsCardTaken(ctx context.Context, st *store.Store, friend, cardID string) (bool, error) {
	record, err := GetTakenCard(ctx, st, friend, cardID)
	if err != nil {
		return false, err
	}
	return record != nil, nil
}

// GetLaneState returns the state of a lane.
func GetLaneState(ctx context.Context, st *store.Store, friend, laneID string) (*LiveLaneState, error) {
	if st == nil || friend == "" || laneID == "" {
		return nil, fmt.Errorf("friend: store, friend, and lane_id are required")
	}

	key := fmt.Sprintf("friend:%s:lanes:%s", friend, laneID)
	val, err := st.Client().Get(ctx, key).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			return nil, nil
		}
		return nil, err
	}

	parts := strings.Split(val, "|")
	if len(parts) < 3 {
		return nil, fmt.Errorf("friend: invalid lane state format")
	}

	ended := (*time.Time)(nil)
	if parts[2] != "" {
		t, err := time.Parse(time.RFC3339, parts[2])
		if err != nil {
			return nil, err
		}
		ended = &t
	}

	return &LiveLaneState{
		Friend:  friend,
		CardID:  parts[0],
		Started: time.UnixMilli(partsInt64(parts[1])),
		Ended:   ended,
		Status:  parts[3],
	}, nil
}

func partsInt64(s string) int64 {
	// Simplified parsing; real implementation should handle errors
	var result int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + int64(c-'0')
		}
	}
	return result
}

// Judgment represents a violation of the row=lanes contract.
type Judgment struct {
	Friend   string
	CardID   string
	LaneID   string
	Reason   string
	Severity string // warning, error
}

// RecordJudgment persists a judgment to Redis.
func RecordJudgment(ctx context.Context, st *store.Store, judgment Judgment) error {
	if st == nil || judgment.Friend == "" {
		return fmt.Errorf("friend: store and friend are required")
	}
	if judgment.CardID == "" || judgment.LaneID == "" {
		return fmt.Errorf("friend: judgment must have card_id and lane_id")
	}

	key := fmt.Sprintf("friend:%s:judgments", judgment.Friend)
	record := fmt.Sprintf("%s:%s:%s:%s", judgment.CardID, judgment.LaneID, judgment.Reason, judgment.Severity)

	// Append to the judgments list
	_, err := st.Client().LPush(ctx, key, record).Result()
	if err != nil {
		return err
	}

	// Set expiration to 24 hours
	_, err = st.Client().Expire(ctx, key, 24*time.Hour).Result()
	return err
}

// GetJudgments returns all judgments for a friend.
func GetJudgments(ctx context.Context, st *store.Store, friend string) ([]Judgment, error) {
	if st == nil || friend == "" {
		return nil, fmt.Errorf("friend: store and friend are required")
	}

	key := fmt.Sprintf("friend:%s:judgments", friend)
	vals, err := st.Client().LRange(ctx, key, 0, -1).Result()
	if err != nil {
		if err.Error() == "redis: nil" {
			return []Judgment{}, nil
		}
		return nil, err
	}

	if len(vals) == 0 {
		return []Judgment{}, nil
	}

	var judgments []Judgment
	for _, val := range vals {
		parts := strings.Split(val, ":")
		if len(parts) >= 4 {
			judgments = append(judgments, Judgment{
				Friend:   friend,
				CardID:   parts[0],
				LaneID:   parts[1],
				Reason:   strings.Join(parts[2:len(parts)-1], ":"),
				Severity: parts[len(parts)-1],
			})
		}
	}

	return judgments, nil
}

// ClearJudgments clears all judgments for a friend.
func ClearJudgments(ctx context.Context, st *store.Store, friend string) error {
	if st == nil || friend == "" {
		return fmt.Errorf("friend: store and friend are required")
	}

	key := fmt.Sprintf("friend:%s:judgments", friend)
	_, err := st.Client().Del(ctx, key).Result()
	return err
}
