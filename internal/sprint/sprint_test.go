package sprint

import (
	"testing"
	"time"
)

func TestJudgmentStruct(t *testing.T) {
	j := Judgment{CardID: "c1", LaneID: "l1", Reason: "test", Severity: "error"}
	if j.CardID != "c1" {
		t.Errorf("Expected card c1, got %s", j.CardID)
	}
}

func TestWorkingSetValidatorStruct(t *testing.T) {
	v := &WorkingSetValidator{}
	if v == nil {
		t.Error("Expected non-nil validator")
	}
}

func TestValidateRowEqualsLanesRequiresInputs(t *testing.T) {
	v := &WorkingSetValidator{}
	if _, err := v.ValidateRowEqualsLanes(nil, ""); err == nil {
		t.Error("Expected error when friend is empty")
	}
}

func TestExpireStaleCardsRequiresInputs(t *testing.T) {
	v := &WorkingSetValidator{}
	if _, err := v.ExpireStaleCards(nil, "", time.Time{}); err == nil {
		t.Error("Expected error when friend is empty")
	}
}

func TestApplyRowRestrictionRequiresInputs(t *testing.T) {
	v := &WorkingSetValidator{}
	if _, _, err := v.ApplyRowRestriction(nil, "", time.Time{}); err == nil {
		t.Error("Expected error when friend is empty")
	}
}

func TestRecordJudgmentValidates(t *testing.T) {
	v := &WorkingSetValidator{}
	if err := v.RecordJudgment(nil, Judgment{}); err == nil {
		t.Error("Expected error when judgment has empty card_id")
	}
}

func TestBuildJudgmentMessage(t *testing.T) {
	j := Judgment{CardID: "c1", LaneID: "l1", Reason: "test reason", Severity: "error"}
	msg := BuildJudgmentMessage(j)
	if msg == "" {
		t.Error("Expected non-empty judgment message")
	}
}

func TestSplitJudgmentMessage(t *testing.T) {
	msg := "JUDGMENT: card=c1 lane=l1 reason=test severity=error"
	j, err := SplitJudgmentMessage(msg)
	t.Logf("SplitJudgmentMessage returned: %+v, err: %v", j, err)
	if j.CardID != "c1" {
		t.Errorf("Expected card c1, got %s", j.CardID)
	}
}

func TestSplitJudgmentMessageEmpty(t *testing.T) {
	j, err := SplitJudgmentMessage("")
	if err != nil {
		t.Errorf("Expected no error on empty string, got %v", err)
	}
	if j.CardID != "" {
		t.Error("Expected empty card_id on empty string")
	}
}
