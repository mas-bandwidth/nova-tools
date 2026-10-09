package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nativeReplyRig(t *testing.T) (string, string, *Daemon, *bus.Bus, bus.Message) {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(t.TempDir(), "daemon-state")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox"), 0o700))
	require.NoError(t, WriteLanes(state, LaneState{Sessions: map[int]string{1: "ses_native"}}))
	now := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	store := bustest.NewFake(now, "freddy", "stella")
	b := &bus.Bus{Store: store}
	request, err := b.Send(context.Background(), bus.Message{From: "stella", To: []string{"freddy"}, Kind: bus.KindRequest, Subject: "ordinary review", Body: "Please review this."})
	require.NoError(t, err)
	entry, ok, err := b.Recv(context.Background(), "freddy", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, request.ID, entry.Message().ID)
	_, err = b.Stamp(context.Background(), "freddy", bus.Read, request.ID)
	require.NoError(t, err)
	d := &Daemon{Friend: "freddy", Harness: "opencode", Dir: dir, StateDir: state, NativeBrokerDir: filepath.Join(state, "native-broker"), Store: store, Record: func(string) {}}
	return dir, state, d, b, request
}

func TestNativeReplyFromDeliveredRequestSendsOnceAndKeepsPrivateEnvelope(t *testing.T) {
	t.Parallel()
	dir, _, d, b, request := nativeReplyRig(t)
	capability, err := d.GrantNativeReply("ses_native", request, "stella")
	require.NoError(t, err)
	reply := NativeReply{Friend: "freddy", Session: "ses_native", Re: request.ID, Capability: capability, Subject: "Freddy review", Body: "reviewed from native turn"}
	require.NoError(t, QueueNativeReply(dir, reply))
	info, err := os.Stat(nativeReplyPath(dir, request.ID))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	var cursor string
	d.SendNativeReplies(context.Background(), b, request.At.Add(time.Second), "stella", &cursor)
	d.SendNativeReplies(context.Background(), b, request.At.Add(2*time.Second), "stella", &cursor)
	entries, err := d.Store.Range(context.Background(), bus.StreamOf("stella"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	got := entries[0].Message()
	assert.Equal(t, "freddy", got.From)
	assert.Equal(t, []string{"stella"}, got.To)
	assert.Equal(t, request.ID, got.Re)
	assert.Equal(t, reply.Body, got.Body)
	var saved nativeReplyReceipt
	found, err := read(nativeReplyReceiptPath(d.NativeBrokerDir, request.ID), &saved)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, saved.Sent)
	assert.Equal(t, got.ID, saved.ID)
}

func TestNativeReplyRefusesForgedCapabilityAndUnprovenRequestWithoutLeak(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		modify func(*testing.T, *NativeReply, *Daemon, *bus.Bus, bus.Message)
	}{
		{"wrong capability", func(_ *testing.T, r *NativeReply, _ *Daemon, _ *bus.Bus, _ bus.Message) {
			r.Capability = strings.Repeat("0", 64)
		}},
		{"wrong session", func(_ *testing.T, r *NativeReply, _ *Daemon, _ *bus.Bus, _ bus.Message) { r.Session = "ses_elsewhere" }},
		{"wrong friend", func(_ *testing.T, r *NativeReply, _ *Daemon, _ *bus.Bus, _ bus.Message) { r.Friend = "stella" }},
		{"unread request", func(t *testing.T, r *NativeReply, d *Daemon, b *bus.Bus, _ bus.Message) {
			// A fresh typed request on the stream has no native-turn read receipt.
			other, err := b.Send(context.Background(), bus.Message{From: "stella", To: []string{"freddy"}, Kind: bus.KindRequest, Subject: "later", Body: "secret payload"})
			require.NoError(t, err)
			r.Re = other.ID
			r.Capability, err = d.GrantNativeReply("ses_native", other, "stella")
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, _, d, b, request := nativeReplyRig(t)
			capability, err := d.GrantNativeReply("ses_native", request, "stella")
			require.NoError(t, err)
			r := NativeReply{Friend: "freddy", Session: "ses_native", Re: request.ID, Capability: capability, Subject: "reply", Body: "literal secret body"}
			tc.modify(t, &r, d, b, request)
			require.NoError(t, QueueNativeReply(dir, r))
			var logs []string
			d.Record = func(s string) { logs = append(logs, s) }
			var cursor string
			d.SendNativeReplies(context.Background(), b, request.At.Add(time.Second), "stella", &cursor)
			entries, err := d.Store.Range(context.Background(), bus.StreamOf("stella"), "-", "+", 0)
			require.NoError(t, err)
			assert.Empty(t, entries)
			assert.NotContains(t, strings.Join(logs, "\n"), "literal secret body")
		})
	}
}

