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
