package friend

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Notifications use the daemon's one receiver (SPEC-FRIEND.md, notifications): one
// hundred different cards are one aggregate ready-queue wake, while useful messages keep
// their payloads and transport acknowledgments and routine status do not wake the model.
func TestCodexNotificationsCoalesceBurstsWithoutLosingUrgentKinds(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.NotificationOnly = true
	r.d.NotificationStateDir = t.TempDir()
	r.d.Notifications = &NotificationPolicy{Kinds: []string{bus.KindRequest, bus.KindBlocker, bus.KindReport}, Window: 10 * time.Second}
	send := func(kind, subject, body string) {
		_, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: kind, Subject: subject, Body: body})
		require.NoError(t, err)
	}
	for i := range 100 {
		send(bus.KindStatus, fmt.Sprintf("card c-%d dealt: FRIEND-CARD DELIVERED friend=bob card=c-%d", i, i), "a card is available")
	}
	send(bus.KindBlocker, "blocked", "blocker payload")
	send(bus.KindRequest, "request", "request payload")
	send(bus.KindReport, "report", "report payload")
	send(bus.KindAck, "receipt", "transport receipt payload")
	send(bus.KindStatus, "routine", "routine status payload")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.cancel = cancel
	original := r.d.Now
	calls := 0
	r.d.Now = func() time.Time {
		calls++
		if calls >= 40 {
			cancel()
		}
		return original()
	}
	r.d.Pause = func(context.Context, time.Duration) { runtime.Gosched() }
	require.NoError(t, r.d.Run(ctx))
	r.mu.Lock()
	text := strings.Join(r.delivered, "\n")
	r.mu.Unlock()
	assert.Equal(t, 1, strings.Count(text, "NOVA-FRIEND READY QUEUE"), "the burst is global, never one wake per card")
	assert.Contains(t, text, "blocker payload")
	assert.Contains(t, text, "request payload")
	assert.Contains(t, text, "report payload")
	assert.False(t, strings.Contains(text, "transport receipt payload"))
	assert.False(t, strings.Contains(text, "routine status payload"))
}

// The entry guard applies before startup/restart work and before any failed delivery
// (SPEC-FRIEND.md, notifications); hook invocation, not only an empty queue, is refused.
func TestNotificationOnlyNeverInvokesNativeMutationHooks(t *testing.T) {
	t.Parallel()
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("enqueue-fails-%t", fail), func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.d.NotificationOnly = true
			r.d.NotificationStateDir = t.TempDir()
			r.d.Notifications = &NotificationPolicy{Window: 10 * time.Second}
			r.d.Beat = func(context.Context, time.Time) error { t.Error("native beat called"); return nil }
			r.d.Held = func(context.Context) (Row, error) { t.Error("held/claim path called"); return Row{}, nil }
			r.d.Stage = func(context.Context, Packet) (string, error) { t.Error("stage called"); return "", nil }
			r.d.Prune = func(context.Context, map[string]bool) ([]string, error) { t.Error("prune called"); return nil, nil }
			r.d.Finish = func(context.Context, []string) error { t.Error("finish called"); return nil }
			r.d.Progress = func(context.Context, []Card) error { t.Error("progress called"); return nil }
			r.d.Proof = func() (bool, string) { t.Error("native proof called"); return true, "" }
			r.d.Row = func() (string, int) { t.Error("native row called"); return ModeOneShot, 4 }
			r.d.Status = func(Status) error { t.Error("native status/proof written"); return nil }
			r.d.Seat = func(context.Context) (string, error) { t.Error("native seat called"); return "", nil }
			r.d.LoadLanes = func() (LaneState, error) { t.Error("lanes loaded"); return LaneState{}, nil }
			r.d.SaveLanes = func(LaneState) error { t.Error("lanes saved"); return nil }
			if fail {
				r.exit = 2
			}
			r.send(t, "ada", "request", "retain this input")
			// Select the legacy no-kind message explicitly so the failure/restart path runs.
			r.d.Notifications.Kinds = []string{bus.KindStatus}
			for range 2 {
				ctx, cancel := context.WithCancel(context.Background())
				r.cancel = cancel
				original := r.d.Now
				calls := 0
				r.d.Now = func() time.Time {
					calls++
					if calls > 25 {
						cancel()
					}
					return original()
				}
				r.d.Pause = func(context.Context, time.Duration) { runtime.Gosched() }
				require.NoError(t, r.d.Run(ctx))
				cancel()
				r.d.Now = original
			}
			state, err := ReadNotificationState(r.d.NotificationStateDir)
			require.NoError(t, err)
			if fail {
				pending := state.Pending
				if pending == nil {
					pending = state.Report
				}
				require.NotNil(t, pending)
				assert.False(t, pending.Accepted)
				assert.Contains(t, pending.Text, "retain this input")
			}
		})
	}
}

