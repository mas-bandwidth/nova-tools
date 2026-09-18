package pulse

// THE SEVEN FIELDS THE WIDTH LINE LOST (the manager dogfood, edge 9).
//
// bin/pulse-loop.sh's WIDTH answered two questions in one line -- is the machine full, and
// is there work for it -- with the load it is under, what is waiting, what is waiting on a
// pull request, what is finished, what failed, the fraction of the bench in use and the
// cached estimate, plus the UNDER-WIDTH, POOL-EMPTY and STARVED markers. The twelve-field
// line said only what the tick itself did, so both questions needed a second window.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// widthBench is a queue with a known standing state: three pending (one of them gated), two
// done, one failed, and a cached estimate.
func widthBench(t *testing.T) (queue, root string) {
	t.Helper()
	base := t.TempDir()
	queue, root = filepath.Join(base, "queue"), filepath.Join(base, "swarm-studio")
	for _, d := range []string{"pending", "launched", "done", "failed"} {
		if err := os.MkdirAll(filepath.Join(queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCard(t, filepath.Join(queue, "pending"), "card-001.md", "RESULT: CARD-1\n")
	writeCard(t, filepath.Join(queue, "pending"), "card-002.md", "RESULT: CARD-2\n")
	writeCard(t, filepath.Join(queue, "pending"), "card-003.md", "RESULT: CARD-3\nAFTER: PR7 merged\n")
	writeCard(t, filepath.Join(queue, "done"), "card-004.md", "done\n")
	writeCard(t, filepath.Join(queue, "done"), "card-005.md", "done\n")
	writeCard(t, filepath.Join(queue, "failed"), "card-006.md", "failed\n")
	if err := os.WriteFile(filepath.Join(queue, "EST"), []byte("remaining_cards=12 hours=3.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queue, ConfigFile), []byte("[slots]\nstudio = 8\nspace = 0\nlocal = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return queue, root
}

// freeSeam is a launcher that reports free slots and places nothing: util= is the one field
// that needs a number from the bench.
type freeSeam int

func (f freeSeam) Launch(int) (int, int, error) { return 0, int(f), nil }

func pulseWidthLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "PULSE WIDTH ") {
			return line
		}
	}
	t.Fatalf("no PULSE WIDTH line in %q", out)
	return ""
}

// TestWidthCarriesTheStandingCounts: one tick over a known queue, and the line says what is
// there as well as what happened.
func TestWidthCarriesTheStandingCounts(t *testing.T) {
	queue, root := widthBench(t)
	var out, errb bytes.Buffer
	code := Run(RunInput{
		Queue: queue, Roots: root, Repo: "owner/name", Branch: "dev", Once: true,
		Stdout: &out, Stderr: &errb,
		Now:      func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
		Sleep:    func(time.Duration) {},
		Load:     func() int { return 37 },
		Launcher: freeSeam(3),
	})
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; stderr=%q", code, errb.String())
	}
	line := pulseWidthLine(t, out.String())
	for _, want := range []string{
		"load=37",
		"pending=3", // every pending card, gated ones included, as the script counted it
		"gated=1",
		"done=2",
		"failed-cards=1", // failed= is the TICK's reap; this is the standing count
		"util=5/8",       // eight slots, three free
		"est=remaining_cards=12,hours=3.5",
		"UNDER-WIDTH", // there is work and there is room
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the WIDTH line is missing %q:\n  %s", want, line)
		}
	}
	if strings.Contains(line, "POOL-EMPTY") || strings.Contains(line, "STARVED") {
		t.Fatalf("a queue with three pending cards read as empty:\n  %s", line)
	}
}

// TestWidthSaysStarvedWhenThereIsNothingToDo: an empty pool AND an almost empty bench, three
// ticks running, is the one state nothing mechanical can fix -- there is no work -- so it is
// the one that reaches a person. Below three ticks it is POOL-EMPTY and nobody is told.
func TestWidthSaysStarvedWhenThereIsNothingToDo(t *testing.T) {
	base := t.TempDir()
	queue, root := filepath.Join(base, "queue"), filepath.Join(base, "swarm-studio")
	for _, d := range []string{"pending", "launched", "done", "failed"} {
		if err := os.MkdirAll(filepath.Join(queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(queue, ConfigFile), []byte("[slots]\nstudio = 8\nspace = 0\nlocal = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	var out, errb bytes.Buffer
	code := Run(RunInput{
		Queue: queue, Roots: root, Repo: "owner/name", Branch: "dev",
		Hours: 1, Stdout: &out, Stderr: &errb,
		// Three ticks: the clock moves past the hour on the fourth read.
		Now: func() func() time.Time {
			n := 0
			return func() time.Time {
				n++
				if n > 8 {
					return now.Add(2 * time.Hour)
				}
				return now
			}
		}(),
		Sleep:    func(time.Duration) {},
		Load:     func() int { return 0 },
		Launcher: freeSeam(8),
	})
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; stderr=%q", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	first := pulseWidthLine(t, lines[0])
	if !strings.Contains(first, "POOL-EMPTY") {
		t.Fatalf("tick 1 does not say the pool is empty:\n  %s", first)
	}
	if strings.Contains(first, "STARVED") {
		t.Fatalf("tick 1 escalated on the first empty tick:\n  %s", first)
	}
	if !strings.Contains(out.String(), "STARVED") {
		t.Fatalf("three empty ticks never said STARVED:\n%s", out.String())
	}
	escalate, err := os.ReadFile(filepath.Join(queue, "ESCALATE"))
	if err != nil {
		t.Fatalf("STARVED reached nobody: %v", err)
	}
	if !strings.Contains(string(escalate), "STARVED in-flight=0 pending=0") {
		t.Fatalf("the escalation does not carry the evidence: %q", escalate)
	}
	if n := strings.Count(string(escalate), "STARVED"); n != 1 {
		t.Fatalf("STARVED was escalated %d times in one ten-minute window, want 1: %q", n, escalate)
	}
}
