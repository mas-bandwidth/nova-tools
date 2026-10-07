package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReaderTiersStoresTheLadderAndRefusesARepeat(t *testing.T) {
	t.Parallel()
	got, err := ParseReaderTiers("pro, flash")
	require.NoError(t, err)
	assert.Equal(t, "flash,pro", got)
	got, err = ParseReaderTiers("all")
	require.NoError(t, err)
	assert.Equal(t, "flash,pro,heavy,frontier", got, "all names every tier")
	got, err = ParseReaderTiers("default")
	require.NoError(t, err)
	assert.Equal(t, "", got, "default is the empty cell: flash on a fleet reader, every tier on a friend's")
	_, err = ParseReaderTiers("flash,flash")
	assert.Error(t, err)
	_, err = ParseReaderTiers("nope")
	assert.Error(t, err)
}
