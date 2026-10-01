package docs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaBoardIsDeprecated pins where nova-board stands since Glenn's ruling
// of 2026-09-27: "Move nova-board into the deprecated folder pls." and "I think
// that nova-sprint directly replaces nova-board". It replaces the pin of
// nova-tools #596, which held the tool in place as the input adapter until
// nova-work's views were dogfooded; the ruling supersedes that order.
//
// Three things are held, and no more: deprecated/README.md exists and names
// the tool, the top-level README no longer lists it, and the tool is gone from
// its old paths. Nothing under deprecated/ is built or tested, so this test
// reads the folder's README and nothing else in it.
func TestNovaBoardIsDeprecated(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile("../../deprecated/README.md")
	require.NoError(t, err, "deprecated/README.md: %v", err)
	assert.Contains(t, string(readme), "nova-board", "deprecated/README.md does not name nova-board; the folder's README lists every tool moved into it")

	top, err := os.ReadFile("../../README.md")
	require.NoError(t, err, "README.md: %v", err)
	assert.NotContains(t, string(top), "nova-board", "README.md still names nova-board; the top-level README lists live tools only")

	for _, path := range []string{
		"../../cmd/nova-board",
		"../../internal/board",
	} {
		_, err = os.Stat(path)
		assert.Error(t, err, "%s exists; nova-board is deprecated and was moved under deprecated/ (Glenn, 2026-09-27)", path)
	}
}
