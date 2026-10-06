package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// set --list prints every policy number as the tick reads it, with where its value came
// from: nova-config's row once applied, nova-sprint set's property, the default, or
// every policy number the card names is one.
func TestSetListPrintsEverySettingWithItsSource(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	out := ta.ok("set --list")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, len(config.SprintPolicies)+1, out)
	for _, p := range config.SprintPolicies {
		assert.Contains(t, out, "SETTING "+p.Name+"="+p.Default+" source=default default="+p.Default+" ", p.Name)
	}
	assert.Contains(t, out, "SETTINGS n=22;")
	assert.NotContains(t, out, "compiled")

	ta.m.SetPolicy("deal_ahead", "3")
	ta.ok("set --friend-idle 40m")
	out = ta.ok("set --list")
	assert.Contains(t, out, "SETTING deal_ahead=3 source=nova-config default=2 ")
	assert.Contains(t, out, "SETTING friend_idle=40m0s source=nova-sprint set default=20m0s ")
	ta.m.SetPolicy("friend_idle", "1h")
	assert.Contains(t, ta.ok("set --list"), "SETTING friend_idle=1h0m0s source=nova-config default=20m0s ")
}

// server switch with no --window takes nova-config's rollback_window from the store, else
// its default; a --window given wins (server_switch.go).
func TestServerSwitchTakesRollbackWindowFromTheStore(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	window := func() time.Duration {
		fs, c := ta.a.verbSetup("server switch")
		_, err := parse(fs, nil)
		require.NoError(t, err)
		w, err := rollbackWindow(ta.a, *c)
		require.NoError(t, err)
		return w
	}
	assert.Equal(t, sprint.DefaultRollbackWindow, window(), "no row: the default")
	ta.m.SetPolicy("rollback_window", "40m")
	assert.Equal(t, 40*time.Minute, window(), "the row's value")
	assert.Contains(t, ta.ok("set --list"), "SETTING rollback_window=40m0s source=nova-config default=15m0s ")
}
