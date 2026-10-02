package typedrec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// NamesADefect is the finding rule of a broken read (docs/SPEC-CARD-CONTRACT.md
// section 3): a file, a line or the rule the work breaks, on some line of it.
func TestABrokenFindingNamesAFileALineOrARule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		finding string
		names   bool
	}{
		{"Request changes.", false},
		{"The diff matches the card: it removes the ten listed history references. No code, string literal, or unrelated file changes were found.", false},
		{"review blocked by unavailable Go toolchain; no source verdict supported", false},
		{"e.g. the gate did not run", false},
		{"i.e. nothing changed", false},
		{"a.go drops the error; return it to the caller", true},
		{"x.c is wrong; remove the unsafe cast", true},
		{"the defect is in a.go", true},
		{"Request changes.\ninternal/ci/testdata/deleted-tests.txt:12 still names the old file", true},
		{"cmd/nova-sprint/land.go:540 joins two causes", true},
		{"main.go names the old flag", true},
		{"f:1 wrong word", true},
		{"at 12:30 the gate ran", false},
		{"the comment on line 40 is history", true},
		{"STEP 2 is not done: git grep prints three hits", true},
		{"it breaks the card's RULES: touch only the files named", true},
		{"rule: keep the diff minimal", true},
	} {
		assert.Equal(t, tc.names, NamesADefect(tc.finding), "%q", tc.finding)
	}
}
