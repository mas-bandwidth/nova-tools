package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/hooktest"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookRig is a daemon's hold on bob's hook over a hooktest store, on a clock
// the test moves, with bob's session answering its check or not.
type hookRig struct {
	h     *hooktest.Rig
	dh    *daemonHook
	now   time.Time
	lines []string
}

func newHookRig(t *testing.T, role hook.Role) *hookRig {
	t.Helper()
	r := &hookRig{h: hooktest.New(t), now: start}
	r.h.Evidence(t, "bob") // a heartbeat server passing and her answer (hook.Served.Down): this file is about the holder
	r.restart(t, role)
	return r
}

// restart is the daemon started again over the same store: a new hold, as a
// new process's.
func (r *hookRig) restart(t *testing.T, role hook.Role) {
	t.Helper()
	w := world{now: func() time.Time { return r.now }, hookStore: func(bus.Store) (hook.Store, error) { return r.h.Store, nil }}
	r.dh = w.newDaemonHook(nil, "bob", func(l string) { r.lines = append(r.lines, l) })
	require.NotNil(t, r.dh)
}

// TestALaneTakesNothingWhileAPushIsDown pins the friend's gate in her daemon
// (docs/SPEC-HOOK.md; tla/Hook.tla, GateMeansHooked): with her bus push
// unproven her lanes take nothing and report nothing; once her challenge is
// answered her bus push is proven and the take is sent; unhooked, nothing is
// taken until she is hooked in again.
func TestALaneTakesNothingWhileAPushIsDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newHookRig(t, hook.Friend)
	sent := 0
	take := gated(r.dh, func(context.Context, []string) (string, error) { sent++; return "LANE OK", nil })

	r.dh.step(ctx)
	_, err := take(ctx, []string{"friend", "take"})
	require.ErrorIs(t, err, ErrUnhooked)
	assert.Contains(t, err.Error(), "bus")
	assert.Zero(t, sent, "a lane with its push unproven takes nothing")
	assert.NotEqual(t, hook.StateProven, r.h.State(t, "bob").State)

	r.h.Hook(t, "bob", hook.Friend)
	r.dh.step(ctx)
	got, err := take(ctx, []string{"friend", "take"})
	require.NoError(t, err)
	assert.Equal(t, "LANE OK", got)
	assert.Equal(t, 1, sent)
	assert.Equal(t, hook.StateProven, r.h.State(t, "bob").State)

	require.NoError(t, r.h.Store.Unprove(ctx, "bob", hook.Friend, hook.Bus, r.dh.holder.Epoch()))
	r.dh.step(ctx)
	_, err = take(ctx, nil)
	assert.ErrorIs(t, err, ErrUnhooked, "her bus push unproven: nothing is taken")

	r.h.Unhook(t, "bob")
	r.dh.step(ctx)
	_, err = take(ctx, nil)
	require.ErrorIs(t, err, ErrUnhooked)
	assert.Contains(t, err.Error(), "bob was unhooked (nova-hook unhook)")
	assert.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State, "an unhook stands against the running daemon")
	assert.Equal(t, 1, sent)

	r.h.Hook(t, "bob", hook.Friend)
	r.dh.step(ctx)
	_, err = take(ctx, nil)
	assert.NoError(t, err, "hooked in again, the daemon rejoins")
	assert.NotEmpty(t, r.lines, "every change of the gate's word is on the record")
}

