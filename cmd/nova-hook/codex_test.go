package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
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
	w.codexChats = func(string) ([]codexdesktop.Chat, error) { return nil, nil }
	w.antigravityHome = func() (string, error) { return r.home, nil }
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
		{"no chat to find", []string{"hook", "--as", "ada", "--role", "comms", "--harness", "codex", "--codex-events-dir", "/private/events"}, nil},
		{"owner unavailable", codexArgs, errors.New("owner is unavailable")},
		{"child has no native owner", codexArgs, errors.New("native app rejected thread-owner-discovery: \"no-client-found\"")},
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

func TestCodexOwnerProbeDoesNotRouteOrArm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, thread string
		probeErr     error
		code         int
		want         string
	}{
		{"top-level owner", "top-level-thread", nil, 0, "native=available"},
		{"child has no owner", "child-thread", errors.New("thread-owner-discovery: no-client-found"), 1, "native=unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := fakeCodexWorld(r)
			calls := 0
			w.codexProbe = func(_ context.Context, socket, thread string) error {
				calls++
				assert.Equal(t, "/private/socket", socket)
				assert.Equal(t, tc.thread, thread)
				return tc.probeErr
			}
			var out, errOut bytes.Buffer
			code := run([]string{"codex-owner", "--thread", tc.thread, "--socket", "/private/socket"}, strings.NewReader(""), &out, &errOut, w)
			assert.Equal(t, tc.code, code, out.String()+errOut.String())
			assert.Contains(t, out.String()+errOut.String(), tc.want)
			assert.Equal(t, 1, calls)
			assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
		})
	}
}

func TestCodexOwnerDryRunNeverContactsDesktop(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	w.codexProbe = func(context.Context, string, string) error {
		t.Fatal("dry-run must not contact Codex desktop")
		return nil
	}
	var out bytes.Buffer
	code := run([]string{"codex-owner", "--thread", "child-thread", "--socket", "/private/socket", "--dry-run"}, strings.NewReader(""), &out, io.Discard, w)
	assert.Zero(t, code, out.String())
	assert.Contains(t, out.String(), "native=unchecked")
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
}

func TestCodexOwnerHelpExampleThroughComparator(t *testing.T) {
	t.Parallel()
	const example = "nova-hook codex-owner --thread example-thread --dry-run"
	cli, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	require.NoError(t, err)
	assert.Contains(t, string(cli), example)
	var out, errOut bytes.Buffer
	code := run([]string{"codex-owner", "--thread", "example-thread", "--dry-run"}, strings.NewReader(""), &out, &errOut, fakeCodexWorld(newRig(t)))
	step := onboarding.Step{Line: "$ " + example, Want: []string{
		"CODEX-OWNER DRY-RUN thread=example-thread native=unchecked dry_run=true",
		"CODEX-OWNER NOTE no native owner discovery was sent",
	}}
	assert.Zero(t, code, out.String()+errOut.String())
	for _, p := range onboarding.CompareTranscript([]onboarding.Step{step}, []onboarding.Result{{Code: code, Stdout: out.String(), Stderr: errOut.String()}}, nil) {
		assert.Fail(t, p.Message)
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

// TestOneLineCodexSetup is the whole setup in one line: nova-hook hook --as
// <name> --role comms --harness codex. The socket, the events directory, the
// chat and the cutover are defaulted; the chat is the friend's newest desktop
// chat, read from a fixture of the Codex home (no socket, no real time).
func TestOneLineCodexSetup(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	home, state := t.TempDir(), t.TempDir()
	env := w.getenv
	w.getenv = func(k string) string {
		switch k {
		case "CODEX_HOME":
			return home
		case "XDG_STATE_HOME":
			return state
		}
		return env(k)
	}
	var askedHome string
	w.codexChats = func(h string) ([]codexdesktop.Chat, error) {
		askedHome = h
		return []codexdesktop.Chat{
			{ID: "chat-old", Cwd: "/Users/u/Documents/ChatGPT/ada 2", Created: time.Date(2026, 10, 14, 20, 39, 18, 0, time.UTC)},
			{ID: "chat-new", Cwd: "/Users/u/Documents/ChatGPT/ada 2", Created: time.Date(2026, 10, 15, 0, 7, 39, 0, time.UTC)},
			{ID: "chat-bob", Cwd: "/Users/u/Documents/ChatGPT/bob", Created: time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)},
		}, nil
	}
	var validated []string
	w.codexValidate = func(socket, thread, dir string) error {
		validated = []string{socket, thread, dir}
		return nil
	}
	var delivered []string
	w.codexDeliver = func(_ context.Context, _, thread, _, _ string) (string, error) {
		delivered = append(delivered, thread)
		return "native-turn", nil
	}
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	var out, errOut bytes.Buffer
	code := run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "codex"}, strings.NewReader(""), &out, &errOut, w)
	require.Zero(t, code, errOut.String())
	assert.Equal(t, home, askedHome)
	events := filepath.Join(state, "nova-hook", "ada-codex")
	assert.Equal(t, []string{filepath.Join(home, "ipc", "ipc.sock"), "chat-new", events}, validated, "the newest chat in her workspace, never another friend's")
	info, err := os.Stat(events)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	cut, err := os.ReadFile(filepath.Join(events, ".cutover"))
	require.NoError(t, err, "the cutover is kept for every restart")
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(cut)))
	require.NoError(t, err)
	assert.Contains(t, out.String(), "since="+at.Format(time.RFC3339))
	for _, thread := range delivered {
		assert.Equal(t, "chat-new", thread)
	}

	// A restart keeps the same cutover.
	require.Zero(t, run([]string{"hook", "--as", "ada", "--role", "comms", "--harness", "codex"}, strings.NewReader(""), io.Discard, io.Discard, w))
	again, err := os.ReadFile(filepath.Join(events, ".cutover"))
	require.NoError(t, err)
	assert.Equal(t, cut, again)
}

// TestNamedCodexThreadIsAnOverride keeps --codex-thread working: no discovery.
func TestNamedCodexThreadIsAnOverride(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	w.codexChats = func(string) ([]codexdesktop.Chat, error) {
		t.Error("a named chat is never discovered")
		return nil, nil
	}
	var validated []string
	w.codexValidate = func(socket, thread, dir string) error {
		validated = []string{socket, thread, dir}
		return nil
	}
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	var errOut bytes.Buffer
	require.Zero(t, run(codexArgs, strings.NewReader(""), io.Discard, &errOut, w), errOut.String())
	assert.Equal(t, []string{"/private/socket", "thread-1", "/private/events"}, validated)
}
