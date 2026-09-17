package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC-PULSE: "Those lines are the string `nova-pulse help` must print, byte for byte —
// the parity is a demand on internal/pulse/cli.go's pulseVerbs, which carries the same
// claim in a comment, and a replay walks it (replay 36)." This test reads the fenced
// block and compares, so a flag added here and not there is red, and so is one added
// there and not here.
func TestHelpIsTheSpecsVerbsBlock(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(doc), "\n## The verbs\n")
	if !ok {
		t.Fatal("the spec has no verbs section")
	}
	_, after, ok = strings.Cut(after, "```\n")
	if !ok {
		t.Fatal("the verbs section has no block")
	}
	block, _, ok := strings.Cut(after, "```")
	if !ok {
		t.Fatal("the verbs block does not close")
	}
	if want, got := strings.TrimRight(block, "\n"), pulseVerbs; want != got {
		t.Fatalf("help has drifted from the spec's verbs block:\nspec:\n%s\nhelp:\n%s", want, got)
	}
	var printed bytes.Buffer
	help(&printed)
	if !strings.HasPrefix(printed.String(), pulseVerbs+"\n") {
		t.Fatalf("help does not open with the verbs block:\n%s", printed.String())
	}
}

// SPEC-PULSE publishes the existing meaning of "pit stop" under a stable
// heading so the glossary can cite it (#576): what activity the term names
// and when that activity is finished.
func TestPitStopIsDefinedUnderAStableHeading(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(doc), "\n## The pit stop\n")
	if !ok {
		t.Fatal("docs/SPEC-PULSE.md has no `## The pit stop` heading: the term needs a stable heading the glossary can cite (#576)")
	}
	body, _, ok := strings.Cut(after, "\n## ")
	if !ok {
		t.Fatal("`## The pit stop` is the last heading in the file")
	}
	flat := strings.Join(strings.Fields(body), " ")
	if !strings.Contains(flat, "bugs only") {
		t.Fatalf("`## The pit stop` does not name the activity (bugs only):\n%s", body)
	}
	if !strings.Contains(flat, "trust batch") {
		t.Fatalf("`## The pit stop` does not name when it is finished (the trust batch):\n%s", body)
	}
}
