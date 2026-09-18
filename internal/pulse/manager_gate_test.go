package pulse

// The manager's half of SPEC-PULSE rule 4, and the two doors the dogfood found shut:
// --once (edge 6) and --dry-run (edge 6 again -- `--hours 0` still receipted, merged and
// moved cards). Every gh, git and nova-bus here is a fixture on PATH that records its argv.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// managerOnce runs one cycle through --once rather than --hours, which is what a person
// retiring the script reaches for.
func (b bench) once(t *testing.T, policy string, dry bool) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	stamp := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	code := Manager(ManagerInput{
		Policy: policy, Queue: b.queue, Roots: b.roots, Bus: b.bus, As: "Rowan",
		Once: true, DryRun: dry, Max: 20, Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return stamp },
	})
	return out.String(), errs.String(), code
}

// TestManagerReleasesTheGateWhenItSeesTheMerge: the manager WROTE `AFTER: PR<n> merged` and
// nothing anywhere took it off, so a gated card sat in pending until a person noticed
// (SPEC-PULSE rule 4, replay 43). One cycle against a forge that says OPEN leaves the line
// on; one against a forge that says MERGED takes it off and says so.
func TestManagerReleasesTheGateWhenItSeesTheMerge(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "floor=0\nwait-timeout=1s\n")
	b.write(t, "REPO", "mas-bandwidth/nova-tools\n")
	card := b.write(t, filepath.Join("pending", "card-001.md"), "RESULT: CARD-1 the gated one\nAFTER: PR7 merged\nSOURCE: repo mas-bandwidth/nova-tools#1\n")

	b.fake(t, "gh", fakeSpec{Rules: []fakeRule{{Arg: 2, Equals: "view", Stdout: `{"state":"OPEN"}`}}})
	out, _, code := b.once(t, p, false)
	if code != 0 {
		t.Fatalf("manager exit = %d, want 0", code)
	}
	if !strings.Contains(out, "released=0 gated=1") {
		t.Fatalf("the MANAGER line does not count the card it is holding: %q", out)
	}
	if body, _ := os.ReadFile(card); !strings.Contains(string(body), "AFTER: PR7 merged") {
		t.Fatalf("the gate came off a card whose PR is still open: %q", body)
	}

	// The one thing that changes: the forge.
	b.fake(t, "gh", fakeSpec{Rules: []fakeRule{{Arg: 2, Equals: "view", Stdout: `{"state":"MERGED"}`}}})
	out, _, code = b.once(t, p, false)
	if code != 0 {
		t.Fatalf("manager exit = %d, want 0", code)
	}
	if !strings.Contains(out, "released=1 gated=0") {
		t.Fatalf("the MANAGER line does not count the release: %q", out)
	}
	if !strings.Contains(out, "MANAGER RELEASED card=card-001.md after=PR7 state=MERGED") {
		t.Fatalf("the release is not named on its own line: %q", out)
	}
	body, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "AFTER:") {
		t.Fatalf("the spent gate is still on the card: %q", body)
	}
	if !strings.Contains(string(body), "RESULT: CARD-1 the gated one") {
		t.Fatalf("taking the gate off took the card with it: %q", body)
	}
}

// TestManagerOnceRunsExactlyOneCycle: --once is the one-cycle door spelled as what it is.
func TestManagerOnceRunsExactlyOneCycle(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "floor=0\nwait-timeout=1s\n")
	out, _, code := b.once(t, p, false)
	if code != 0 {
		t.Fatalf("manager exit = %d, want 0", code)
	}
	if !strings.Contains(out, "SHIFT END cycles=1 ") {
		t.Fatalf("--once ran more or fewer than one cycle: %q", out)
	}
}

// TestManagerDryRunChangesNothing: `--hours 0` was the one-cycle door and it still
// receipted on the bus, handed PRs to the merge lane and moved cards (the manager dogfood,
// edge 6). --dry-run reads and changes nothing -- no bus advance, no card moved, no card
// cut, no gate taken off -- and the line says so.
func TestManagerDryRunChangesNothing(t *testing.T) {
	b := setupManager(t)
	p := b.policy(t, "floor=0\nwait-timeout=1s\n")
	b.write(t, "REPO", "mas-bandwidth/nova-tools\n")
	card := b.write(t, filepath.Join("pending", "card-001.md"), "RESULT: CARD-1\nAFTER: PR7 merged\n")
	b.fake(t, "gh", fakeSpec{Rules: []fakeRule{{Arg: 2, Equals: "view", Stdout: `{"state":"MERGED"}`}}})

	out, _, code := b.once(t, p, true)
	if code != 0 {
		t.Fatalf("manager exit = %d, want 0", code)
	}
	if !strings.Contains(out, "dry-run=yes") {
		t.Fatalf("the MANAGER line does not say it changed nothing: %q", out)
	}
	if !strings.Contains(out, "released=1") {
		t.Fatalf("a dry run must still COUNT what it would have done: %q", out)
	}
	if body, _ := os.ReadFile(card); !strings.Contains(string(body), "AFTER: PR7 merged") {
		t.Fatalf("a dry run took the gate off the card: %q", body)
	}
	if argv := b.argv(t); strings.Contains(argv, "nova-bus wait") || strings.Contains(argv, "receipt") {
		t.Fatalf("a dry run advanced the bus cursor: %q", argv)
	}
	if _, err := os.Stat(filepath.Join(b.queue, "MANAGER.log")); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote the ledger: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.queue, QueueLockName)); !os.IsNotExist(err) {
		t.Fatalf("a dry run took the queue's lock")
	}
}
