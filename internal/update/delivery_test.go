package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// These tests check the caller's persisted state machine over the Redis bus
// (SPEC-UPDATE rules 24 and 25) with the bus faked behind Environment.Bus: no
// process, no socket.

// pendingJSON is a saved note as a snapshot holds it.
func pendingJSON(subject, note string) string {
	n := newPending(subject, note, time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC))
	b, _ := json.Marshal(map[string]string{"schema": n.Schema, "id": n.ID, "subject": n.Subject, "note": n.Note, "sha256": n.SHA256, "at": n.At})
	return string(b)
}

func sendArgs(manifestPath, snap string) []string {
	return []string{"report", "--file", manifestPath, "--send", "--snapshot", snap, "--as", "fixture", "--to", "integrator", "--redis", "127.0.0.1:6381"}
}

func TestNewObservationCannotReplaceUnresolvedPending(t *testing.T) {
	t.Parallel()

	fb := newFakeRedisBus("fixture", "integrator")
	p := manifest(t, row("x", "tool", "v1.0.0", "npm:unused", "none"))
	sp := filepath.Join(t.TempDir(), "s.json")
	args := sendArgs(p, sp)
	fb.Fail = errors.New("connection refused")
	run(t, fb.env(), args...)
	s, _ := readSnapshot(sp)
	var old string
	for _, v := range s.Pending {
		old = v.ID
	}
	require.NotEmpty(t, old)
	require.NoError(t, os.WriteFile(p, []byte(Header+"\n"+row("x", "tool", "v2.0.0", "npm:unused", "none")+"\n"), 0o600))
	c, _, _ := run(t, fb.env(), args...)
	require.EqualValues(t, 1, c)
	s, _ = readSnapshot(sp)
	require.Len(t, s.Pending, 1)
	for _, v := range s.Pending {
		require.Equal(t, old, v.ID, "pending was replaced")
		require.Equal(t, "1.0.0", v.Observed["x"].Raw)
	}
	fb.Fail = nil
	c, o, e := run(t, fb.env(), args...)
	require.EqualValuesf(t, 0, c, "%s %s", o, e)
	s, _ = readSnapshot(sp)
	require.Empty(t, s.Pending, "pending not cleared")
	require.Len(t, s.Delivered, 1)
	for _, v := range s.Delivered {
		require.Equal(t, "2.0.0", v.Observed["x"].Raw)
		require.NotEqual(t, old, v.ID)
	}
	// The older report went first, then the newer: two messages, in order.
	msgs := fb.log(t)
	require.Len(t, msgs, 2)
	require.Contains(t, msgs[0].Body, "1.0.0")
	require.Contains(t, msgs[1].Body, "2.0.0")
}

func TestDeliveryScopeAndPendingNoteChecks(t *testing.T) {
	t.Parallel()

	o := options{as: "a", to: "c,b", redis: "127.0.0.1:6381", host: "studio"}
	same := o
	same.to = "b,c"
	require.Equal(t, snapshotScope(o), snapshotScope(same), "recipient order changed scope")
	other := o
	other.host = "air"
	require.NotEqual(t, snapshotScope(o), snapshotScope(other), "bench silently shared delivery state")
	other = o
	other.redis = "127.0.0.1:6382"
	require.NotEqual(t, snapshotScope(o), snapshotScope(other), "a second bus silently shared delivery state")
	b := pendingJSON("versions on x at y", "synthetic\n")
	a, err := validatePending([]byte(b))
	require.NoError(t, err)
	require.Equal(t, "synthetic\n", a.Note)
	_, err = validatePending(append([]byte(b), []byte("{}")...))
	require.Error(t, err, "trailing note accepted")
	_, err = validatePending([]byte(strings.Replace(b, `synthetic\n`, `changed\n`, 1)))
	require.Error(t, err, "wrong digest accepted")
}

