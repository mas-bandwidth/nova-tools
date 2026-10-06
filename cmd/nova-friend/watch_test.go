package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type droppedMsg struct {
	from    string
	subject string
}

func dropMsg(r *rig, to, from, id, subject string) {
	m := bus.Message{ID: id, From: from, To: []string{to}, Subject: subject, Body: "the body"}
	if err := r.store.AddAll(context.Background(), []string{bus.StreamOf(to)}, m.Fields()); err != nil {
		panic("dropMsg: fake store refused: " + err.Error())
	}
}

func sleepDropMsg(r *rig, to string, ds ...droppedMsg) {
	r.store.Sleep = func(time.Duration) {
		r.store.Sleep = nil
		for _, d := range ds {
			dropMsg(r, to, d.from, "01J"+strings.ToUpper(strings.ReplaceAll(d.subject, " ", "")), d.subject)
		}
	}
}

func afterOfWatch(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	v, ok := strings.CutPrefix(lines[len(lines)-1], "WATCH OK after=")
	require.True(t, ok, "no WATCH OK after= line in %q", out)
	return v
}

// TestWatchWakesOnARealMessageAWakeLineOrAnEvent: watch wakes on a real message,
// a line appended to the wake file, or an event message (subject starting with event:).
func TestWatchWakesOnARealMessageAWakeLineOrAnEvent(t *testing.T) {
	t.Parallel()

	// 1. Real message
	r1 := newRig(t, "ada", "bob")
	cli1 := r1.cli()
	sleepDropMsg(r1, "bob", droppedMsg{"ada", "hello"})
	got1 := cli1.Do(t, "watch", "--as", "bob").Exit(0)
	lines1 := strings.Split(strings.TrimSpace(got1.Stdout), "\n")
	require.Len(t, lines1, 2, "one MESSAGE line then OK: %q", got1.Stdout)
	assert.Equal(t, "WATCH MESSAGE id=01JHELLO from=ada subject=hello", lines1[0])
	assert.Regexp(t, `^WATCH OK after=\d+-\d+$`, lines1[1])

	// 2. Wake file line
	r2 := newRig(t, "ada", "bob")
	cli2 := r2.cli()
	state2 := filepath.Join(r2.home, ".nova-friend", "bob")
	require.NoError(t, os.MkdirAll(state2, 0o755))
	r2.fileLine = func(_ string, from int64) (string, int64, error) {
		return "session: 1 message waiting", from + 27, nil
	}
	got2 := cli2.Do(t, "watch", "--as", "bob").Exit(0)
	lines2 := strings.Split(strings.TrimSpace(got2.Stdout), "\n")
	require.Len(t, lines2, 2, "one WAKE line then OK: %q", got2.Stdout)
	assert.Equal(t, `WATCH WAKE line="session: 1 message waiting"`, lines2[0])
	assert.Regexp(t, `^WATCH OK after=`, lines2[1])

	// Wake file line JSON
	r2json := newRig(t, "ada", "bob")
	cli2json := r2json.cli()
	r2json.fileLine = func(_ string, from int64) (string, int64, error) {
		return "session: 2 waiting", from + 19, nil
	}
	got2json := cli2json.Do(t, "watch", "--as", "bob", "--json").Exit(0)
	var wJSON watchJSON
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(got2json.Stdout)), &wJSON))
	assert.Equal(t, "ok", wJSON.Status)
	assert.Equal(t, "OK", wJSON.Word)
	require.NotNil(t, wJSON.Wake)
	assert.Equal(t, "session: 2 waiting", wJSON.Wake.Line)

	// 3. Event message (subject starting with "event:")
	r3 := newRig(t, "ada", "bob")
	cli3 := r3.cli()
	sleepDropMsg(r3, "bob", droppedMsg{"ada", "event:machine-stopped"})
	got3 := cli3.Do(t, "watch", "--as", "bob").Exit(0)
	lines3 := strings.Split(strings.TrimSpace(got3.Stdout), "\n")
	require.Len(t, lines3, 2, "one EVENT line then OK: %q", got3.Stdout)
	assert.Equal(t, "WATCH EVENT id=01JEVENT:MACHINE-STOPPED from=ada subject=event:machine-stopped", lines3[0])
	assert.Regexp(t, `^WATCH OK after=\d+-\d+$`, lines3[1])
}

