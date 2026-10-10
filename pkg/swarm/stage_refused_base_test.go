package swarm

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card staged at its BASE: ref and refused for want of a bench mirror keeps the ref on the
// result, so native's STAGE FAIL line names the card's base (the fleet store's line of
// 2026-10-01 printed `base=` empty).
func TestAMirrorRefusalKeepsTheCardsBaseRef(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	res, err := StageCard(StageOptions{
		Card:      []byte("base-repo: https://example.com/mas-bandwidth/missing-mirror.git\nBASE: main\n"),
		TargetDir: filepath.Join(root, "job", "repo"), JobDir: filepath.Join(root, "job"),
		BenchHome: filepath.Join(root, "home"), BenchName: "testhost", Timeout: 30 * time.Second,
	})
	require.ErrorContains(t, err, "staging refused: no bench mirror")
	assert.Empty(t, res.BaseSha)
	assert.Equal(t, "main", res.Ref)
}
