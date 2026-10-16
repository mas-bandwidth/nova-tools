package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemawire_conformance_test.go pins the schema wire's conformance gate: the
// Makefile declares `make schemawire-conformance`, .PHONY names it and help
// lists it, CI reaches it through `make check` (the union of the gates CI
// runs, the one entry the workflow has), and its recipe walks every unit in
// schemas/, runs the pinned schema compiler over each (the Go oracle and the
// wire fuzz harness, both by the generate line's pin), bounds the fuzz with a
// timeout, and prints the elapsed seconds (schemas/README.md).
func TestSchemawireConformanceTargetNamed(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	mk := parseMakefile(t, filepath.Join(root, "Makefile"))

	recipe, ok := mk.recipes["schemawire-conformance"]
	require.True(t, ok, "Makefile declares no schemawire-conformance target")
	assert.True(t, mk.phony["schemawire-conformance"], "Makefile .PHONY does not name schemawire-conformance")

	help := strings.Join(mk.recipeFor("help"), "\n")
	assert.Contains(t, help, "make schemawire-conformance", "the help target does not list make schemawire-conformance:\n%s", help)

	body := strings.Join(recipe, "\n")
	assert.Contains(t, body, "schemas", "the schemawire-conformance recipe does not walk schemas/: %q", body)
	assert.Contains(t, body, "cmd/schema@", "the recipe does not run the pinned schema compiler's Go oracle: %q", body)
	assert.Contains(t, body, "test/conformance/harness@", "the recipe does not run the pinned compiler's wire fuzz harness: %q", body)
	assert.Contains(t, body, "timeout", "the wire fuzz runs under no time bound: %q", body)
	assert.Contains(t, body, "date +%s", "the recipe prints no elapsed seconds: %q", body)

	// CI runs the gate through the Makefile alone: `check`, the union of the
	// gates CI runs, names it as a prerequisite, so the workflow carries no
	// line of its own.
	invoked := false
	for _, dep := range mk.deps["check"] {
		if dep == "schemawire-conformance" {
			invoked = true
		}
	}
	assert.True(t, invoked, "`make check`, the union of the gates CI runs, does not invoke schemawire-conformance; its prerequisites are %q", mk.deps["check"])
}
