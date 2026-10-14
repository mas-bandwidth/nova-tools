package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var codexArgs = []string{"hook", "--as", "ada", "--role", "comms", "--codex-socket", "/private/socket", "--codex-thread", "thread-1", "--codex-events-dir", "/private/events"}

func fakeCodexWorld(r *rig) world {
	w := r.world()
	w.codexLock = func(string) (func() error, error) { return func() error { return nil }, nil }
	w.codexValidate = func(string, string, string) error { return nil }
	w.files = filesFunc(func(string, []byte, bool) error { return nil })
	w.codexDeliver = func(context.Context, string, string, string, string) (string, error) { return "native-turn", nil }
	return w
}

func TestCodexRouteRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		args     []string
		validate error
	}{
		{"bad cutover", append(append([]string{}, codexArgs...), "--codex-since", "now"), nil},
		{"offset cutover", append(append([]string{}, codexArgs...), "--codex-since", "2026-10-11T12:00:00+01:00"), nil},
		{"partial", []string{"hook", "--as", "ada", "--role", "comms", "--codex-thread", "thread-1"}, nil},
		{"owner unavailable", codexArgs, errors.New("owner is unavailable")},
		{"once", append(append([]string{}, codexArgs...), "--once"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := fakeCodexWorld(r)
			w.codexValidate = func(string, string, string) error { return tc.validate }
			var out bytes.Buffer
			code := run(tc.args, strings.NewReader(""), &out, io.Discard, w)
			assert.Equal(t, 2, code, out.String())
			assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
		})
	}
}

func TestCodexBootstrapAcceptedDoesNotProve(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	saved := map[string]notice.Envelope{}
	w.files = filesFunc(func(path string, data []byte, _ bool) error {
		var e notice.Envelope
		require.NoError(t, json.Unmarshal(data, &e))
		saved[e.EventID] = e
		return nil
	})
	calls := 0
	w.codexDeliver = func(_ context.Context, _, _, id, _ string) (string, error) {
		calls++
		e := saved[id]
		assert.Equal(t, "ada", e.LocalReceiver.Identity)
		assert.Equal(t, "127.0.0.1:6379", e.LocalReceiver.Redis)
		require.NotNil(t, e.Challenge, "the bootstrap challenge is the first notice")
		assert.Equal(t, "2026-10-11T12:00:00Z", e.RecoverySince)
		assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
		return "accepted-turn", nil
	}
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	var out bytes.Buffer
	code := run(append(append([]string{}, codexArgs...), "--codex-since", "2026-10-11T12:00:00Z"), strings.NewReader(""), &out, io.Discard, w)
	assert.Zero(t, code, out.String())
	assert.Equal(t, 1, calls)
	assert.Contains(t, out.String(), "receipt=accepted-turn")
	assert.Contains(t, out.String(), "recovery-cutover")
	assert.Contains(t, out.String(), "archived history")
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
}

// A deterministic waiter advances only the fake clock; no wall time or socket.
type codexWaiter struct {
	bus.Store
	read func() []bus.Entry
}

func (s codexWaiter) Tail(context.Context, string) (string, bool, error) { return "0-0", true, nil }
func (s codexWaiter) BlockRead(context.Context, string, string, time.Duration, int) ([]bus.Entry, error) {
	return s.read(), nil
}

func TestCodexDryRunDoesNotContactNativeAppOrWriteEvents(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	w.codexValidate = func(string, string, string) error { t.Fatal("dry-run must not connect native IPC"); return nil }
	w.files = filesFunc(func(string, []byte, bool) error { t.Fatal("dry-run must not save events"); return nil })
	args := append(append([]string{}, codexArgs...), "--dry-run")
	var out bytes.Buffer
	code := run(args, strings.NewReader(""), &out, io.Discard, w)
	assert.Zero(t, code, out.String())
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
}

// filesFunc is a fake notice store: a test sees every saved notice.
type filesFunc func(path string, data []byte, replace bool) error

func (f filesFunc) Save(path string, data []byte, replace bool) error { return f(path, data, replace) }
