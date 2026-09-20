package docs

import (
	"os"
	"strings"
	"testing"
)

// guard-lines-are-documented: the lines `nova-review guard` actually prints (#2042).
func TestGuardLinesAreDocumented(t *testing.T) {
	t.Parallel()

	spec, err := os.ReadFile("../../docs/SPEC-REVIEW.md")
	if err != nil {
		t.Fatalf("docs/SPEC-REVIEW.md: %v", err)
	}
	for _, want := range []string{
		"GUARD <head8> platform=<goos>/<goarch> reverted=<n> red=<n> green=<n> status=<GUARDED|UNGUARDED|COMPILER-HELD>",
		"GUARD <head8> platform=<goos>/<goarch> status=NOT-APPLICABLE reason=build-tags",
		"status=ABSTAIN",
		"GUARD TAIL which=<baseline|control> pkg=<path> exit=<n> last=<text>",
	} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("docs/SPEC-REVIEW.md missing the line %q (#2042)", want)
		}
	}

	cli, err := os.ReadFile("../../docs/CLI.md")
	if err != nil {
		t.Fatalf("docs/CLI.md: %v", err)
	}
	for _, want := range []string{
		"nova-review guard --repo <dir> --head <ref>",
		"NOT-APPLICABLE",
		"COMPILER-HELD",
	} {
		if !strings.Contains(string(cli), want) {
			t.Errorf("docs/CLI.md missing %q (#2042)", want)
		}
	}
}
