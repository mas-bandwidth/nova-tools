package pulse

// ONE GUARD, EVERY ROAD INTO A BENCH (placement.go).
//
// `fill --machines` refused a runner host BY NAME and the run verb's launcher placed a card
// on that same host without a word, because Wiring.Launch read neither the registry nor the
// lanes table (the manager dogfood, edge 10). Both roads take a card through the same guard
// now, and these are the differences that used to exist between them.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// launchBench is a queue with one pending card and one root, which is the smallest thing
// the run verb's launcher can be pointed at.
func launchBench(t *testing.T, rootName string, cards map[string]string) (queue, root string) {
	t.Helper()
	base := t.TempDir()
	queue, root = filepath.Join(base, "queue"), filepath.Join(base, rootName)
	for _, d := range []string{"pending", "launched", "done", "failed"} {
		if err := os.MkdirAll(filepath.Join(queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range cards {
		writeCard(t, filepath.Join(queue, "pending"), name, body)
	}
	return queue, root
}

// wiredLauncher builds the run verb's launcher over one root, with the tables named.
func wiredLauncher(t *testing.T, queue, root, machines, lanes string, forge PRSource) (*Wiring, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	w := NewWiring(WiringInput{
		Queue: queue, Roots: root, Repo: "owner/name", Branch: "dev",
		Machines: machines, Lanes: lanes, Log: &log, PRs: forge,
		Now:    func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
		Config: func() Config { return Config{Slots: map[string]int{"studio": 4, "space": 4, "local": 4}} },
	})
	return w, &log
}

// TestRunRoadRefusesARunnerHost: a root whose bench the registry calls a CI-only runner
// host takes no card at all. That is Glenn's lock of 2026-09-18, and it used to hold on the
// fill road only.
func TestRunRoadRefusesARunnerHost(t *testing.T) {
	// The root is named so benchOf reads it as `space`, and the registry says space is a
	// runner host and not a bench.
	queue, root := launchBench(t, "swarm-space", map[string]string{"card-001.md": "RESULT: CARD-1\n"})
	machines := machinesFile(t, filepath.Dir(queue), []string{"studio"}, []string{"space"})
	w, log := wiredLauncher(t, queue, root, machines, "", nil)

	launched, _, err := w.Launch(1)
	if err != nil {
		t.Fatalf("launch failed: %v", err)
	}
	if launched != 0 {
		t.Fatalf("the run road placed %d cards on a CI runner host, want 0", launched)
	}
	if _, err := os.Stat(filepath.Join(queue, "pending", "card-001.md")); err != nil {
		t.Fatalf("the refused card left pending: %v", err)
	}
	if !strings.Contains(log.String(), "runner-host") {
		t.Fatalf("the refusal does not name the reason: %q", log.String())
	}
}

// TestRunRoadHoldsAGatedCard: the gate is SPEC-PULSE rule 4's, and it is the card's, not
// the road's. A card gated on an open PR stays in pending here exactly as it stays in ready
// on the fill road.
func TestRunRoadHoldsAGatedCard(t *testing.T) {
	queue, root := launchBench(t, "swarm-studio", map[string]string{
		"card-001.md": "RESULT: CARD-1\nAFTER: PR7 merged\n",
	})
	machines := machinesFile(t, filepath.Dir(queue), []string{"studio"}, nil)
	forge := &gateForge{state: map[int]string{7: "OPEN"}}
	w, log := wiredLauncher(t, queue, root, machines, "", forge)

	launched, _, err := w.Launch(1)
	if err != nil {
		t.Fatalf("launch failed: %v", err)
	}
	if launched != 0 {
		t.Fatalf("the run road launched a card gated on an OPEN pull request")
	}
	if !strings.Contains(log.String(), "LAUNCH GATED card=card-001.md after=PR7 state=OPEN") {
		t.Fatalf("the run road does not name the gate it held: %q", log.String())
	}
	if _, err := os.Stat(filepath.Join(queue, "pending", "card-001.md")); err != nil {
		t.Fatalf("the gated card left pending: %v", err)
	}
}

// TestRunRoadWithNoRegistrySaysSoOnce: a launcher with no registry cannot tell a bench from
// a runner host. It is named once per shift and never guessed -- and never said every tick,
// which is the poll the run verb exists to end.
func TestRunRoadWithNoRegistrySaysSoOnce(t *testing.T) {
	queue, root := launchBench(t, "swarm-studio", map[string]string{"card-001.md": "RESULT: CARD-1\n"})
	w, log := wiredLauncher(t, queue, root, "", "", nil)
	// Two ticks; the note is one.
	_, _, _ = w.Launch(1)
	_, _, _ = w.Launch(2)
	if n := strings.Count(log.String(), "no machines registry"); n != 1 {
		t.Fatalf("the unwired guard was named %d times, want once per shift: %q", n, log.String())
	}
}

// TestModelForReadsTheKindLineAndAllNineMarks: bin/pulse-loop.sh's model_for reads
// `sed -n '2p'` and matches nine marks; this knew six and read the whole card, so three
// kinds of text card went to the code routes and a code card whose BODY said "a reader"
// went to the text ones (the manager dogfood, edge 8).
func TestModelForReadsTheKindLineAndAllNineMarks(t *testing.T) {
	queue := t.TempDir()
	if err := os.WriteFile(filepath.Join(queue, "ROUTES-text"), []byte("text/route\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queue, "ROUTES-code"), []byte("code/route\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := NewWiring(WiringInput{Queue: queue, Roots: queue, Log: &bytes.Buffer{}})

	for _, mark := range textMarks {
		card := writeCard(t, queue, "card-1.md", "RESULT: CARD-1 a contract\nthis card is "+mark+"\nSTEP 1.\n")
		if got := w.modelFor(card); got != "text/route" {
			t.Fatalf("a %q card routed to %q, want the text routes", mark, got)
		}
	}
	// The body is not the class: a code card that happens to mention a reader is a code card.
	card := writeCard(t, queue, "card-2.md", "RESULT: CARD-2 a contract\nfix the bug\nSTEP 3. ask a reader for a read\n")
	if got := w.modelFor(card); got != "code/route" {
		t.Fatalf("a code card whose body mentions a reader routed to %q, want the code routes", got)
	}
}
