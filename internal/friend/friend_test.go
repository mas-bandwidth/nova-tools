package friend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveLaneStruct(t *testing.T) {
	t.Parallel()
	lane := LiveLane{ID: "card-123", Target: "feature-x"}
	assert.Equal(t, "card-123", lane.ID)
	assert.Equal(t, "feature-x", lane.Target)
}

func TestRowWorkingSetStruct(t *testing.T) {
	t.Parallel()
	set := RowWorkingSet{Friend: "f1", Cards: map[string]CardStatus{"c1": {State: "taken"}}}
	assert.Equal(t, "f1", set.Friend)
	assert.Equal(t, "taken", set.Cards["c1"].State)
}

func TestTakeRecordStruct(t *testing.T) {
	t.Parallel()
	rec := TakeRecord{Friend: "f1", CardID: "c1"}
	assert.Equal(t, "c1", rec.CardID)
	assert.Equal(t, "f1", rec.Friend)
}

func TestUpdateLiveLanesRequiresInputs(t *testing.T) {
	t.Parallel()
	err := UpdateLiveLanes(context.Background(), nil, "", []LiveLane{})
	require.Error(t, err)
}

func TestGetLiveLanesRequiresInputs(t *testing.T) {
	t.Parallel()
	_, err := GetLiveLanes(context.Background(), nil, "")
	require.Error(t, err)
}

func TestRecordTakeRequiresInputs(t *testing.T) {
	t.Parallel()
	err := RecordTake(context.Background(), nil, "", "", time.Time{}, time.Time{})
	require.Error(t, err)
}

func TestIsCardTakenReturnsTrueWhenTaken(t *testing.T) {
	t.Parallel()
	taken, _ := IsCardTaken(context.Background(), nil, "", "")
	assert.False(t, taken)
}

func TestGetRowWorkingSetRequiresInputs(t *testing.T) {
	t.Parallel()
	_, err := GetRowWorkingSet(context.Background(), nil, "")
	require.Error(t, err)
}
