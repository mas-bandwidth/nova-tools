package cardlimits

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The brief's two sizes are the ones the docs and the refusal text state, and the advice
// sits under the refusal.
func TestTheBriefSizes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 16384, MaxBriefBytes)
	assert.Equal(t, 12000, BriefAdvisoryBytes)
	assert.Less(t, BriefAdvisoryBytes, MaxBriefBytes)
}
