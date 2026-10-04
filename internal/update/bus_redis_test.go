package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// fakeRedisBus is the Redis bus the delivery tests send into: bus.Fake behind
// the Environment.Bus seam, so no test opens a socket or starts a process. It
// records the address it was asked for.
type fakeRedisBus struct {
	*bus.Fake
	dialed []string
}

func newFakeRedisBus(names ...string) *fakeRedisBus {
	return &fakeRedisBus{Fake: bus.NewFake(time.Now(), names...)}
}

func (f *fakeRedisBus) env() Environment {
	return Environment{Bus: func(_ context.Context, addr string) (bus.Store, func(), error) {
		f.dialed = append(f.dialed, addr)
		return f.Fake, func() {}, nil
	}}
}

// log is the bus's log, oldest first.
func (f *fakeRedisBus) log(t *testing.T) []bus.Message {
	t.Helper()
	entries, err := (&bus.Bus{Store: f.Fake}).Log(context.Background(), "-")
	require.NoError(t, err)
	var out []bus.Message
	for _, e := range entries {
		out = append(out, e.Message())
	}
	return out
}

// SPEC-UPDATE rules 24, 25 and 27: report --send, watch --redis and the
// adoption receipt send through the Redis bus (internal/bus), never through a
// git bus or a nova-bus child. The test fakes the bus client: no process, no
// socket.
func TestSendWatchAndAdoptUseTheRedisBus(t *testing.T) {
	t.Parallel()

	fb := newFakeRedisBus("fixture", "integrator", "coordinator", "duty")
	env := fb.env()
	addr := "127.0.0.1:6381"
	p := manifest(t, row("x", "tool", "v1.2.3", "npm:unused", "none"))
	snap := filepath.Join(t.TempDir(), "s.json")
	send := []string{"report", "--file", p, "--send", "--snapshot", snap, "--as", "fixture", "--to", "integrator", "--redis", addr, "--host", "studio"}

	t.Run("report --send puts one message on the recipient's stream and the log", func(t *testing.T) {
		c, out, errs := run(t, env, send...)
		require.EqualValuesf(t, 0, c, "%s\n%s", out, errs)
		need(t, out, "REPORT SENT to=integrator", `SEND\x20OK\x20id\x3d`)
		assert.NotContains(t, out+errs, "prepared", "no prepared artifact is part of the Redis bus")
		msgs := fb.log(t)
		require.Len(t, msgs, 1)
		m := msgs[0]
		assert.Equal(t, "fixture", m.From)
		assert.Equal(t, []string{"integrator"}, m.To)
		assert.True(t, strings.HasPrefix(m.Subject, "versions on studio at "), m.Subject)
		assert.Contains(t, m.Body, "REPORT")
		assert.NotContains(t, m.Body, "From: ", "the headers are the message's fields, not lines of its body")
		assert.Equal(t, 1, fb.Len(bus.StreamOf("integrator")))
		assert.Equal(t, []string{addr}, fb.dialed)
	})

	t.Run("an unchanged report sends nothing", func(t *testing.T) {
		c, out, errs := run(t, env, send...)
		require.EqualValuesf(t, 0, c, "%s\n%s", out, errs)
		need(t, out+errs, "unchanged since")
		assert.Len(t, fb.log(t), 1)
	})

	t.Run("watch --redis posts the adoption receipt as the coordinator", func(t *testing.T) {
		checks := filepath.Join(t.TempDir(), "checks.tsv")
		// A check whose command is not found is refused without starting anything.
		rows := []string{"check\tcommand\towner", "snapshot-report\tnova-update-no-such-tool-fixture\trowan"}
		require.NoError(t, os.WriteFile(checks, []byte(strings.Join(rows, "\n")+"\n"), 0o600))
		c, out, errs := run(t, env, "watch", "--adopt", checks, "--redis", addr, "--as", "coordinator", "--to", "duty", "--host", "studio")
		combined := out + "\n" + errs
		require.EqualValuesf(t, 1, c, "one refused check: %s", combined)
		need(t, combined, "ADOPT SENT to=duty", `SEND\x20OK\x20id\x3d`, "ADOPT DONE sha=")
		msgs := fb.log(t)
		require.Len(t, msgs, 2)
		assert.Equal(t, "coordinator", msgs[1].From)
		assert.Equal(t, []string{"duty"}, msgs[1].To)
		assert.True(t, strings.HasPrefix(msgs[1].Subject, "adoption on studio at "), msgs[1].Subject)
		assert.Contains(t, msgs[1].Body, "ADOPT REFUSED check=snapshot-report")
		assert.Contains(t, msgs[1].Body, "ADOPT ESCALATE")
	})

	t.Run("the redis flags go together and a bus flag of the git bus is gone", func(t *testing.T) {
		checks := filepath.Join(t.TempDir(), "checks.tsv")
		require.NoError(t, os.WriteFile(checks, []byte("check\tcommand\towner\n"), 0o600))
		c, _, errs := run(t, env, "watch", "--adopt", checks, "--redis", addr)
		assert.EqualValues(t, 2, c)
		need(t, errs, "missing --as, --to")
		c, _, errs = run(t, env, "watch", "--adopt", checks, "--bus", "b", "--remote", "r", "--branch", "m")
		assert.EqualValues(t, 2, c)
		need(t, errs, "unknown flag --bus", "--redis")
	})
}

