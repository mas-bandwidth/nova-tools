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
	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var dshArgs = []string{"hook", "--as", "ada", "--role", "comms", "--dsh-gateway", "http://127.0.0.1:19387", "--dsh-session", "session-abc", "--dsh-cookie", "c=v", "--dsh-events-dir", "/private/events"}

func fakeDshWorld(r *rig) world {
	w := r.world()
	w.dshValidate = func(dshdesktop.Route, string) error { return nil }
	w.dshSave = func(string, string, []byte) error { return nil }
	w.dshDeliver = func(context.Context, dshdesktop.Route, string, string) (string, error) { return "session-abc", nil }
	return w
}

func TestDshRouteRefusesBeforeArming(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"partial gateway only", []string{"hook", "--as", "ada", "--role", "comms", "--dsh-gateway", "http://127.0.0.1:19387"}},
		{"once", append(append([]string{}, dshArgs...), "--once")},
		{"both codex and dsh", append(append([]string{}, dshArgs...), "--codex-socket", "/s", "--codex-thread", "t", "--codex-events-dir", "/d")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := fakeDshWorld(r)
			var out bytes.Buffer
			code := run(tc.args, strings.NewReader(""), &out, io.Discard, w)
			assert.Equal(t, 2, code, out.String())
			assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
		})
	}
}

func TestDshLoopDeliversChallengeAsSteerAndMessageAsFollowup(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeDshWorld(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)

	messages := []bus.Message{
		{ID: "challenge-1", From: "server", Subject: "HOOK CHALLENGE nonce=abc", Body: "challenge"},
		{ID: "ordinary-1", From: "bob", Subject: "untrusted subject", Body: "untrusted body"},
	}
	i := 0
	notices := make([]string, 0, 2)
	st := codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		r.advance(hook.ChallengeEvery + time.Second)
		m := messages[i]
		i++
		return []bus.Entry{{Entry: "1-1", Fields: m.Fields()}}
	}}
	s := &session{bus: st, hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
	w.dshDeliver = func(_ context.Context, _ dshdesktop.Route, _ string, notice string) (string, error) {
		notices = append(notices, notice)
		if len(notices) == len(messages) {
			cancel()
		}
		return "session-abc", nil
	}
	var out bytes.Buffer
	c := &tool.Call{Stdout: &out}
	challengeCalls := 0
	result := w.loop(c, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { challengeCalls++; return nil }, nil, &dshRoute{route: dshdesktop.Route{Gateway: "http://127.0.0.1:19387", SessionID: "session-abc", Cookie: "c=v"}, directory: "/private/events"})
	assert.Equal(t, tool.OK, result.Status, out.String())
	require.Len(t, notices, 2)
	assert.Equal(t, dshdesktop.ChallengeNotice, notices[0], "a challenge is steered into the running turn")
	assert.Equal(t, dshdesktop.BusNotice, notices[1], "an ordinary message is a followup")
	assert.Zero(t, challengeCalls)
}

func TestDshPushSavesEnvelopeAndSteersChallenge(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeDshWorld(r)
	s := &session{hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
	var saved savedDshEvent
	w.dshSave = func(_, _ string, data []byte) error {
		require.NoError(t, json.Unmarshal(data, &saved))
		return nil
	}
	notice := ""
	w.dshDeliver = func(_ context.Context, _ dshdesktop.Route, _ string, n string) (string, error) {
		notice = n
		return "session-abc", nil
	}
	route := &dshRoute{route: dshdesktop.Route{Gateway: "http://127.0.0.1:19387", SessionID: "session-abc", Cookie: "c=v"}, directory: "/private/events"}
	var out bytes.Buffer
	got := w.dshPush(&tool.Call{Stdout: &out}, s, route, hook.Comms, 1, "event-1", &bus.Message{ID: "event-1", Subject: "HOOK CHALLENGE nonce=abc", Body: "external body"})
	assert.Nil(t, got, out.String())
	assert.True(t, saved.Steer)
	assert.Equal(t, "external body", saved.Message.Body)
	assert.Equal(t, dshdesktop.ChallengeNotice, notice)
}

func TestDshPushFailureRevokesRouteProof(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeDshWorld(r)
	ctx := context.Background()
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	require.NoError(t, r.h.Store.Renew(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
	require.NoError(t, r.h.Store.Prove(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
	w.dshDeliver = func(context.Context, dshdesktop.Route, string, string) (string, error) {
		return "", errors.New("gateway down")
	}
	s := &session{hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
	var out bytes.Buffer
	got := w.dshPush(&tool.Call{Stdout: &out}, s, &dshRoute{route: dshdesktop.Route{Gateway: "http://127.0.0.1:19387", SessionID: "session-abc", Cookie: "c=v"}, directory: "/private/events"}, hook.Comms, epoch, "event-1", &bus.Message{ID: "event-1"})
	require.NotNil(t, got)
	assert.Equal(t, tool.Failed, got.Status)
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State, "route failure drops the proof, not the hook")
}
