package friend

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Receipt acceptance is independent of subprocess output (SPEC-BUS.md, message-receipts).
func TestReadReceiptWaitsForAdapterAcceptance(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	m := r.send(t, "ada", "acceptance", "one")
	e, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	l := &loop{d: r.d, b: r.bus, ctx: context.Background(), silentStop: time.Hour}
	tr := &turn{entries: []string{e.Entry}, msgs: []bus.Message{m}, seen: &atomic.Int64{}, lastOut: t0}
	tr.seen.Add(1) // a preflight or refusal printed, but no adapter accepted
	l.watch(tr, t0.Add(time.Second))
	got, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.Delivered, got[0].State)
	tr.accepted.Store(true) // an explicit acceptance, even with no new output
	l.watch(tr, t0.Add(2*time.Second))
	got, _, err = r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.Read, got[0].State)
}

// A failed launch is no word from the session (SPEC-BUS.md, message-receipts).
func TestReadReceiptNeverOnUnacceptedFailure(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	m := r.send(t, "ada", "launch", "one")
	e, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	l := &loop{d: r.d, b: r.bus, ctx: context.Background(), inHand: map[string]bool{e.Entry: true}, failed: map[string]int{}}
	l.settle(&turn{entries: []string{e.Entry}, msgs: []bus.Message{m}}, false, errors.New("exec: unavailable"), t0)
	got, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.Delivered, got[0].State)
}

// A launch that never starts is not the session taking the turn.
func TestRealExecDoesNotAcceptACommandThatDoesNotStart(t *testing.T) {
	t.Parallel()
	var n atomic.Int64
	ctx := WithTurnAccepted(context.Background(), func() { n.Add(1) })
	_, _, err := realExec(ctx, time.Second, t.TempDir(), filepath.Join(t.TempDir(), "missing-delivery"), nil, "")
	require.Error(t, err)
	assert.Equal(t, int64(0), n.Load())
}

// A listing or export context carries no acceptance, even if something calls it.
func TestWithoutTurnAcceptanceDropsTheSignal(t *testing.T) {
	t.Parallel()
	var n atomic.Int64
	ctx := WithTurnAccepted(context.Background(), func() { n.Add(1) })
	TurnAccepted(withoutTurnAcceptance(ctx))
	assert.Equal(t, int64(0), n.Load())
	TurnAccepted(ctx)
	assert.Equal(t, int64(1), n.Load())
}

// Acceptance can arrive before exit and does not depend on printing.
func TestAdapterAcceptanceIsSignalledOnceWithoutOutput(t *testing.T) {
	t.Parallel()
	var accepted atomic.Int64
	ctx := WithTurnAccepted(context.Background(), func() { accepted.Add(1) })
	TurnAccepted(ctx)
	TurnAccepted(ctx)
	assert.Equal(t, int64(1), accepted.Load())
}

// A failed durable completion commit is retried before ack, never by replaying
// the external session (SPEC-BUS.md, message-receipts).
func TestFailedActedReceiptCommitWaitsForStoreBeforeAck(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	m := r.send(t, "ada", "commit", "one")
	e, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	l := &loop{d: r.d, b: r.bus, ctx: context.Background(), inHand: map[string]bool{e.Entry: true}, failed: map[string]int{}}
	tr := &turn{entries: []string{e.Entry}, msgs: []bus.Message{m}}
	r.store.FailForward = errors.New("receipts unavailable")
	assert.Contains(t, l.settle(tr, true, nil, t0), "ack=waiting-for-durable-receipt")
	require.Len(t, l.completed, 1)
	pending, _, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	r.store.FailForward = nil
	l.flushCompleted(t0)
	assert.Empty(t, l.completed)
	pending, _, err = r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	got, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.Acted, got[0].State)
	assert.Empty(t, r.delivered, "only the receipt commit is retried")
}

// A receipt outage cannot turn an already acted id into an eligible delivery
// (SPEC-BUS.md, message-receipts).
func TestReceiptLookupFailureNeverPermitsTake(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	m := r.send(t, "ada", "once", "one")
	_, ok, err := r.bus.Recv(context.Background(), "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = r.bus.Stamp(context.Background(), "bob", bus.Acted, m.ID)
	require.NoError(t, err)
	r.store.Advance(bus.ClaimAfter)
	l := &loop{d: r.d, b: r.bus, ctx: context.Background()}
	r.bus.OnStampError = func(err error) { l.receiptErr = err }
	r.store.FailForward = errors.New("receipt lookup unavailable")
	_, ok, err = l.recv(0)
	require.Error(t, err)
	assert.False(t, ok, "unknown store state is no authorization to deliver")
	r.store.FailForward = nil
	r.store.Advance(bus.ClaimAfter)
	e, ok, err := l.recv(0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, bus.Acted, e.Stage, "the durable receipt still directs the duplicate drop")
}
