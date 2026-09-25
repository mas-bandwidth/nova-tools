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

// routeThen routes one unit by the rules into a fresh log and returns the log's
// path. No key, no network: the outcome verb is about the log, not the provider.
func routeThen(t *testing.T, id, kind string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "decide.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit-id", id, "--kind", kind, "--files", "1", "--packages", "1", "--no-jev", "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("route exit = %d (stderr=%q)", code, stderr.String())
	}
	return log
}

// The other half of rule 8: what happened to the unit the decision routed. The
// verb reads the kind and the rung from that decision, appends one row of its
// own, and the summary then counts a success where it counted none.
func TestOutcomeWritesTheOtherHalfOfTheRow(t *testing.T) {
	log := routeThen(t, "row-card-9", "row-test")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"outcome", "--log", log, "--unit-id", "row-card-9", "--result", "green"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	line := strings.TrimSpace(stdout.String())
	for _, want := range []string{"OUTCOME ", "unit=row-card-9", "kind=row-test", "rung=pro", "result=green", "outcome=ok"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is missing %q: %s", want, line)
		}
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the decision and its outcome are two rows, got %d:\n%s", len(lines), raw)
	}
	var e decide.Entry
	if err := json.Unmarshal([]byte(lines[1]), &e); err != nil {
		t.Fatalf("the outcome row is not one JSON object: %v", err)
	}
	if e.Source != decide.SourceOutcome || e.RungSucceeded != "pro" || e.Outcome != decide.OutcomeOK {
		t.Errorf("the outcome row lost its facts: %+v", e)
	}
	// And the summary now has the success it did not have.
	stdout.Reset()
	if code := run([]string{"log", "--log", log, "--summary"}, &stdout, &stderr); code != 0 {
		t.Fatalf("log exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "decisions=1") || !strings.Contains(stdout.String(), "successes=1") {
		t.Errorf("one decision and one success, got: %s", stdout.String())
	}
}

// red and blocked are the other two words a manager has, and each maps to an
// outcome the ladder already counts.
func TestOutcomeMapsEveryResult(t *testing.T) {
	for result, want := range map[string]string{
		"green":   decide.OutcomeOK,
		"red":     decide.OutcomeFailed,
		"blocked": decide.OutcomeAbandoned,
	} {
		log := routeThen(t, "u-"+result, "rebase")
		var stdout, stderr bytes.Buffer
		if code := run([]string{"outcome", "--log", log, "--unit-id", "u-" + result, "--result", result}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s: exit = %d (stderr=%q)", result, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "outcome="+want) {
			t.Errorf("%s maps to %s, got %s", result, want, stdout.String())
		}
	}
}

// Every refusal names what is missing and guesses nothing: an outcome against a
// decision nobody made would regenerate a starting rung out of thin air.
func TestOutcomeRefusals(t *testing.T) {
	log := routeThen(t, "known", "rebase")
	for name, args := range map[string][]string{
		"no log":        {"outcome", "--unit-id", "known", "--result", "green"},
		"no unit":       {"outcome", "--log", log, "--result", "green"},
		"no result":     {"outcome", "--log", log, "--unit-id", "known"},
		"bad result":    {"outcome", "--log", log, "--unit-id", "known", "--result", "mostly"},
		"no decision":   {"outcome", "--log", log, "--unit-id", "never-routed", "--result", "green"},
		"missing log":   {"outcome", "--log", filepath.Join(t.TempDir(), "nope.jsonl"), "--unit-id", "known", "--result", "green"},
		"bad flag":      {"outcome", "--log", log, "--unit-id", "known", "--result", "green", "--nope"},
		"bare argument": {"outcome", "--log", log, "--unit-id", "known", "--result", "green", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stdout=%q)", name, code, stdout.String())
		}
		if !strings.HasPrefix(stderr.String(), "OUTCOME REFUSED reason=") {
			t.Errorf("%s: the refusal names its reason, got %q", name, stderr.String())
		}
	}
}
