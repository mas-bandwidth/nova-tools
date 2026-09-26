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