// The accepted journal phase survives a failed acknowledgment: recovery never calls
// enqueue again just to settle the same source entries (SPEC-FRIEND.md, notifications).
func TestNotificationAcceptanceIsNotModelProcessingAndRestartDoesNotEnqueueAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.NotificationOnly = true
	r.d.NotificationStateDir = t.TempDir()
	m := r.send(t, "ada", "request", "accepted input")
	e, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, WriteNotificationState(r.d.NotificationStateDir, NotificationState{Pending: &NotificationBatch{Text: "accepted", Entries: []string{e.Entry}, Messages: []bus.Message{m}, Accepted: true}}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.cancel = cancel
	original := r.d.Now
	calls := 0
	r.d.Now = func() time.Time {
		calls++
		if calls > 4 {
			cancel()
		}
		return original()
	}
	r.d.Pause = func(context.Context, time.Duration) { runtime.Gosched() }
	require.NoError(t, r.d.Run(ctx))
	assert.Empty(t, r.delivered, "accepted input is not sent again on restart")
	stages, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	require.Len(t, stages, 1)
	assert.Equal(t, bus.Delivered, stages[0].State, "queue acceptance is not read/acted proof")
}

// Queue families survive repeated delivery and daemon restarts while a queued wake is
// unread; a taken wake is distinct from a new eligible event (SPEC-FRIEND.md, notifications).
func TestCodexNotificationQueueStaysBoundedAndNewWorkAfterConsumptionCanWake(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	c := f.codex(nil)
	c.QueueOnly = true
	for range 100 {
		codexDeliver(t, c, NotificationReadyText)
	}
	assert.Equal(t, 1, f.queuedN)
	require.Len(t, f.texts(), 1)
	// A new adapter is a daemon restart, with no in-memory dedupe cache.
	c = f.codex(nil)
	c.QueueOnly = true
	codexDeliver(t, c, NotificationReadyText)
	assert.Equal(t, 1, f.queuedN)
	f.mu.Lock()
	f.queue = nil
	f.now = f.now.Add(2 * NotificationWindow)
	f.mu.Unlock()
	codexDeliver(t, c, NotificationReadyText)
	assert.Equal(t, 2, f.queuedN, "the previous wake was taken; new work is not suppressed forever")
	f.mu.Lock()
	f.listErr = fmt.Errorf("queue unavailable")
	f.mu.Unlock()
	exit, err := c.Deliver(context.Background(), NotificationReadyText)
	assert.Zero(t, exit)
	var deferred Deferred
	assert.ErrorAs(t, err, &deferred)
	assert.Equal(t, 2, f.queuedN, "no speculative enqueue when the queue cannot be checked")
}

// Distinct full report batches stay source-pending while one report is unread. Their
// capacity never borrows the urgent or ready category's slot (SPEC-FRIEND.md, notifications).
func TestDistinctUsefulNotificationsAreBackpressuredWithUrgentCapacityReserved(t *testing.T) {
	t.Parallel()
	f := &codexApp{now: t0}
	c := f.codex(nil)
	c.QueueOnly = true
	for i := range 100 {
		text := fmt.Sprintf("%sreport %064x\nfull report %d", NotificationBatchPrefix, i, i)
		exit, err := c.Deliver(context.Background(), text)
		assert.Zero(t, exit)
		if i == 0 {
			require.NoError(t, err)
		} else {
			var deferred Deferred
			require.ErrorAs(t, err, &deferred)
		}
	}
	assert.Equal(t, 1, f.queuedN)
	require.Len(t, f.texts(), 1)
	codexDeliver(t, c, NotificationBatchPrefix+"urgent first\nblocker payload")
	codexDeliver(t, c, NotificationReadyText)
	assert.Equal(t, 3, f.queuedN)
	require.Len(t, f.texts(), 3)
}

type notificationDelivery func(context.Context, string) (int, error)

func (f notificationDelivery) Deliver(ctx context.Context, text string) (int, error) {
	return f(ctx, text)
}

