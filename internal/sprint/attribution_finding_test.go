package sprint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAttributionOnlyFindingsNeverBounce pins the classifier behind
// `read --broken`'s refusal: the findings readers bounced good work on
// today (2026-10-04; the coordinator's log, judgments "a reader found it
// broken") are each attribution-only, and a real defect, with or without an
// attribution remark, is not (docs/SPEC-SPRINT.md, reader-ignores-attribution).
func TestAttributionOnlyFindingsNeverBounce(t *testing.T) {
	t.Parallel()
	read := func(t *testing.T, dir string) map[string]string {
		files, err := filepath.Glob(filepath.Join("testdata", "attribution-findings", dir, "*.txt"))
		require.NoError(t, err)
		out := map[string]string{}
		for _, f := range files {
			b, err := os.ReadFile(f)
			require.NoError(t, err)
			out[filepath.Base(f)] = string(b)
		}
		return out
	}
	t.Run("attribution", func(t *testing.T) {
		t.Parallel()
		fx := read(t, "attribution")
		require.GreaterOrEqual(t, len(fx), 5)
		for name, text := range fx {
			assert.True(t, AttributionOnly(text), name)
		}
	})
	t.Run("defect", func(t *testing.T) {
		t.Parallel()
		fx := read(t, "defect")
		require.GreaterOrEqual(t, len(fx), 3)
		for name, text := range fx {
			assert.False(t, AttributionOnly(text), name)
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		assert.False(t, AttributionOnly(" \n"))
	})
}
