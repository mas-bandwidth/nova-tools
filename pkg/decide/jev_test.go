package decide

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A choice the wire answers with only a confidence is not a probability of
// that choice. P stays empty; the confidence is recorded beside the choice
// with method wire, and neither is promoted (SPEC-NOVA-DECIDE section 3).
func TestAChoiceWithOnlyConfidenceIsNotAProbability(t *testing.T) {
	t.Parallel()
	answers, _, err := jevAnswers([]byte(`{"answers":{"verdict":{"type":"choice","choice":"LAND","confidence":0.6}}}`))
	require.NoError(t, err)
	got := answers["verdict"]
	assert.Empty(t, got.P)
	assert.Equal(t, "LAND", got.Value)
	assert.Zero(t, got.Prob("LAND"))
	assert.InDelta(t, 0.6, got.Confidence, 1e-9)
	assert.Equal(t, "wire", got.Method)
}
