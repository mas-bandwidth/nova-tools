package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #121: the report verb's contract is a paragraph in SPEC-UPDATE.md, and the
// spec the issue names must read as prose, not as a half-resolved merge. The
// stray conflict marker left in that paragraph is red the same way the verbs
// block is: the sentences a reader needs are still there, but the document they
// live in is broken, so a reader cannot trust the section the issue points at.
func TestSpecUpdateReportVerbSectionIsIntact(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-UPDATE.md"))
	if err != nil {
		require.NoError(t, err, err)
	}
	doc := string(raw)
	for _, marker := range []string{"<<<<<<<", "|||||||", ">>>>>>>", "\n=======\n"} {
		if strings.Contains(doc, marker) {
			assert.NotContainsf(t, doc, marker, "SPEC-UPDATE.md carries an unresolved merge marker %q", marker)
		}
	}
	for _, phrase := range []string{
		"nova-version report …",
		"nova-version send …",
		"prints the inventory and composes nothing (rule 26)",
		"the ready-to-send draft is `nova-version report --draft …`, the flag typed.",
	} {
		if !strings.Contains(doc, phrase) {
			assert.Containsf(t, doc, phrase, "SPEC-UPDATE.md does not name the report-verb contract keyed by %q", phrase)
		}
	}
}
