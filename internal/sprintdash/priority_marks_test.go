package sprintdash

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// The page has a single state bar; priority data remains available in the API.
func TestNoSecondPriorityBar(t *testing.T) {
	t.Parallel()
	page, js := string(file("index.html")), string(file("app.js"))
	assert.NotContains(t, page, `id="priority-marks"`)
	assert.NotContains(t, page, `.marks`)
	assert.NotContains(t, js, "renderPriorityMarks")
	assert.Contains(t, page, `.fix { background: var(--s-fix); }`)
	assert.Contains(t, js, `var STATES = ["landed", "merging", "fix", "review", "working", "ready", "waiting"];`)
}
