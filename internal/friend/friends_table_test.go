package friend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFriendsTableAndSkipped(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"friends": [
			{"name": "amy", "status": "up"},
			{"name": "bob", "status": "up"},
			{"name": "cy", "status": "up"},
			{"name": "dee", "status": "held", "reason": "on hold"},
			{"name": "alex", "status": "up", "never_wake": true},
			{"name": "zhi", "status": "down"}
		]
	}`)
	rows, err := ParseFriendsTable(raw)
	require.NoError(t, err)
	require.Len(t, rows, 6)

	assert.False(t, rows[0].Skipped()) // amy: up
	assert.False(t, rows[1].Skipped()) // bob: up
	assert.False(t, rows[2].Skipped()) // cy: up
	assert.True(t, rows[3].Skipped())  // dee: held
	assert.True(t, rows[4].Skipped())  // alex: never-wake
	assert.True(t, rows[5].Skipped())  // zhi: down

	// Also direct slice
	rawDirect := []byte(`[
		{"name": "f1", "status": "up"},
		{"name": "f2", "status": "up", "never-wake": true},
		{"name": "f3", "status": "up", "class": "pro,never-wake"}
	]`)
	rowsDirect, err := ParseFriendsTable(rawDirect)
	require.NoError(t, err)
	require.Len(t, rowsDirect, 3)
	assert.False(t, rowsDirect[0].Skipped())
	assert.True(t, rowsDirect[1].Skipped())
	assert.True(t, rowsDirect[2].Skipped())
}
