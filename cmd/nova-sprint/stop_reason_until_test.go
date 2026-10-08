package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStopByHandSaysWhoWhyAndWhenEverywhere pins stop's --reason and --until
// at the command (docs/SPEC-SPRINT.md section 14): a stop missing either is
// refused naming both, where's header and its JSON (the dashboard's machine
// line) say "STOPPED by <actor>: <reason>, back by <time>"; the time does
// not restart it, and an explicit start does.
func TestStopByHandSaysWhoWhyAndWhenEverywhere(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers reader-a,reader-b")
	ta.ok("start")

	code, out, errs := ta.do("stop")
	assert.Equal(t, 2, code, "a bare stop: %s%s", out, errs)
	assert.Contains(t, errs, "--reason <text>")
	assert.Contains(t, errs, "--until <time or duration>")
	code, _, errs = ta.do("stop --reason 'a bench' --until yesterday")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "found yesterday")

	stop := ta.ok("stop --reason 'the bench is rebooting' --until 1h")
	want := "STOPPED by coordinator: the bench is rebooting, back by " + t0.Add(time.Hour).Format("3:04 PM")
	assert.Contains(t, stop, "STOP OK before=RUNNING after=STOPPED changed")
	assert.Contains(t, ta.ok("where"), "\n"+want+"\n", "where's header")
	var v struct {
		Machine string `json:"machine"`
	}
	ta.json("where --json", &v)
	assert.Equal(t, "machine: "+want, v.Machine, "the dashboard's machine line")

	ta.mu.Lock()
	ta.now = ta.now.Add(time.Hour)
	ta.mu.Unlock()
	tick := ta.ok("tick")
	assert.Contains(t, tick, "state=STOPPED", "the tick at --until leaves the machine stopped:\n%s", tick)
	ta.ok("start")
	inbox := ta.ok("inbox")
	require.True(t, strings.HasSuffix(inbox, "machine: running\n"), "inbox:\n%s", inbox)
}
