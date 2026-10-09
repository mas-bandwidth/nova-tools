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
	assert.Contains(t, ta.ok("set --rework-priority fix"), "rework-priority fix")
	for _, policy := range []string{"high", "keep", "urgent"} {
		code, _, why := ta.do("set --rework-priority " + policy)
		assert.NotZero(t, code)
		assert.Contains(t, why, "use: nova-sprint set --rework-priority fix")
	}
}