func TestNativeMessageTurnCarriesScopedReplyCommandForTypedSeatRequest(t *testing.T) {
	t.Parallel()
	dir, _, d, b, request := nativeReplyRig(t)
	d.Seat = func(context.Context) (string, error) { return "stella", nil }
	d.ReplyCommand = func(session, re, capability string) string {
		return "nova-friend reply --session " + session + " --re " + re + " --capability " + capability
	}
	d.m = Start(request.At)
	entries, err := d.Store.Range(context.Background(), bus.StreamOf("freddy"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	h := &lanesHarness{dir: dir, finish: map[string]bool{}, reject: map[string]string{}, active: map[string]int{}}
	ln := &lane{n: 1, session: "ses_native"}
	l := &loop{d: d, ctx: context.Background(), b: b, hand: entries,
		inHand: map[string]bool{entries[0].Entry: true}, lanes: &laneSet{results: make(chan laneResult, 1)}}
	require.True(t, l.messageTurn(ln, h, 32, request.At))
	require.NotNil(t, ln.t)
	assert.Contains(t, ln.t.text, "For request "+request.ID+", reply through this local command")
	assert.Contains(t, ln.t.text, "nova-friend reply --session ses_native --re "+request.ID)
	l.controlWG.Wait()
}

func TestNonOpenCodeOneShotCheckAndRequestUseOriginalTurnWithoutBrokerGrant(t *testing.T) {
	t.Parallel()
	dir, _, d, b, request := nativeReplyRig(t)
	d.Harness = "claude"
	d.Seat = func(context.Context) (string, error) { return "stella", nil }
	d.ControlCheck = func() string { return "claudecheck" }
	d.PongCommand = func(nonce string) string { return "claude-pong " + nonce }
	d.m = Start(request.At)
	entries, err := d.Store.Range(context.Background(), bus.StreamOf("freddy"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	h := &lanesHarness{dir: dir, finish: map[string]bool{}, reject: map[string]string{}, active: map[string]int{}}
	ln := &lane{n: 1, session: "ses_native"}
	l := &loop{d: d, ctx: context.Background(), b: b, hand: entries,
		inHand: map[string]bool{entries[0].Entry: true}, lanes: &laneSet{results: make(chan laneResult, 1)}}
	require.True(t, l.messageTurn(ln, h, 1, request.At))
	require.NotNil(t, ln.t)
	assert.Contains(t, ln.t.text, "claude-pong claudecheck")
	assert.Contains(t, ln.t.text, request.ID)
	assert.NotContains(t, ln.t.text, "local-reply")
	assert.NotContains(t, ln.t.text, "local-pong")
	l.controlWG.Wait()
}

func TestNativeBrokerRejectsSymlinkBackIntoWritableWork(t *testing.T) {
	t.Parallel()
	dir, state, d, _, request := nativeReplyRig(t)
	inside := filepath.Join(dir, "broker")
	require.NoError(t, os.Mkdir(inside, 0o700))
	link := filepath.Join(filepath.Dir(state), "broker-link")
	require.NoError(t, os.Symlink(inside, link))
	d.NativeBrokerDir = link
	_, err := d.GrantNativeReply("ses_native", request, "stella")
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(inside, "native-reply-grants", request.ID+".json"))
	assert.True(t, os.IsNotExist(err))
}

func TestNativeBrokerRefusesWallVisibleConfigAndReadRoots(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"config", "read"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, d, _, request := nativeReplyRig(t)
			root := filepath.Dir(d.NativeBrokerDir)
			d.NativeBrokerRoots = func() []string { return []string{root} }
			_, err := d.GrantNativeReply("ses_native", request, "stella")
			require.ErrorContains(t, err, "wall-visible root")
			_, err = os.Stat(nativeReplyGrantPath(d.NativeBrokerDir, request.ID))
			assert.True(t, os.IsNotExist(err), "no capability is written under an exposed root")
		})
	}
}

