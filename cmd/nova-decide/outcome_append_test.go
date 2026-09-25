package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// readRows reads the log back as raw lines, because the point of these tests is
// what is ON DISK: a row written is never rewritten, so the bytes of an earlier
// line are part of the assertion and not an implementation detail.
func readRows(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// outcomeRun runs the verb and fails the test if it did not exit 0.
func outcomeRun(t *testing.T, log, unit, result string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"outcome", "--log", log, "--unit-id", unit, "--result", result}, &stdout, &stderr); code != 0 {
		t.Fatalf("outcome --result %s: exit = %d (stderr=%q)", result, code, stderr.String())
	}
	return stdout.String()
}

// The log is append-only, and eligibility rule 7 of SPEC-TOOLWORK says so in the
// shape that matters here: "A later HOLD on the accepted PR appends a SECOND
// outcome row, `red`, keyed to the same unit." A verb that keyed rows by unit
// and replaced the green with the red would destroy exactly the arithmetic that
// is supposed to cost a bad route its floor -- the green would vanish and the
// kind would look like it had never succeeded.
func TestOutcomeAppendsOneRowAndKeepsTheEarlierOne(t *testing.T) {
	log := routeThen(t, "hold-me", "row-test")

	decisionRow := readRows(t, log)[0] // the bytes before any outcome exists

	outcomeRun(t, log, "hold-me", "green")
	afterGreen := readRows(t, log)
	if len(afterGreen) != 2 {
		t.Fatalf("one decision and one outcome are two rows, got %d:\n%s", len(afterGreen), strings.Join(afterGreen, "\n"))
	}
	greenRow := afterGreen[1]

	// The later HOLD. This is the row the whole test exists for.
	outcomeRun(t, log, "hold-me", "red")
	afterRed := readRows(t, log)
	if len(afterRed) != 3 {
		t.Fatalf("the later HOLD APPENDS: want 3 rows, got %d:\n%s", len(afterRed), strings.Join(afterRed, "\n"))
	}

	// KEPT, not replaced: the earlier rows are byte-identical to what they were.
	if afterRed[0] != decisionRow {
		t.Errorf("the decision row was rewritten\n before: %s\n  after: %s", decisionRow, afterRed[0])
	}
	if afterRed[1] != greenRow {
		t.Errorf("the green outcome row was rewritten by the later red\n before: %s\n  after: %s", greenRow, afterRed[1])
	}

	// And both outcome rows are the same unit's, in the order they happened.
	var green, red decide.Entry
	if err := json.Unmarshal([]byte(afterRed[1]), &green); err != nil {
		t.Fatalf("row 2 is not one JSON object: %v", err)
	}
	if err := json.Unmarshal([]byte(afterRed[2]), &red); err != nil {
		t.Fatalf("row 3 is not one JSON object: %v", err)
	}
	for i, e := range []decide.Entry{green, red} {
		if e.Unit != "hold-me" || e.Source != decide.SourceOutcome {
			t.Errorf("outcome row %d is not this unit's outcome: %+v", i+1, e)
		}
	}
	if green.Outcome != decide.OutcomeOK {
		t.Errorf("the first outcome is still the green one, got %q", green.Outcome)
	}
	if red.Outcome != decide.OutcomeFailed {
		t.Errorf("the later HOLD is a red, got %q", red.Outcome)
	}

	// NEGATIVE CONTROL. The same result twice is still two rows: the verb does
	// not deduplicate either. If this test could pass against a verb that keys
	// by (unit, result), the assertions above would be proving nothing.
	outcomeRun(t, log, "hold-me", "red")
	if got := len(readRows(t, log)); got != 4 {
		t.Errorf("negative control: a repeated result appends too, want 4 rows, got %d", got)
	}
}

// NEGATIVE CONTROL for the append itself: an outcome for one unit writes into
// no other unit's rows, and a refused outcome writes NOTHING. A verb that
// appended on the refusal path would still pass the test above.
func TestOutcomeTouchesNoOtherUnitAndARefusalWritesNoRow(t *testing.T) {
	log := routeThen(t, "unit-a", "row-test")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "unit-b", "--kind", "rebase", "--files", "1", "--packages", "1", "--no-jev", "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("route b: exit = %d (stderr=%q)", code, stderr.String())
	}
	before := readRows(t, log)

	outcomeRun(t, log, "unit-a", "green")
	after := readRows(t, log)
	if len(after) != len(before)+1 {
		t.Fatalf("exactly one row is appended, got %d rows from %d", len(after), len(before))
	}
	for i, row := range before {
		if after[i] != row {
			t.Errorf("row %d was rewritten\n before: %s\n  after: %s", i+1, row, after[i])
		}
	}

	// The refusals: none of them may leave a row behind.
	held := readRows(t, log)
	for name, args := range map[string][]string{
		"no decision": {"outcome", "--log", log, "--unit-id", "never-routed", "--result", "green"},
		"bad result":  {"outcome", "--log", log, "--unit-id", "unit-a", "--result", "mostly"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%s: exit = %d, want 2", name, code)
		}
		if got := readRows(t, log); len(got) != len(held) {
			t.Errorf("%s: a refusal wrote a row: %d rows, want %d", name, len(got), len(held))
		}
	}
}

