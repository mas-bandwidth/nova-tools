package pulse

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// launch-writes-the-json-line-beside-the-stdout-line: the PULSE OK line the human reads
// stays exactly as it was, and the structured event goes to its own writer, never instead
// of it. The clock and the guid are injected, so the object is deterministic and the test
// reads no /proc.
func TestLaunchWritesTheJSONLineBesideTheStdoutLine(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 2)

	var out, stderr, events bytes.Buffer
	code := Launch(LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Stdout: &out, Stderr: &stderr, Log: &events,
		GUID: func() string { return "run-guid" },
		Now:  func() time.Time { return time.Date(2026, 9, 17, 16, 56, 3, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, stderr.String())
	}

	// Beside, not instead of: the stdout event line is the one it always was.
	if !strings.Contains(out.String(), "PULSE OK id=") || !strings.Contains(out.String(), "n=2") {
		t.Fatalf("the stdout PULSE line changed: %q", out.String())
	}
	if strings.Contains(out.String(), `"event"`) {
		t.Fatalf("the JSON line landed on stdout instead of beside it: %q", out.String())
	}

	// The event sink holds the start/done spine, one JSON object per line.
	raw := strings.TrimRight(events.String(), "\n")
	if raw == "" {
		t.Fatalf("no structured event was written")
	}
	lines := strings.Split(raw, "\n")
	if len(lines) != 2 {
		t.Fatalf("want a start and a done event, got %d line(s): %s", len(lines), events.String())
	}
	var first, last map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("start event is not one JSON object: %v\n%s", err, lines[0])
	}
	if err := json.Unmarshal([]byte(lines[1]), &last); err != nil {
		t.Fatalf("done event is not one JSON object: %v\n%s", err, lines[1])
	}
	if first["event"] != "start" || last["event"] != "done" {
		t.Fatalf("the spine is not start then done: %s", events.String())
	}
	id := pulseID(t, out.String())
	for _, ev := range []map[string]any{first, last} {
		if ev["source"] != "nova-pulse" {
			t.Errorf("source = %v, want nova-pulse", ev["source"])
		}
		if ev["verb"] != "launch" {
			t.Errorf("verb = %v, want launch", ev["verb"])
		}
		if ev["guid"] != "run-guid" {
			t.Errorf("guid = %v, want the injected run-guid", ev["guid"])
		}
		if ev["ts"] != "2026-09-17T16:56:03Z" {
			t.Errorf("ts = %v, want the injected clock", ev["ts"])
		}
		if _, ok := ev["card"]; !ok {
			t.Errorf("card is omitted, want the empty string")
		}
	}
	if !strings.Contains(last["msg"].(string), id) {
		t.Errorf("the done msg does not name the pulse %s: %v", id, last["msg"])
	}
}
