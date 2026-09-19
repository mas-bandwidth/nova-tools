package docs

import (
	"os"
	"strings"
	"testing"
)

// mutate-lines-are-documented: the two lines `nova-review mutate` actually prints.
//
// The cold read of 2026-09-19 (#1708 finding 4): the range verdict gained
// `reverted=<n>` and the verb gained a whole second form, and neither reached a doc.
// docs/SPEC-REVIEW.md carries the grammar of every line the review layer prints, and
// docs/CLI.md carries the verbs and their flags; a printed line that appears in
// neither is a line no caller can gate on without reading the source.
func TestMutateLinesAreDocumented(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-REVIEW.md")
	if err != nil {
		t.Fatalf("docs/SPEC-REVIEW.md: %v", err)
	}
	for _, want := range []string{
		"MUTATE <head8> reverted=<n> red=<n> green=<n> <PASS|FAIL>",
		// The selected form, #1849: its own line, because it carries a field the
		// default one does not and a caller gating on `test=` needs to know when it
		// is there.
		"MUTATE <head8> reverted=<n> red=<n> green=<n> test=<name> <PASS|FAIL>",
		"MUTATE <head8> seed=<hex8> edits=<n> red=<n> green=<n> <PASS|FAIL>",
		// The typed abstain, #1850. It is not a refusal and not a verdict, and a
		// grammar that still spelled it as the old refusal would be false.
		"MUTATE <head8> ABSTAIN reason=no-change-to-revert",
	} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("docs/SPEC-REVIEW.md missing the line %q (#1708)", want)
		}
	}
	if strings.Contains(string(spec), "MUTATE <head8> red=<n> green=<n> <PASS|FAIL>") {
		t.Errorf("docs/SPEC-REVIEW.md still carries the range verdict without reverted=<n> (#1708)")
	}

	cli, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	for _, want := range []string{
		"nova-review mutate --repo <dir> --base <ref> --head <ref>",
		"nova-review mutate --repo <dir> --head <ref> --seed <patch file> --tests <package>[,<package>...]",
		"reverted=<n>",
		"--test <name>",
		"ABSTAIN reason=no-change-to-revert",
	} {
		if !strings.Contains(string(cli), want) {
			t.Errorf("docs/CLI.md missing %q (#1708)", want)
		}
	}
}