// H2 of the SPEC-DECIDE amendment (#1627, spec-ahead for #1623) adds a FOURTH
// result: a unit harvest skipped for an unmet precondition is not a failure of
// the rung that was never asked to run it. It is an outcome row all the same,
// so coverage counts it, but it moves no floor in either direction.
func TestOutcomeSkippedIsTheFourthResult(t *testing.T) {
	log := routeThen(t, "skip-me", "row-test")
	line := outcomeRun(t, log, "skip-me", "skipped")
	if !strings.Contains(line, "result=skipped") || !strings.Contains(line, "outcome=skipped") {
		t.Errorf("the line names the fourth result, got: %s", line)
	}
	rows := readRows(t, log)
	if len(rows) != 2 {
		t.Fatalf("one decision and one outcome, got %d", len(rows))
	}
	var e decide.Entry
	if err := json.Unmarshal([]byte(rows[1]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Source != decide.SourceOutcome || e.Outcome != decide.OutcomeSkipped {
		t.Errorf("the skipped row lost its facts: %+v", e)
	}
	if e.RungSucceeded != "" {
		t.Errorf("a skipped unit is no rung's success, got rung_succeeded=%q", e.RungSucceeded)
	}

	// It moves no floor: neither a success nor a failure in the summary.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"log", "--log", log, "--summary"}, &stdout, &stderr); code != 0 {
		t.Fatalf("log exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "successes=0") || !strings.Contains(stdout.String(), "failures=0") {
		t.Errorf("skipped is neither a success nor a failure, got: %s", stdout.String())
	}

	// NEGATIVE CONTROL: the three words that DO move a floor still do. If
	// `skipped` had been wired to one of them this would catch it.
	for result, want := range map[string]string{"green": "successes=1", "red": "failures=1"} {
		other := routeThen(t, "ctl-"+result, "row-test")
		outcomeRun(t, other, "ctl-"+result, result)
		stdout.Reset()
		if code := run([]string{"log", "--log", other, "--summary"}, &stdout, &stderr); code != 0 {
			t.Fatalf("log exit = %d (stderr=%q)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("negative control: %s still counts (%s), got: %s", result, want, stdout.String())
		}
	}
}

// H2 again: `log --summary` prints coverage=<outcomes>/<decisions>, because the
// hurt it was written for is a number nobody could see -- 141 outcomes for 412
// decisions on 2026-09-19, and a floor tuned on a third of the rows is tuned on
// the rows somebody remembered.
func TestLogSummaryPrintsCoverage(t *testing.T) {
	log := routeThen(t, "covered", "row-test")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", "bare", "--kind", "row-test", "--files", "1", "--packages", "1", "--no-jev", "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("route: exit = %d (stderr=%q)", code, stderr.String())
	}

	stdout.Reset()
	if code := run([]string{"log", "--log", log, "--summary"}, &stdout, &stderr); code != 0 {
		t.Fatalf("log exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "coverage=0/2") {
		t.Errorf("two decisions and no outcome is coverage=0/2, got: %s", stdout.String())
	}

	outcomeRun(t, log, "covered", "green")
	stdout.Reset()
	if code := run([]string{"log", "--log", log, "--summary"}, &stdout, &stderr); code != 0 {
		t.Fatalf("log exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "coverage=1/2") {
		t.Errorf("one outcome of two decisions is coverage=1/2, got: %s", stdout.String())
	}

	// NEGATIVE CONTROL: the outcome row is NOT counted as a decision. A
	// denominator that grew with the numerator would read coverage=1/3 and
	// could never be short of 100%, which is the one thing this line is for.
	if strings.Contains(stdout.String(), "coverage=1/3") || !strings.Contains(stdout.String(), "decisions=2") {
		t.Errorf("an outcome row is no decision of its own, got: %s", stdout.String())
	}

	// A second outcome row for the same unit DOES move the numerator: coverage
	// is rows against rows, the arithmetic the receipt in #1623 was measured
	// with (141 outcomes for 412 decisions on 2026-09-19). The denominator is
	// what must not move, and the control above is what holds it still.
	outcomeRun(t, log, "covered", "red")
	stdout.Reset()
	if code := run([]string{"log", "--log", log, "--summary"}, &stdout, &stderr); code != 0 {
		t.Fatalf("log exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "coverage=2/2") {
		t.Errorf("two outcome rows against two decisions is coverage=2/2, got: %s", stdout.String())
	}
}

// A log path outside a temp dir is never touched by these tests; this is the
// one place the helper's assumption is stated.
var _ = filepath.Join