func TestNativeBrokerRefusesChangedWallBeforeAnOldGrantCanBeExposed(t *testing.T) {
	t.Parallel()
	_, _, d, _, request := nativeReplyRig(t)
	_, err := d.GrantNativeReply("ses_native", request, "stella")
	require.NoError(t, err)
	err = d.ValidateNativeBrokerRoots([]string{filepath.Dir(d.NativeBrokerDir)})
	require.ErrorContains(t, err, "wall-visible root")
}

func TestNativeBrokerCreatesPrivateFreshParentOutsideWork(t *testing.T) {
	t.Parallel()
	_, _, d, _, request := nativeReplyRig(t)
	d.NativeBrokerDir = filepath.Join(t.TempDir(), "fresh", "friend", "native-broker")
	d.NativeBrokerRoots = func() []string { return []string{d.Dir} }
	_, err := d.GrantNativeReply("ses_native", request, "stella")
	require.NoError(t, err)
	info, err := os.Lstat(d.NativeBrokerDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestNativeBrokerAcceptsDefaultWorkLocalWallHome(t *testing.T) {
	t.Parallel()
	_, _, d, _, request := nativeReplyRig(t)
	// With no explicit wall config, sandbox.LaneProfile uses Work for HOME.
	d.NativeBrokerRoots = func() []string { return []string{d.Dir} }
	_, err := d.GrantNativeReply("ses_native", request, "stella")
	require.NoError(t, err)
}

func TestNativeCardTurnCarriesScopedRequestAndWakeAtFullWidth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		r.d.Harness = "opencode"
		r.d.StateDir = filepath.Join(t.TempDir(), "lane-state")
		r.d.NativeBrokerDir = filepath.Join(t.TempDir(), "native-broker")
		r.d.NativeReplyStore = r.store
		r.d.CardDone = func(string, string) string { return "" }
		r.d.ReplyCommand = func(session, re, capability string) string {
			return "local-reply " + session + " " + re + " " + capability
		}
		r.d.NativePongCommand = func(session, nonce, capability string) string {
			return "local-pong " + session + " " + nonce + " " + capability
		}
		r.d.PongCommand = func(nonce string) string { return "direct-bus-pong " + nonce }
		r.d.SaveLanes = func(s LaneState) error {
			*state = s
			return WriteLanes(r.d.StateDir, s)
		}
		request, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindRequest, Subject: "typed ask", Body: "Please answer."})
		require.NoError(t, err)
		r.send(t, "ada", "PING wake1", WakePingText("ada", t0, "wake1"))
		r.run(t, 8)
		_, texts, _ := h.got()
		var card string
		for _, text := range texts {
			if strings.Contains(text, "one card this turn, c1.") {
				card = text
				break
			}
		}
		require.NotEmpty(t, card)
		assert.Contains(t, card, "local-reply ses_1 "+request.ID)
		assert.Contains(t, card, "local-pong ses_1 wake1")
		assert.NotContains(t, card, "direct-bus-pong")
		assert.NotContains(t, card, "nova-bus send")
		assert.Contains(t, card, "daemon reads the finished outbox and sends the completion notice")
		assert.Contains(t, card, "handle the messages below and stop")
	})
}

func TestNativeFullWidthBusyCardEventuallyHandsRequestInScopedTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}, block: make(chan struct{})}
		r, state := laneRig(t, h, 1)
		r.d.Harness = "opencode"
		r.d.StateDir = filepath.Join(t.TempDir(), "lane-state")
		r.d.NativeBrokerDir = filepath.Join(t.TempDir(), "native-broker")
		r.d.NativeReplyStore = r.store
		r.d.CardDone = func(string, string) string { return "" }
		r.d.ReplyCommand = func(session, re, capability string) string {
			return "local-reply " + session + " " + re + " " + capability
		}
		r.d.SaveLanes = func(s LaneState) error { *state = s; return WriteLanes(r.d.StateDir, s) }
		var request bus.Message
		r.at[5] = func() {
			var err error
			request, err = r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindRequest, Subject: "while busy", Body: "Answer when free."})
			require.NoError(t, err)
		}
		r.at[10] = func() { close(h.block) }
		r.run(t, 16)
		_, texts, _ := h.got()
		require.NotEmpty(t, request.ID)
		found := false
		for _, text := range texts {
			if strings.Contains(text, "local-reply ses_1 "+request.ID) {
				found = true
			}
		}
		assert.True(t, found, "the pending typed request receives a scoped command after the busy card ends")
	})
}
