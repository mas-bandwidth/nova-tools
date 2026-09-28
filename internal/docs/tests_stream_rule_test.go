package docs

import (
	"os"
	"strings"
	"testing"
)

// TestTheTranscriptDocumentSaysWhichStreamALineIsOn holds docs/TESTS.md's
// header to the one rule a reader needs before comparing a single line: which
// stream a transcript line comes out of.
//
// The tools write both streams and the document shows both, and before #1549
// the page named the stream only per-verb, never once as a rule. A reader who
// merged the two -- which is what `2>&1` does -- read a progress line as the
// first line of a protocol block and filed a defect that was the page's fault
// and not the tool's: six of the twenty-one readings in the 2026-09-19
// two-bench dogfood run were only that. So the rule lives in the header, above
// any single section, where a reader meets it before the first block rather
// than after the six.
func TestTheTranscriptDocumentSaysWhichStreamALineIsOn(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/TESTS.md")
	if err != nil {
		t.Fatalf("docs/TESTS.md: %v", err)
	}
	// The header is everything above the first `## nova-<tool>` section: the
	// title, the preamble and the reading rules, including the `## Reading a
	// block` section that states this one. A statement that lives only inside a
	// tool's own section is the per-verb naming #1549 complains about, not a
	// rule a reader meets before the first block.
	header, _, found := strings.Cut(string(body), "\n## nova-")
	if !found {
		t.Fatal("docs/TESTS.md has no `## nova-<tool>` section; this test is reading the wrong document")
	}

	for _, want := range []struct{ phrase, why string }{
		{"standard output", "an unmarked line is the one the tool wrote to standard output"},
		{"standard error", "a marked line is the one the tool wrote to standard error"},
		{"`! `", "the marker that puts a line on standard error is `! `"},
		{"compared apart", "the two streams are compared apart, never merged"},
	} {
		if !strings.Contains(header, want.phrase) {
			t.Errorf("docs/TESTS.md's header never says %s: it carries no %q. A reader told only to compare by SHAPE cannot tell which of the tool's two streams a line came from, and one who merges them with `2>&1` reads a correct progress line as drift (nova-tools#1549). State the convention once, in the header.", want.why, want.phrase)
		}
	}
}
