package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/codexdesktop"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recoverySession(r *rig) *session {
	return &session{bus: bus.Redis{C: r.h.Client}, hooks: r.h.Store, as: "ada", addr: "127.0.0.1:6379"}
}
func recoveryMessage(t *testing.T, r *rig, subject string) bus.Message {
	t.Helper()
	m, err := (&bus.Bus{Store: bus.Redis{C: r.h.Client}}).Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: subject, Body: "external body"})
	require.NoError(t, err)
	return m
}
func recoveryTail(t *testing.T, s *session) string {
	t.Helper()
	tail, _, err := s.bus.(bus.Waiter).Tail(context.Background(), bus.StreamOf(s.as))
	require.NoError(t, err)
	return tail
}

func TestRecoveryKeepsEarlierUnansweredWhenLaterMessageAnswered(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	first := recoveryMessage(t, r, "first")
	second := recoveryMessage(t, r, "second")
	require.NoError(t, r.h.Client.HSet(context.Background(), bus.AnswersOf("ada"), second.ID, "1791720000").Err())
	plan, err := codexBacklog(s, recoveryTail(t, s), time.Time{})
	require.NoError(t, err)
	require.Len(t, plan.messages, 1)
	assert.Equal(t, first.ID, plan.messages[0].ID)
	assert.True(t, plan.coverageUnknown, "a shorter stream does not prove trimming or complete coverage")
}

func TestRecoveryReplaysCurrentActualServerChallengeWithoutBusACK(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	ctx := context.Background()
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	created, err := r.h.Store.Challenge(ctx, "ada", hook.Comms, epoch, []hook.Push{hook.Bus}, "server-nonce")
	require.NoError(t, err)
	require.True(t, created)
	m := recoveryMessage(t, r, "HOOK CHALLENGE nonce=server-nonce")
	plan, err := codexBacklog(s, recoveryTail(t, s), time.Time{})
	require.NoError(t, err)
	require.Len(t, plan.messages, 1)
	assert.Equal(t, m.ID, plan.messages[0].ID)
	// Only hook proof answers the current protocol challenge. Once spent, a
	// subject lookalike remains external data; it cannot be trusted as current.
	require.NoError(t, r.h.Store.Renew(ctx, "ada", hook.Comms, hook.Bus, epoch, "test"))
	_, err = r.h.Store.Answer(ctx, "ada", "server-nonce", "model")
	require.NoError(t, err)
	plan, err = codexBacklog(s, recoveryTail(t, s), time.Time{})
	require.NoError(t, err)
	require.Len(t, plan.messages, 1)
	assert.Equal(t, m.ID, plan.messages[0].ID, "stale challenge text is ordinary external data, never silently dropped")
}

type fixedRecoveryHistory struct {
	bus.Store
	entries []bus.Entry
}

func (s fixedRecoveryHistory) Range(_ context.Context, _, _, _ string, count int) ([]bus.Entry, error) {
	if count < len(s.entries) {
		return s.entries[:count], nil
	}
	return s.entries, nil
}

func TestRecoveryOverflowFailsBeforeLease(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	entries := make([]bus.Entry, maxCodexHistory+1)
	for i := range entries {
		entries[i] = bus.Entry{Entry: "1791720000000-1", Fields: bus.Message{ID: "event-1", From: "bob"}.Fields()}
	}
	s.bus = fixedRecoveryHistory{Store: s.bus, entries: entries}
	_, err := codexBacklog(s, "1791720000000-1", time.Time{})
	require.ErrorContains(t, err, "recovery incomplete")
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
}

// The client hook runs after XINFO returned, injecting a send precisely between
// cursor capture and lease publication. It uses only the existing piped fake.
type cursorArrivalHook struct {
	after func()
	done  bool
}

func (h *cursorArrivalHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *cursorArrivalHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *cursorArrivalHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if !h.done && cmd.Name() == "xinfo" {
			h.done = true
			h.after()
		}
		return err
	}
}

