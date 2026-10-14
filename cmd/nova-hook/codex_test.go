package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var codexArgs = []string{"hook", "--as", "ada", "--role", "comms", "--codex-socket", "/private/socket", "--codex-thread", "thread-1", "--codex-events-dir", "/private/events"}

func fakeCodexWorld(r *rig) world {
	w := r.world()
	w.codexLock = func(string) (func() error, error) { return func() error { return nil }, nil }
	w.codexValidate = func(string, string, string) error { return nil }
	w.codexSave = func(string, string, []byte) error { return nil }
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
	saved := map[string]savedCodexEvent{}
	w.codexSave = func(_ string, id string, data []byte) error {
		var e savedCodexEvent
		require.NoError(t, json.Unmarshal(data, &e))
		saved[id] = e
		return nil
	}
	calls := 0
	w.codexDeliver = func(_ context.Context, _, _, id, _ string) (string, error) {
		calls++
		e := saved[id]
		assert.Equal(t, "ada", e.LocalReceiver.Identity)
		assert.Equal(t, "127.0.0.1:6379", e.LocalReceiver.Redis)
		assert.NotEmpty(t, e.BootstrapNonce)
		assert.Equal(t, "2026-10-11T12:00:00Z", e.RecoverySince)
		assert.Nil(t, e.Message)
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
	assert.Contains(t, out.String(), "native=accepted")
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

func TestCodexLoopDeliversServerChallengesAndOrdinaryEventsWithoutClientChallenges(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	messages := []bus.Message{
		{ID: "server-event", From: "bob", Subject: "HOOK CHALLENGE nonce=server", Body: "external server challenge"},
		{ID: "ordinary-event", From: "bob", Subject: "untrusted subject", Body: "untrusted body"},
	}
	i := 0
	st := codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		r.advance(hook.ChallengeEvery + time.Second)
		if i == 1 {
			require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("ada"), messages[0].ID, "1791720000").Err())
		}
		m := messages[i]
		i++
		return []bus.Entry{{Entry: "1-1", Fields: m.Fields()}}
	}}
	s := &session{bus: st, hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
	var saved savedCodexEvent
	w.codexSave = func(_, id string, data []byte) error {
		require.NoError(t, json.Unmarshal(data, &saved))
		assert.Equal(t, messages[i-1].ID, id)
		assert.Equal(t, messages[i-1].Body, saved.Message.Body)
		return nil
	}
	delivered := 0
	w.codexDeliver = func(context.Context, string, string, string, string) (string, error) {
		delivered++
		if delivered == len(messages) {
			cancel()
		}
		return "native-turn", nil
	}
	var out bytes.Buffer
	c := &tool.Call{Stdout: &out}
	challengeCalls := 0
	result := w.loop(c, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { challengeCalls++; return nil }, &codexRoute{socket: "/private/socket", thread: "thread-1", directory: "/private/events"})
	assert.Equal(t, tool.OK, result.Status, out.String())
	assert.Equal(t, 2, delivered)
	assert.Zero(t, challengeCalls)
	assert.NotEqual(t, hook.StateProven, r.h.State(t, "ada").State)
}

