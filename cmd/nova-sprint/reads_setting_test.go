package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// set --reads (sprint.PropReadsNeeded): where says "reads: <n>" only while a count is set,
// and where --json carries reads_needed, the count or "default", always.
func TestWhereShowsTheReadsSettingOnlyWhileSet(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	readsOf := func() any {
		var top map[string]any
		require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &top))
		return top["reads_needed"]
	}
	assert.NotContains(t, ta.ok("where"), "reads: ")
	assert.Equal(t, "default", readsOf())

	assert.Contains(t, ta.ok("set --reads 1"), "reads-needed 1")
	assert.Contains(t, ta.ok("where"), "reads: 1\n")
	assert.Equal(t, float64(1), readsOf())

	assert.Contains(t, ta.ok("set --reads 0"), "reads-needed 0")
	assert.Contains(t, ta.ok("where"), "reads: 0\n")
	assert.Equal(t, float64(0), readsOf())

	code, _, errs := ta.do("set --reads 3")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errs, "--reads wants 0, 1, 2 or default")

	assert.Contains(t, ta.ok("set --reads default"), "reads-needed default")
	assert.NotContains(t, ta.ok("where"), "reads: ")
	assert.Equal(t, "default", readsOf())
}
