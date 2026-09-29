package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecNamesSparseCheckoutOfPATHSPackages(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM.md"))
	if err != nil {
		t.Fatalf("SPEC-SWARM.md is missing: %s", err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")
	for _, phrase := range []string{
		"## Sparse checkout of PATHS packages (#2498 S10)",
		"minimal tree",
		"those packages and their in-module dependencies only",
		"A package the card did not name is not materialized",
		"The named package's tests still run",
		"TestSparseCheckoutDoesNotMaterializeAnUnrelatedPackage",
		"TestPrepareStagesASparseJobClone",
		"valid empty set",
		"An import that cannot be resolved is not empty",
		"TestSparseCheckoutRefusesAMissingInModuleImport",
		"TestSparseCheckoutEmptyInModuleSetStillChecksOutPATHS",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("SPEC-SWARM.md does not name the sparse-checkout rule keyed by %q", phrase)
		}
	}
}

// TestCardTestPackageReadsTheOneGrammar is nova-tools#4401's fix round, item 4:
// the sparse checkout reads TEST through cardhdr.ParseTest, so `none <why>` is no
// package (not a cone named "none"), a tagged TEST's package is the one after the
// tags (not "-tags"), and a line ParseTest refuses names no package.
func TestCardTestPackageReadsTheOneGrammar(t *testing.T) {
	t.Parallel()
	for card, want := range map[string]string{
		"RESULT: x\nTEST: ./internal/pulse/ TestThing\n":             "./internal/pulse",
		"RESULT: x\nTEST: internal/swarm TestA\n":                    "internal/swarm",
		"RESULT: x\nTEST: -tags functional ./internal/swarm TestA\n": "./internal/swarm",
		"RESULT: x\nTEST: none the change is one docs page\n":        "",
		"RESULT: x\nTEST: none\n":                                    "",
		"RESULT: x\nTEST: rm -rf /\n":                                "",
		"RESULT: x\nPATHS: internal/x/a.go\n":                        "",
	} {
		if got := cardTestPackage(card); got != want {
			t.Errorf("cardTestPackage(%q) = %q, want %q", card, got, want)
		}
	}
}
