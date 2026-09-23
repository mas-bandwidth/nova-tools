package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// reproducer for nova-tools #2034: the funnel is a standing report and a
// mistake in the outcome log must be voided by a void record, never rewritten.
// A manager who recorded ten `outcome --result green` lines that were not
// greens had no way to retract them (SPEC-PULSE rule 18, nova-tools #2034).
// The fix is `nova-decide outcome --result void --of-time <rfc3339>`: every
// void APPENDS a row of its own keyed to the outcome row's time, both rows
// stand, and the summary excludes the voided row from every count.
func TestIssue2034(t *testing.T) {
	log := routeThen(t, "row-2034", "row-test")

	outcomeRun(t, log, "row-2034", "green")
	before := readRows(t, log)
	if len(before) != 2 {
		t.Fatalf("one decision and one wrong green are two rows, got %d:\n%s", len(before), strings.Join(before, "\n"))
	}
	var wrongGreen decide.Entry
	if err := json.Unmarshal([]byte(before[1]), &wrongGreen); err != nil {
		t.Fatalf("the wrong green is not one JSON object: %v", err)
	}
	if wrongGreen.Source != decide.SourceOutcome || wrongGreen.Outcome != decide.OutcomeOK {
		t.Fatalf("the wrong green lost its facts: %+v", wrongGreen)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run([]string{"log", "--log", log, "--summary"}, stdout, stderr); code != 0 {
		t.Fatalf("log --summary exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "successes=1") {
		t.Fatalf("baseline: the wrong green is one success, got: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"outcome", "--log", log, "--unit-id", "row-2034", "--result", "void", "--of-time", wrongGreen.Time}, stdout, stderr); code != 0 {
		t.Fatalf("outcome --result void exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "result=void") {
		t.Errorf("the void line names its result, got: %s", stdout.String())
	}

	after := readRows(t, log)
	if len(after) != 3 {
		t.Fatalf("a void APPENDS: want 3 rows, got %d:\n%s", len(after), strings.Join(after, "\n"))
	}
	if after[0] != before[0] || after[1] != before[1] {
		t.Errorf("the earlier rows were rewritten\n before: %s\n  after: %s", strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
	var voidRow decide.Entry
	if err := json.Unmarshal([]byte(after[2]), &voidRow); err != nil {
		t.Fatalf("the void row is not one JSON object: %v", err)
	}
	if voidRow.Source != resultVoid {
		t.Errorf("the void row loses its source, got %q", voidRow.Source)
	}
	if voidRow.Unit != "row-2034" || voidRow.Reason != voidPrefix+wrongGreen.Time {
		t.Errorf("the void row loses its key, got %+v", voidRow)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"log", "--log", log, "--summary"}, stdout, stderr); code != 0 {
		t.Fatalf("log --summary exit = %d (stderr=%q)", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "successes=1") {
		t.Errorf("the voided row is still a success, want successes=0: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "successes=0") {
		t.Errorf("the voided row is not a success any more, want successes=0: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "coverage=0/1") {
		t.Errorf("a voided outcome no longer counts toward coverage, want coverage=0/1: %s", stdout.String())
	}
}

// void is its own argument; --of-time without --result void is a refusal
// because a void without a target is a row nobody can join to a mistake.
func TestIssue2034VoidRefusesWithoutTarget(t *testing.T) {
	log := routeThen(t, "no-target", "row-test")
	outcomeRun(t, log, "no-target", "green")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run([]string{"outcome", "--log", log, "--unit-id", "no-target", "--result", "void"}, stdout, stderr); code != 2 {
		t.Fatalf("void without --of-time: exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "OUTCOME REFUSED reason=") {
		t.Errorf("the refusal names its reason, got %q", stderr.String())
	}
}
