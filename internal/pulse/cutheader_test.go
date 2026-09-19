package pulse

// T06a (#1651), SPEC-TOOLWORK §5 rule 1: the five typed header lines sit under the
// contract line and inside its hash, are written by `cut` from the pool row and never by
// a model, and are what `accept` reads. A header `cut` writes that `ReadCardHeader` does
// not read back is a card the gate cannot judge, so the test is the ROUND TRIP: cut a
// card, read its header, compare.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cutOneKindCard cuts one card and returns the path it was written to.
func cutOneKindCard(t *testing.T, in CutKindInput) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	in.Stdout, in.Stderr = &out, &errs
	code := CutKind(in)
	return out.String(), errs.String(), code
}

func baseCutKindInput(t *testing.T) CutKindInput {
	t.Helper()
	dir := t.TempDir()
	return CutKindInput{
		Kind: "fix", Repo: "mas-bandwidth/nova-tools", Issue: 1650,
		Title: "the harvest runs the gate",
		Out:   filepath.Join(dir, "pending"), Queue: filepath.Join(dir, "queue"),
	}
}

// onlyCard is the one card file under a directory.
func onlyCard(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no card directory %s: %v", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatalf("no card written under %s", dir)
	return ""
}

func TestCutWritesTheFiveHeaderLinesReadCardHeaderReads(t *testing.T) {
	in := baseCutKindInput(t)
	in.CardKind = "fix-red"
	in.Paths = "internal/pulse/**, cmd/nova-pulse/**"
	in.Test = "internal/pulse TestHarvestRunsAcceptBeforeAnyPush"
	in.Legs = "go,windows"
	_, errs, code := cutOneKindCard(t, in)
	if code != 0 {
		t.Fatalf("cut refused a complete gated card (exit %d): %s", code, errs)
	}
	card := onlyCard(t, in.Out)
	h, err := ReadCardHeader(card)
	if err != nil {
		t.Fatalf("the header cut wrote does not read back: %v", err)
	}
	if h.Label == "" {
		t.Errorf("the contract line's label did not read back; accept keys every line it prints on it")
	}
	if h.Kind != "fix-red" {
		t.Errorf("KIND read back as %q, want fix-red", h.Kind)
	}
	if strings.Join(h.Paths, ",") != "internal/pulse/**,cmd/nova-pulse/**" {
		t.Errorf("PATHS read back as %v", h.Paths)
	}
	if h.TestPkg != "internal/pulse" || h.TestName != "TestHarvestRunsAcceptBeforeAnyPush" {
		t.Errorf("TEST read back as %q %q", h.TestPkg, h.TestName)
	}
	if strings.Join(h.Legs, ",") != "go,windows" {
		t.Errorf("LEGS read back as %v", h.Legs)
	}
	if h.Source == "" {
		t.Errorf("SOURCE read back empty; it is the card writer's receipt for a reader")
	}
	if miss := h.Missing(); len(miss) != 0 {
		t.Errorf("a card cut whole is missing %v", miss)
	}
	raw, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	// The header sits directly under line 1, before any prose: ReadCardHeader stops at
	// the first line that is not `KEY: value`, so a line of prose above them hides them.
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for i, key := range []string{"KIND:", "PATHS:", "TEST:", "LEGS:", "SOURCE:"} {
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], key) {
			t.Fatalf("line %d is %q, want the %s line (the five are under the contract line, in the spec's order)", i+2, safeLine(lines, i+1), key)
		}
	}
}

func safeLine(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<past the end of the card>"
}

func TestCutRefusesAGatedKindWithoutPathsOrTest(t *testing.T) {
	for _, tc := range []struct {
		name, paths, test, want string
	}{
		{"no paths", "", "internal/pulse TestX", "no PATHS"},
		{"no test", "internal/pulse/**", "", "no TEST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baseCutKindInput(t)
			in.CardKind = "fix-red"
			in.Paths, in.Test = tc.paths, tc.test
			_, errs, code := cutOneKindCard(t, in)
			if code == 0 {
				t.Fatalf("cut wrote a gated card with %s; the gate would then judge a card whose header it cannot use", tc.name)
			}
			if !strings.Contains(errs, tc.want) || !strings.Contains(errs, "kind=fix-red") {
				t.Errorf("the refusal does not name the missing line: %q", errs)
			}
		})
	}
}

func TestCutRefusesACardKindTheTableDoesNotHold(t *testing.T) {
	in := baseCutKindInput(t)
	in.CardKind = "fix-everything"
	in.Paths, in.Test = "internal/pulse/**", "internal/pulse TestX"
	_, errs, code := cutOneKindCard(t, in)
	if code == 0 {
		t.Fatal("cut wrote a card of a kind the table does not hold; there is no default kind")
	}
	if !strings.Contains(errs, "fix-everything") {
		t.Errorf("the refusal does not name the kind: %q", errs)
	}
}

func TestCutWritesNoKindLineWhenNoneIsAsked(t *testing.T) {
	in := baseCutKindInput(t)
	_, errs, code := cutOneKindCard(t, in)
	if code != 0 {
		t.Fatalf("cut refused an ungated card (exit %d): %s", code, errs)
	}
	h, err := ReadCardHeader(onlyCard(t, in.Out))
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	if h.Kind != "" {
		t.Errorf("a card nobody asked to gate carries KIND: %q; the harvest row would claim a gate it never ran", h.Kind)
	}
	if h.Label == "" {
		t.Errorf("the contract line's label did not read back even for an ungated card")
	}
}
