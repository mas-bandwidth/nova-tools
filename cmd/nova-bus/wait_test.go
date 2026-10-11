package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wait_test.go pins the wait verb: the wake a harness runs beside a session,
// made general for any AI on the bus (docs/SPEC-BUS.md, the verbs: wait). It
// takes nothing -- a recv after the wait still delivers and acks -- and its
// cursor is the only state, the caller's to hold between runs. Every test
// drives the store's block through the fake's Sleep (the test's hand, no
// real time) and the wake file through the injected reader.

// dropped is one entry that arrives while a wait is parked on the store.
type dropped struct{ from, subject string }

// drop appends one message to bob's stream as from would: what arrives past
// the cursor the wait holds.
func drop(r *rig, from, id, subject string) {
	m := bus.Message{ID: id, From: from, To: []string{"bob"}, Subject: subject, Body: "the body"}
	if err := r.store.AddAll(context.Background(), []string{bus.StreamOf("bob")}, m.Fields()); err != nil {
		panic("drop: the fake store refused the message: " + err.Error())
	}
}

// sleepDrop makes the store's next empty block deliver the entries: what
// arrives while the wait is parked on it.
func sleepDrop(r *rig, ds ...dropped) {
	r.store.Sleep = func(time.Duration) {
		r.store.Sleep = nil
		for _, d := range ds {
			drop(r, d.from, "01J"+strings.ToUpper(d.subject), d.subject)
		}
	}
}

// afterOf is the cursor a wait's last line names: what to re-arm with.
func afterOf(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	v, ok := strings.CutPrefix(lines[len(lines)-1], "WAIT OK after=")
	require.True(t, ok, "no WAIT OK after= line in %q", out)
	return v
}

// A wait takes nothing: it reads the recipient's stream past a cursor with
// XREAD, never the consumer group, so the first entries that arrive for the
// recipient are printed and a recv after the wait still delivers and acks
// them (SPEC-BUS.md, the verbs: wait).
func TestWaitReturnsOnTheFirstMessageForMeWithoutTakingIt(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.OK(t, "send", "--as", "ada", "--to", "bob", "--subject", "hello", "--body", "the body")
	sleepDrop(r, dropped{"ada", "one"}, dropped{"ada", "two"})
	got := cli.Do(t, "wait", "--as", "bob").Exit(0)
	lines := strings.Split(strings.TrimSpace(got.Stdout), "\n")
	require.Len(t, lines, 4, "ARMED, one MESSAGE line per entry that counts, then OK: %q", got.Stdout)
	assert.Regexp(t, `^WAIT ARMED after=\d+-\d+$`, lines[0], "the cursor is the stream's last id at start")
	assert.Equal(t, "WAIT MESSAGE id=01JONE from=ada subject=one bytes=8", lines[1])
	assert.Equal(t, "WAIT MESSAGE id=01JTWO from=ada subject=two bytes=8", lines[2])
	assert.Regexp(t, `^WAIT OK after=\d+-\d+$`, lines[3])
	cli.Do(t, "recv", "--as", "bob", "--all").Exit(0).Out(`subject="hello"`, `subject="one"`, `subject="two"`)
	cli.Do(t, "peek", "--as", "bob").Exit(0).Out("PEEK OK pending=3 new=0")
}

// Entries from the waiter itself and subjects starting with a skip prefix
// (the default PING,PONG, matched without case) move the cursor and are not
// printed; a re-arm at the cursor the run printed does not see them again
// (SPEC-BUS.md, the verbs: wait).
func TestWaitSkipsMyOwnAndPingPongAndMovesTheCursor(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	sleepDrop(r,
		dropped{"bob", "note to self"},
		dropped{"ada", "PING nonce"},
		dropped{"ada", "pong"},
		dropped{"ada", "hello"},
	)
	got := cli.Do(t, "wait", "--as", "bob").Exit(0)
	assert.Contains(t, got.Stdout, "WAIT MESSAGE id=01JHELLO from=ada subject=hello bytes=8")
	assert.Equal(t, 1, strings.Count(got.Stdout, "WAIT MESSAGE"), "only the entry that counts prints: %q", got.Stdout)
	after := afterOf(t, got.Stdout)
	r.wireClock()
	got = cli.Do(t, "wait", "--as", "bob", "--after", after, "--timeout", "1s").Exit(1)
	got.Out("WAIT ARMED after=" + after)
	got.Err("WAIT NONE after=" + after + " waited=1s")
	assert.NotContains(t, got.Stdout, "WAIT MESSAGE", "the skipped entries are not the next run's either")
}

