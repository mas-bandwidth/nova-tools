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

// writeUnit writes one unit file and returns its path. Every path is a flag;
// nothing here depends on the working directory.
func writeUnit(t *testing.T, u map[string]any) string {
	t.Helper()
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "unit.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// With --no-jev the verb answers by the rules alone: no key, no network, one
// line, and the same line twice.
func TestRouteNoJevIsDeterministic(t *testing.T) {
	unit := writeUnit(t, map[string]any{"id": "card-41", "kind": "rebase", "files": 2, "packages": 1})
	var first, second bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"route", "--unit", unit, "--no-jev"}, &first, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, first.String(), stderr.String())
	}
	if code := run([]string{"route", "--unit", unit, "--no-jev"}, &second, &stderr); code != 0 {
		t.Fatalf("second run exit = %d", code)
	}
	if first.String() != second.String() {
		t.Errorf("the rules alone are deterministic:\n%s\n%s", first.String(), second.String())
	}
	line := strings.TrimSuffix(first.String(), "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("exactly one line: %q", first.String())
	}
	for _, want := range []string{"ROUTE ", "unit=card-41", "rung=flash", "confidence=", "reason=\"", "ask=card"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is missing %q: %s", want, line)
		}
	}
}

// The unit is evidence, and every field of it is a flag too.
func TestRouteTakesTheUnitAsFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"route", "--no-jev", "--unit-id", "card-77", "--kind", "fix-with-red-test",
		"--files", "3", "--packages", "1", "--lanes", "1", "--lane-owner", "code",
		"--attempt", "opus:failed:missed the cause", "--deadline", "45m",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	line := stdout.String()
	if !strings.Contains(line, "rung=sol") {
		t.Errorf("opus failed: sideways to sol before up: %s", line)
	}
	if !strings.Contains(line, "ask=child") {
		t.Errorf("a child rung is asked as a child: %s", line)
	}
}

// Below the floor the answer steps up and the exit is 3 -- a suggestion, never
// an authorization.
func TestRouteBelowTheFloorExits3(t *testing.T) {
	unit := writeUnit(t, map[string]any{"id": "thin", "kind": "new-verb"})
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit", unit, "--no-jev", "--floor", "0.9"}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "floor=0.90") {
		t.Errorf("every decision line carries the floor: %s", stdout.String())
	}
}

// A unit the tool cannot read as evidence is a refusal, on stderr, at exit 2.
func TestRouteRefusals(t *testing.T) {
	good := writeUnit(t, map[string]any{"id": "u", "kind": "rebase", "files": 1})
	for name, args := range map[string][]string{
		"no unit":        {"route", "--no-jev"},
		"unknown kind":   {"route", "--no-jev", "--unit-id", "u", "--kind", "vibes"},
		"missing file":   {"route", "--no-jev", "--unit", filepath.Join(t.TempDir(), "nope.json")},
		"bad floor":      {"route", "--no-jev", "--unit", good, "--floor", "2"},
		"bad attempt":    {"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--attempt", "opus"},
		"bad registry":   {"route", "--no-jev", "--unit", good, "--registry", filepath.Join(t.TempDir(), "nope.json")},
		"stray argument": {"route", "--no-jev", "--unit", good, "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stdout=%q stderr=%q)", name, code, stdout.String(), stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%s: a refusal belongs on stderr, stdout had %q", name, stdout.String())
		}
		if !strings.Contains(stderr.String(), "REFUSED reason=") {
			t.Errorf("%s: the refusal must say REFUSED reason=...: %q", name, stderr.String())
		}
	}
}

// Every decision is logged: the evidence, the rung tried, the floor, and what
// Rowan would have picked, one JSON object per line.
func TestRouteAppendsToTheLog(t *testing.T) {
	unit := writeUnit(t, map[string]any{"id": "card-9", "kind": "rebase", "files": 2, "packages": 1})
	log := filepath.Join(t.TempDir(), "decide.jsonl")
	var stdout, stderr bytes.Buffer
	for i := 0; i < 2; i++ {
		stdout.Reset()
		if code := run([]string{"route", "--unit", unit, "--no-jev", "--log", log}, &stdout, &stderr); code != 0 {
			t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
		}
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("two decisions are two lines, got %d:\n%s", len(lines), raw)
	}
	var e decide.Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("the log line is not one JSON object: %v", err)
	}
	if e.Unit != "card-9" || e.RungTried != "flash" || e.RowanPick != "flash" || e.Floor != decide.DefaultFloor {
		t.Errorf("the row lost its evidence: %+v", e)
	}
}