// A deferred nonurgent batch has a separate durable slot (SPEC-FRIEND.md,
// Notifications). Reports and opted-in notices cannot own urgent intake.
func TestDeferredNonUrgentDoesNotHoldLaterBlockerBehindIt(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{bus.KindReport, bus.KindStatus} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			dir := t.TempDir()
			send := func(kind, body string) {
				_, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: kind, Subject: kind, Body: body})
				require.NoError(t, err)
			}
			var delivered []string
			r.d.Deliver = notificationDelivery(func(_ context.Context, text string) (int, error) {
				if NotificationCategory(text) != "urgent" {
					return 0, Deferred{Reason: "nonurgent capacity occupied"}
				}
				delivered = append(delivered, text)
				return 0, nil
			})
			n := &notificationReceiver{d: r.d, b: r.bus, policy: NotificationPolicy{Kinds: []string{bus.KindReport, bus.KindStatus}, Window: NotificationWindow}, dir: dir}
			send(kind, "full nonurgent payload pending")
			require.NoError(t, n.step(context.Background(), t0))
			send(bus.KindBlocker, "urgent blocker survives")
			require.NoError(t, n.step(context.Background(), t0.Add(time.Second)))
			require.Len(t, delivered, 1)
			assert.Contains(t, delivered[0], "urgent blocker survives")
			require.NotNil(t, n.state.Report)
			assert.Contains(t, n.state.Report.Text, "full nonurgent payload pending")
			saved, err := ReadNotificationState(dir)
			require.NoError(t, err)
			require.NotNil(t, saved.Report)
		})
	}
}

// Category bounds apply through multiple real receive passes, not only to the queue
// helper (SPEC-FRIEND.md, Notifications; FriendNotificationsCapacity.tla).
func TestDistinctUsefulBusBurstsKeepOneUnreadBatchPerCategory(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{bus.KindReport, bus.KindRequest, bus.KindBlocker} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			app := &codexApp{now: t0}
			adapter := app.codex(nil)
			adapter.QueueOnly = true
			n := &notificationReceiver{d: r.d, b: r.bus, policy: NotificationPolicy{}, dir: t.TempDir()}
			n.d.Deliver = adapter
			for i := 0; i < 100; i++ {
				_, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: kind, Subject: fmt.Sprintf("distinct %d", i), Body: fmt.Sprintf("full distinct payload %d", i)})
				require.NoError(t, err)
			}
			for i := 0; i < 6; i++ {
				require.NoError(t, n.step(context.Background(), t0.Add(time.Duration(i)*time.Minute)))
			}
			assert.Len(t, app.texts(), 1)
			assert.Equal(t, 1, app.queuedN)
			pending, err := r.store.Pending(context.Background(), bus.StreamOf("bob"), "bob", 1000)
			require.NoError(t, err)
			assert.NotEmpty(t, pending)
		})
	}
}

// An enqueue refusal, restart and long idle retain the report's immutable input,
// while recovery uses newly available queue capacity (SPEC-FRIEND.md, Notifications).
func TestDeferredReportSurvivesRestartThenEnqueuesWhenCapacityOpens(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	app := &codexApp{now: t0, queueFail: true}
	adapter := app.codex(nil)
	adapter.QueueOnly = true
	n := &notificationReceiver{d: r.d, b: r.bus, policy: NotificationPolicy{}, dir: t.TempDir()}
	n.d.Deliver = adapter
	_, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindReport, Subject: "report", Body: "complete retained report"})
	require.NoError(t, err)
	require.NoError(t, n.step(context.Background(), t0))
	state, err := ReadNotificationState(n.dir)
	require.NoError(t, err)
	require.NotNil(t, state.Report)
	text := state.Report.Text
	app.queueFail = false
	n.state = state
	require.NoError(t, n.step(context.Background(), t0.Add(48*time.Hour)))
	require.Len(t, app.texts(), 1)
	assert.Equal(t, text, app.texts()[0])
	assert.Nil(t, n.state.Report)
	assert.Nil(t, n.state.Pending)
	pending, err := r.store.Pending(context.Background(), bus.StreamOf("bob"), "bob", 1000)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

