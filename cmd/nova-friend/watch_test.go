package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchRig is a coordinator ada and a friend bob over the rig's fake store,
// with a state directory of its own; the clock is the rig's (a second a call).
type watchRig struct {
	*rig
	state string
}

func newWatchRig(t *testing.T) watchRig {
	t.Helper()
	return watchRig{rig: newRig(t, "ada", "bob", "mach"), state: t.TempDir()}
}

// send puts one message on the bus and answers its id.
func (w watchRig) send(t *testing.T, from, to, subject string) string {
	t.Helper()
	m, err := (&bus.Bus{Store: w.store}).Send(context.Background(), bus.Message{From: from, To: []string{to}, Subject: subject, Body: "body"})
	require.NoError(t, err)
	return m.ID
}

// wake appends one line to ada's wake file, as the claude adapter does.
func (w watchRig) wake(t *testing.T, line string) {
	t.Helper()
	f, err := os.OpenFile(friend.ClaudeWakePath(w.state, "ada"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(line + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func (w watchRig) watch(t *testing.T, extra ...string) testkit.Ran {
	t.Helper()
	return w.cli().Do(t, append([]string{"watch", "--as", "ada", "--state-dir", w.state}, extra...)...)
}

// arm is the first run: it saves the cursor at the stream's end and the wake
// file's end, and finds nothing.
func (w watchRig) arm(t *testing.T) {
	t.Helper()
	w.watch(t, "--timeout", "2s").Exit(1)
}

// Watch (docs/SPEC-FRIEND.md, Watch): a real message, a line on the wake file
// and an event each end the watch on its own line, then WATCH OK.
func TestWatchWakesOnARealMessageAWakeLineOrAnEvent(t *testing.T) {
	t.Parallel()
	t.Run("message", func(t *testing.T) {
		t.Parallel()
		w := newWatchRig(t)
		w.arm(t)
		id := w.send(t, "bob", "ada", "done with the card")
		got := w.watch(t, "--timeout", "1m").Exit(0)
		got.Out("WATCH MESSAGE id="+id+" from=bob subject=\"done with the card\"", "WATCH OK after=")
		assert.NotContains(t, got.Stdout, "WATCH EVENT")
		assert.NotContains(t, got.Stdout, "WATCH WAKE")
		lines := strings.Split(strings.TrimSpace(got.Stdout), "\n")
		assert.True(t, strings.HasPrefix(lines[len(lines)-1], "WATCH OK after="), "WATCH OK is the last line")
	})
	t.Run("wake line", func(t *testing.T) {
		t.Parallel()
		w := newWatchRig(t)
		w.arm(t)
		w.wake(t, "12:00 pushed a message from bob")
		got := w.watch(t, "--timeout", "1m").Exit(0)
		got.Out(`WATCH WAKE line="12:00 pushed a message from bob"`, "WATCH OK after=")
		assert.NotContains(t, got.Stdout, "WATCH MESSAGE")
	})
	t.Run("event", func(t *testing.T) {
		t.Parallel()
		w := newWatchRig(t)
		w.arm(t)
		id := w.send(t, "mach", "ada", "event: machine stopped unasked")
		w.watch(t, "--timeout", "1m").Exit(0).Out("WATCH EVENT id="+id+` from=mach subject="event: machine stopped unasked"`, "WATCH OK after=")
	})
	t.Run("a wake line comes before a message, five at most", func(t *testing.T) {
		t.Parallel()
		w := newWatchRig(t)
		w.arm(t)
		for i := range 7 {
			w.wake(t, "line "+string(rune('a'+i)))
		}
		got := w.watch(t, "--timeout", "1m").Exit(0)
		assert.Equal(t, 5, strings.Count(got.Stdout, "WATCH WAKE"))
		assert.Contains(t, got.Stdout, `line="line e"`)
		assert.NotContains(t, got.Stdout, `line="line f"`)
		got = w.watch(t, "--timeout", "1m").Exit(0)
		assert.Equal(t, 2, strings.Count(got.Stdout, "WATCH WAKE"))
	})
	t.Run("json", func(t *testing.T) {
		t.Parallel()
		w := newWatchRig(t)
		w.arm(t)
		id := w.send(t, "bob", "ada", "event: disk full")
		got := w.watch(t, "--timeout", "1m", "--json").Exit(0)
		var v struct {
			Status, Word, After string
			Wakes               []map[string]string
		}
		require.NoError(t, json.Unmarshal([]byte(got.Stdout), &v))
		assert.Equal(t, "ok", v.Status)
		assert.Equal(t, "OK", v.Word)
		assert.NotEmpty(t, v.After)
		require.Len(t, v.Wakes, 1)
		assert.Equal(t, map[string]string{"kind": "EVENT", "id": id, "from": "bob", "subject": "event: disk full"}, v.Wakes[0])
	})
	t.Run("a name the roster lacks is refused", func(t *testing.T) {
		t.Parallel()
		w := newWatchRig(t)
		w.cli().Do(t, "watch", "--as", "zed", "--state-dir", w.state, "--timeout", "1s").Exit(2).Err("REFUSED", "zed")
	})
}

// The cursor is saved in the state directory after each run, whole, so the
// next run needs no flag and misses nothing.
func TestWatchSavesTheCursorSoTheNextRunMissesNothing(t *testing.T) {
	t.Parallel()
	w := newWatchRig(t)
	w.arm(t)
	saved, found, err := friend.ReadWatch(w.state)
	require.NoError(t, err)
	require.True(t, found, "the first run saves its cursor even when nothing came")

	for _, s := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		w.send(t, "bob", "ada", s)
	}
	first := w.watch(t, "--timeout", "1m").Exit(0)
	assert.Equal(t, 5, strings.Count(first.Stdout, "WATCH MESSAGE"), "five lines at most")
	assert.Contains(t, first.Stdout, "subject=\"five\"")
	assert.NotContains(t, first.Stdout, "subject=\"six\"")
	mid, _, err := friend.ReadWatch(w.state)
	require.NoError(t, err)
	assert.NotEqual(t, saved.After, mid.After, "the cursor moved past what was printed")

	second := w.watch(t, "--timeout", "1m").Exit(0)
	assert.Equal(t, 2, strings.Count(second.Stdout, "WATCH MESSAGE"), "the two past the five are what the next run gives")
	assert.Contains(t, second.Stdout, "subject=\"six\"")
	assert.Contains(t, second.Stdout, "subject=\"seven\"")
	assert.NotContains(t, second.Stdout, "subject=\"one\"")

	w.watch(t, "--timeout", "2s").Exit(1)
	end, _, err := friend.ReadWatch(w.state)
	require.NoError(t, err)
	tail, _, err := w.store.Tail(context.Background(), bus.StreamOf("ada"))
	require.NoError(t, err)
	assert.Equal(t, tail, end.After)

	// the cursor file is written whole and renamed: nothing else is left beside it
	names, err := os.ReadDir(w.state)
	require.NoError(t, err)
	for _, n := range names {
		assert.Contains(t, []string{friend.WatchFile}, n.Name())
	}
	raw, err := os.ReadFile(filepath.Join(w.state, friend.WatchFile))
	require.NoError(t, err)
	assert.True(t, json.Valid(raw))
}

// Its own messages, ping, pong, daemon-pong and keepalive never wake it, and
// the cursor moves past them.
func TestWatchSkipsOwnPingPongAndKeepalive(t *testing.T) {
	t.Parallel()
	w := newWatchRig(t)
	w.arm(t)
	w.send(t, "ada", "ada", "a note to myself")
	w.send(t, "bob", "ada", "PING abc123")
	w.send(t, "bob", "ada", "pong")
	w.send(t, "bob", "ada", "daemon-pong")
	w.send(t, "bob", "ada", "Keepalive")
	w.watch(t, "--timeout", "3s").Exit(1).Err("WATCH NONE waited=3s")
	tail, _, err := w.store.Tail(context.Background(), bus.StreamOf("ada"))
	require.NoError(t, err)
	cur, _, err := friend.ReadWatch(w.state)
	require.NoError(t, err)
	assert.Equal(t, tail, cur.After, "a skipped entry moves the cursor")

	id := w.send(t, "bob", "ada", "ready for the next card")
	got := w.watch(t, "--timeout", "1m").Exit(0)
	got.Out("WATCH MESSAGE id=" + id)
	assert.Equal(t, 1, strings.Count(got.Stdout, "WATCH MESSAGE"))
}

// Past --timeout it is WATCH NONE at exit 1, on the injected clock.
func TestWatchTimesOutAsNone(t *testing.T) {
	t.Parallel()
	w := newWatchRig(t)
	w.watch(t, "--timeout", "5s").Exit(1).Err("WATCH NONE waited=5s")
	got := w.watch(t, "--timeout", "5s", "--json").Exit(1)
	var v struct {
		Status, Word, After, Waited string
		Wakes                       []map[string]string
	}
	require.NoError(t, json.Unmarshal([]byte(got.Stdout), &v))
	assert.Equal(t, "NONE", v.Word)
	assert.Equal(t, "5s", v.Waited)
	assert.Empty(t, v.Wakes)
	w.cli().Do(t, "watch", "--as", "ada", "--timeout", "-1s").Exit(2).Err("--timeout wants a duration of at least 0")
}

// TestWatchHelpExampleIsWhatTheToolPrints runs the watch verb's help example as
// written, over the fake store and its injected clock, through the one
// comparator: the line a reader pastes prints the line the help shows
// (docs/SPEC-FRIEND.md, Watch).
func TestWatchHelpExampleIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	step := onboarding.Step{
		Line: "$ nova-friend watch --as ada --timeout 10m",
		Args: []string{"watch", "--as", "ada", "--timeout", "10m"},
		Want: []string{onboarding.StderrMarker + "WATCH NONE waited=10m0s"},
	}
	doc, err := os.ReadFile("../../docs/CLI.md")
	require.NoError(t, err)
	assert.Contains(t, string(doc), strings.TrimPrefix(step.Line, "$ "), "the executed command is also in the reference")
	var out, errb strings.Builder
	w := newRig(t, "ada", "bob").world()
	code := run(step.Args, strings.NewReader(""), &out, &errb, w)
	got := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
	require.Equal(t, 1, code, errb.String())
	for _, p := range onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{got}, nil) {
		assert.Fail(t, "the help example differs", p.Message)
	}
}
