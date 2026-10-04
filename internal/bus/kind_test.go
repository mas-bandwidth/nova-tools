package bus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendCarriesTheKindAndReadersFilterOnIt pins SPEC-BUS.md, the kind of a
// message: a message carries its kind, a reader may ask for some kinds, a
// message the filter skips is left for the next reader, and a message with no
// kind field reads as status.
func TestSendCarriesTheKindAndReadersFilterOnIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	send := func(b *Bus, kind string) Message {
		t.Helper()
		m := msg("ada", "bob")
		m.Kind = kind
		got, err := b.Send(ctx, m)
		require.NoError(t, err)
		return got
	}

	t.Run("sent with a kind, read back with it, filtered on it", func(t *testing.T) {
		t.Parallel()
		b, _ := rig(t, "ada", "bob")
		status := send(b, "")
		request := send(b, "request")
		assert.Equal(t, KindStatus, status.Kind, "no kind sent is status")
		assert.Equal(t, KindRequest, request.Kind)

		e, ok, err := b.RecvKinds(ctx, "bob", 0, []string{KindRequest})
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, request.ID, e.Message().ID)
		assert.Equal(t, KindRequest, e.Message().Kind)

		_, ok, err = b.RecvKinds(ctx, "bob", 0, []string{KindRequest})
		require.NoError(t, err)
		assert.False(t, ok, "the status message is skipped, not the request again")

		// the skipped message is still there for a reader that asks for all
		e, ok, err = b.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, status.ID, e.Message().ID)
	})

	t.Run("a skipped message is neither acked nor held", func(t *testing.T) {
		t.Parallel()
		b, _ := rig(t, "ada", "bob")
		status := send(b, "status")
		send(b, "blocker")
		_, _, err := b.RecvKinds(ctx, "bob", 0, []string{KindBlocker})
		require.NoError(t, err)
		e, ok, err := b.RecvKinds(ctx, "bob", 0, []string{KindStatus, KindReport})
		require.NoError(t, err)
		require.True(t, ok, "the skipped status message is handed at once, not after ClaimAfter")
		assert.Equal(t, status.ID, e.Message().ID)
	})

	t.Run("an old message without the field reads as status", func(t *testing.T) {
		t.Parallel()
		b, f := rig(t, "ada", "bob")
		old := msg("ada", "bob").Fields()
		delete(old, "kind")
		old["id"] = "OLD"
		require.NoError(t, f.AddAll(ctx, []string{StreamOf("bob"), LogKey}, old))
		e, ok, err := b.RecvKinds(ctx, "bob", 0, []string{KindStatus})
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "OLD", e.Message().ID)
		assert.Equal(t, KindStatus, e.Message().KindName())
	})

	t.Run("an unknown kind is refused, sending or asking", func(t *testing.T) {
		t.Parallel()
		b, _ := rig(t, "ada", "bob")
		m := msg("ada", "bob")
		m.Kind = "gossip"
		_, err := b.Send(ctx, m)
		var r *Refusal
		require.ErrorAs(t, err, &r)
		assert.Contains(t, err.Error(), "report, ack, status, request, blocker")
		_, _, err = b.RecvKinds(ctx, "bob", 0, []string{"gossip"})
		require.ErrorAs(t, err, &r)
	})

	t.Run("peek filters on the kind", func(t *testing.T) {
		t.Parallel()
		b, _ := rig(t, "ada", "bob")
		send(b, "status")
		ack := send(b, "ack")
		_, fresh, err := b.Peek(ctx, "bob")
		require.NoError(t, err)
		got := FilterKinds(fresh, []string{KindAck})
		require.Len(t, got, 1)
		assert.Equal(t, ack.ID, got[0].Message().ID)
		assert.Len(t, FilterKinds(fresh, nil), 2, "no filter keeps every message")
	})
}
