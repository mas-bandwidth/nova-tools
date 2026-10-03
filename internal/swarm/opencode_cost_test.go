package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The live read carries the harness's own cost beside the tokens, so native can hold a card
// to a dollar budget while it runs (nova-tools #5094): the per-message costs summed exactly,
// a message that reported none adding nothing, and no cost at all where none reported one.
func TestTheLiveReadSumsTheHarnessesCost(t *testing.T) {
	t.Parallel()
	u, err := foldOpenCodeRows([][]string{
		{"inception", "mercury-2.5", "300000", "400", "", "", "", "0.0125"},
		{"inception", "mercury-2.5", "209940", "310", "", "", "", "0.0086"},
		{"inception", "mercury-2.5", "10", "1", "", "", "", ""},
	})
	require.NoError(t, err)
	assert.Equal(t, "0.0211", u.Values["cost"], "the cost is the exact sum of the messages' costs")
	assert.Equal(t, "509950", u.Values["tokens_in"])

	u, err = foldOpenCodeRows([][]string{{"p", "m", "10", "1", "", "", "", ""}})
	require.NoError(t, err)
	_, has := u.Values["cost"]
	assert.False(t, has, "no message reported a cost: none is written, never a zero")
}