func TestCodexUnknownStopsAndRevokesRouteProofWithoutUnhook(t *testing.T) {
	t.Parallel()
	for _, outcome := range []error{codexdesktop.ErrOutcomeUnknown, codexdesktop.ErrRejected, codexdesktop.ErrPreSend} {
		r := newRig(t)
		w := fakeCodexWorld(r)
		ctx := context.Background()
		epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
		require.NoError(t, err)
		require.NoError(t, r.h.Store.Renew(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
		require.NoError(t, r.h.Store.Prove(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
		attempts := 0
		w.codexDeliver = func(context.Context, string, string, string, string) (string, error) { attempts++; return "", outcome }
		var out bytes.Buffer
		s := &session{hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
		got := w.codexPush(&tool.Call{Stdout: &out}, s, &codexRoute{socket: "/private/socket", thread: "thread-1", directory: "/private/events"}, hook.Comms, epoch, "event-1", "", &bus.Message{ID: "event-1"})
		require.NotNil(t, got)
		assert.Equal(t, tool.Failed, got.Status)
		assert.Equal(t, 1, attempts)
		assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State)
		assert.Equal(t, epoch, r.h.State(t, "ada").Epoch)
	}
}

func TestCodexOversizedEventNeverReachesNativeTransport(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	w.codexSave = func(string, string, []byte) error { t.Fatal("oversized data must not be saved"); return nil }
	w.codexDeliver = func(context.Context, string, string, string, string) (string, error) {
		t.Fatal("oversized data must not be delivered")
		return "", nil
	}
	got := w.codexPush(&tool.Call{Stdout: io.Discard}, &session{hooks: r.h.Store, as: "ada"}, &codexRoute{}, hook.Comms, 1, "event-1", "", &bus.Message{Body: strings.Repeat("x", maxCodexEvent)})
	require.NotNil(t, got)
	assert.Equal(t, tool.Failed, got.Status)
}

func TestCodexExistingModelAnswerSuppressesNativeMutation(t *testing.T) {
	t.Parallel()
	for _, afterUnknown := range []bool{false, true} {
		r := newRig(t)
		w := fakeCodexWorld(r)
		ctx := context.Background()
		s := &session{hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
		if !afterUnknown {
			require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("ada"), "event-1", "1791720000").Err())
		}
		attempts := 0
		w.codexDeliver = func(context.Context, string, string, string, string) (string, error) {
			attempts++
			require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("ada"), "event-1", "1791720000").Err())
			return "", codexdesktop.ErrOutcomeUnknown
		}
		var out bytes.Buffer
		result := w.codexPush(&tool.Call{Stdout: &out}, s, &codexRoute{socket: "/private/socket", thread: "thread-1", directory: "/private/events"}, hook.Comms, 1, "event-1", "", &bus.Message{ID: "event-1"})
		assert.Nil(t, result)
		if afterUnknown {
			assert.Equal(t, 1, attempts)
			assert.Contains(t, out.String(), "native=unknown")
		} else {
			assert.Zero(t, attempts)
		}
		assert.Contains(t, out.String(), "acknowledgment=model-answer")
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State, "answer reconciliation never hooks or proves")
	}
}

func TestCodexDryRunDoesNotContactNativeAppOrWriteEvents(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	w.codexValidate = func(string, string, string) error { t.Fatal("dry-run must not connect native IPC"); return nil }
	w.codexSave = func(string, string, []byte) error { t.Fatal("dry-run must not save events"); return nil }
	args := append(append([]string{}, codexArgs...), "--dry-run")
	var out bytes.Buffer
	code := run(args, strings.NewReader(""), &out, io.Discard, w)
	assert.Zero(t, code, out.String())
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
}

