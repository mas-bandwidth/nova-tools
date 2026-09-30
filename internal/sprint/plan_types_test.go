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

// The answer types of the errata (E3), each built once with every field named.
func TestPlanTypesTheReadAnswer(t *testing.T) {
	t.Parallel()
	card := &Card{ID: "a", Row: "s", Col: "ready", Score: 1, Rev: 2}
	ans := ReadAnswer{
		Epoch: "3", ActiveEpoch: "4", TimeMS: "1790000000123",
		Tset: []TsetAnswer{{
			Kind: AnswerRange, IDs: []string{"a"}, Scores: []float64{1}, HasMore: true, Counts: []int{2}, Sum: 2,
			Records: []*Card{card}, Lines: []LogLine{{ID: "7-0", Body: []byte("{}")}},
		}},
		Sprint: []Answer{{
			Kind: QueryFleet, IDs: []string{"a"}, Records: []TableCard{{Table: Work, Card: card}}, Rows: []string{"m"}, HasMore: true,
			Counts: []CellCount{{Row: "m", Col: "ready", N: 5}},
			Front:  &FrontAnswer{Stream: "s", G: "g", Sigma: 3, NBefore: 1, GQuarantined: true},
		}},
	}
	a, b := ans.Tset[0], ans.Sprint[0]
	if ans.Epoch != "3" || ans.ActiveEpoch != "4" || ans.TimeMS != "1790000000123" || a.Kind != "range" || a.IDs[0] != "a" || a.Scores[0] != 1 ||
		!a.HasMore || a.Counts[0] != 2 || a.Sum != 2 || a.Records[0] != card || a.Lines[0].ID != "7-0" || string(a.Lines[0].Body) != "{}" {
		t.Fatalf("ReadAnswer: %+v", ans)
	}
	if b.Kind != "fleet" || b.IDs[0] != "a" || b.Records[0].Table != Work || b.Records[0].Card != card || b.Rows[0] != "m" || !b.HasMore ||
		b.Counts[0] != (CellCount{Row: "m", Col: "ready", N: 5}) || b.Front.Stream != "s" || b.Front.G != "g" || b.Front.Sigma != 3 || b.Front.NBefore != 1 || !b.Front.GQuarantined {
		t.Fatalf("Answer: %+v", b)
	}
}

// The answer kinds are the words of tset.ReadAnswer.Kind (E1).
func TestPlanTypesTheAnswerKindsAreTsetsWords(t *testing.T) {
	t.Parallel()
	for got, want := range map[string]string{AnswerIDs: "ids", AnswerRange: "range", AnswerCount: "count", AnswerRCount: "rcount", AnswerLines: "lines"} {
		if got != want {
			t.Errorf("answer kind %q, want %q", got, want)
		}
	}
}

func TestDecimalIsAnExactUnsignedInteger(t *testing.T) {
	t.Parallel()
	for in, want := range map[Decimal]uint64{"": 0, "0": 0, "7": 7, "007": 7, "1790000000123": 1790000000123, "18446744073709551615": 1<<64 - 1} {
		if got, err := in.Uint64(); err != nil || got != want {
			t.Errorf("Decimal(%q) = %d, %v; want %d", string(in), got, err, want)
		}
	}
	for _, in := range []Decimal{"-1", "+1", "1.5", "1e3", " 1", "1 ", "x", "18446744073709551616", "99999999999999999999999"} {
		if got, err := in.Uint64(); err == nil {
			t.Errorf("Decimal(%q) = %d, want an error", string(in), got)
		}
	}
}
