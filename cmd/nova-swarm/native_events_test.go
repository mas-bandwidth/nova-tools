package main

// THE CARD'S OWN TWO LINES. `nova-swarm native` is the verb that spends the tokens, so it
// is the verb whose start and done answer "what is running right now" and "what did this
// card cost" -- a start with no done is a hung card, which is the question SPEC-LOGS.md
// Part 5 measures the whole stack by.
//
// No socket, no provider: the harness is this package's own fake binary and the usage store
// is absent, so the done line carries the dashes a card with no reported usage carries.
//
// Seen red first: before --log existed the run exited 2 on an unknown flag, and before
// emitNativeCard existed the file held no line.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nativeEvent struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Bench  string `json:"bench"`
	Verb   string `json:"verb"`
	Card   string `json:"card"`
	Slot   string `json:"slot"`
	Event  string `json:"event"`
	Msg    string `json:"msg"`
	TS     string `json:"ts"`
	GUID   string `json:"guid"`
	DurMS  int64  `json:"dur_ms"`
}

func readNativeEvents(t *testing.T, path string) []nativeEvent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the --log file was not written: %v", err)
	}
	var out []nativeEvent
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e nativeEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("a logged line is not one JSON object: %v\n%s", err, line)
		}
		out = append(out, e)
	}
	return out
}

func nativeEventOfKind(t *testing.T, lines []nativeEvent, event string) nativeEvent {
	t.Helper()
	var got []nativeEvent
	for _, l := range lines {
		if l.Event == event {
			got = append(got, l)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one %q line, got %d", event, len(got))
	}
	return got[0]
}

// a-card-writes-a-start-and-a-done-with-its-usage: one run of the fake harness through the
// binary, with --log naming the file Alloy tails and --bench naming this machine.
func TestNativeEmitsStartAndDoneWithUsage(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("test card line 1\nline 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eventLog := filepath.Join(root, "nova-events.log")

	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", "card-9382",
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "10s",
		"--no-wall",
		"--bench", "hulk",
		"--log", eventLog,
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("the run exits 0, got %d:\nstdout: %s\nstderr: %s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("the human line is untouched and still printed:\n%s", stdout.String())
	}

	lines := readNativeEvents(t, eventLog)
	for _, l := range lines {
		if l.Source != "nova-swarm" || l.Verb != "native" || l.Bench != "hulk" {
			t.Fatalf("a line is missing its labels: source=%q verb=%q bench=%q", l.Source, l.Verb, l.Bench)
		}
		if l.Card != "card-9382" {
			t.Fatalf("every line of a card carries the card as a FIELD, got %q", l.Card)
		}
		if l.TS == "" || l.GUID == "" {
			t.Fatalf("ts and guid are never absent: %+v", l)
		}
	}

	start := nativeEventOfKind(t, lines, "start")
	if !strings.Contains(start.Msg, "model=fake/fake-model") {
		t.Fatalf("the start line names the model the card runs on: %q", start.Msg)
	}
	if !strings.Contains(start.Msg, "deadline=10s") {
		t.Fatalf("the start line names the deadline the card runs under: %q", start.Msg)
	}

	done := nativeEventOfKind(t, lines, "done")
	if !strings.Contains(done.Msg, "rc=0") {
		t.Fatalf("the done line carries the child's exit code: %q", done.Msg)
	}
	// THE USAGE COLUMNS, in the order usage.tsv writes them, each a dash when no store
	// answered -- never a zero, which would be a claim that the card spent nothing.
	for _, want := range []string{"tokens_in=", "tokens_out=", "cache_write=", "cache_read=", "reasoning=", "usd="} {
		if !strings.Contains(done.Msg, want) {
			t.Fatalf("the done line carries the usage column %s: %q", want, done.Msg)
		}
	}
	if strings.Contains(done.Msg, "tokens_in=0") {
		t.Fatalf("a column nobody reported is a dash and never a zero: %q", done.Msg)
	}
	if done.DurMS < 0 {
		t.Fatalf("the done line carries the wall as dur_ms: %+v", done)
	}

	// NOTHING OF THE CARD'S TEXT IS ON THE LINE: the card is a path and an id, and its
	// prose stays in the file.
	if strings.Contains(strings.Join([]string{start.Msg, done.Msg}, " "), "test card line 1") {
		t.Fatalf("the card's text reached the structured stream")
	}
}

// A --log that cannot be opened is a refusal BEFORE the child runs: the cheapest possible
// moment, and the only one at which no tokens have been spent yet.
func TestNativeRefusesALogItCannotOpen(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", bin, "--model", "fake/fake-model", "--card", cardPath,
		"--slot", slot, "--root", root, "--deadline", "10s", "--no-wall",
		"--log", filepath.Join(root, "no-such-directory", "nova-events.log"),
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 2 {
		t.Fatalf("a --log that cannot be opened is exit 2, got %d:\n%s", rc, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--log") {
		t.Fatalf("the refusal names the flag: %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("a refused run reported a card that never ran: %q", stdout.String())
	}
}