// The cursor is the caller's: a wait ends with the id to re-arm with, and a
// run that re-arms at it misses nothing that lands between two runs. The
// default arm (no --after) is the stream's last id read once at start, so a
// message already on the stream is not waited for again (SPEC-BUS.md, the
// verbs: wait).
func TestWaitArmedCursorMissesNothingBetweenRuns(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	sleepDrop(r, dropped{"ada", "first"})
	one := cli.Do(t, "wait", "--as", "bob", "--json").Exit(0)
	var got struct {
		Status   string `json:"status"`
		Word     string `json:"word"`
		After    string `json:"after"`
		Messages []struct {
			ID      string `json:"id"`
			From    string `json:"from"`
			Subject string `json:"subject"`
			Bytes   int    `json:"bytes"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(one.Stdout)), &got))
	assert.Equal(t, "ok", got.Status)
	assert.Equal(t, "OK", got.Word)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "01JFIRST", got.Messages[0].ID)
	assert.Equal(t, 8, got.Messages[0].Bytes)
	assert.Equal(t, fmt.Sprintf(`{"status":"ok","word":"OK","after":%q,"messages":[{"id":"01JFIRST","from":"ada","subject":"first","bytes":8}]}`, got.After),
		strings.TrimSpace(one.Stdout), "the JSON is one object with the help's fields")
	drop(r, "ada", "01JSECOND", "second")
	two := cli.Do(t, "wait", "--as", "bob", "--after", got.After).Exit(0)
	two.Out("WAIT ARMED after="+got.After, "WAIT MESSAGE id=01JSECOND from=ada subject=second bytes=8", "WAIT OK after=")
	r.wireClock()
	none := cli.Do(t, "wait", "--as", "bob", "--timeout", "1s").Exit(1)
	none.Err("WAIT NONE after=")
	assert.NotContains(t, none.Stdout, "WAIT MESSAGE", "the default arm is the stream's last id: the message already seen waits for no one")
}

// --wake-file also ends the wait when a line is appended to the file after
// the start: the file is read through the world's seam, so no test opens
// one (SPEC-BUS.md, the verbs: wait).
func TestWaitWakesOnALineAppendedToTheWakeFile(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.wake = []string{"", "session: 1 message waiting"}
	cli := r.cli()
	got := cli.Do(t, "wait", "--as", "bob", "--wake-file", "./bob.wake").Exit(0)
	assert.Contains(t, got.Stdout, `WAIT WAKE file=./bob.wake`)
	assert.Contains(t, got.Stdout, `line="session: 1 message waiting"`)
	assert.Contains(t, got.Stdout, "wake-after=")
	assert.Contains(t, got.Stdout, "wake-offset=27")
	r.wake = []string{"", "session: 2 waiting"}
	got = cli.Do(t, "wait", "--as", "bob", "--wake-file", "./bob.wake", "--json").Exit(0)
	var v waitJSON
	require.NoError(t, json.Unmarshal([]byte(got.Stdout), &v))
	assert.Equal(t, "WAKE", v.Word)
	assert.Equal(t, "session: 2 waiting", v.Wake.Line)
	assert.NotEmpty(t, v.WakeAfter)
	require.NotNil(t, v.WakeOffset)
	assert.Equal(t, int64(19), *v.WakeOffset)
}

// Past --timeout the wait is WAIT NONE at exit 1, waited out on the fake
// clock: no real time passes (SPEC-BUS.md, the verbs: wait).
func TestWaitTimesOutAsNoneAtExitOne(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.wireClock()
	cli := r.cli()
	got := cli.Do(t, "wait", "--as", "bob", "--timeout", "5s").Exit(1)
	assert.Equal(t, "WAIT ARMED after=0-0\n", got.Stdout)
	assert.Equal(t, "WAIT NONE after=0-0 waited=5s\n", got.Stderr)
	r.wireClock()
	got = cli.Do(t, "wait", "--as", "bob", "--timeout", "5s", "--json").Exit(1)
	assert.Equal(t, `{"status":"ok","word":"NONE","after":"0-0","messages":[]}`+"\n", got.Stdout)
}

// A wrong flag, a wrong timeout, a name off the roster and a store that does
// not answer are one refusal each, every problem named at once
// (docs/ONBOARDING point 2; SPEC-BUS.md, the exit codes).
func TestWaitRefusesEveryWrongThingAtOnce(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	cli := r.cli()
	cli.Do(t, "wait", "--as", "bob", "--after", "soon", "--timeout", "-1s").Exit(2).
		Err("WAIT REFUSED", "--after wants a stream entry id, <ms>-<seq>", "--timeout wants a duration of at least 0")
	cli.Do(t, "wait", "--as", "zed").Exit(2).Err("WAIT REFUSED", "zed is no known name", "nova-config friend add zed")
	cli.Do(t, "wait").Exit(2).Err("WAIT REFUSED", "--as is required")
	r.openErr = io.ErrUnexpectedEOF
	cli.Do(t, "wait", "--as", "bob").Exit(2).Err("WAIT REFUSED", "unexpected EOF")
}
