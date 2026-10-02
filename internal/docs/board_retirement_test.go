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
// Two things are held, and no more: the top-level README no longer lists it,
// and the tool is gone from its old paths. (The deprecated/ folder it moved to
// was removed on 2026-10-01; its README, which names nova-board, lives in the
// nova-work-old repository.)
func TestNovaBoardIsDeprecated(t *testing.T) {
	t.Parallel()

	top, err := os.ReadFile("../../README.md")
	require.NoError(t, err, "README.md: %v", err)
	assert.NotContains(t, string(top), "nova-board", "README.md still names nova-board; the top-level README lists live tools only")

	for _, path := range []string{
		"../../cmd/nova-board",
		"../../internal/board",
	} {
		_, err = os.Stat(path)
		assert.Error(t, err, "%s exists; nova-board is deprecated and was removed (Glenn, 2026-09-27)", path)
	}
}
