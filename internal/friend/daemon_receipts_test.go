package friend

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A turn carrying a message marks it read when it starts and acted when it
// ends at exit 0, and a second delivery of that message id is dropped with one
// record line and acked, never pushed in twice (SPEC-BUS.md, receipts;
// tla/Bus2.tla: Drop, NoIdActedTwice).
func TestARedeliveredIdAfterAnActedTurnIsDroppedAndAcked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	m := r.send(t, "ada", "hello", "are you there?")
	r.at[3] = func() { // the claim hands the same message in again, as a new entry of the stream
		require.NoError(t, r.store.AddAll(context.Background(), []string{bus.StreamOf("bob")}, m.Fields()))
	}
	r.run(t, 8)
	require.Len(t, r.delivered, 1, "the message goes into the session once")
	got, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.StateActed, got[0].State)
	var dropped int
	for _, line := range r.records {
		if strings.Contains(line, "duplicate dropped id="+m.ID) {
			dropped++
		}
	}
	assert.Equal(t, 1, dropped, "one record line says the duplicate was dropped: %v", r.records)
	pending, fresh, err := r.bus.Peek(context.Background(), "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "the duplicate is acked")
	assert.Empty(t, fresh)
}

// A turn that exits non-zero marks the message read and never acted, so a
// redelivery is pushed in again.
func TestAFailedTurnIsReadAndNeverActed(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.exit = 3
	m := r.send(t, "ada", "hello", "x")
	r.run(t, 4)
	got, _, err := r.bus.Stages(context.Background(), "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, bus.StateRead, got[0].State)
}
