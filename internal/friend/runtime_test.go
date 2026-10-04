package friend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeBus struct {
	session    friendbus.Session
	registered int
	accepted   []friendbus.Acceptance
	failures   []string
	acceptErr  error
	failureErr error
	recovered  []friendbus.Delivery
	stop       context.CancelFunc
}

func (b *fakeBus) GetSession(context.Context, string) (friendbus.Session, bool, error) {
	return b.session, b.session.ID != "", nil
}

func (b *fakeBus) Register(_ context.Context, _ string, s friendbus.Session) error {
	b.session = s
	b.registered++
	return nil
}

func (b *fakeBus) Claim(context.Context, string, string, int, time.Duration) ([]friendbus.Delivery, error) {
	if b.stop != nil {
		b.stop()
	}
	return nil, nil
}

func (b *fakeBus) ReclaimPage(context.Context, string, string, time.Duration, int, string) ([]friendbus.Delivery, string, error) {
	return b.recovered, "0-0", nil
}

func (b *fakeBus) Accept(_ context.Context, _ string, _ friendbus.Delivery, a friendbus.Acceptance) error {
	if b.acceptErr != nil {
		return b.acceptErr
	}
	b.accepted = append(b.accepted, a)
	b.recovered = nil
	return nil
}

func (b *fakeBus) RecordFailure(_ context.Context, _ string, _ friendbus.Delivery, reason string) error {
	b.failures = append(b.failures, reason)
	return b.failureErr
}

type fakeAdapter struct {
	session   friendbus.Session
	receipt   friendbus.Acceptance
	err       error
	previous  friendbus.Session
	wakeCalls int
	deadline  bool
}

func (a *fakeAdapter) Register(ctx context.Context, _ string, previous friendbus.Session) (friendbus.Session, error) {
	a.previous = previous
	_, a.deadline = ctx.Deadline()
	return a.session, a.err
}

func (a *fakeAdapter) Wake(ctx context.Context, _ friendbus.Delivery, _ friendbus.Session) (friendbus.Acceptance, error) {
	a.wakeCalls++
	_, a.deadline = ctx.Deadline()
	return a.receipt, a.err
}

func nativeSession() friendbus.Session {
	return friendbus.Session{ID: "session-1", Adapter: "native", Revision: "route-1", Capabilities: []string{"durable-delivery-id", "durable-receipt"}}
}

func nativeReceipt() friendbus.Acceptance {
	return friendbus.Acceptance{ReceiptID: "receipt-1", Adapter: "native", Session: "session-1", Revision: "route-1", Durable: true}
}

// A notification or stale route never acknowledges an addressed stream entry.
func TestOnlyTheCurrentDurableNativeReceiptAcknowledgesDelivery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*friendbus.Acceptance)
		want   int
	}{
		{"native durable receipt", func(*friendbus.Acceptance) {}, 1},
		{"notification alone", func(a *friendbus.Acceptance) { a.Durable = false }, 0},
		{"missing receipt", func(a *friendbus.Acceptance) { a.ReceiptID = "" }, 0},
		{"different session", func(a *friendbus.Acceptance) { a.Session = "other" }, 0},
		{"different adapter", func(a *friendbus.Acceptance) { a.Adapter = "other" }, 0},
		{"stale route", func(a *friendbus.Acceptance) { a.Revision = "old" }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &fakeBus{}
			a := &fakeAdapter{receipt: nativeReceipt()}
			tc.change(&a.receipt)
			r := Runtime{Bus: b, Adapter: a, Name: "friend", Timeout: time.Second}
			require.NoError(t, r.deliver(t.Context(), friendbus.Delivery{Key: "message-1/friend"}, nativeSession()))
			assert.Len(t, b.accepted, tc.want)
			assert.Len(t, b.failures, 1-tc.want)
			assert.True(t, a.deadline, "every injection is bounded")
		})
	}
}

// A lost Redis acceptance reply leaves the same stable delivery pending. The
// restored native adapter replays its prior receipt rather than injecting twice.
func TestPendingRecoveryReplaysTheStableNativeReceipt(t *testing.T) {
	t.Parallel()
	d := friendbus.Delivery{Key: "message-1/friend", Recipient: "friend"}
	b := &fakeBus{recovered: []friendbus.Delivery{d}, acceptErr: errors.New("acceptance reply lost")}
	a := &fakeAdapter{session: nativeSession(), receipt: nativeReceipt()}
	r := Runtime{Bus: b, Adapter: a, Name: "friend", Consumer: "consumer", Timeout: time.Second, Lease: time.Second, Retry: time.Second, Block: time.Second}
	require.ErrorContains(t, r.Run(t.Context()), "acceptance reply lost")
	assert.Empty(t, b.accepted)
	assert.Equal(t, []friendbus.Delivery{d}, b.recovered)
	b.acceptErr = nil
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b.stop = cancel
	require.ErrorIs(t, r.Run(ctx), context.Canceled)
	assert.Equal(t, 2, a.wakeCalls)
	assert.Equal(t, nativeSession(), a.previous, "startup passes the stored session to the native restore handshake")
	assert.Equal(t, []friendbus.Acceptance{nativeReceipt()}, b.accepted)
	assert.Empty(t, b.recovered)
}

func TestStartupCannotRegisterAnAdapterWithoutDurableRecovery(t *testing.T) {
	t.Parallel()
	b := &fakeBus{session: nativeSession()}
	a := &fakeAdapter{session: nativeSession()}
	a.session.Capabilities = []string{"notifications"}
	r := Runtime{Bus: b, Adapter: a, Name: "friend", Timeout: time.Second}
	_, err := r.Register(t.Context())
	require.ErrorContains(t, err, "durable-delivery-id")
	assert.Zero(t, b.registered)
	assert.Equal(t, nativeSession(), b.session, "an unsupported handshake does not replace the durable route")
}

func TestNativeFailureStaysPendingAndItsRecordingFailureSurfaces(t *testing.T) {
	t.Parallel()
	b := &fakeBus{}
	a := &fakeAdapter{err: errors.New("native session unavailable")}
	r := Runtime{Bus: b, Adapter: a, Name: "friend", Timeout: time.Second}
	require.NoError(t, r.deliver(t.Context(), friendbus.Delivery{Key: "message-1/friend"}, nativeSession()))
	assert.Empty(t, b.accepted)
	require.Len(t, b.failures, 1)
	assert.Contains(t, b.failures[0], "native session unavailable")
	b.failureErr = errors.New("failure record unavailable")
	err := r.deliver(t.Context(), friendbus.Delivery{Key: "message-1/friend"}, nativeSession())
	require.ErrorContains(t, err, "native session unavailable")
	require.ErrorContains(t, err, "failure record unavailable")
}
