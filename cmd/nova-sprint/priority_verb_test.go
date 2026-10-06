package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// priority set, where and why (docs/SPEC-SPRINT.md section 5, the deal: priority): a
// release's streams set to a level, where names the level and the group dealt now, and why
// prints what keeps a ready card of a lower level from dealing, one line each.
func TestPrioritySetWhereAndWhy(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("add --stream rel --count 2")
	ta.ok("stream set rel --release v1.1.0")

	code, _, errs := ta.do("priority set --level 1")
	assert.Equal(t, 2, code, "no group: %s", errs)
	code, _, errs = ta.do("priority set rel")
	assert.Equal(t, 2, code, "no level: %s", errs)
	assert.Contains(t, errs, "--level <n> is required")
	code, _, errs = ta.do("priority set --release v9 --level 1")
	assert.Equal(t, 1, code, "a release no stream is of: %s", errs)
	assert.Contains(t, errs, "no stream is of release v9")

	out := ta.ok("priority set --release v1.1.0 --level 1")
	assert.Contains(t, out, "stream rel level 1 (was 0)")
	where := ta.ok("where")
	assert.Contains(t, where, "priority: dealing level 1 (rel); levels rel 1, the rest 0")
	var v struct {
		Priority struct {
			Levels  map[string]int `json:"levels"`
			Group   int            `json:"group"`
			Streams []string       `json:"streams"`
		} `json:"priority"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("where --json")), &v))
	assert.Equal(t, map[string]int{"rel": 1}, v.Priority.Levels)
	assert.Equal(t, 1, v.Priority.Group)
	assert.Equal(t, []string{"rel"}, v.Priority.Streams)

	why := ta.ok("why s1-1")
	assert.Contains(t, why, "WHY s1-1 group: level 0 (stream s1): the groups ahead of it are dealt first: rel level 1 (2 ready)")
	assert.Contains(t, why, "WHY s1-1 order: 2 ready ahead of it")
	assert.Contains(t, why, "WHY OK card=s1-1 lines=8")
	code, _, errs = ta.do("why nosuch-1")
	assert.NotZero(t, code)
	assert.Contains(t, errs, "no card nosuch-1")

	ta.ok("priority set rel --level 0")
	assert.NotContains(t, ta.ok("where"), "priority:", "level 0 takes it off, and where says nothing of it")
}
