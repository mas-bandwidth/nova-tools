package ci

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheVersionGrammarIsSpelledOutOnceInTheSpec: the grammar a reader enforces
// and the grammar the spec states are one sentence, and the spec is where a
// friend adding a binary looks first.
func TestTheVersionGrammarIsSpelledOutOnceInTheSpec(t *testing.T) {
	t.Parallel()

	spec := readFile(t, filepath.Join(repoRoot(t), "docs", "SPEC.md"))
	for _, want := range []string{
		"<tool> <build identity> <goos>/<goarch> <go version>",
		"key=value",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC.md no longer states %q; the grammar every binary obeys is stated there once", want)
		}
	}
	for _, gone := range []string{
		"answers with its `SANDBOX VERSION` line",
	} {
		if strings.Contains(spec, gone) {
			t.Errorf("docs/SPEC.md still carves out a second version-line shape (%q); #1297 was that carve-out reaching every reader", gone)
		}
	}
}