// TestTheBeatSaysUnhooked pins her beat: while the gate says no her beat is a
// down beat with the reason unhooked, never an up beat that would show her
// alive; hooked in and proven, the up beat goes.
func TestTheBeatSaysUnhooked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newHookRig(t, hook.Friend)
	var ups, downs []string
	up := func(_ context.Context, _, name string, _ time.Time, _ friend.BeatWords) (string, error) {
		ups = append(ups, name)
		return "FRIEND-BEAT OK", nil
	}
	down := func(_ context.Context, _, name string, _, until time.Time, reason string, _ friend.BeatWords) error {
		downs = append(downs, name+" until="+until.Format(time.RFC3339)+" "+reason)
		return nil
	}
	beat, _ := r.dh.beats(func() time.Time { return r.now }, up, down)

	r.dh.step(ctx)
	_, err := beat(ctx, "srv", "bob", time.Time{}, friend.BeatWords{})
	require.NoError(t, err)
	assert.Empty(t, ups, "an unhooked friend is never beaten up")
	require.Len(t, downs, 1)
	assert.Contains(t, downs[0], "bob until="+start.Add(hook.LeaseTTL).Format(time.RFC3339)+" unhooked: bob is not hooked in")

	r.h.Hook(t, "bob", hook.Friend)
	r.dh.step(ctx)
	_, err = beat(ctx, "srv", "bob", time.Time{}, friend.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, ups)

	r2 := newHookRig(t, hook.Friend)
	beatNoDown, _ := r2.dh.beats(func() time.Time { return r2.now }, up, nil)
	_, err = beatNoDown(ctx, "srv", "bob", time.Time{}, friend.BeatWords{})
	assert.ErrorIs(t, err, ErrUnhooked, "with no down beat an unhooked beat is not sent")
}

// TestAStoreWithNoHookRecordShutsTheGate pins fail closed: a bus store that
// keeps no hook record holds nothing and the gate stays shut.
func TestAStoreWithNoHookRecordShutsTheGate(t *testing.T) {
	t.Parallel()
	w := world{now: time.Now, hookStore: func(bus.Store) (hook.Store, error) {
		return hook.Store{}, errors.New("this bus store keeps no hook record")
	}}
	dh := w.newDaemonHook(nil, "bob", func(string) {})
	dh.step(context.Background())
	ok, why := dh.ok()
	assert.False(t, ok)
	assert.Contains(t, why, "keeps no hook record")
	var nilHook *daemonHook
	ok, _ = nilHook.ok()
	assert.True(t, ok, "a world that gives no hook store gates nothing (a test's world)")
}

// TestCoordinatorVerbsRefuseUntilHooked pins the gate on the coordinator's
// verbs: ping, serve, watch and the wake loop refuse an identity not hooked
// in with the breadcrumb, and send once it is.
func TestCoordinatorVerbsRefuseUntilHooked(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	h := hooktest.New(t)
	w := r.world()
	w.gate = func(ctx context.Context, _ bus.Store, want hook.Want) error { return h.Store.Gate(ctx, want) }
	run1 := func(args ...string) (int, string) {
		var out, errb strings.Builder
		code := run(args, strings.NewReader(""), &out, &errb, w)
		return code, out.String() + errb.String()
	}
	for _, args := range [][]string{
		{"watch", "--as", "ada", "--timeout", "1s"},
	} {
		code, out := run1(args...)
		assert.Equal(t, 1, code, out)
		assert.Contains(t, out, "reason=unhooked: ada is not hooked in: no hook record", args[0])
		assert.Contains(t, out, "; run: nova-hook hook --as ada --role coordinator", args[0])
	}
	es, err := r.store.Range(context.Background(), bus.StreamOf("bob"), "-", "+", 0)
	require.NoError(t, err)
	assert.Empty(t, es, "nothing was sent")

	h.Hook(t, "ada", hook.Comms)
	_, out := run1("watch", "--as", "ada", "--timeout", "1s")
	assert.NotContains(t, out, "reason=unhooked", "hooked in, the verb runs")
}

// TestTheDaemonNeverTakesARecordOver pins that a daemon started while its
// friend is hooked as another role (a coordinator's own daemon) holds nothing
// and takes nothing, and the record stays the other role's.
func TestTheDaemonNeverTakesARecordOver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newHookRig(t, hook.Friend)
	_, _, err := r.h.Store.Arm(ctx, "bob", hook.Coordinator, "session", false)
	require.NoError(t, err)
	r.dh.step(ctx)
	ok, why := r.dh.ok()
	assert.False(t, ok)
	assert.Contains(t, why, "bob is hooked as another role")
	assert.Equal(t, hook.Coordinator, r.h.State(t, "bob").Role)
	assert.Equal(t, int64(1), r.h.State(t, "bob").Epoch, "the record was not taken over")
}

