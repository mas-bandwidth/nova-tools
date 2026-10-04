package docs

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeatFileUsesActualNameNotSwarmPrefix keeps the seat-file naming rule from
// nova-tools #2000: a store file without the shared prefix is requested
// under its actual name. The documentation states the rule without host names.
func TestSeatFileUsesActualNameNotSwarmPrefix(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/TESTS.md")
	require.NoError(t, err, "docs/TESTS.md: %v", err)
	content := strings.Join(strings.Fields(string(body)), " ")

	for _, want := range []string{
		"`swarm-` prefix",
		"must be asked for under that file's name",
	} {
		assert.Contains(t, content, want, "docs/TESTS.md missing %q (nova-tools #2000)", want)
	}
}