func TestCodexBurstOfFiftyMessagesCoalescesIntoSingleTurnAndOneSummaryAtTurnBoundary(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)

	senders := []string{"rowan", "glenn", "emma"}
	makeBurst := func(offset, count int) []bus.Entry {
		entries := make([]bus.Entry, 0, count)
		for i := 0; i < count; i++ {
			idx := offset + i + 1
			m := bus.Message{
				ID:      fmt.Sprintf("msg-%d", idx),
				From:    senders[i%len(senders)],
				Subject: fmt.Sprintf("subject-%d", idx),
				Body:    fmt.Sprintf("body-%d", idx),
			}
			entries = append(entries, bus.Entry{Entry: fmt.Sprintf("1-%d", idx), Fields: m.Fields()})
		}
		return entries
	}

	var deliveredIDs []string
	savedEvents := map[string]savedCodexEvent{}
	w.codexSave = func(_, id string, data []byte) error {
		var e savedCodexEvent
		require.NoError(t, json.Unmarshal(data, &e))
		savedEvents[id] = e
		return nil
	}
	w.codexDeliver = func(_ context.Context, _, _, id, _ string) (string, error) {
		deliveredIDs = append(deliveredIDs, id)
		return fmt.Sprintf("turn-%d", len(deliveredIDs)), nil
	}

	step := 0
	st := codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		step++
		r.advance(time.Second)
		switch step {
		case 1:
			// Initial burst of 50 messages when idle: starts 1 turn for msg-1 and folds 49.
			return makeBurst(0, 50)
		case 2:
			// While turn 1 is still running, time advances and no new prompt is delivered.
			assert.Equal(t, []string{"msg-1"}, deliveredIDs, "50-message burst must produce exactly one startTurn")
			return nil
		case 3:
			// Turn 1 boundary: ada answers msg-1; loop delivers exactly one summary for the 49 folded messages.
			require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("ada"), "msg-1", "1791720001").Err())
			return nil
		case 4:
			// While the summary turn is running, another burst of 50 messages arrives: zero new turns started.
			require.Len(t, deliveredIDs, 2, "turn boundary must deliver exactly one summary")
			return makeBurst(50, 50)
		case 5:
			// Still in summary turn: deliveredIDs remains 2 despite 50 more messages arriving.
			assert.Len(t, deliveredIDs, 2, "50 messages arriving while turn is running must fold without starting a turn")
			// Next turn boundary: ada answers the summary event; loop delivers one 50-message summary.
			require.NoError(t, r.h.Client.HSet(ctx, bus.AnswersOf("ada"), deliveredIDs[1], "1791720002").Err())
			return nil
		default:
			cancel()
			return nil
		}
	}}

	s := &session{bus: st, hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
	var out bytes.Buffer
	c := &tool.Call{Stdout: &out}
	result := w.loop(c, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, &codexRoute{socket: "/private/socket", thread: "thread-1", directory: "/private/events"})
	assert.Equal(t, tool.OK, result.Status, out.String())

	require.Len(t, deliveredIDs, 3)
	assert.Equal(t, "msg-1", deliveredIDs[0])
	assert.True(t, strings.HasPrefix(deliveredIDs[1], "summary-"), deliveredIDs[1])
	assert.Equal(t, "49 more messages from glenn, emma, rowan; read the bus", savedEvents[deliveredIDs[1]].Message.Subject)
	assert.Equal(t, "49 more messages from glenn, emma, rowan; read the bus", savedEvents[deliveredIDs[1]].Message.Body)
	assert.Contains(t, out.String(), "49 more messages from glenn, emma, rowan; read the bus")

	assert.True(t, strings.HasPrefix(deliveredIDs[2], "summary-"), deliveredIDs[2])
	assert.Equal(t, "50 more messages from rowan, glenn, emma; read the bus", savedEvents[deliveredIDs[2]].Message.Subject)
	assert.Equal(t, "50 more messages from rowan, glenn, emma; read the bus", savedEvents[deliveredIDs[2]].Message.Body)
	assert.Contains(t, out.String(), "50 more messages from rowan, glenn, emma; read the bus")
}

