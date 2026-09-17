package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #121: the report verb's contract is a paragraph in SPEC-UPDATE.md, and the
// spec the issue names must read as prose, not as a half-resolved merge. The
// stray conflict marker left in that paragraph is red the same way the verbs
// block is: the sentences a reader needs are still there, but the document they
// live in is broken, so a reader cannot trust the section the issue points at.
func TestSpecUpdateReportVerbSectionIsIntact(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-UPDATE.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, marker := range []string{"<<<<<<<", "|||||||", ">>>>>>>", "\n=======\n"} {
		if strings.Contains(doc, marker) {
			t.Errorf("SPEC-UPDATE.md carries an unresolved merge marker %q", marker)
		}
	}
	for _, phrase := range []string{
		"nova-version report …",
		"nova-version send …",
		"prints the inventory and composes nothing (rule 26)",
		"Emma's ready-to-send draft (#121) is `nova-version report --draft …`, the flag typed.",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-UPDATE.md does not name the report-verb contract keyed by %q", phrase)
		}
	}
}
