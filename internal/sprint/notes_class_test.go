package sprint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A class test: no code that builds a printed command, or anything else,
// picks a card out of a note's primaries by its position. Primaries is a set;
// a note names the cards a decision acts on in fields of their own (Card,
// Other, Suspects). Cutting the list to a bound (Primaries[:n]) is not a pick.
func TestNothingPicksACardByItsPlaceInANotesPrimaries(t *testing.T) {
	t.Parallel()
	pick := regexp.MustCompile(`Primaries\[[^:\]]`)
	for _, dir := range []string{".", "store", "driver", filepath.Join("..", "..", "cmd", "nova-sprint")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		require.NotEmpty(t, files, "no Go files in %s", dir)
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			for i, line := range strings.Split(string(b), "\n") {
				assert.False(t, pick.MatchString(line), "%s:%d picks a card by its place in a note's primaries; name it in a field of the note: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
