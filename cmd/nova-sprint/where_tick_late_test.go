package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
)

// A RUNNING machine whose last tick is older than the window is running, with
// the whole seconds since that tick, never STOPPED (docs/SPEC-SPRINT.md
// sections 1 and 14): the coordinator measured it at 2:23 PM on 2026-10-04,
// where saying "machine: STOPPED" for about 7 s, then running, over and over,
// while the server was RUNNING the whole time and ticking 7 to 16 s apart.
// where's header keeps the progress line, the inbox, where --json and the
// dashboard's machine line say "running (tick late <n>s)"; STOPPED is a real
// stop alone: the machine key STOPPED (a new sprint) or a stop on the log.
func TestWhereShowsRunningWithATickLateNoteNotStopped(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	srv := &sprintdash.Server{Read: func() ([]byte, error) { return ta.a.whereJSON("", false) }, Now: ta.a.now, Every: time.Second}
	dashboard := func() string {
		srv.Refresh()
		w := httptest.NewRecorder()
		srv.Pull().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/team", nil))
		var v sprintdash.TeamView
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v), w.Body.String())
		return v.Sprint.Machine
	}
	var w whereView

	// the machine key STOPPED (a new sprint, no stop on the log): STOPPED
	got := whereHead(t, ta.ok("where"))
	assert.True(t, strings.HasPrefix(got, "SPRINT TABLE  coordinator coordinator\n\nSTOPPED"), "a STOPPED key:\n%q", got)
	ta.json("where", &w)
	assert.True(t, strings.HasPrefix(w.Machine, "machine: STOPPED"), "a STOPPED key: %q", w.Machine)

	// RUNNING and ticked: running, no note
	ta.ok("start")
	ta.ok("tick")
	got = whereHead(t, ta.ok("where"))
	assert.True(t, strings.HasPrefix(got, "SPRINT TABLE  coordinator coordinator\n\n0/3 0.0% -> ETA -\n\n"), "ticked:\n%q", got)
	ta.json("where", &w)
	assert.Equal(t, "machine: running", w.Machine, "ticked")
	assert.Contains(t, ta.ok("inbox"), "machine: running\n", "ticked")
	assert.Equal(t, "running", dashboard(), "ticked")

	// RUNNING, the last tick older than the window: running (tick late 16s),
	// with the progress line, never STOPPED
	ta.a.sleep(store.MachineSilence + time.Second)
	out := ta.ok("where")
	got = whereHead(t, out)
	assert.True(t, strings.HasPrefix(got, "SPRINT TABLE  coordinator coordinator\n\n0/3 0.0% -> ETA -  running (tick late 16s)\n\n"), "a late tick:\n%q", got)
	assert.NotContains(t, out, "STOPPED", "a late tick")
	ta.json("where", &w)
	assert.Equal(t, "machine: running (tick late 16s)", w.Machine, "a late tick")
	assert.Equal(t, "0/3 0.0% -> ETA -", w.Summary, "a late tick")
	inbox := ta.ok("inbox")
	assert.Contains(t, inbox, "machine: running (tick late 16s)\n", "a late tick")
	assert.NotContains(t, inbox, "machine: STOPPED", "a late tick")
	assert.Equal(t, "running (tick late 16s)", dashboard(), "a late tick: the dashboard carries where's line")
	ta.a.sleep(9 * time.Second)
	ta.json("where", &w)
	assert.Equal(t, "machine: running (tick late 25s)", w.Machine, "the count is the whole seconds since the tick")

	// a tick again: running, no note
	ta.ok("tick")
	ta.json("where", &w)
	assert.Equal(t, "machine: running", w.Machine, "ticked again")

	// a stop on the log (the stop of the store, as stop runs it): STOPPED
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	_, _, _, err = st.SetMachine(context.Background(), false)
	require.NoError(t, err)
	got = whereHead(t, ta.ok("where"))
	assert.True(t, strings.HasPrefix(got, "SPRINT TABLE  coordinator coordinator\n\nSTOPPED"), "a stop:\n%q", got)
	ta.json("where", &w)
	assert.True(t, strings.HasPrefix(w.Machine, "machine: STOPPED"), "a stop: %q", w.Machine)
	assert.True(t, strings.HasPrefix(dashboard(), "STOPPED"), "a stop")
	ta.a.sleep(store.MachineSilence + time.Second)
	ta.json("where", &w)
	assert.True(t, strings.HasPrefix(w.Machine, "machine: STOPPED"), "a stop, long after: %q", w.Machine)
	assert.NotContains(t, w.Machine, "tick late", "a stop, long after")
}
