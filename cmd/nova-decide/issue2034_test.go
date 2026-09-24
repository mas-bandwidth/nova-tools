package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// Stella's hold on #2850: an outcome's Time is RFC3339 at second precision, so
// two units' outcomes can share one stamp. A void names --unit-id AND
// --of-time and retracts exactly that one row; the other unit's outcome in the
// same second stands and still counts (nova-tools #2034).
func TestIssue2034VoidTiesToOneRowInTheSameSecond(t *testing.T) {
	saved := now
	defer func() { now = saved }()
	fixed := time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }

	log := routeThen(t, "same-a", "row-test")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run([]string{"route", "--unit-id", "same-b", "--kind", "row-test", "--files", "1", "--packages", "1", "--no-jev", "--log", log}, stdout, stderr); code != 0 {
		t.Fatalf("route same-b exit = %d (stderr=%q)", code, stderr.String())
	}
	outcomeRun(t, log, "same-a", "green")
	outcomeRun(t, log, "same-b", "green")
	stamp := fixed.Format(time.RFC3339)

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"outcome", "--log", log, "--unit-id", "same-b", "--result", "void", "--of-time", stamp}, stdout, stderr); code != 0 {
		t.Fatalf("void same-b exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "unit=same-b") {
		t.Errorf("the void names the unit it was asked to retract, got: %s", stdout.String())
	}
	rows := readRows(t, log)
	var voidRow decide.Entry
	if err := json.Unmarshal([]byte(rows[len(rows)-1]), &voidRow); err != nil {
		t.Fatalf("the void row is not one JSON object: %v", err)
	}
	if voidRow.Unit != "same-b" {
		t.Errorf("the void keyed the wrong unit's row: %+v", voidRow)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"log", "--log", log, "--summary"}, stdout, stderr); code != 0 {
		t.Fatalf("log --summary exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "successes=1") {
		t.Errorf("same-a's green in the same second must still count, want successes=1: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "coverage=1/2") {
		t.Errorf("only same-b's row is voided, want coverage=1/2: %s", stdout.String())
	}

	// A unit with no outcome at that stamp is no target, even though another
	// unit's row carries the same Time.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"outcome", "--log", log, "--unit-id", "nobody", "--result", "void", "--of-time", stamp}, stdout, stderr); code != 2 {
		t.Fatalf("void of a unit with no row at the stamp: exit = %d, want 2 (stdout=%q)", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "reason=no-target") {
		t.Errorf("want reason=no-target, got %q", stderr.String())
	}

	// Two rows of the SAME unit in the same second cannot be told apart by
	// unit plus stamp: the verb refuses rather than retract a guess.
	outcomeRun(t, log, "same-a", "red")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"outcome", "--log", log, "--unit-id", "same-a", "--result", "void", "--of-time", stamp}, stdout, stderr); code != 2 {
		t.Fatalf("void of an ambiguous unit+stamp: exit = %d, want 2 (stdout=%q)", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "reason=ambiguous-target") {
		t.Errorf("want reason=ambiguous-target, got %q", stderr.String())
	}
}