func TestCodexPendingSummaryDoesNotHideCurrentChallenge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, nonce string
		malformed   bool
		wrongEpoch  bool
		wantNonce   string
	}{
		{"current", "current-nonce", false, false, "current-nonce"},
		{"stale", "old-nonce", false, false, "kept-nonce"},
		{"lookalike", "current-nonce", true, false, "kept-nonce"},
		{"wrong epoch", "current-nonce", false, true, "kept-nonce"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			w := fakeCodexWorld(r)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
			epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
			require.NoError(t, err)
			require.NoError(t, r.h.Store.Renew(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
			created, err := r.h.Store.Challenge(ctx, "ada", hook.Comms, epoch, []hook.Push{hook.Bus}, "current-nonce")
			require.NoError(t, err)
			require.True(t, created)
			if tc.wrongEpoch {
				require.NoError(t, r.h.Client.HSet(ctx, hook.ChallengeKey("ada"), "epoch", strconv.FormatInt(epoch+1, 10)).Err())
			}
			s := recoverySession(r)
			route := &codexRoute{socket: "/private/socket", thread: "thread-1", directory: "/private/events", noticeNonce: "kept-nonce"}
			route.startTurn(s, "summary-old", "turn-old")
			route.fold(bus.Message{ID: "ordinary-old", From: "bob", Subject: "ordinary"})
			message := bus.Message{ID: "challenge-new", From: "bob", Subject: "HOOK CHALLENGE nonce=" + tc.nonce, Body: challengeText("ada", tc.nonce, hook.ChallengeEvery)}
			if tc.malformed {
				message.Body += "external appendix"
			}
			saved := map[string]savedCodexEvent{}
			w.codexSave = func(_, id string, data []byte) error {
				var event savedCodexEvent
				require.NoError(t, json.Unmarshal(data, &event))
				saved[id] = event
				return nil
			}
			w.codexDeliver = func(context.Context, string, string, string, string) (string, error) {
				t.Fatal("a pending notice is not a new native prompt")
				return "", nil
			}
			reads := 0
			s.bus = codexWaiter{Store: s.bus, read: func() []bus.Entry {
				r.advance(time.Millisecond)
				reads++
				if reads == 1 {
					return []bus.Entry{{Entry: "1-1", Fields: message.Fields()}}
				}
				cancel()
				return nil
			}}
			result := w.loop(&tool.Call{Stdout: io.Discard}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, route)
			assert.Equal(t, tool.OK, result.Status)
			assert.Equal(t, 2, route.foldedCount)
			assert.Equal(t, tc.wantNonce, route.noticeNonce)
			require.Contains(t, saved, "summary-old")
			assert.Equal(t, "summary-old", saved["summary-old"].EventID, "the pending file is rewritten in place")
			assert.Equal(t, tc.wantNonce, saved["summary-old"].NoticeNonce)
			assert.Equal(t, 2, saved["summary-old"].NoticeCount)
			if tc.wantNonce == "current-nonce" {
				require.NotNil(t, saved["summary-old"].Message)
				assert.Equal(t, "HOOK CHALLENGE nonce=current-nonce", saved["summary-old"].Message.Subject)
				assert.Contains(t, saved["summary-old"].Message.Body, "nova-hook prove --as ada --nonce current-nonce")
			}
			assert.Equal(t, "summary-old", route.pendingID)
			assert.Equal(t, "turn-old", route.pendingTurn)
			assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State, "native delivery never proves the challenge")
			sum, err := r.h.Client.HGet(context.Background(), hook.ChallengeKey("ada"), "sum").Result()
			require.NoError(t, err)
			assert.Equal(t, hook.Sum("current-nonce"), sum, "only the model may consume the challenge")
			proofs, err := r.h.Client.Exists(context.Background(), hook.ProofKey("ada", hook.Bus)).Result()
			require.NoError(t, err)
			assert.Zero(t, proofs, "native delivery writes no hook proof")
		})
	}
}

