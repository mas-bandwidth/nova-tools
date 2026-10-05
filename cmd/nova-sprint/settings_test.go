package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// set --list prints every policy number as the tick reads it, with where its value came
// from: nova-sprint set's property, the default, or compiled for a number that is not a
// setting yet.
func TestSetListPrintsEverySettingWithItsSource(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	out := ta.ok("set --list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, len(config.SprintPolicies)+len(sprint.PolicyCompiled)+1, out)
	for _, p := range config.SprintPolicies {
		assert.Contains(t, out, "SETTING "+p.Name+"="+p.Default+" source=default default="+p.Default+" ", p.Name)
	}
	for _, c := range sprint.PolicyCompiled {
		assert.Contains(t, out, "SETTING "+c.Name+"="+c.Value+" source=compiled ", c.Name)
	}
	assert.Contains(t, out, fmt.Sprintf("SETTINGS n=%d;", len(lines)-1))

	ta.ok("set --friend-idle 40m")
	out = ta.ok("set --list")
	assert.Contains(t, out, "SETTING friend_idle=40m0s source=nova-sprint set default=20m0s ")
	assert.Contains(t, out, "SETTING deal_ahead=2 source=default default=2 ")
}