// TestWatchSavesTheCursorSoTheNextRunMissesNothing: the cursor is saved in the
// state dir after each run, so the next run misses nothing and needs no flag.
func TestWatchSavesTheCursorSoTheNextRunMissesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	state := filepath.Join(r.home, ".nova-friend", "bob")

	// Drop first message
	sleepDropMsg(r, "bob", droppedMsg{"ada", "first"})
	got1 := cli.Do(t, "watch", "--as", "bob").Exit(0)
	c1 := afterOfWatch(t, got1.Stdout)
	assert.Contains(t, got1.Stdout, "WATCH MESSAGE id=01JFIRST from=ada subject=first")

	// Cursor file exists in state dir
	saved, found, err := friend.ReadWatchCursor(state)
	require.NoError(t, err)
	require.True(t, found, "cursor file should exist in %s", state)
	assert.Equal(t, c1, saved)

	// Drop second message while watch is not running
	dropMsg(r, "bob", "ada", "01JSECOND", "second")

	// Next run with NO cursor flag: picks up from saved cursor
	got2 := cli.Do(t, "watch", "--as", "bob").Exit(0)
	c2 := afterOfWatch(t, got2.Stdout)
	assert.Contains(t, got2.Stdout, "WATCH MESSAGE id=01JSECOND from=ada subject=second")
	assert.NotContains(t, got2.Stdout, "first", "first message was already consumed")

	// Cursor updated in state directory
	saved2, found2, err := friend.ReadWatchCursor(state)
	require.NoError(t, err)
	require.True(t, found2)
	assert.Equal(t, c2, saved2)

	// Next run with timeout times out as none
	r.wireClock()
	got3 := cli.Do(t, "watch", "--as", "bob", "--timeout", "1s").Exit(1)
	got3.Err("WATCH NONE waited=1s")
	assert.NotContains(t, got3.Stdout, "WATCH MESSAGE")
}

// TestWatchSkipsOwnPingPongAndKeepalive: watch skips own messages, ping, pong,
// daemon-pong and keepalive, advancing the cursor past them.
func TestWatchSkipsOwnPingPongAndKeepalive(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	sleepDropMsg(r, "bob",
		droppedMsg{"bob", "note to self"},
		droppedMsg{"ada", "PING nonce1"},
		droppedMsg{"ada", "pong nonce1"},
		droppedMsg{"ada", "daemon-pong nonce1"},
		droppedMsg{"ada", "keepalive"},
		droppedMsg{"ada", "hello"},
	)
	got := cli.Do(t, "watch", "--as", "bob").Exit(0)
	assert.Contains(t, got.Stdout, "WATCH MESSAGE id=01JHELLO from=ada subject=hello")
	assert.Equal(t, 1, strings.Count(got.Stdout, "WATCH MESSAGE"), "only the real message prints: %q", got.Stdout)
	assert.NotContains(t, got.Stdout, "note to self")
	assert.NotContains(t, got.Stdout, "PING")
	assert.NotContains(t, got.Stdout, "daemon-pong")
	assert.NotContains(t, got.Stdout, "keepalive")

	// The cursor moved past all skipped entries
	r.wireClock()
	got2 := cli.Do(t, "watch", "--as", "bob", "--timeout", "1s").Exit(1)
	got2.Err("WATCH NONE waited=1s")
	assert.NotContains(t, got2.Stdout, "WATCH MESSAGE", "skipped entries are not re-read")
}

// TestWatchTimesOutAsNone: past --timeout the wait is WATCH NONE waited=<d> at exit 1.
func TestWatchTimesOutAsNone(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	r.wireClock()
	cli := r.cli()

	got := cli.Do(t, "watch", "--as", "bob", "--timeout", "5s").Exit(1)
	assert.Equal(t, "WATCH NONE waited=5s\n", got.Stderr)

	r.wireClock()
	gotJSON := cli.Do(t, "watch", "--as", "bob", "--timeout", "5s", "--json").Exit(1)
	var v watchJSON
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(gotJSON.Stdout)), &v))
	assert.Equal(t, "ok", v.Status)
	assert.Equal(t, "NONE", v.Word)
	assert.Equal(t, "5s", v.Waited)
	assert.Empty(t, v.Messages)
	assert.Empty(t, v.Events)
}
