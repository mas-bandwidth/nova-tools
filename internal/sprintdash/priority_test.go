package sprintdash

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The deal group (docs/SPEC-SPRINT-DASHBOARD.md, Merge; docs/SPEC-SPRINT.md, the deal:
// priority): the merge row shows the level the deal takes from now, from where --json's
// priority, and "-" while no stream has a level.
func TestTheMergeRowShowsTheDealGroup(t *testing.T) {
	t.Parallel()
	js := string(file("app.js"))
	assert.Contains(t, js, "d.priority", "the page reads where --json's priority")
	assert.Contains(t, js, "pr.group")
	assert.Contains(t, js, "pr.levels")
	panel := parsePage(t, file("index.html")).one(t, "the merge row", byID("merge-row"))
	panel.one(t, "the merge row's #mr-group", byID("mr-group"))
	sec := readSpec(t).section(t, "Merge")
	assert.Contains(t, sec, "deal group")
	assert.Contains(t, sec, "`priority`")
}
