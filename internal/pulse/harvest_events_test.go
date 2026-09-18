package pulse

// The fold's own structured events (SPEC-LOGS.md Part 2, round 2). The assertions are on
// the SHAPE of the lines: the kind, the labels a LogQL query selects on, and the fields a
// panel reads out of the message. Nothing here opens a socket -- git and gh are the
// package's own Go fakes on PATH, and the emitter writes into a buffer.
//
// They were seen red before HarvestInput carried an Events emitter: with the emitter
// removed the file holds no line at all and every one of these fails on the first read.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
)

// pulseEvent is one structured line, decoded. The fields are the spec's fifteen; a test
// names only the ones it is about.
type pulseEvent struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Bench  string `json:"bench"`
	Verb   string `json:"verb"`
	Card   string `json:"card"`
	Slot   string `json:"slot"`
	PR     int    `json:"pr"`
	Event  string `json:"event"`
	Msg    string `json:"msg"`
	Err    string `json:"err"`
	TS     string `json:"ts"`
	GUID   string `json:"guid"`
	DurMS  int64  `json:"dur_ms"`
}

// decodePulseEvents reads a buffer of lines. Every line must be one JSON object: a log with
// a half-written line in it is a log Alloy's json stage drops, so the decode IS an
// assertion and not a convenience.
func decodePulseEvents(t *testing.T, raw string) []pulseEvent {
	t.Helper()
	var out []pulseEvent
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e pulseEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("a logged line is not one JSON object: %v\n%s", err, line)
		}
		out = append(out, e)
	}
	return out
}

func pulseEventsOfKind(lines []pulseEvent, event string) []pulseEvent {
	var out []pulseEvent
	for _, l := range lines {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

func onePulseEvent(t *testing.T, lines []pulseEvent, event string) pulseEvent {
	t.Helper()
	got := pulseEventsOfKind(lines, event)
	if len(got) != 1 {
		t.Fatalf("want exactly one %q line, got %d", event, len(got))
	}
	return got[0]
}

// testEmitter is the production emitter with the two outside things injected: a fixed clock
// so ts is the test's number, and a fixed guid so nothing reads /proc.
func testEmitter(w *bytes.Buffer, source, verb, bench string) *novalog.Emitter {
	e := novalog.NewEmitter(w, source, verb, bench)
	e.Clock = func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) }
	e.GUID = func() string { return "test-guid" }
	return e
}

// harvest-emits-start-a-line-per-card-and-done: one fold of three cards -- one that pushes
// and opens a PR, two whose RESULT does not match their contract -- writes a start, one
// harvest-card per card disposed, and a done carrying the same counts the HARVEST line
// prints. The per-card line is what answers "what happened to card X" without an ssh.
func TestHarvestEmitsStartCardLinesAndDone(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	// The fake gh answers `pr create` with this URL and harvest reads the number off
	// its tail. The host is a fixture's and deliberately not the real forge's: the
	// CI-NET class test refuses a real host in a test file, and this one needs no host
	// at all -- see cmd/nova-pulse's own allowlist entry for the tests that predate it.
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/42")

	addCard(t, root, "a", "1", "flash", "RESULT a sha=aaa",
		"RESULT a sha=aaa\nDONE\nBRANCH br1\nREPO owner/repo\n")
	addCard(t, root, "b", "2", "flash", "RESULT b sha=bbb",
		"RESULT b sha=bbc\nDONE\nBRANCH br2\nREPO owner/repo\n")

	var events bytes.Buffer
	var out, errs bytes.Buffer
	Harvest(HarvestInput{
		ID: "p1", Root: root, Sources: filepath.Join(root, "sources.tsv"),
		Templates: root, MaxBodyBytes: 4096, Max: 20,
		Stdout: &out, Stderr: &errs,
		Events: testEmitter(&events, "nova-pulse", "harvest", "hulk"),
	})

	lines := decodePulseEvents(t, events.String())
	if len(lines) == 0 {
		t.Fatalf("the fold wrote no structured line at all; stdout was:\n%s", out.String())
	}

	// THE LABELS. Every line of this verb carries the four a query selects on, and the ts
	// and guid the spec says are never absent.
	for _, l := range lines {
		if l.Source != "nova-pulse" || l.Verb != "harvest" || l.Bench != "hulk" {
			t.Fatalf("a line is missing its labels: source=%q verb=%q bench=%q", l.Source, l.Verb, l.Bench)
		}
		if l.TS == "" || l.GUID == "" {
			t.Fatalf("ts and guid are never absent: %+v", l)
		}
	}

	start := onePulseEvent(t, lines, "start")
	if !strings.Contains(start.Msg, "pulse p1") {
		t.Fatalf("the start line names the pulse it took: %q", start.Msg)
	}

	cards := pulseEventsOfKind(lines, EventHarvestCard)
	if len(cards) != 2 {
		t.Fatalf("one harvest-card line per card disposed, got %d:\n%s", len(cards), events.String())
	}
	byCard := map[string]pulseEvent{}
	for _, c := range cards {
		byCard[c.Card] = c
	}
	pushed, ok := byCard["a"]
	if !ok {
		t.Fatalf("no line for the card that landed:\n%s", events.String())
	}
	if pushed.PR != 42 {
		t.Fatalf("the landed card's line carries its pr as a FIELD (pr=42), got %d", pushed.PR)
	}
	if pushed.Slot != "1" {
		t.Fatalf("the card's slot is a field: %q", pushed.Slot)
	}
	if !strings.Contains(pushed.Msg, "disposition=pr") {
		t.Fatalf("the disposition is on the line: %q", pushed.Msg)
	}
	mismatched, ok := byCard["b"]
	if !ok {
		t.Fatalf("no line for the mismatched card:\n%s", events.String())
	}
	if !strings.Contains(mismatched.Msg, "disposition=mismatch") {
		t.Fatalf("a mismatch says so: %q", mismatched.Msg)
	}

	done := onePulseEvent(t, lines, "done")
	for _, want := range []string{"done=1", "pushed=1", "prs=1", "mismatch=1"} {
		if !strings.Contains(done.Msg, want) {
			t.Fatalf("the done line carries the HARVEST counts (%s): %q", want, done.Msg)
		}
	}

	// NO RESULT BODY REACHES THE STREAM. The lines carry ids, counts and this program's
	// own sentences; a model's prose stays in RESULT.md where it is evidence.
	if strings.Contains(events.String(), "sha=aaa") {
		t.Fatalf("a RESULT line reached the structured stream:\n%s", events.String())
	}
}

// harvest-refuses-into-the-stream: a fold that cannot start says so as a refuse event, so a
// bench that harvested nothing is one query away from its reason and is never a silence a
// reader has to guess at.
func TestHarvestRefusalIsARefuseEvent(t *testing.T) {
	var events, out, errs bytes.Buffer
	code := Harvest(HarvestInput{
		ID: "", Root: t.TempDir(), Stdout: &out, Stderr: &errs,
		Events: testEmitter(&events, "nova-pulse", "harvest", "hulk"),
	})
	if code != 2 {
		t.Fatalf("a fold with no --id is exit 2, got %d", code)
	}
	refusal := onePulseEvent(t, decodePulseEvents(t, events.String()), "refuse")
	if refusal.Level != "ERROR" {
		t.Fatalf("a refusal carrying a reason is ERROR, got %q", refusal.Level)
	}
	if !strings.Contains(refusal.Msg, "--id") {
		t.Fatalf("the refuse line names what was missing: %q", refusal.Msg)
	}
	if refusal.Err == "" {
		t.Fatalf("the refusal's own error text is the err field: %+v", refusal)
	}
}
