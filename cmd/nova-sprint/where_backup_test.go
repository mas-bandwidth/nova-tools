package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
)

// where shows the backup state (docs/SPEC-SPRINT.md section 1): none while working holds the
// most, reads once review exceeds working, with the reads waiting, in --json and on a line
// under the summary.
func TestWhereShowsTheBackupState(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.deal(100)
	ta.ok("take --as m1 --limit 100")
	var v whereView
	ta.json("where", &v)
	assert.Equal(t, sprint.BackupNone, v.Backup, "three working, none in review")
	assert.NotContains(t, ta.ok("where"), "backup:")

	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	var words []string
	for _, c := range q.Cards {
		words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
	}
	ta.ok("finish --as m1 " + strings.Join(words, " "))
	ta.ok("tick") // no reader: the three reads wait, counted by the tick
	ta.json("where", &v)
	assert.Equal(t, sprint.BackupReads, v.Backup, "three in review above none working")
	assert.Equal(t, 3, v.ReadsWaiting)
	assert.Equal(t, sprint.ReviewWaits{Review: 3, Wanting: 3}, v.ReviewWaits, "what review waits on, each primary once")
	assert.Contains(t, ta.ok("where"), "backup: reads (review 3 > working 0, merging 0), 3 reads waiting; review 3: 0 reads out, 3 want a reader, 0 found broken (0 brief defects), 0 failed (0 brief defects), 0 acceptable")
}