// SPEC-UPDATE rule 25: an unconfirmed send keeps its note pending; the next
// --send resolves it first, and a message that did land before the answer was
// lost is found on the log, never sent twice.
func TestAnUnconfirmedSendIsResolvedOnTheNextRunWithoutDuplicating(t *testing.T) {
	t.Parallel()

	fb := newFakeRedisBus("fixture", "integrator")
	p := manifest(t, row("x", "tool", "v1.2.3", "npm:unused", "none"))
	snap := filepath.Join(t.TempDir(), "s.json")
	args := []string{"report", "--file", p, "--send", "--snapshot", snap, "--as", "fixture", "--to", "integrator", "--redis", "127.0.0.1:6381"}

	// The store is down: nothing is sent, the note stays pending.
	fb.Fail = errors.New("connection refused")
	c, out, errs := run(t, fb.env(), args...)
	require.EqualValuesf(t, 1, c, "%s\n%s", out, errs)
	need(t, errs, "pending", "not confirmed", "SEND FAIL")
	s, err := readSnapshot(snap)
	require.NoError(t, err)
	require.Len(t, s.Pending, 1)
	var pendingID string
	for _, v := range s.Pending {
		pendingID = v.ID
	}

	// The message lands and the answer is lost: the run is uncertain.
	fb.Fail = nil
	lost := &lostAnswer{Store: fb.Fake}
	env := fb.env()
	env.Bus = func(context.Context, string) (bus.Store, func(), error) { return lost, func() {}, nil }
	c, _, _ = run(t, env, args...)
	require.EqualValues(t, 1, c)
	require.Len(t, fb.log(t), 1, "the message landed")

	// The next run finds it on the log and confirms; it sends nothing more.
	c, out, errs = run(t, fb.env(), args...)
	require.EqualValuesf(t, 0, c, "%s\n%s", out, errs)
	need(t, out, `state\x3dalready-published`)
	require.Len(t, fb.log(t), 1, "no second message")
	s, err = readSnapshot(snap)
	require.NoError(t, err)
	assert.Empty(t, s.Pending)
	for _, d := range s.Delivered {
		assert.Equal(t, fb.log(t)[0].ID, d.ID, "delivered carries the bus's message id")
		assert.NotEqual(t, pendingID, d.ID)
	}
}

// lostAnswer is a store whose transaction lands and whose reply is lost.
type lostAnswer struct{ bus.Store }

func (l *lostAnswer) AddAll(ctx context.Context, streams []string, fields map[string]string) error {
	if err := l.Store.AddAll(ctx, streams, fields); err != nil {
		return err
	}
	return errors.New("i/o timeout")
}
