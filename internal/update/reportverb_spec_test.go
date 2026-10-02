package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The report contract must remain present and free of merge conflict markers.
func TestSpecUpdateReportVerbSectionIsIntact(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-UPDATE.md"))
	require.NoError(t, err)
	doc := string(raw)
	for _, marker := range []string{"<<<<<<<", "|||||||", ">>>>>>>", "\n=======\n"} {
		assert.NotContainsf(t, doc, marker, "SPEC-UPDATE.md carries an unresolved merge marker %q", marker)
	}
	for _, phrase := range []string{
		"nova-version report …",
		"nova-version send …",
		"prints the inventory and composes nothing (rule 26)",
		"the ready-to-send draft is `nova-version report --draft …`, the flag typed.",
	} {
		assert.Containsf(t, doc, phrase, "SPEC-UPDATE.md does not name the report-verb contract keyed by %q", phrase)
	}
}
