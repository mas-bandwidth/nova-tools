package friend

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The owner's rule, 2026-10-11: the claude harness runs the newest model of each named
// class for each tier (flash haiku, pro sonnet, heavy opus, frontier fable). ReadModels is
// the one table that says so; nova-friend's ReadModel hook reads it for every claude lane.

// TestReadModelsAreTheNewestOfEachClass pins the whole tier map: exactly these four tiers,
// each on the newest model of its class, and nothing else.
func TestReadModelsAreTheNewestOfEachClass(t *testing.T) {
	t.Parallel()
	require.Equal(t, map[string]string{
		"flash":    "claude-haiku-5-5",
		"pro":      "claude-sonnet-5-5",
		"heavy":    "claude-opus-5-5",
		"frontier": "claude-fable-5-1",
	}, ReadModels)
}

// TestNoTierMapsToABiggerClass pins that each tier's model is of its own class or a smaller
// one (flash never sonnet, opus or fable; pro never opus or fable; heavy never fable), and
// that every model names a known class: an unknown family is a failure, never a pass.
func TestNoTierMapsToABiggerClass(t *testing.T) {
	t.Parallel()
	tierRank := map[string]int{"flash": 0, "pro": 1, "heavy": 2, "frontier": 3}
	classRank := []string{"claude-haiku-", "claude-sonnet-", "claude-opus-", "claude-fable-"}
	for tier, model := range ReadModels {
		want, ok := tierRank[tier]
		require.Truef(t, ok, "tier %q is not one of flash, pro, heavy, frontier", tier)
		got := -1
		for rank, prefix := range classRank {
			if strings.HasPrefix(model, prefix) {
				got = rank
			}
		}
		require.NotEqualf(t, -1, got, "tier %s maps to %q, a model of no known class", tier, model)
		assert.LessOrEqualf(t, got, want, "tier %s maps to %q, a model of a bigger class than its own", tier, model)
	}
}
