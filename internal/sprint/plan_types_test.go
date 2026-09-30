package sprint

import "testing"

// The shared types of the upper design (8.0), each built once with every
// field named, so that the package compiles the shapes the layers agree on
// and a change to one is a change to this test.

func TestPlanTypesEveryTypeBuilt(t *testing.T) {
	t.Parallel()
	now := Now{R: 1, Wall: 2, Running: true}
	intent := Intent{
		Kind:    "needmet",
		Card:    "a",
		Need:    "n",
		Needs:   []string{"n", "m"},
		Waiters: []string{"a", "b"},
	}
	guard := XGuard{Kind: "beatstale", Member: "m", Key: "beat:m", Score: 3}
	quarantined := Quarantined{ID: "a", Stream: "s", Code: "DRIFT", Rule: "resolve", Cells: []string{"work:s:ready"}}
	note := NoteReq{
		Op:        "open",
		Type:      "the machine could not move a card",
		Cause:     "deal",
		Subjects:  []string{"a"},
		Text:      "a could not move",
		Decisions: []string{"ack"},
		Until:     4,
	}
	plan := RulePlan{
		Plan:       Plan{Refused: []Refusal{{Key: "a", Why: "x"}}},
		Intents:    []Intent{intent},
		Guards:     []XGuard{guard},
		Notes:      []NoteReq{note},
		Done:       []AgendaKey{{}},
		Requeue:    []AgendaKey{{}, {}},
		Quarantine: []Quarantined{quarantined},
		HeldBack:   []AgendaKey{{}, {}, {}},
	}
	bounds := ReadBounds{Queries: 1, Records: 2, RangeIDs: 3, Bytes: 4}
	cost := Cost{Records: 2, RangeIDs: 3, Bytes: 4}

	if !now.Running || now.R != 1 || now.Wall != 2 {
		t.Fatalf("Now: %+v", now)
	}
	if intent.Kind != "needmet" || intent.Card != "a" || intent.Need != "n" || len(intent.Needs) != 2 || len(intent.Waiters) != 2 {
		t.Fatalf("Intent: %+v", intent)
	}
	if guard.Kind != "beatstale" || guard.Member != "m" || guard.Key != "beat:m" || guard.Score != 3 {
		t.Fatalf("XGuard: %+v", guard)
	}
	if quarantined.ID != "a" || quarantined.Stream != "s" || quarantined.Code != "DRIFT" || quarantined.Rule != "resolve" || len(quarantined.Cells) != 1 {
		t.Fatalf("Quarantined: %+v", quarantined)
	}
	if note.Op != "open" || note.Cause != "deal" || len(note.Subjects) != 1 || len(note.Decisions) != 1 || note.Until != 4 {
		t.Fatalf("NoteReq: %+v", note)
	}
	if len(plan.Plan.Refused) != 1 || len(plan.Intents) != 1 || len(plan.Guards) != 1 || len(plan.Notes) != 1 ||
		len(plan.Done) != 1 || len(plan.Requeue) != 2 || len(plan.Quarantine) != 1 || len(plan.HeldBack) != 3 {
		t.Fatalf("RulePlan: %+v", plan)
	}
	if bounds.Queries != 1 || bounds.Records != cost.Records || bounds.RangeIDs != cost.RangeIDs || bounds.Bytes != cost.Bytes {
		t.Fatalf("ReadBounds %+v against Cost %+v", bounds, cost)
	}
}
