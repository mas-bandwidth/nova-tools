package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watch_test.go pins nova-friend watch: the coordinator's wake on the bus,
// the wake file and an event (docs/SPEC-FRIEND.md, Watch). The store is the
// package's fake, and the clock moves only when a blocking read waits, so
// no test sleeps or opens a socket.

// watchRig is the friend rig with a state directory and a clock the fake
// store's block moves.
type watchRig struct {
	*rig
	state string
}

func newWatchRig(t *testing.T) *watchRig {
	t.Helper()
	r := newRig(t, "ada", "bob")
	r.now = start
	return &watchRig{rig: r, state: t.TempDir()}
}

// cli runs the tool on the rig's fake store and a clock that does not move
// until a blocking read waits it out.
func (r *watchRig) cli() testkit.Main {
	w := r.world()
	w.now = func() time.Time { return r.now }
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}

func (r *watchRig) wireClock() {
	r.store.Sleep = func(d time.Duration) { r.now = r.now.Add(d) }
}

// onSleep runs fn the next time a blocking read finds nothing, once.
func (r *watchRig) onSleep(fn func()) {
	r.store.Sleep = func(time.Duration) {
		r.store.Sleep = nil
		fn()
	}
}

// drop appends one message to ada's stream, the coordinator the tests watch.
func (r *watchRig) drop(from, id, subject string) {
	m := bus.Message{ID: id, From: from, To: []string{"ada"}, Subject: subject, Body: "the body"}
	if err := r.store.AddAll(context.Background(), []string{bus.StreamOf("ada")}, m.Fields()); err != nil {
		panic("drop: the fake store refused the message: " + err.Error())
	}
}

func (r *watchRig) args(extra ...string) []string {
	return append([]string{"watch", "--as", "ada", "--state-dir", r.state}, extra...)
}

// watchAfter is the cursor a WATCH OK line names.
func watchAfter(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	v, ok := strings.CutPrefix(lines[len(lines)-1], "WATCH OK after=")
	require.True(t, ok, "no WATCH OK after= line in %q", out)
	return v
}

