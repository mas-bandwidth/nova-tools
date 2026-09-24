package swarm

import (
	"os"
	"strings"
	"testing"
)

// TestIssue2579 verifies that a card ended for idleness (not wall refusal)
// reports CARD IDLE, not WALL REFUSED. The bug was that WriteBlockedResult
// always wrote WALL REFUSED regardless of whether a refusal occurred.
//
// Measured 2026-09-22: a card that stopped making progress cost its whole
// deadline before anybody looked. The idle watch was added to end such cards
// early, but the report it wrote said WALL REFUSED even when no wall refused
// anything -- just the card became silent.
//
// From nova-tools#2579 issue: "the report says WALL REFUSED for a card no
// wall refused. This is exactly the misattribution `internal/swarm/nativeidle.go:96-103`
// was written to prevent: It is deliberately not a WALL line."
func TestIssue2579(t *testing.T) {
	job := t.TempDir()

	// Case 1: Card that became idle with NO refusal
	// Should write CARD IDLE line, not WALL REFUSED
	// kind="" indicates no refusal, so WriteBlockedResult will use CardIdleLine
	path, wrote, err := WriteBlockedResult(
		job, "task-idle", "", "", "-",
		"the card's log and its process tree were both still for 300s; the run ended it rather than holding the slot to its deadline",
	)
	if err != nil || !wrote {
		t.Fatalf("WriteBlockedResult for idle card failed: %v %v", wrote, err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	// The report should say CARD IDLE, not WALL REFUSED
	if !strings.Contains(body, "CARD IDLE task=task-idle") {
		t.Fatalf("idle card report should contain 'CARD IDLE task=task-idle':\n%s", body)
	}

	// The report should NOT say WALL REFUSED when there was no refusal
	if strings.Contains(body, "WALL REFUSED") {
		t.Fatalf("idle card (not wall refused) should not contain 'WALL REFUSED' line:\n%s", body)
	}

	// Case 2: Clean up and test a card that HIT a wall refusal
	os.RemoveAll(job)
	job = t.TempDir()

	// A card that hit a wall refusal should still say WALL REFUSED
	// kind="write" indicates a refusal, so WriteBlockedResult will use WallRefusedLine
	path2, wrote2, err := WriteBlockedResult(
		job, "task-wall", "write", "/etc/passwd", "2",
		"the wall refused write /etc/passwd and the card wrote nothing for 300s after it",
	)
	if err != nil || !wrote2 {
		t.Fatalf("WriteBlockedResult for wall-refused card failed: %v %v", wrote2, err)
	}

	raw2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	body2 := string(raw2)

	// A card blocked by wall refusal SHOULD have WALL REFUSED
	if !strings.Contains(body2, "WALL REFUSED write /etc/passwd") {
		t.Fatalf("wall-refused card should contain 'WALL REFUSED write /etc/passwd':\n%s", body2)
	}
}
