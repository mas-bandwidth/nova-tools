package main

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// fakeDriver is the decisions table in memory: the fake the Postgres
// subsection of SPEC-DECIDE and rule 8 are tested against, so no test needs a
// Postgres on a bench and none dials the network.
type fakeDriver struct {
	mu   sync.Mutex
	rows []decide.DecisionRow
}

func (f *fakeDriver) Append(row decide.DecisionRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeDriver) Rows(kind string) ([]decide.DecisionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]decide.DecisionRow, 0)
	for _, row := range f.rows {
		if row.Kind == kind {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeDriver) Close() error { return nil }

// The Postgres subsection's red test: a floor with no rows behind it is
// refused, naming the kind, and a row joins its confidence to the outcome that
// followed (rule 8).
func TestNovaDecideTuneRefusesAFloorWithNoRows(t *testing.T) {
	fake := &fakeDriver{}
	restore := decisionsOpener
	decisionsOpener = func(dsn string) (decide.DecisionDriver, error) { return fake, nil }
	defer func() { decisionsOpener = restore }()

	var stdout, stderr bytes.Buffer
	code := run([]string{"tune", "--kind", "gate"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("empty table: exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "reason=no-rows") {
		t.Fatalf("empty table: refusal must say reason=no-rows: %q", combined)
	}
	if !strings.Contains(combined, "gate") {
		t.Fatalf("empty table: refusal must name the kind: %q", combined)
	}

	if err := fake.Append(decide.DecisionRow{
		QuestionHash:       "6f1c",
		Kind:               "gate",
		Answer:             "flaky",
		ProviderConfidence: 0.93,
		Floor:              0.9,
		Outcome:            "landed",
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"tune", "--kind", "gate"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("one row: exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "landed") {
		t.Fatalf("one row: tune must report the outcome: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "6f1c") {
		t.Fatalf("one row: tune must report the question hash: %q", stdout.String())
	}
}
