package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reason of a write (nova-tools#5101): the verb's --reason travels to the
// store with the write and is recorded in its history row, so
// `nova-config <kind> history` tells why each change was made. The reason is
// metadata of the write, never a row field: a write that names none is the
// history row it was before the reason existed.

func TestTheReasonOfAWriteIsInItsHistoryRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	m, err := machine.NewRow("box", map[string]string{"user": "u", "seat": "s", "slots": "8"})
	require.NoError(t, err)
	_, err = st.Insert(WithReason(ctx, "the only 64-core box left"), KindMachine, m, "rowan")
	require.NoError(t, err)
	_, _, err = st.Update(WithReason(ctx, "held for the measured load"), KindMachine, "box", map[string]string{"note": "held"}, "rowan")
	require.NoError(t, err)
	_, err = st.Delete(WithReason(ctx, "returned to the pool"), KindMachine, "box", "rowan")
	require.NoError(t, err)

	hist, err := st.History(ctx, KindMachine, "box")
	require.NoError(t, err)
	require.Len(t, hist, 3)
	assert.Equal(t, "the only 64-core box left", hist[0].Reason, "the add's reason")
	assert.Equal(t, "held for the measured load", hist[1].Reason, "the set's reason")
	assert.Equal(t, "returned to the pool", hist[2].Reason, "the remove's reason")
	assert.Contains(t, HistoryLine(hist[0]), `reason=the\x20only\x2064-core\x20box\x20left`, "history prints the add's reason")
	assert.Contains(t, HistoryLine(hist[1]), `reason=held\x20for\x20the\x20measured\x20load`, "history prints the set's reason")
	assert.Contains(t, HistoryLine(hist[2]), `reason=returned\x20to\x20the\x20pool`, "history prints the remove's reason")
}

func TestAWriteWithoutAReasonRecordsNone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	m, err := machine.NewRow("box", map[string]string{"user": "u", "seat": "s", "slots": "8"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, m, "rowan")
	require.NoError(t, err)

	hist, err := st.History(ctx, KindMachine, "box")
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.Equal(t, "", hist[0].Reason)
	assert.NotContains(t, HistoryLine(hist[0]), "reason=", "a write that named no reason prints none")
	assert.Contains(t, HistoryLine(hist[0]), "HISTORY id=1 kind=machine name=box op=add actor=rowan at=")
}

func TestTheDryRunNamesTheReasonItWouldRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	row, err := machine.NewRow("box", map[string]string{"user": "u", "seat": "s", "slots": "8"})
	require.NoError(t, err)
	plan, err := PlanWrite(ctx, st, OpAdd, KindMachine, row, nil)
	require.NoError(t, err)
	plan.Actor = "rowan"
	assert.NotContains(t, PlanLine(plan), "reason=", "a plan with no reason prints none")

	plan.Reason = "the only 64-core box left"
	assert.Contains(t, PlanLine(plan), `reason=the\x20only\x2064-core\x20box\x20left`, "the plan names the reason it would record")
}
