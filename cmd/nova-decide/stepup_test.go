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

// Edge 24 at the command line: below the floor the line names the next rung,
// --step-up turns the signal into the rung above, and every step is a row in
// the decision log.

// Below the floor the line says where the work goes next.
func TestRouteLineCarriesTheNextRung(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--no-jev", "--unit-id", "thin", "--kind", "new-verb", "--floor", "0.9"}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	line := stdout.String()
	if !strings.Contains(line, "next=") || strings.Contains(line, "next=-") {
		t.Errorf("a below-floor line must name the NEXT rung: %s", line)
	}
	if !strings.Contains(line, "steps=1") {
		t.Errorf("one decision is one step: %s", line)
	}
}

// --step-up re-asks with the below-floor rung excluded, logs every step, and
// the final line carries the count.
func TestRouteStepUpLogsEveryStep(t *testing.T) {
	useFake(t, &fake{conf: 0.40, pick: 0})
	dir := t.TempDir()
	log := filepath.Join(dir, "decide.jsonl")
	usage := filepath.Join(dir, "usage.tsv")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit-id", "s-1", "--kind", "new-verb", "--files", "3",
		"--packages", "1", "--lanes", "1", "--step-up", "--max-steps", "3",
		"--log", log, "--usage", usage}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (a below-floor answer is still a suggestion); stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	line := strings.TrimSuffix(stdout.String(), "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("exactly one line is printed, whatever the step count: %q", stdout.String())
	}
	if !strings.Contains(line, "steps=") || strings.Contains(line, "steps=1") {
		t.Errorf("the final line must carry the step count it took: %s", line)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("every step is a logged decision: %v", err)
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(rows) < 2 {
		t.Fatalf("every step is a logged decision, got %d row(s):\n%s", len(rows), raw)
	}
	for i, row := range rows {
		var e decide.Entry
		if err := json.Unmarshal([]byte(row), &e); err != nil {
			t.Fatalf("row %d is not one JSON object: %v", i+1, err)
		}
		if e.Unit != "s-1" {
			t.Errorf("row %d lost its evidence pointer: %+v", i+1, e)
		}
	}
}

// --max-steps without --step-up, and a step count that is not a count, are both
// refusals naming the flag.
func TestRouteStepUpFlagRefusals(t *testing.T) {
	for name, args := range map[string][]string{
		"max-steps alone": {"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--files", "2", "--max-steps", "2"},
		"zero steps":      {"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--files", "2", "--step-up", "--max-steps", "0"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stdout=%q stderr=%q)", name, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "max-steps") {
			t.Errorf("%s: the refusal must name the flag: %q", name, stderr.String())
		}
	}
}

// A sub-verb refuses an unknown flag BY NAME rather than swallowing it and
// printing the banner at exit 0 (the report's `nova-decide help --state`).
func TestHelpRefusesUnknownFlagsByName(t *testing.T) {
	for name, args := range map[string][]string{
		"a flag that wants a value": {"help", "--state"},
		"a flag help does not hold": {"help", "--registry", "x"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		if code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stdout=%q)", name, code, stdout.String())
		}
		if strings.Contains(stdout.String(), "usage:") {
			t.Errorf("%s: a bad flag is a refusal, not the banner", name)
		}
		if !strings.Contains(stderr.String(), "REFUSED reason=") {
			t.Errorf("%s: %q", name, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help", "--registry", "x"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "registry") {
		t.Errorf("the refusal must name the flag it does not hold: %q", stderr.String())
	}
}
