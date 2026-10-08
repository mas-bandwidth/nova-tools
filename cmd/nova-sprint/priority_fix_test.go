package main

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
)

func TestFixPriorityCLIAndReworkSetting(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("priority s1-1 --fix --reason repair")
	level, _ := ta.cardPriority("s1-1")
	assert.Equal(t, sprint.PriorityFix, level)
	for _, policy := range []string{"fix", "high", "keep"} {
		assert.Contains(t, ta.ok("set --rework-priority "+policy), "rework-priority "+policy)
	}
	code, _, why := ta.do("set --rework-priority urgent")
	assert.NotZero(t, code)
	assert.Contains(t, why, "fix, high or keep")
}
