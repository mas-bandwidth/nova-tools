package hygiene

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 2026-10-18: ValidatePaths refused a PATHS: line of more than eight globs, and the card
// lint's own corrections need more: paths-cover-named, paths-cover-testdata and tla-records
// print the PATHS line that would cover what a card names (a ledger, its class test, a TLA+
// model with its cfg witnesses and records), and the line they print can pass eight, so
// following the lint made the next lint refuse the card. The cap was a size bound on a
// bound: what bounds a card is each glob (a literal segment, repository-relative, no `..`),
// and those rules stay. A long list of good globs is valid; one bad glob in a long list is
// still refused, and named.
func TestValidatePathsHasNoCapOnTheNumberOfGlobs(t *testing.T) {
	t.Parallel()
	var long []string
	for i := 1; i <= 24; i++ {
		long = append(long, fmt.Sprintf("tla/Model%d.tla", i))
	}
	require.NoError(t, ValidatePaths(long), "twenty-four literal globs bound a card as well as eight")

	for _, bad := range []struct{ glob, want string }{
		{".." + "/outside.go", "climbs"},
		{"/etc/passwd", "absolute"},
		{"**/*", "matches every file"},
	} {
		err := ValidatePaths(append(append([]string(nil), long...), bad.glob))
		require.Error(t, err, "%q in a long list", bad.glob)
		assert.Contains(t, err.Error(), bad.want)
	}
}