// A second value for one field of a pending note or a snapshot is an
// ambiguous identity. Both readers must refuse it, and must do so without
// quoting the offending key or any of the note back into the diagnostic.
func TestStrictDecodingRefusesAmbiguousAndWrongInput(t *testing.T) {
	t.Parallel()

	note, subject := "a note\n", "versions on x at y"
	sum := shaText(subject + "\n" + note)
	const at = "2026-10-04T12:00:00Z"
	good := fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":%q,"sha256":%q,"at":%q}`, subject, note, sum, at)
	a, err := validatePending([]byte(good))
	require.NoErrorf(t, err, "valid note refused")
	require.Equal(t, "update-1", a.ID)
	secret := "tell-nobody"
	bad := map[string]string{
		"duplicate id":         fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","id":"update-2","subject":%q,"note":%q,"sha256":%q,"at":%q}`, subject, note, sum, at),
		"duplicate note":       fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":%q,"note":%q,"sha256":%q,"at":%q}`, subject, note, secret+"\n", sum, at),
		"unknown field":        fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":%q,"sha256":%q,"at":%q,"extra":%q}`, subject, note, sum, at, secret),
		"wrong type":           fmt.Sprintf(`{"schema":"nova.update.pending/1","id":7,"subject":%q,"note":%q,"sha256":%q,"at":%q}`, subject, note, sum, at),
		"trailing data":        good + `{"schema":"nova.update.pending/1"}`,
		"digest mismatch":      fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":%q,"sha256":%q,"at":%q}`, subject, note, shaText(secret), at),
		"note without newline": fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":"no lf","sha256":%q,"at":%q}`, subject, shaText(subject+"\nno lf"), at),
		"a subject of two":     fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":"a\nb","note":%q,"sha256":%q,"at":%q}`, note, shaText("a\nb\n"+note), at),
		"no instant":           fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":%q,"sha256":%q,"at":"soon"}`, subject, note, sum),
		"the git bus's schema": fmt.Sprintf(`{"schema":"nova.bus.prepared/1","id":"update-1","path":"p","note":%q,"sha256":%q}`, note, sum),
		"too deeply nested":    `{"schema":` + strings.Repeat("[", 40) + strings.Repeat("]", 40) + "}",
	}
	for name, raw := range bad {
		_, err := validatePending([]byte(raw))
		if !assert.Errorf(t, err, "%s accepted", name) {
			continue
		}
		assert.NotContainsf(t, err.Error(), secret, "%s diagnostic echoed content: %v", name, err)
		assert.NotContainsf(t, err.Error(), "a note", "%s diagnostic echoed content: %v", name, err)
	}
	dir := t.TempDir()
	dup := filepath.Join(dir, "dup.json")
	require.NoError(t, os.WriteFile(dup, []byte(`{"observed":{},"observed":{"x":{"raw":"`+secret+`","status":"tool","at":"t"}},"delivered":{},"pending":{}}`), 0o600))
	_, err = readSnapshot(dup)
	require.Error(t, err, "ambiguous snapshot accepted")
	require.NotContains(t, err.Error(), secret, "snapshot diagnostic echoed content")
	b, _ := os.ReadFile(dup)
	require.Contains(t, string(b), secret, "refusal did not preserve the snapshot byte for byte")
}

