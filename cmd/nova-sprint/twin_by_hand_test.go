package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// twinAt is one command of a twin as a shell runs it (twinProcess), its clock
// read as now.
func twinAt(t *testing.T, file string, now time.Time, line string) (int, string, string) {
	t.Helper()
	env := map[string]string{"NOVA_SPRINT_REDIS": "mem:" + file, "NOVA_SPRINT_ACTOR": "boss"}
	a := newApp(func(k string) string { return env[k] })
	a.now = func() time.Time { return now }
	defer a.close()
	var out, errb bytes.Buffer
	code := a.run(split(line), &out, &errb)
	return code, out.String(), errb.String()
}

// twinOK is twinAt for a line that succeeds.
func twinOK(t *testing.T, file string, now time.Time, line string) string {
	t.Helper()
	code, out, errs := twinAt(t, file, now, line)
	require.Equal(t, 0, code, "%s:\n%s%s", line, out, errs)
	return out
}

// A twin is ticked by hand (docs/SPEC-SPRINT.md section 14): a RUNNING twin
// that has not ticked for longer than MachineSilence is running, not STOPPED,
// in the inbox's machine line, every verb's sprint line and where's header;
// and the inbox's not-ticking judgment names nova-sprint tick, never run,
// which a twin refuses. The two lines of the inbox agree.
func TestATwinNotTickingIsRunningAndItsRemedyIsTheTick(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	twinOK(t, file, t0, "init --readers ra,rb --members m1")
	twinOK(t, file, t0, "add --stream s1 --count 2")
	twinOK(t, file, t0, "start")
	twinOK(t, file, t0, "tick")
	later := t0.Add(store.MachineSilence + 30*time.Second)

	inbox := twinOK(t, file, later, "inbox")
	assert.Contains(t, inbox, "machine: running\n", "the machine line of a twin RUNNING and not ticked")
	assert.NotContains(t, inbox, "machine: STOPPED")
	assert.Contains(t, inbox, "a twin ticks only by hand")
	assert.Contains(t, inbox, "    nova-sprint tick\n", "the remedy is the tick")
	assert.NotContains(t, inbox, "nova-sprint run\n", "a twin refuses run")

	where := twinOK(t, file, later, "where")
	assert.Contains(t, where, "SPRINT TABLE\n\n0/2 0.0% -> ETA\n\n", "where's header shows the progress of a running machine")

	add := twinOK(t, file, later, "add --stream s1 --count 1")
	assert.Contains(t, add, "-> ETA  machine: running\n", "a verb's sprint line")
	assert.NotContains(t, add, "STOPPED")

	// a stop by hand is still STOPPED on a twin
	twinOK(t, file, later, "stop")
	assert.Contains(t, twinOK(t, file, later, "inbox"), "machine: STOPPED\n")
}