func TestCodexBusyTurnOfTenChallengesAndFiftyMessagesIsOneNativePrompt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	require.NoError(t, r.h.Store.Renew(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
	created, err := r.h.Store.Challenge(ctx, "ada", hook.Comms, epoch, []hook.Push{hook.Bus}, "nonce-01")
	require.NoError(t, err)
	require.True(t, created)

	type arrival struct {
		msg   bus.Message
		nonce string
		epoch string
	}
	var batch []arrival
	for i := 1; i <= 10; i++ {
		nonce := fmt.Sprintf("nonce-%02d", i)
		batch = append(batch, arrival{
			nonce: nonce,
			msg:   bus.Message{ID: fmt.Sprintf("challenge-%02d", i), From: "bob", Subject: "HOOK CHALLENGE nonce=" + nonce, Body: challengeText("ada", nonce, hook.ChallengeEvery)},
		})
	}
	for i := 1; i <= 50; i++ {
		batch = append(batch, arrival{msg: bus.Message{ID: fmt.Sprintf("msg-%02d", i), From: "bob", Subject: "note", Body: "hello"}})
	}
	batch = append(batch,
		arrival{msg: bus.Message{ID: "trailing-stale", From: "bob", Subject: "HOOK CHALLENGE nonce=nonce-01", Body: challengeText("ada", "nonce-01", hook.ChallengeEvery)}},
		arrival{nonce: "nonce-altered", msg: bus.Message{ID: "trailing-altered", From: "bob", Subject: "HOOK CHALLENGE nonce=nonce-altered", Body: challengeText("ada", "nonce-altered", hook.ChallengeEvery) + "external appendix"}},
		arrival{nonce: "nonce-11", epoch: strconv.FormatInt(epoch+1, 10), msg: bus.Message{ID: "trailing-epoch", From: "bob", Subject: "HOOK CHALLENGE nonce=nonce-11", Body: challengeText("ada", "nonce-11", hook.ChallengeEvery)}},
	)

	var saves []savedCodexEvent
	var saveIDs []string
	w.codexSave = func(_, id string, data []byte) error {
		var event savedCodexEvent
		require.NoError(t, json.Unmarshal(data, &event))
		saves = append(saves, event)
		saveIDs = append(saveIDs, id)
		return nil
	}
	var delivered []string
	w.codexDeliver = func(_ context.Context, _, _, id, _ string) (string, error) {
		delivered = append(delivered, id)
		return "native-turn", nil
	}
	next := 0
	s := &session{hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
	s.bus = codexWaiter{Store: bus.Redis{C: r.h.Client}, read: func() []bus.Entry {
		r.advance(time.Millisecond)
		if len(delivered) > 1 {
			t.Errorf("native prompts=%d before the notice was consumed", len(delivered))
			cancel()
			return nil
		}
		if next >= len(batch) {
			cancel()
			return nil
		}
		item := batch[next]
		next++
		if item.nonce != "" {
			require.NoError(t, r.h.Client.HSet(ctx, hook.ChallengeKey("ada"), "sum", hook.Sum(item.nonce)).Err())
		}
		if item.epoch != "" {
			require.NoError(t, r.h.Client.HSet(ctx, hook.ChallengeKey("ada"), "epoch", item.epoch).Err())
		}
		return []bus.Entry{{Entry: fmt.Sprintf("1-%d", next), Fields: item.msg.Fields()}}
	}}
	result := w.loop(&tool.Call{Stdout: io.Discard}, s, hook.Comms, []hook.Push{hook.Bus}, epoch, "0-0", func(int64) *tool.Out { return nil }, &codexRoute{socket: "/private/socket", thread: "thread-1", directory: "/private/events"})
	assert.Equal(t, tool.OK, result.Status)
	require.Equal(t, []string{"challenge-01"}, delivered, "one busy turn is one native prompt")
	require.Len(t, saves, len(batch))
	for _, id := range saveIDs {
		assert.Equal(t, "challenge-01", id, "later messages rewrite the pending file")
	}
	assert.Equal(t, "nonce-01", saves[0].NoticeNonce)
	assert.Equal(t, "nonce-10", saves[9].NoticeNonce)
	assert.Equal(t, 9, saves[9].NoticeCount)
	assert.Equal(t, "nonce-10", saves[59].NoticeNonce)
	assert.Equal(t, 59, saves[59].NoticeCount)
	assert.Equal(t, "nonce-10", saves[60].NoticeNonce, "a trailing stale challenge does not replace the saved nonce")
	assert.Equal(t, "nonce-10", saves[61].NoticeNonce, "an altered body does not replace the saved nonce")
	assert.Equal(t, "nonce-10", saves[62].NoticeNonce, "a wrong epoch does not replace the saved nonce")
	last := saves[len(saves)-1]
	assert.Equal(t, "nonce-10", last.NoticeNonce)
	assert.Equal(t, 62, last.NoticeCount)
	require.NotNil(t, last.Message)
	assert.Equal(t, "HOOK CHALLENGE nonce=nonce-10", last.Message.Subject)
	assert.Contains(t, last.Message.Body, "nova-hook prove --as ada --nonce nonce-10")
	assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State, "native delivery never proves the challenge")
	proofs, err := r.h.Client.Exists(context.Background(), hook.ProofKey("ada", hook.Bus)).Result()
	require.NoError(t, err)
	assert.Zero(t, proofs, "native delivery writes no hook proof")
}
