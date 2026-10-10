package sprint

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLaneJudgmentStruct(t *testing.T) {
	t.Parallel()
	j := LaneJudgment{CardID: "c1", LaneID: "l1", Reason: "test", Severity: "error"}
	assert.Equal(t, "c1", j.CardID)
	assert.Equal(t, "l1", j.LaneID)
	assert.Equal(t, "test", j.Reason)
	assert.Equal(t, "error", j.Severity)
}

func TestWorkingSetValidatorStruct(t *testing.T) {
	t.Parallel()
	v := &WorkingSetValidator{}
	assert.NotNil(t, v)
}

func TestValidateRowEqualsLanesRequiresInputs(t *testing.T) {
	t.Parallel()
	v := &WorkingSetValidator{}
	_, err := v.ValidateRowEqualsLanes(context.Background(), "")
	require.Error(t, err)
}

func TestExpireStaleCardsRequiresInputs(t *testing.T) {
	t.Parallel()
	v := &WorkingSetValidator{}
	_, err := v.ExpireStaleCards(context.Background(), "", time.Time{})
	require.Error(t, err)
}

func TestApplyRowRestrictionRequiresInputs(t *testing.T) {
	t.Parallel()
	v := &WorkingSetValidator{}
	_, _, err := v.ApplyRowRestriction(context.Background(), "", time.Time{})
	require.Error(t, err)
}

func TestRecordJudgmentValidates(t *testing.T) {
	t.Parallel()
	v := &WorkingSetValidator{}
	err := v.RecordJudgment(context.Background(), LaneJudgment{})
	require.Error(t, err)
}

func TestBuildJudgmentMessage(t *testing.T) {
	t.Parallel()
	j := LaneJudgment{CardID: "c1", LaneID: "l1", Reason: "test reason", Severity: "error"}
	msg := BuildJudgmentMessage(j)
	assert.NotEmpty(t, msg)
}

func TestSplitJudgmentMessage(t *testing.T) {
	t.Parallel()
	msg := "JUDGMENT: card=c1 lane=l1 reason=test severity=error"
	j, err := SplitJudgmentMessage(msg)
	require.NoError(t, err)
	assert.Equal(t, "c1", j.CardID)
	assert.Equal(t, "l1", j.LaneID)
	assert.Equal(t, "test", j.Reason)
	assert.Equal(t, "error", j.Severity)
}

func TestSplitJudgmentMessageEmpty(t *testing.T) {
	t.Parallel()
	j, err := SplitJudgmentMessage("")
	require.NoError(t, err)
	assert.Empty(t, j.CardID)
}
