package typedrec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
	"github.com/stretchr/testify/require"
)

func findRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not find repository root")
		dir = parent
	}
}

// 1. TestSpecSwarmContractMatches verifies that docs/SPEC-SWARM.md matches
// typedrec.Contract.Markdown() byte for byte between the typedrec markers.
func TestSpecSwarmContractMatches(t *testing.T) {
	t.Parallel()

	root := findRoot(t)
	specPath := filepath.Join(root, "docs", "SPEC-SWARM.md")
	data, err := os.ReadFile(specPath)
	require.NoError(t, err, "read SPEC-SWARM.md: %v", err)
	s := string(data)
	const beginMarker = "<!-- typedrec:begin -->\n"
	const endMarker = "<!-- typedrec:end -->"
	begin := strings.Index(s, beginMarker)
	require.NotEqual(t, -1, begin, "<!-- typedrec:begin --> not found in docs/SPEC-SWARM.md")
	begin += len(beginMarker)
	end := strings.Index(s[begin:], endMarker)
	require.NotEqual(t, -1, end, "<!-- typedrec:end --> not found in docs/SPEC-SWARM.md")
	got := s[begin : begin+end]
	want := typedrec.Contract.Markdown()
	require.Equal(t, want, got, "docs/SPEC-SWARM.md drift:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
}
