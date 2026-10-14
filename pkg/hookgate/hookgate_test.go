package hookgate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/hooktest"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOutIsTheGatesNoAsAResult pins the result a gate's no becomes in every
// verb (docs/SPEC-HOOK.md, the gate in every tool): FAILED at exit 1 with
// reason unhooked, the hook command its remedy and the breadcrumb its notes;
// a record that cannot be read is REFUSED at exit 2; a yes is no result.
func TestOutIsTheGatesNoAsAResult(t *testing.T) {
	t.Parallel()
	assert.Nil(t, Out(nil))

	h := hooktest.New(t)
	err := h.Store.Gate(context.Background(), hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}, Role: hook.Comms})
	o := Out(err)
	require.NotNil(t, o)
	assert.Equal(t, 1, o.Exit)
	assert.Equal(t, []string{Reason}, o.Reasons)
	assert.Equal(t, "nova-hook hook --as ada --role comms", o.Remedy)
	var text strings.Builder
	o.Verb = "send"
	o.Render(&text, false)
	assert.Contains(t, text.String(), "SEND FAILED reason=unhooked: ada is not hooked in: no hook record")
	assert.Contains(t, text.String(), "SEND NOTE the bus push of a comms is held by: the friend daemon")

	o = Out(errors.New("LOADING the store is loading"))
	assert.Equal(t, 2, o.Exit)
	assert.Contains(t, o.Why[0], "the hook gate could not decide, so nothing runs: LOADING")
}

// TestGateReadsTheRecordOfTheBusStore pins Gate over the bus store a verb
// opened: the Redis store's record is the hook's, and a store that is no Redis
// keeps no record, so nothing runs (fail closed).
func TestGateReadsTheRecordOfTheBusStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := hooktest.New(t)
	st := bus.Redis{C: h.Client}
	want := hook.Want{Name: "ada", Need: []hook.Push{hook.Bus}}
	var refused *hook.Refusal
	assert.ErrorAs(t, Gate(ctx, st, want), &refused)
	h.Hook(t, "ada", hook.Comms)
	assert.NoError(t, Gate(ctx, st, want))

	assert.ErrorIs(t, Gate(ctx, bustest.NewFake(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), "ada"), want), ErrNoRecord)
}

// TestGatePassesAnActOnItsOwnNeed pins the gate over the bus store the verbs
// meet: a friend with her bus push proven and her work push down opens a bus
// act and is refused a work act, as Out words it.
func TestGatePassesAnActOnItsOwnNeed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := hooktest.New(t)
	st := bus.Redis{C: h.Client}
	hd := &hook.Holder{Store: h.Store, Name: "emma", Role: hook.Friend, Pushes: []hook.Push{hook.Bus}, By: "session"}
	require.NoError(t, hd.Start(ctx))
	require.NoError(t, hd.Beat(ctx, map[hook.Push]bool{hook.Bus: true}))

	assert.NoError(t, Gate(ctx, st, hook.Want{Name: "emma", Need: []hook.Push{hook.Bus}}))
	o := Out(Gate(ctx, st, hook.Want{Name: "emma", Need: []hook.Push{hook.Work}}))
	require.NotNil(t, o)
	assert.Equal(t, 1, o.Exit)
	assert.Equal(t, []string{Reason}, o.Reasons)
}