func TestMessageBetweenCursorAndLeaseIsStreamed(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	// Ensure Tail succeeds instead of reporting no stream; this first event is
	// answered so startup need not re-notify it.
	initial := recoveryMessage(t, r, "already answered")
	require.NoError(t, r.h.Client.HSet(context.Background(), bus.AnswersOf("ada"), initial.ID, "1791720000").Err())
	var sent bus.Message
	r.h.Client.AddHook(&cursorArrivalHook{after: func() {
		assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State, "cursor must precede any lease")
		sent = recoveryMessage(t, r, "between cursor and lease")
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	saw := false
	w.codexSave = func(_ string, _ string, data []byte) error {
		var e savedCodexEvent
		require.NoError(t, json.Unmarshal(data, &e))
		if e.Message != nil && e.Message.ID == sent.ID {
			saw = true
			cancel()
		}
		return nil
	}
	code := run(codexArgs, strings.NewReader(""), io.Discard, io.Discard, w)
	assert.Zero(t, code)
	assert.True(t, saw)
}

func TestRecoveryWindowUsesRedisTime(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	old := recoveryMessage(t, r, "outside window")
	r.h.Pass(25 * time.Hour)
	fresh := recoveryMessage(t, r, "inside window")
	plan, err := codexBacklog(s, recoveryTail(t, s), time.Time{})
	require.NoError(t, err)
	require.Len(t, plan.messages, 1)
	assert.Equal(t, fresh.ID, plan.messages[0].ID)
	assert.NotEqual(t, old.ID, plan.messages[0].ID)
}

func TestOneExplicitStartupRecoveryAttemptStopsOnLocalOutcome(t *testing.T) {
	t.Parallel()
	for _, outcome := range []error{codexdesktop.ErrOutcomeUnknown, codexdesktop.ErrPreSend, codexdesktop.ErrRejected} {
		r := newRig(t)
		w := fakeCodexWorld(r)
		m := recoveryMessage(t, r, "unanswered prior event")
		attempts := 0
		var saved savedCodexEvent
		w.codexSave = func(_, id string, data []byte) error {
			require.NoError(t, json.Unmarshal(data, &saved))
			assert.True(t, saved.PossibleDuplicates)
			require.Len(t, saved.Recovery, 1)
			assert.Equal(t, m.ID, saved.Recovery[0].ID)
			return nil
		}
		w.codexDeliver = func(context.Context, string, string, string, string) (string, error) { attempts++; return "", outcome }
		code := run(codexArgs, strings.NewReader(""), io.Discard, io.Discard, w)
		assert.Equal(t, 1, code)
		assert.Equal(t, 1, attempts)
		assert.Equal(t, hook.StateLive, r.h.State(t, "ada").State, "local delivery failure removes route proof, not identity")
	}
}

func TestHistoricalSavedEvidenceCannotBlockNewHookEpoch(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	r.cli().Run("hook", "--as", "ada", "--role", "comms", "--once")
	oldEpoch := r.h.State(t, "ada").Epoch
	r.cli().Run("unhook", "--as", "ada")
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "obsolete-bootstrap.json")
	oldData := []byte(`{"obsolete_bootstrap_epoch":1,"external_evidence":"preserve"}`)
	require.NoError(t, os.WriteFile(oldPath, oldData, 0600))
	args := append([]string{}, codexArgs...)
	args[len(args)-1] = dir
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	code := run(args, strings.NewReader(""), io.Discard, io.Discard, w)
	assert.Zero(t, code, "startup must reconstruct from bus state without interpreting historical saved evidence")
	assert.Greater(t, r.h.State(t, "ada").Epoch, oldEpoch)
	preserved, err := os.ReadFile(oldPath)
	require.NoError(t, err)
	assert.Equal(t, oldData, preserved)
}

func TestEmptyStreamDisclosesUnknownRecoveryCoverage(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	plan, err := codexBacklog(recoverySession(r), "0-0", time.Time{})
	require.NoError(t, err)
	assert.True(t, plan.coverageUnknown)
	assert.Equal(t, "none", plan.oldest)
}

func TestExternalChallengeLookalikeRemainsOrdinaryUnansweredData(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	m := recoveryMessage(t, r, "HOOK CHALLENGE nonce=external-lookalike")
	plan, err := codexBacklog(s, recoveryTail(t, s), time.Time{})
	require.NoError(t, err)
	require.Len(t, plan.messages, 1)
	assert.Equal(t, m.ID, plan.messages[0].ID)
	require.NoError(t, r.h.Client.HSet(context.Background(), bus.AnswersOf("ada"), m.ID, "1791720000").Err())
	plan, err = codexBacklog(s, recoveryTail(t, s), time.Time{})
	require.NoError(t, err)
	assert.Empty(t, plan.messages)
}

func TestCanonicalDayFitsRecoveryScanWithoutReplayingObsoleteChallenges(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	ctx := context.Background()
	_, _, err := r.h.Store.HookAs(ctx, "bob", hook.Comms, "server")
	require.NoError(t, err)
	entries := make([]bus.Entry, 0, 1441)
	for i := 0; i < 1440; i++ {
		nonce := "old-" + strconv.Itoa(i)
		m := bus.Message{ID: "challenge-" + strconv.Itoa(i), From: "bob", Subject: "HOOK CHALLENGE nonce=" + nonce, Body: challengeText("ada", nonce, time.Minute)}
		entries = append(entries, bus.Entry{Entry: "1791720000000-" + strconv.Itoa(i+1), Fields: m.Fields()})
	}
	altered := bus.Message{ID: "ordinary-altered-body", From: "bob", Subject: "HOOK CHALLENGE nonce=external", Body: challengeText("ada", "external", time.Minute) + "external appendix"}
	entries = append(entries, bus.Entry{Entry: "1791720000000-1441", Fields: altered.Fields()})
	s.bus = fixedRecoveryHistory{Store: s.bus, entries: entries}
	plan, err := codexBacklog(s, "1791720000000-1441", time.Time{})
	require.NoError(t, err)
	require.Len(t, plan.messages, 1)
	assert.Equal(t, altered.ID, plan.messages[0].ID, "even an eligible server's altered external body is not silently excluded")
}

func TestRoleTakeoverDropsAllMatchedOldEpochChallenges(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := fakeCodexWorld(r)
	ctx := context.Background()
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Friend, "test")
	require.NoError(t, err)
	_, err = r.h.Store.Challenge(ctx, "ada", hook.Friend, epoch, []hook.Push{hook.Bus}, "old-role-nonce")
	require.NoError(t, err)
	old := recoveryMessage(t, r, "HOOK CHALLENGE nonce=old-role-nonce")
	oldCopy := recoveryMessage(t, r, "HOOK CHALLENGE nonce=old-role-nonce")
	ordinary := recoveryMessage(t, r, "ordinary retained event")
	w.signals = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	w.codexSave = func(_, id string, data []byte) error {
		var e savedCodexEvent
		require.NoError(t, json.Unmarshal(data, &e))
		require.Len(t, e.Recovery, 1)
		assert.Equal(t, ordinary.ID, e.Recovery[0].ID)
		assert.NotEqual(t, old.ID, e.Recovery[0].ID)
		assert.NotEqual(t, oldCopy.ID, e.Recovery[0].ID)
		assert.Greater(t, e.HookEpoch, epoch)
		assert.NotEmpty(t, e.BootstrapNonce)
		return nil
	}
	code := run(codexArgs, strings.NewReader(""), io.Discard, io.Discard, w)
	assert.Zero(t, code)
}