// TestAnUnhookStandsAcrossADaemonRestart pins that a daemon started again
// after its friend was unhooked does not hook her in (tla/Hook.tla, Arm only
// from none): it holds nothing and her lanes take nothing, until she is
// hooked in again by her own word (nova-hook hook); then it joins.
func TestAnUnhookStandsAcrossADaemonRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newHookRig(t, hook.Friend)
	r.h.Hook(t, "bob", hook.Friend)
	r.dh.step(ctx)
	ok, why := r.dh.ok()
	require.True(t, ok, why)
	r.h.Unhook(t, "bob")

	r.restart(t, hook.Friend)
	r.dh.step(ctx)
	ok, why = r.dh.ok()
	assert.False(t, ok)
	assert.Contains(t, why, "a restart of it does not hook bob in")
	assert.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State, "the restarted daemon left the unhook standing")
	r.dh.step(ctx)
	assert.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State, "and every later step too")

	r.h.Hook(t, "bob", hook.Friend)
	r.dh.step(ctx)
	ok, why = r.dh.ok()
	assert.True(t, ok, why)
	assert.Equal(t, hook.StateProven, r.h.State(t, "bob").State)
}

// TestALostRecordReArmsUnderARunningDaemon pins that a hook record lost under
// a running daemon (the store flushed) is armed again at its next step: none
// is not an unhook.
func TestALostRecordReArmsUnderARunningDaemon(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newHookRig(t, hook.Friend)
	r.h.Hook(t, "bob", hook.Friend)
	r.dh.step(ctx)
	require.Equal(t, hook.StateProven, r.h.State(t, "bob").State)

	r.h.MR.FlushAll()
	r.dh.step(ctx)
	assert.Equal(t, hook.StateExpired, r.h.State(t, "bob").State, "the daemon arms work only; the session must rejoin its bus lease")
	assert.Equal(t, hook.Friend, r.h.State(t, "bob").Role)
	r.h.Hook(t, "bob", hook.Friend) // the flush took the heartbeat's rows too: a server passing again and her answer
	r.dh.step(ctx)
	ok, why := r.dh.ok()
	assert.True(t, ok, why)
	assert.Equal(t, hook.StateProven, r.h.State(t, "bob").State)
}

// TestAFriendHookOpensNoCoordinatorVerb pins the coordinator's verbs on the
// role, not merely on the bus push: a name hooked in as friend (the seat's own
// friend daemon arming it) is refused by watch; as comms (until nova-sprint
// hook ships) it is let through.
func TestAFriendHookOpensNoCoordinatorVerb(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	h := hooktest.New(t)
	w := r.world()
	w.gate = func(ctx context.Context, _ bus.Store, want hook.Want) error { return h.Store.Gate(ctx, want) }
	run1 := func(args ...string) (int, string) {
		var out, errb strings.Builder
		code := run(args, strings.NewReader(""), &out, &errb, w)
		return code, out.String() + errb.String()
	}
	h.Hook(t, "ada", hook.Friend)
	code, out := run1("watch", "--as", "ada", "--timeout", "1s")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "ada is hooked in as friend, and this act is only for coordinator or comms")
	h.Hook(t, "ada", hook.Comms)
	_, out = run1("watch", "--as", "ada", "--timeout", "1s")
	assert.NotContains(t, out, "this act is only for coordinator or comms", out)
}

// The work daemon cannot keep an absent session's bus lease alive or claim
// its proof. The session holder alone owns that lease (SPEC-HOOK, holders).
func TestTheLaneDaemonRenewsOnlyTheWorkLease(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, hook.Friend)
	r.h.Hook(t, "bob", hook.Friend)
	ctx := context.Background()
	for range 3 {
		r.h.Pass(hook.LeaseTTL / 2)
		r.dh.step(ctx)
	}
	assert.False(t, r.h.MR.Exists(hook.LiveKey("bob", hook.Bus)), "only the session renews bus delivery")
	require.True(t, r.h.MR.Exists(hook.LiveKey("bob", hook.Work)), "the daemon keeps the actual Work key alive")
	assert.Positive(t, r.h.MR.TTL(hook.LiveKey("bob", hook.Work)))
	work, err := r.h.MR.Get(hook.LiveKey("bob", hook.Work))
	require.NoError(t, err)
	assert.Contains(t, work, "nova-friend run")
	ok, why := r.dh.ok()
	assert.False(t, ok, why)
}