// Go's decoder matches a struct field case-insensitively, so refusing only
// BYTE-identical duplicate keys left the ambiguity it was written to close: two
// keys that fold to one field still carried two values, and the LAST won.
func TestStrictDecodingRefusesKeysThatFoldTogether(t *testing.T) {
	t.Parallel()

	note, subject := "a note\n", "versions on x at y"
	sum := shaText(subject + "\n" + note)
	tail := fmt.Sprintf(`"subject":%q,"note":%q,"sha256":%q,"at":"2026-10-04T12:00:00Z"`, subject, note, sum)
	for name, raw := range map[string]string{
		"id and ID":         `{"schema":"nova.update.pending/1","id":"update-1","ID":"update-2",` + tail + `}`,
		"id and Id":         `{"schema":"nova.update.pending/1","id":"update-1","Id":"update-2",` + tail + `}`,
		"ID alone":          `{"schema":"nova.update.pending/1","ID":"update-2",` + tail + `}`,
		"long s in a key":   fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":%q,"ſha256":%q,"at":"2026-10-04T12:00:00Z"}`, subject, note, sum),
		"a key short":       fmt.Sprintf(`{"schema":"nova.update.pending/1","id":"update-1","subject":%q,"note":%q}`, subject, note),
		"kelvin in a key":   `{"schema":"nova.update.pending/1","id":"update-1",` + tail + `,"K":"x"}`,
		"schema and Schema": `{"schema":"nova.update.pending/1","Schema":"nova.update.pending/1","id":"update-1",` + tail + `}`,
	} {
		_, err := validatePending([]byte(raw))
		assert.Errorf(t, err, "%s accepted", name)
	}
	_, err := validatePending([]byte(`{"schema":"nova.update.pending/1","id":"update-1",` + tail + `}`))
	require.NoError(t, err, "the exact note was refused")
	dir := t.TempDir()
	for name, body := range map[string]string{
		"observed and Observed":   `{"observed":{"x":{"raw":"one","status":"tool","at":"t"}},"Observed":{"x":{"raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{},"pending":{}}`,
		"nested raw and Raw":      `{"observed":{"x":{"raw":"one","Raw":"tell-nobody","status":"tool","at":"t"}},"delivered":{},"pending":{}}`,
		"delivered and DELIVERED": `{"observed":{},"delivered":{},"DELIVERED":{},"pending":{}}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		s, err := readSnapshot(p)
		if !assert.Errorf(t, err, "%s accepted: %+v", name, s) {
			continue
		}
		assert.NotContainsf(t, err.Error(), "tell-nobody", "%s echoed content", name)
	}
}

// A failed delivery says what the bus or the store said, as one clipped line,
// and never a note body or a secret (SPEC-UPDATE rule 24).
func TestTheBusOwnWordsReachTheCallerBoundedToOneLine(t *testing.T) {
	t.Parallel()

	got := busSaid(&bus.Refusal{Problems: []string{"a is no known name", "b is no known name"}})
	require.True(t, strings.HasPrefix(got, "SEND REFUSED: a is no known name"), got)
	require.NotContains(t, got, "\n")
	got = busSaid(errors.New("dial tcp 127.0.0.1:6381: connect: connection refused\nsecond line"))
	require.True(t, strings.HasPrefix(got, "SEND FAIL dial tcp"), got)
	require.NotContains(t, got, "\n")
	got = busSaid(errors.New(strings.Repeat("x", 500)))
	require.Lenf(t, got, len("SEND FAIL ")+203, "unbounded: %d bytes", len(got))
	require.True(t, strings.HasSuffix(got, "..."))

	fb := newFakeRedisBus("fixture", "integrator")
	p := manifest(t, row("x", "tool", "v1.2.3", "npm:unused", "none"))
	sp := filepath.Join(t.TempDir(), "s.json")
	// A recipient the roster does not hold: the bus refuses, the note stays pending.
	args := sendArgs(p, sp)
	args[slices.Index(args, "integrator")] = "nobody"
	c, _, errout := run(t, fb.env(), args...)
	require.EqualValues(t, 1, c)
	need(t, errout, "SEND REFUSED: ", "nobody is no known name")
	var notes int
	for _, line := range strings.Split(errout, "\n") {
		if strings.HasPrefix(line, "REPORT NOTE") {
			notes++
			require.LessOrEqual(t, len(line), 700, line)
		}
	}
	require.Equal(t, 1, notes, "one refusal became several lines:\n%s", errout)
	require.Empty(t, fb.log(t))
}

// blockingBus is a store that never answers until its context ends.
type blockingBus struct{ bus.Store }

func (blockingBus) Roster(ctx context.Context) ([]string, time.Time, error) {
	<-ctx.Done()
	return nil, time.Time{}, ctx.Err()
}

// A bus that never answers is bounded by the BUDGET, not by the version probe's
// --timeout (SPEC-UPDATE rule 24): the send ends inside the allowance with the
// note pending, and the same --send resolves it once the bus answers.
func TestABusThatNeverAnswersIsCutOffByTheBudget(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		p := manifest(t, row("x", "tool", "v1.2.3", "npm:unused", "none"))
		sp := filepath.Join(t.TempDir(), "s.json")
		args := append(sendArgs(p, sp), "--budget", "10s")
		hang := Environment{Bus: func(context.Context, string) (bus.Store, func(), error) { return blockingBus{}, func() {}, nil }}
		start := time.Now()
		c, _, errout := run(t, hang, args...)
		require.EqualValues(t, 1, c)
		need(t, errout, "sent=uncertain", "not confirmed")
		require.LessOrEqual(t, time.Since(start), 10*time.Second, "the budget did not bound the send")
		s, err := readSnapshot(sp)
		require.NoError(t, err)
		require.Len(t, s.Pending, 1)
		require.Empty(t, s.Delivered)
		fb := newFakeRedisBus("fixture", "integrator")
		c, out, errout := run(t, fb.env(), args...)
		require.EqualValuesf(t, 0, c, "%s %s", out, errout)
		need(t, out, "REPORT SENT")
		c, out, _ = run(t, fb.env(), args...)
		require.EqualValues(t, 0, c)
		need(t, out, "nothing sent", "sent=no")
		require.Len(t, fb.log(t), 1, "an unchanged confirmed run sent again")
	})
}

// A store that answers in a line of its own must not be able to put more than
// one clipped line on the caller's event line, and a password in its words
// is not part of what this tool relays: it relays the connection's error only.
func TestAStoreFailureIsOneLineOnTheEventLine(t *testing.T) {
	t.Parallel()

	fb := newFakeRedisBus("fixture", "integrator")
	fb.Fail = errors.New("NOAUTH Authentication required.\nand more words on a second line")
	p := manifest(t, row("x", "tool", "v1.2.3", "npm:unused", "none"))
	c, out, errs := run(t, fb.env(), sendArgs(p, filepath.Join(t.TempDir(), "s.json"))...)
	require.EqualValues(t, 1, c)
	need(t, errs, "SEND FAIL NOAUTH")
	require.NotContains(t, out+errs, "second line\n")
}
