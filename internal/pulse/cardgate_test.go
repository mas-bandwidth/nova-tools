package pulse

// The card gate (cardgate.go) and the one guard every road into a bench (placement.go),
// tested against the unit seam rather than a whole Fill tick: a card carrying
// `AFTER: PR7 merged` is held while the forge reports PR 7 open and released the first tick
// it reports merged, with the gate line taken off as it goes. The forge is a fake PRSource,
// so no test here reaches GitHub.

import (
	"fmt"
	"strings"
	"testing"
)

// gateForge is a PRSource a test teaches: PR number to state. A PR it does not know is an
// error, which is the forge answering "unread" and a gate that stays shut.
type gateForge struct {
	state map[int]string
	asked int
}

func (f *gateForge) View(repo string, pr int) (PRView, error) {
	f.asked++
	state, ok := f.state[pr]
	if !ok {
		return PRView{}, fmt.Errorf("no pull request %d", pr)
	}
	return PRView{Number: pr, State: state}, nil
}

func TestCardGateOfReadsTheAfterLine(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"RESULT: CARD-1\nAFTER: PR7 merged\n", 7},
		{"RESULT: CARD-1\nAFTER: PR1348 merged\nSOURCE: x#1\n", 1348},
		{"RESULT: CARD-1\n", 0},
		{"RESULT: CARD-1\nAFTER: PR 7 merged\n", 0},
		{"RESULT: CARD-1\nAFTER: PR0 merged\n", 0},
	}
	for _, c := range cases {
		if got := cardGateOf(c.text); got != c.want {
			t.Fatalf("cardGateOf(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestReleaseCardGateStripsTheSpentLine(t *testing.T) {
	out := releaseCardGate("RESULT: CARD-1\nAFTER: PR7 merged\nLANE: pulse\n")
	if strings.Contains(out, "AFTER:") {
		t.Fatalf("the spent gate line is still on the card: %q", out)
	}
	if !strings.Contains(out, "RESULT: CARD-1") || !strings.Contains(out, "LANE: pulse") {
		t.Fatalf("a card that is not the gate line was dropped: %q", out)
	}
}

// TestGateStateAnswersTheForge: merged is open, everything else is the state word, and a
// missing forge or an unreadable PR is a shut gate.
func TestGateStateAnswersTheForge(t *testing.T) {
	forge := &gateForge{state: map[int]string{7: "OPEN", 8: "MERGED", 9: "CLOSED"}}
	if open, state := gateState(forge, "owner/name", 8); !open || state != "MERGED" {
		t.Fatalf("a MERGED PR answered open=%v state=%q, want open=MERGED", open, state)
	}
	if open, state := gateState(forge, "owner/name", 7); open || state != "OPEN" {
		t.Fatalf("an OPEN PR answered open=%v state=%q, want shut=OPEN", open, state)
	}
	if open, state := gateState(forge, "owner/name", 99); open || state != "unread" {
		t.Fatalf("an unknown PR answered open=%v state=%q, want shut=unread", open, state)
	}
	if open, state := gateState(nil, "owner/name", 7); open || state != "no-forge" {
		t.Fatalf("a nil forge answered open=%v state=%q, want shut=no-forge", open, state)
	}
}

// TestPlacementAdmitHoldsAGatedCard: the gate is SPEC-PULSE rule 4's, and it holds the card
// (with the PR and what the forge said) until the forge reports merged.
func TestPlacementAdmitHoldsAGatedCard(t *testing.T) {
	dir := t.TempDir()
	card := writeCard(t, dir, "card-001.md", "RESULT: CARD-1\nAFTER: PR7 merged\n")
	forge := &gateForge{state: map[int]string{7: "OPEN"}}

	p, err := newPlacement("", "", dir, "owner/name", forge)
	if err != nil {
		t.Fatal(err)
	}
	v := p.admit(card)
	if v.ok() {
		t.Fatalf("a card gated on an OPEN PR was admitted: %+v", v)
	}
	if v.Kind != "gated" || v.PR != 7 || v.State != "OPEN" {
		t.Fatalf("the held card does not name its gate: %+v", v)
	}

	// The one thing that changes: the forge. Each tick builds a fresh guard, so the forge
	// is asked again and now answers merged.
	forge.state[7] = "MERGED"
	p2, err := newPlacement("", "", dir, "owner/name", forge)
	if err != nil {
		t.Fatal(err)
	}
	v = p2.admit(card)
	if !v.ok() || !v.Opened {
		t.Fatalf("the merged PR did not release the gate: %+v", v)
	}
	if strings.Contains(v.Text, "AFTER:") {
		t.Fatalf("the opened gate left its line on the card: %q", v.Text)
	}
}

// TestPlacementAskTheForgeOncePerPR: nine cards behind one PR is one forge call, not nine.
func TestPlacementAskTheForgeOncePerPR(t *testing.T) {
	dir := t.TempDir()
	forge := &gateForge{state: map[int]string{1348: "OPEN"}}
	p, err := newPlacement("", "", dir, "owner/name", forge)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 9; i++ {
		card := writeCard(t, dir, fmt.Sprintf("card-00%d.md", i), "RESULT: CARD\nAFTER: PR1348 merged\n")
		p.admit(card)
	}
	if forge.asked != 1 {
		t.Fatalf("the forge was asked %d times about one PR, want 1", forge.asked)
	}
}

// TestPlacementAdmitHoldsALane: at most one live card per lane, and a lane the table does
// not name (with a strict table) is refused.
func TestPlacementAdmitHoldsALane(t *testing.T) {
	dir := t.TempDir()
	lanes := laneFile(t, dir, "pulse\tinternal/pulse/")
	machines := machinesFile(t, dir, []string{"bench-a"}, nil)
	p, err := newPlacement(machines, lanes, dir, "owner/name", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := writeCard(t, dir, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	second := writeCard(t, dir, "card-002.md", "RESULT: CARD-2\nLANE: pulse\n")

	if v := p.admit(first); !v.ok() {
		t.Fatalf("the first card of a lane was held: %+v", v)
	}
	p.take("pulse", "card-001.md")
	if v := p.admit(second); v.Kind != "lane-held" || v.Holder != "card-001.md" {
		t.Fatalf("the second card of a live lane was not held: %+v", v)
	}
	// An unknown lane, with a strict table, is a refusal.
	other := writeCard(t, dir, "card-003.md", "RESULT: CARD-3\nLANE: nosuch\n")
	if v := p.admit(other); v.Kind != "lane-unknown" {
		t.Fatalf("an unknown lane was not refused: %+v", v)
	}
}

// TestPlacementBenchHoldsAgainstTheRegistry: a bench whose roles lack `bench` takes no card.
func TestPlacementBenchHoldsAgainstTheRegistry(t *testing.T) {
	dir := t.TempDir()
	machines := machinesFile(t, dir, []string{"studio"}, []string{"space"})
	p, err := newPlacement(machines, "", dir, "owner/name", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.bench("space"); err == nil {
		t.Fatal("a CI runner host was admitted as a bench")
	}
	if err := p.bench("studio"); err != nil {
		t.Fatalf("a certified bench was refused: %v", err)
	}
}
