package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoopMarkerAndRestartCount(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "free_gib=3\nstop=1\n", FormatLoopMarker(3<<30, true))
	assert.Equal(t, "free_gib=0\nstop=0\n", FormatLoopMarker(0, false))
	assert.True(t, LoopMarkerStops("free_gib=1\nstop=1\n"))
	assert.False(t, LoopMarkerStops(""))
	assert.False(t, LoopMarkerStops("stop=0\n"))

	n, err := NextRestart("")
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = NextRestart("4")
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	_, err = NextRestart("-1")
	assert.Error(t, err)
	_, err = NextRestart("nope")
	assert.Error(t, err)
	assert.Equal(t, "nova_loop_restarts_total{loop=\"a b\"} 2\n", RestartProm("a b", 2))
}
