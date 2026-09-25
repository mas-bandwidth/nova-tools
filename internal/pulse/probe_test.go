package pulse

// Nova-tools #585, the learned admission checklist: `cut --probe` refuses a card whose
// failure class is already in the abstain history before it is launched. The red test first
// for the smallest slice: a class present in the history refuses its card, a class absent is
// skipped, and a card clean against every learned class passes.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func probeHistory(t *testing.T, classes ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abstain-history.tsv")
	if err := os.WriteFile(path, []byte(strings.Join(classes, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProbeReadsTheAbstainHistory(t *testing.T) {
	// The class is not in the history, so the check is not learned and the card passes:
	// the card leaves its job (`../`) but no `admission` class was ever recorded.
	card := "STEP 1. cd ../scratch and write\nRESULT.md red line\n"
	var out, errb bytes.Buffer
	if code := Probe(ProbeInput{Label: "1", Card: card, History: probeHistory(t, "no-result"), Stdout: &out, Stderr: &errb}); code != 0 {
		t.Fatalf("unlearned class refused the card: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "PROBE OK card=1") {
		t.Fatalf("PROBE OK not printed: %q", out.String())
	}

	// The same card with the class learned is refused, and the refusal names the class.
	out.Reset()
	errb.Reset()
	if code := Probe(ProbeInput{Label: "1", Card: card, History: probeHistory(t, "admission"), Stdout: &out, Stderr: &errb}); code != 1 {
		t.Fatalf("learned class admitted the card: code=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "PROBE REFUSED card=1 class=admission") {
		t.Fatalf("refusal does not name the class: %q", errb.String())
	}
}

func TestProbeNamesTheFiveChecks(t *testing.T) {
	card := "STEP 1. mkdir -p scratch && git clone -q https://example.invalid/o/r.git .\n" +
		"STEP 2. red line then green line\nRESULT.md\n"
	for _, class := range []string{"admission", "wall refusal", "line1-mismatch", "idle kill", "no-result"} {
		var out, errb bytes.Buffer
		if code := Probe(ProbeInput{Label: "2", Card: card, History: probeHistory(t, class), Budget: 4096, Stdout: &out, Stderr: &errb}); code != 0 {
			t.Fatalf("clean card refused for class %q: %s", class, errb.String())
		}
	}
}

func TestProbeRefusalNamesTheClass(t *testing.T) {
	var out, errb bytes.Buffer
	code := Probe(ProbeInput{Label: "7", Card: "no result written\n", History: probeHistory(t, "no-result"), Stdout: &out, Stderr: &errb})
	if code != 1 {
		t.Fatalf("code=%d, want 1; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "class=no-result") || !strings.Contains(errb.String(), "(") {
		t.Fatalf("refusal wants `class=` and a parenthesised remedy: %q", errb.String())
	}
}
