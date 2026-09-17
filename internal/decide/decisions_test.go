package decide

import (
	"path/filepath"
	"testing"
)

// memoryDriver is the decisions table in memory: the fake the one-writer and
// the read paths are tested against, so no test needs a Postgres on a bench.
type memoryDriver struct{ rows []DecisionRow }

func (m *memoryDriver) Append(row DecisionRow) error {
	m.rows = append(m.rows, row)
	return nil
}

func (m *memoryDriver) Rows(kind string) ([]DecisionRow, error) {
	out := make([]DecisionRow, 0)
	for _, row := range m.rows {
		if row.Kind == kind {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *memoryDriver) Close() error { return nil }

// Rule 8's one writer: after a call the client appends one row per answer
// carrying the question hash, the kind, the answer, the confidence and the
// floor.
func TestDecideRecordsOneRowPerAnswer(t *testing.T) {
	fake := &memoryDriver{}
	t.Setenv("CARD9330_JEV_KEY", "sekret")
	c, err := New("http://example.invalid", "CARD9330_JEV_KEY")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.UseDecisions(fake)
	c.SetFloor(0.9)
	qs := map[string]Question{
		"gate": {Instructions: "flaky or real?", Choice: map[string]string{"flaky": "re-run", "real": "page"}},
		"risk": {Instructions: "how risky?", Score: []string{"low", "high"}},
	}
	answers := map[string]Answer{
		"gate": {Type: "choice", Choice: "flaky", Confidence: 0.93},
		"risk": {Type: "score", Score: 1, Confidence: 0.6},
	}
	c.record("finished task", qs, answers)

	if len(fake.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(fake.rows))
	}
	byKind := map[string]DecisionRow{}
	for _, row := range fake.rows {
		byKind[row.Kind] = row
		if len(row.QuestionHash) != 64 {
			t.Fatalf("question hash %q is not a sha256 hex", row.QuestionHash)
		}
		if row.Floor != 0.9 {
			t.Fatalf("floor = %v, want 0.9", row.Floor)
		}
	}
	gate := byKind["choice"]
	if gate.Answer != "flaky" || gate.ProviderConfidence != 0.93 {
		t.Fatalf("gate row = %+v", gate)
	}
	if byKind["score"].Answer != "1" {
		t.Fatalf("score row answer = %q, want 1", byKind["score"].Answer)
	}
	// The same state and question hash to the same value: a replay names one row.
	if again := QuestionHash("finished task", "gate", qs["gate"]); again != gate.QuestionHash {
		t.Fatalf("hash is not stable: %q then %q", gate.QuestionHash, again)
	}
	if other := QuestionHash("another task", "gate", qs["gate"]); other == gate.QuestionHash {
		t.Fatal("different state must hash differently")
	}
}

// The TSV fallback is the same contract: append and read back by kind.
func TestDecisionsTSVRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.tsv")
	driver, err := OpenDecisions(path)
	if err != nil {
		t.Fatalf("OpenDecisions: %v", err)
	}
	defer driver.Close()
	rows := []DecisionRow{
		{QuestionHash: "aa", Kind: "gate", Answer: "flaky", ProviderConfidence: 0.93, Floor: 0.9, Outcome: "landed"},
		{QuestionHash: "bb", Kind: "route", Answer: "work", ProviderConfidence: 0.7, Floor: 0.9},
		{QuestionHash: "cc", Kind: "gate", Answer: "real", ProviderConfidence: 0.8, Floor: 0.9, Outcome: "paged"},
	}
	for _, row := range rows {
		if err := driver.Append(row); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	got, err := driver.Rows("gate")
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("gate rows = %d, want 2", len(got))
	}
	if got[0].QuestionHash != "aa" || got[0].Outcome != "landed" || got[1].QuestionHash != "cc" {
		t.Fatalf("gate rows wrong: %+v", got)
	}
	miss, err := driver.Rows("nothing")
	if err != nil {
		t.Fatalf("rows nothing: %v", err)
	}
	if len(miss) != 0 {
		t.Fatalf("unknown kind rows = %d, want 0", len(miss))
	}
}

// An empty DSN is a refusal, never a guess; a repo with no Postgres driver
// linked refuses rather than opening nothing.
func TestOpenDecisionsRefusesNoTable(t *testing.T) {
	if _, err := OpenDecisions(""); err == nil {
		t.Fatal("empty DSN must refuse")
	}
	if _, err := OpenDecisions("postgres://bench/decisions"); err == nil {
		t.Fatal("a postgres DSN with no linked driver must refuse")
	}
}
