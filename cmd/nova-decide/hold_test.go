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

// Stella's four findings on #1327 at 78620ba3, at the verb.

// (2) Security reaches the designated rung through the verb too, whichever way
// the touch is named and whatever floor is passed.
func TestRouteSecurityNeverFallsThrough(t *testing.T) {
	for name, args := range map[string][]string{
		"guard flag":    {"--guard"},
		"secrets flag":  {"--secrets"},
		"guard kind":    {"--kind", "guard"},
		"touch sandbox": {"--touches", "sandbox"},
		"touch sudo":    {"--touches", "sudo"},
		"touch keys":    {"--touches", "deploy-keys"},
		"touch network": {"--touches", "network"},
		"after johnny":  {"--guard", "--attempt", "johnny:failed:still red"},
		"at floor 1":    {"--guard", "--floor", "1"},
		"at floor 0":    {"--guard", "--floor", "0"},
	} {
		base := []string{"route", "--no-jev", "--unit-id", "sec-1", "--files", "1"}
		if !contains(args, "--kind") {
			base = append(base, "--kind", "fleet-chore")
		}
		var stdout, stderr bytes.Buffer
		if code := run(append(base, args...), &stdout, &stderr); code != 0 {
			t.Errorf("%s: exit = %d (stderr=%q)", name, code, stderr.String())
			continue
		}
		if !strings.Contains(stdout.String(), "rung=johnny") {
			t.Errorf("%s: security is the designated rung's always: %s", name, stdout.String())
		}
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// An unknown touch is a refusal naming the six.
func TestRouteRefusesAnUnknownTouch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--files", "1", "--touches", "the vibes"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q)", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "REFUSED reason=") {
		t.Errorf("want a refusal line: %q", stderr.String())
	}
}

// (3) A timeout holds its own rung until there is proof the attempt is dead,
// and the log row says the decision is an await.
func TestRouteHoldsTheRungOnAnUnterminatedTimeout(t *testing.T) {
	log := filepath.Join(t.TempDir(), "decide.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--no-jev", "--unit-id", "t-1", "--kind", "fix-with-red-test",
		"--files", "3", "--packages", "1", "--attempt", "opus:timeout", "--log", log}, &stdout, &stderr)
	// A wait is not permission, and only exit 0 is permission (SPEC.md).
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rung=opus") {
		t.Errorf("the rung is still occupied: %s", stdout.String())
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var e decide.Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &e); err != nil {
		t.Fatal(err)
	}
	if !e.AwaitingTermination {
		t.Errorf("the log row must carry the await: %s", raw)
	}

	// Termination proof moves it on.
	stdout.Reset()
	if code := run([]string{"route", "--no-jev", "--unit-id", "t-1", "--kind", "fix-with-red-test",
		"--files", "3", "--packages", "1", "--attempt", "opus:timeout-terminated:killed at 10m"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "rung=sol") {
		t.Errorf("a confirmed timeout moves sideways: %s", stdout.String())
	}
}

// (4) A floor that is not a number is a refusal with one remedy line.
func TestRouteRefusesANaNFloor(t *testing.T) {
	for _, floor := range []string{"NaN", "nan", "+Inf", "-1", "1.5"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"route", "--no-jev", "--unit-id", "u", "--kind", "rebase", "--files", "1", "--floor", floor}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("--floor %s: exit = %d, want 2 (stdout=%q)", floor, code, stdout.String())
			continue
		}
		line := strings.TrimSuffix(stderr.String(), "\n")
		if strings.Contains(line, "\n") {
			t.Errorf("--floor %s: a refusal is one line: %q", floor, stderr.String())
		}
		if !strings.Contains(line, "reason=bad-floor") || !strings.Contains(line, "0.9") {
			t.Errorf("--floor %s: the refusal wants reason=bad-floor and a remedy: %q", floor, line)
		}
		if stdout.Len() != 0 {
			t.Errorf("--floor %s: a refusal belongs on stderr: %q", floor, stdout.String())
		}
	}
	// The decision verb's own floor is the same class of bug, and the same fix.
	q := writeQuestions(t, choiceQuestions())
	var stdout, stderr bytes.Buffer
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	if code := run([]string{"--questions", q, "--state", q, "--floor", "NaN", "--key-env", "CARD8331_JEV_KEY"}, &stdout, &stderr); code != 2 {
		t.Errorf("a NaN floor on the decision verb exits %d, want 2", code)
	}
}