// Deal notices coalesce per friend in the notification receiver (SPEC-FRIEND.md,
// notifications): five deal notices while the session is busy collapse into one turn
// carrying the newest, a batch subject coalesces with the per-card ones, and a notice
// whose cards are all taken before delivery is withdrawn without a turn. A batch list
// truncated at ten (with its "and N more" marker) is never withdrawn on its visible
// subset alone, since a card it omits may still be held.
func TestDealNoticesCoalescePerFriend(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := t.TempDir()
	r.d.Friend = "bob"
	var heldCards []string
	r.d.Cards = func() []string { return heldCards }
	heldCards = []string{"c-1", "c-2", "c-3", "c-4", "c-5", "c-6"}

	var mu sync.Mutex
	busy := true
	var delivered []string
	r.d.Deliver = notificationDelivery(func(_ context.Context, text string) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		if busy {
			return 0, Deferred{Reason: "busy"}
		}
		delivered = append(delivered, text)
		return 0, nil
	})

	n := &notificationReceiver{d: r.d, b: r.bus, policy: NotificationPolicy{Window: 5 * time.Second}, dir: dir}
	send := func(subject string) bus.Message {
		m, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindStatus, Subject: subject, Body: "cards ready in inbox"})
		require.NoError(t, err)
		return m
	}

	// Five deal notices arrive while the session is busy, batch and per-card mixed.
	send("card c-1 dealt: FRIEND-CARD DELIVERED friend=bob card=c-1")
	send("card c-2 dealt: FRIEND-CARD DELIVERED friend=bob card=c-2")
	send("cards dealt: 2 (c-3, c-4)")
	send("card c-5 dealt: FRIEND-CARD DELIVERED friend=bob card=c-5")
	m5 := send("cards dealt: 1 (c-6)")

	require.NoError(t, n.step(context.Background(), t0))
	require.NoError(t, n.step(context.Background(), t0.Add(10*time.Second)))
	mu.Lock()
	assert.Empty(t, delivered, "the session is busy: nothing is delivered yet")
	mu.Unlock()

	// The session is free: exactly one turn is delivered, carrying the newest notice.
	mu.Lock()
	busy = false
	mu.Unlock()
	require.NoError(t, n.step(context.Background(), t0.Add(10*time.Second+RecheckEvery)))
	mu.Lock()
	require.Len(t, delivered, 1, "the five notices coalesce into one turn")
	assert.Contains(t, delivered[0], "c-6", "the turn carries the newest notice")
	assert.NotContains(t, delivered[0], "c-1")
	assert.NotContains(t, delivered[0], "c-2")
	assert.NotContains(t, delivered[0], "c-5")
	mu.Unlock()
	stages, _, err := r.bus.Stages(context.Background(), "bob", m5.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stages)
	assert.Equal(t, bus.Delivered, stages[len(stages)-1].State)

	// A notice whose cards are all taken before delivery is withdrawn, not delivered.
	m7 := send("card c-7 dealt: FRIEND-CARD DELIVERED friend=bob card=c-7")
	heldCards = []string{"c-1"} // c-7 is not held: every card it names is taken
	now := t0.Add(100 * time.Second)
	require.NoError(t, n.step(context.Background(), now))
	require.NoError(t, n.step(context.Background(), now.Add(10*time.Second)))
	mu.Lock()
	assert.Len(t, delivered, 1, "the withdrawn notice produces no turn")
	mu.Unlock()
	stages7, _, err := r.bus.Stages(context.Background(), "bob", m7.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stages7)
	assert.Equal(t, bus.Delivered, stages7[len(stages7)-1].State)

	// A batch list is cut at ten with "and N more": its visible ten are a subset, so a
	// held card the list omits must keep the notice from being withdrawn on the subset alone.
	m8 := send("cards dealt: 12 (c-1, c-2, c-3, c-4, c-5, c-6, c-7, c-8, c-9, c-10, and 2 more)")
	heldCards = []string{"c-11"} // the visible ten have left the row; an omitted card is still held
	now = now.Add(100 * time.Second)
	require.NoError(t, n.step(context.Background(), now))
	require.NoError(t, n.step(context.Background(), now.Add(10*time.Second)))
	mu.Lock()
	require.Len(t, delivered, 2, "a truncated batch is delivered, not withdrawn on its visible subset")
	assert.Contains(t, delivered[1], "c-10")
	mu.Unlock()
	stages8, _, err := r.bus.Stages(context.Background(), "bob", m8.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stages8)
	assert.Equal(t, bus.Delivered, stages8[len(stages8)-1].State)
}
