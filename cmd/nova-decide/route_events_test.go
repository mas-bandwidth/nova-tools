package main

// THE DECISION AS AN OBSERVATION. The escalation log (--log) is the RECORD: replayable,
// summarised by `nova-decide log --summary`, and the thing an accounting obligation is
// discharged against. This is the other half: one structured line per decision, in the same
// shape every other verb emits, so "which minds is the ladder choosing, and how often does
// the floor step us up" is a query beside the fill ticks and the card runs rather than a
// file somebody has to find first (SPEC-LOGS.md Part 2).
//
// --no-jev throughout: the rules alone, no key, no network, deterministic.
//
// Seen red first: before --event-log existed the run exited 2 on an unknown flag.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type routeEvent struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Bench  string `json:"bench"`
	Verb   string `json:"verb"`
	Job    string `json:"job"`
	Event  string `json:"event"`
	Msg    string `json:"msg"`
	Err    string `json:"err"`
	TS     string `json:"ts"`
	GUID   string `json:"guid"`
}

func readRouteEvents(t *testing.T, path string) []routeEvent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the --event-log file was not written: %v", err)
	}
	var out []routeEvent
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e routeEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("a logged line is not one JSON object: %v\n%s", err, line)
		}
		out = append(out, e)
	}
	return out
}

// one-decision-one-line, carrying the four fields the question is asked with: the kind, the
// rung, the confidence and the floor it was gated on.
func TestRouteEmitsOneEventPerDecision(t *testing.T) {
	unit := writeUnit(t, map[string]any{"id": "card-41", "kind": "rebase", "files": 2, "packages": 1})
	eventLog := filepath.Join(t.TempDir(), "nova-events.log")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit", unit, "--no-jev",
		"--bench", "hulk", "--event-log", eventLog}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "ROUTE unit=card-41") {
		t.Fatalf("the human line is untouched and still first: %q", stdout.String())
	}

	lines := readRouteEvents(t, eventLog)
	if len(lines) != 1 {
		t.Fatalf("one decision is one line, got %d:\n%s", len(lines), strings.Join(msgsOf(lines), "\n"))
	}
	e := lines[0]
	if e.Source != "nova-decide" || e.Verb != "route" || e.Bench != "hulk" || e.Event != "route" {
		t.Fatalf("the line is missing its labels: %+v", e)
	}
	if e.TS == "" || e.GUID == "" {
		t.Fatalf("ts and guid are never absent: %+v", e)
	}
	if e.Job != "card-41" {
		t.Fatalf("the unit id is the line's work item, got %q", e.Job)
	}
	for _, want := range []string{"kind=rebase", "rung=flash", "confidence=0.90", "floor=0.90", "source=", "stepped_up=", "escalated="} {
		if !strings.Contains(e.Msg, want) {
			t.Fatalf("the decision line does not carry %q: %q", want, e.Msg)
		}
	}
}

// A decision under its own floor is a WARN, so the panel that watches the ladder step up is
// a label selector and not a line filter.
func TestRouteBelowTheFloorIsAWarnEvent(t *testing.T) {
	unit := writeUnit(t, map[string]any{"id": "thin", "kind": "new-verb"})
	eventLog := filepath.Join(t.TempDir(), "nova-events.log")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"route", "--unit", unit, "--no-jev", "--floor", "0.9",
		"--event-log", eventLog}, &stdout, &stderr); code != 3 {
		t.Fatalf("exit = %d, want 3 (an answer below the floor)", code)
	}
	lines := readRouteEvents(t, eventLog)
	if len(lines) != 1 {
		t.Fatalf("one decision is one line, got %d", len(lines))
	}
	if lines[0].Level != "WARN" {
		t.Fatalf("a decision below its floor is WARN, got %q: %q", lines[0].Level, lines[0].Msg)
	}
	if !strings.Contains(lines[0].Msg, "floor=0.90") {
		t.Fatalf("every decision line carries the floor it was gated on: %q", lines[0].Msg)
	}
}

// An --event-log that cannot be opened is refused BEFORE the provider could be called: a
// decision that cost a call must never be the thing that discovers the path was wrong.
func TestRouteRefusesAnEventLogItCannotOpen(t *testing.T) {
	unit := writeUnit(t, map[string]any{"id": "card-41", "kind": "rebase", "files": 2})
	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "--unit", unit, "--no-jev",
		"--event-log", filepath.Join(t.TempDir(), "no-such-directory", "nova-events.log")}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--event-log") {
		t.Fatalf("the refusal names the flag: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "ROUTE ") {
		t.Fatalf("a refused route printed a decision: %q", stdout.String())
	}
}

func msgsOf(lines []routeEvent) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Event+" "+l.Msg)
	}
	return out
}
