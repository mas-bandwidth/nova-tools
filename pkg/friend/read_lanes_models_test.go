package friend

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ReadModels is the model of each tier for a claude account's reads, the table the bud
// runners carried. This test pins that each tier maps to the newest model of its class,
// and no tier maps to a model of a bigger class than its own (flash never opus or fable,
// pro never opus or fable).
func TestReadModelsMapToNewestOfEachClass(t *testing.T) {
	t.Parallel()

	// Expected mapping: flash=haiku-5-5, pro=sonnet-5-5, heavy=opus-5-5, frontier=fable-5-1
	expected := map[string]string{
		"flash":    "claude-haiku-5-5",
		"pro":      "claude-sonnet-5-5",
		"heavy":    "claude-opus-5-5",
		"frontier": "claude-fable-5-1",
	}

	for tier, expModel := range expected {
		got := ReadModels[tier]
		assert.Equal(t, expModel, got, "tier %s should map to %s, got %s", tier, expModel, got)
	}
}

// No tier maps to a model of a bigger class than its own.
// Class hierarchy (smallest to largest): flash < pro < heavy < frontier
func TestReadModelsTierClassBoundaries(t *testing.T) {
	t.Parallel()

	// Class ordering: 0=flash, 1=pro, 2=heavy, 3=frontier
	tierClass := map[string]int{
		"flash":    0,
		"pro":      1,
		"heavy":    2,
		"frontier": 3,
	}

	// Model class mapping: haiku=0, sonnet=1, opus=2, fable=3
	modelClass := map[string]int{
		"claude-haiku":  0,
		"claude-sonnet": 1,
		"claude-opus":   2,
		"claude-fable":  3,
	}

	for tier, model := range ReadModels {
		// Extract base model name (strip version suffix)
		baseModel := ""
		if strings.HasPrefix(model, "claude-haiku") {
			baseModel = "claude-haiku"
		} else if strings.HasPrefix(model, "claude-sonnet") {
			baseModel = "claude-sonnet"
		} else if strings.HasPrefix(model, "claude-opus") {
			baseModel = "claude-opus"
		} else if strings.HasPrefix(model, "claude-fable") {
			baseModel = "claude-fable"
		}

		tierCl := tierClass[tier]
		modelCl := modelClass[baseModel]

		assert.LessOrEqual(t, modelCl, tierCl,
			"tier %s (class %d) should not map to model %s (class %d)",
			tier, tierCl, model, modelCl)
	}
}