// A real message, a wake-file line, or an event each ends the watch
// (docs/SPEC-FRIEND.md, Watch).
func TestWatchWakesOnARealMessageAWakeLineOrAnEvent(t *testing.T) {
	t.Parallel()
	t.Run("message", func(t *testing.T) {
		t.Parallel()
		r := newWatchRig(t)
		r.onSleep(func() { r.drop("bob", "m1", "hello") })
		got := r.cli().Do(t, r.args()...).Exit(0)
		assert.Equal(t, "WATCH MESSAGE id=m1 from=bob subject=hello\nWATCH OK after="+watchAfter(t, got.Stdout)+"\n", got.Stdout)
		assert.Empty(t, got.Stderr)
		saved, off, found, err := friend.ReadWatchCursor(r.state)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, watchAfter(t, got.Stdout), saved)
		assert.GreaterOrEqual(t, off, int64(0))

		j := newWatchRig(t)
		j.onSleep(func() { j.drop("bob", "m1", "hello") })
		raw := j.cli().Do(t, j.args("--json")...).Exit(0)
		var obj struct {
			Status   string `json:"status"`
			Word     string `json:"word"`
			After    string `json:"after"`
			Messages []struct {
				ID, From, Subject string
			} `json:"messages"`
			Events []struct {
				ID, From, Subject string
			} `json:"events"`
			Wake json.RawMessage `json:"wake"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(raw.Stdout)), &obj))
		assert.Equal(t, "ok", obj.Status)
		assert.Equal(t, "OK", obj.Word)
		require.Len(t, obj.Messages, 1)
		assert.Equal(t, "m1", obj.Messages[0].ID)
		assert.Equal(t, "bob", obj.Messages[0].From)
		assert.Equal(t, "hello", obj.Messages[0].Subject)
		assert.Empty(t, obj.Events)
		assert.Empty(t, obj.Wake)
		assert.NotContains(t, raw.Stdout, "\n{")
	})
	t.Run("wake line", func(t *testing.T) {
		t.Parallel()
		r := newWatchRig(t)
		wake := friend.ClaudeWakePath(r.state, "ada")
		r.onSleep(func() {
			require.NoError(t, os.WriteFile(wake, []byte("session: 1 message waiting\n"), 0o644))
		})
		got := r.cli().Do(t, r.args()...).Exit(0)
		assert.Equal(t, "WATCH WAKE line=\"session: 1 message waiting\"\nWATCH OK after=0-0\n", got.Stdout)
	})
	t.Run("event", func(t *testing.T) {
		t.Parallel()
		r := newWatchRig(t)
		const subject = "event: machine stopped unasked"
		r.onSleep(func() { r.drop("bob", "e1", subject) })
		got := r.cli().Do(t, r.args()...).Exit(0)
		assert.Equal(t, "WATCH EVENT id=e1 from=bob subject="+oneline.Field(subject)+"\nWATCH OK after="+watchAfter(t, got.Stdout)+"\n", got.Stdout)
		assert.NotContains(t, got.Stdout, "WATCH MESSAGE")
	})
}

// The cursor saved after a run is where the next run starts, so a message
// past the five-line cap, and one that lands between runs, is still seen,
// and one already shown is not (docs/SPEC-FRIEND.md, Watch).
func TestWatchSavesTheCursorSoTheNextRunMissesNothing(t *testing.T) {
	t.Parallel()
	r := newWatchRig(t)
	r.onSleep(func() {
		for i := 1; i <= 6; i++ {
			r.drop("bob", "m"+string(rune('0'+i)), "n"+string(rune('0'+i)))
		}
	})
	one := r.cli().Do(t, r.args()...).Exit(0)
	assert.Equal(t, 5, strings.Count(one.Stdout, "WATCH MESSAGE"), "one run prints at most five: %s", one.Stdout)
	assert.NotContains(t, one.Stdout, "id=m6")
	assert.Contains(t, one.Stdout, "id=m1")
	assert.Contains(t, one.Stdout, "id=m5")
	after := watchAfter(t, one.Stdout)
	saved, _, found, err := friend.ReadWatchCursor(r.state)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, after, saved)
	r.drop("bob", "m7", "n7")
	two := r.cli().Do(t, r.args()...).Exit(0)
	assert.Contains(t, two.Stdout, "WATCH MESSAGE id=m6 from=bob subject=n6")
	assert.Contains(t, two.Stdout, "WATCH MESSAGE id=m7 from=bob subject=n7")
	assert.NotContains(t, two.Stdout, "id=m1")
	assert.NotContains(t, two.Stdout, "id=m5")
}

// Messages from the watcher, and ping, pong, daemon-pong and keepalive,
// move the cursor and are not printed (docs/SPEC-FRIEND.md, Watch).
func TestWatchSkipsOwnPingPongAndKeepalive(t *testing.T) {
	t.Parallel()
	r := newWatchRig(t)
	r.onSleep(func() {
		r.drop("ada", "self", "note to self")
		r.drop("bob", "p1", "PING nonce")
		r.drop("bob", "p2", "pong")
		r.drop("bob", "p3", "daemon-pong")
		r.drop("bob", "p4", "keepalive")
		r.drop("bob", "real", "hello")
	})
	got := r.cli().Do(t, r.args()...).Exit(0)
	assert.Equal(t, "WATCH MESSAGE id=real from=bob subject=hello\nWATCH OK after="+watchAfter(t, got.Stdout)+"\n", got.Stdout)
	for _, skipped := range []string{"id=self", "id=p1", "id=p2", "id=p3", "id=p4", "PING", "pong", "daemon-pong", "keepalive"} {
		assert.NotContains(t, got.Stdout, skipped)
	}
}

// The example in the help runs as written: every word after the tool's name
// is an argument of the run (docs/SPEC-FRIEND.md, Watch).
func TestWatchHelpExampleRunsAsWritten(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	help := cli.Do(t, "watch", "-h").Exit(0).Stdout
	var example string
	for _, line := range strings.Split(help, "\n") {
		if rest, ok := strings.CutPrefix(line, "example: nova-friend "); ok {
			example = rest
		}
	}
	require.Equal(t, "watch --as ada --timeout 1s", example)
	got := cli.Do(t, strings.Fields(example)...).Exit(1)
	assert.Equal(t, "WATCH NONE waited=1s\n", got.Stderr)
	assert.Empty(t, strings.TrimSpace(got.Stdout))
}

// Past --timeout the watch is WATCH NONE at exit 1, waited out on the fake
// clock (docs/SPEC-FRIEND.md, Watch).
func TestWatchTimesOutAsNone(t *testing.T) {
	t.Parallel()
	r := newWatchRig(t)
	r.wireClock()
	got := r.cli().Do(t, r.args("--timeout", "5s")...).Exit(1)
	assert.Empty(t, strings.TrimSpace(got.Stdout))
	assert.Equal(t, "WATCH NONE waited=5s\n", got.Stderr)

	j := newWatchRig(t)
	j.wireClock()
	raw := j.cli().Do(t, j.args("--timeout", "5s", "--json")...).Exit(1)
	assert.Equal(t, `{"status":"ok","word":"NONE","after":"0-0","waited":"5s","messages":[],"events":[]}`+"\n", raw.Stdout)
	assert.Empty(t, raw.Stderr)
}