// A registry of one's own is a data file, named by a flag.
func TestRouteTakesARegistryFile(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "registry.json")
	body := `{"minds":[
	  {"name":"tiny","lineage":"local","height":0,"availability":"available","ask":"card"},
	  {"name":"big","lineage":"local","height":1,"availability":"available","ask":"bus"}
	]}`
	if err := os.WriteFile(reg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	unit := writeUnit(t, map[string]any{"id": "u", "kind": "rebase", "files": 2, "packages": 1})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit", unit, "--no-jev", "--registry", reg}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rung=tiny") {
		t.Errorf("the registry names the ladder: %s", stdout.String())
	}
}

// The second decision: continue, ask all friends, ask Glenn.
func TestHelpVerbAnswers(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	body := `{"hours": 3, "retries_on_rung": 1, "failures_last_hour": 1, "self_inflicted": 0, "class_recurring": false, "landing_moved": true, "uncertainty": 0.2}`
	if err := os.WriteFile(state, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help", "--state", state}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "HELP answer=ask-all-friends") {
		t.Errorf("three hours on one problem is an ask: %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"help", "--hours", "0.5", "--landing-moved", "--uncertainty", "0.1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "HELP answer=continue") {
		t.Errorf("a moving landing is a continue: %s", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"help", "--hours", "6", "--asked-all-friends"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "HELP answer=ask-glenn") {
		t.Errorf("six hours after the friends is Glenn's: %s", stdout.String())
	}
}

// `nova-decide help` with no arguments is still the door the onboarding
// standard names: the usage, on stdout, at exit 0.
func TestHelpWithNoArgumentsIsTheUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "usage:") || !strings.Contains(stdout.String(), "example:") {
		t.Errorf("`nova-decide help` prints the usage: %q", stdout.String())
	}
	for _, verb := range []string{"route", "help --state", "log --log"} {
		if !strings.Contains(stdout.String(), verb) {
			t.Errorf("the usage does not name %q", verb)
		}
	}
}

// An impossible state is a refusal, never an answer.
func TestHelpRefusesImpossibleState(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help", "--hours", "-1"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q)", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "REFUSED reason=") {
		t.Errorf("the refusal must say REFUSED reason=...: %q", stderr.String())
	}
}

// The log verb reads the rows back: escalations per kind and the starting rung
// regenerated from them.
func TestLogVerbSummary(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "decide.jsonl")
	unit := writeUnit(t, map[string]any{"id": "u", "kind": "rebase", "files": 2, "packages": 1})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit", unit, "--no-jev", "--log", log}, &stdout, &stderr); code != 0 {
		t.Fatalf("route exit = %d (stderr=%q)", code, stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"log", "--log", log, "--summary"}, &stdout, &stderr); code != 0 {
		t.Fatalf("log exit = %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"LOG kind=rebase", "decisions=1", "escalations=0", "start_rung=", "LOG OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
	stdout.Reset()
	if code := run([]string{"log", "--log", filepath.Join(dir, "nope.jsonl"), "--summary"}, &stdout, &stderr); code != 2 {
		t.Errorf("a missing log is a refusal, got exit %d", code)
	}
	stdout.Reset()
	if code := run([]string{"log", "--summary"}, &stdout, &stderr); code != 2 {
		t.Errorf("--log is required, got exit %d", code)
	}
}

// No verb takes a key on argv: the key reaches the process only as an
// environment variable nova-secrets exec set (rule 3).
func TestNoVerbTakesAKeyOnArgv(t *testing.T) {
	if strings.Contains(usage, "--key ") || strings.Contains(usage, "--api-key") {
		t.Error("the usage offers a key flag; the key comes only from the environment")
	}
}