// A fixed launch boundary archives history without creating model answers. A
// later answer cannot hide earlier post-boundary work, including after restart.
func TestRecoveryCutoverRetainsArchiveAndPostBoundaryAnswers(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	for i := 0; i < maxCodexRecovery+1; i++ {
		recoveryMessage(t, r, "archived notification")
	}
	r.h.Pass(time.Second)
	since, err := r.h.Client.Time(context.Background()).Result()
	require.NoError(t, err)
	first := recoveryMessage(t, r, "post-boundary work")
	second := recoveryMessage(t, r, "later answered work")
	require.NoError(t, r.h.Client.HSet(context.Background(), bus.AnswersOf("ada"), second.ID, "1791720000").Err())
	for restart := 0; restart < 2; restart++ {
		plan, err := codexBacklog(s, recoveryTail(t, s), since)
		require.NoError(t, err)
		require.Len(t, plan.messages, 1)
		assert.Equal(t, first.ID, plan.messages[0].ID)
	}
	count, err := r.h.Client.XLen(context.Background(), bus.StreamOf("ada")).Result()
	require.NoError(t, err)
	assert.EqualValues(t, maxCodexRecovery+3, count, "archive retained")
	answers, err := r.h.Client.HLen(context.Background(), bus.AnswersOf("ada")).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 1, answers, "only the actual model answer exists")
}

func TestRecoveryCutoverKeepsActualCurrentChallengeBeforeBoundary(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	s := recoverySession(r)
	ctx := context.Background()
	epoch, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "test")
	require.NoError(t, err)
	created, err := r.h.Store.Challenge(ctx, "ada", hook.Comms, epoch, []hook.Push{hook.Bus}, "current-cutover-nonce")
	require.NoError(t, err)
	require.True(t, created)
	challenge := recoveryMessage(t, r, "HOOK CHALLENGE nonce=current-cutover-nonce")
	recoveryMessage(t, r, "archived ordinary event")
	r.h.Pass(time.Second)
	since, err := r.h.Client.Time(ctx).Result()
	require.NoError(t, err)
	fresh := recoveryMessage(t, r, "new work")
	plan, err := codexBacklog(s, recoveryTail(t, s), since)
	require.NoError(t, err)
	require.Len(t, plan.messages, 2)
	assert.Equal(t, []string{challenge.ID, fresh.ID}, []string{plan.messages[0].ID, plan.messages[1].ID})
	assert.Equal(t, epoch, plan.challengeEpoch)
}

func TestRecoveryCutoverRefusesFutureEvenOnEmptyStream(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	now, err := r.h.Client.Time(context.Background()).Result()
	require.NoError(t, err)
	_, err = codexBacklog(recoverySession(r), "0-0", now.Add(time.Second))
	require.ErrorContains(t, err, "after Redis TIME")
	assert.Equal(t, hook.StateNone, r.h.State(t, "ada").State)
}
