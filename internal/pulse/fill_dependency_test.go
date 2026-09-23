package pulse

// THE FILL TAKES WHAT IS READY (#3251). A card in a bench's ready queue was dealt there by
// the dealer after it decided the card is ready; the fill launches it, whatever its
// DEPENDS-ON, LEG or LANE says. The measured defect: 15 real cards HELD for 50 minutes on
// three benches, 321 `FILL HELD ... depends-on=- reason=dependency - not merged into dev`
// lines, because the fill re-decided readiness from its own partial view.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// routed is the dealer's mark every dealt card carries.
const routed = "ROUTE: test-route\nMODEL: test-model\n"

// TestFillTakesEveryReadyCard: a queue holding a card with DEPENDS-ON: -, one whose parent
// never landed, one whose leg no bench here was ever said to carry, and two on one lane
// launches every one of them, in order, and holds none.
func TestFillTakesEveryReadyCard(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "KIND: fix\nDEPENDS-ON: -\n"+routed)
	writeCard(t, ready, "card-002.md", "KIND: fix\nDEPENDS-ON: card-999-never-landed\n"+routed)
	writeCard(t, ready, "card-003.md", "KIND: fix\nLEG: squirrel\n"+routed)
	writeCard(t, ready, "card-004.md", "LANE: pulse\n"+routed)
	writeCard(t, ready, "card-005.md", "LANE: pulse\n"+routed)
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines:     machinesFile(t, dir, []string{"bench-a"}, nil),
		Benches:      []string{"bench-a"},
		Once:         true,
		Stdout:       &out,
		Stderr:       &errb,
		Capacity:     laneCap{"bench-a": 10},
		Launcher:     l,
		RequireRoute: true,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 5 {
		t.Fatalf("launched %d cards, want all 5 ready cards: %q\nstdout=%q", len(l.calls), l.calls, out.String())
	}
	for i, call := range l.calls {
		if want := "card-00" + string(rune('1'+i)) + ".md"; !strings.HasSuffix(call, want) {
			t.Fatalf("launch %d = %q, want %s (ready order)", i, call, want)
		}
	}
	if strings.Contains(out.String(), "HELD") || strings.Contains(errb.String(), "REFUSED") {
		t.Fatalf("the fill held or refused a ready card: out=%q err=%q", out.String(), errb.String())
	}
	if got := len(readyCards(ready)); got != 0 {
		t.Fatalf("ready still holds %d cards, want 0", got)
	}
	// The marker still RECORDS the lane and the dependency, as data for the dealer.
	m := readLaunchedMarker(launched, "card-004.md")
	if m["lane"] != "pulse" {
		t.Fatalf("launched marker lane = %q, want pulse", m["lane"])
	}
	if m := readLaunchedMarker(launched, "card-002.md"); m["depends-on"] != "card-999-never-landed" {
		t.Fatalf("launched marker depends-on = %q", m["depends-on"])
	}
}

// TestFillRefusesACardWithNoRoute: the one check the fill makes on a card is an execution
// guard -- the dealer's ROUTE: and MODEL: must be on it. A card without them is refused
// once, stays in ready, takes no slot, and the fill never picks a route for it.
func TestFillRefusesACardWithNoRoute(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "KIND: fix\nROUTE: test-route\n")
	writeCard(t, ready, "card-002.md", "KIND: fix\n"+routed)
	tick := func() (string, *laneLauncher) {
		l := &laneLauncher{}
		var out, errb bytes.Buffer
		if code := Fill(FillInput{
			Ready: ready, Launched: launched,
			Machines:     machinesFile(t, dir, []string{"bench-a"}, nil),
			Benches:      []string{"bench-a"},
			Once:         true,
			Stdout:       &out,
			Stderr:       &errb,
			Capacity:     laneCap{"bench-a": 1},
			Launcher:     l,
			RequireRoute: true,
		}); code != 0 {
			t.Fatalf("fill exit = %d; stderr=%q", code, errb.String())
		}
		return errb.String(), l
	}
	errs, l := tick()
	if len(l.calls) != 1 || !strings.HasSuffix(l.calls[0], "card-002.md") {
		t.Fatalf("launches = %q, want only card-002.md (the refused card takes no slot)", l.calls)
	}
	if !strings.Contains(errs, "FILL REFUSED card=card-001.md missing=MODEL") {
		t.Fatalf("no refusal naming the missing MODEL: %q", errs)
	}
	if _, err := os.Stat(filepath.Join(ready, "card-001.md")); err != nil {
		t.Fatalf("the refused card left ready: %v", err)
	}
	if errs, _ = tick(); strings.Contains(errs, "FILL REFUSED card=card-001.md") {
		t.Fatalf("the refusal reprinted on the next tick: %q", errs)
	}
}

// TestFillDependencyMutationProtectionWithTeeth verifies that bypassing dependency checks fails tests loudly
func TestFillDependencyMutationProtectionWithTeeth(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	_ = os.MkdirAll(ready, 0o755)

	// card-unmerged-dep depends on unmerged-foundation
	cardPath := writeCard(t, ready, "card-dependent.md", "RESULT card-dependent\ndepends-on: unmerged-foundation\n")

	mockChecker := MapDependencyChecker{
		"unmerged-foundation": false,
	}

	unmetDep, reason, ok := CheckCardDependencies(cardPath, mockChecker)
	if ok {
		t.Fatalf("MUTATION DETECTED: CheckCardDependencies admitted unmerged dependency!")
	}
	if unmetDep != "unmerged-foundation" {
		t.Errorf("unmetDep = %q, want unmerged-foundation", unmetDep)
	}
	if !strings.Contains(reason, "not merged") {
		t.Errorf("reason = %q, want mention of not merged", reason)
	}
}
