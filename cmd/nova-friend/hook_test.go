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
	h       *hooktest.Rig
	dh      *daemonHook
	now     time.Time
	session bool
	lines   []string
}

func newHookRig(t *testing.T, role hook.Role) *hookRig {
	t.Helper()
	r := &hookRig{h: hooktest.New(t), now: start, session: true}
	w := world{now: func() time.Time { return r.now }, hookStore: func(bus.Store) (hook.Store, error) { return r.h.Store, nil }}
	r.dh = w.newDaemonHook(nil, "bob", role, func() bool { return r.session }, func(l string) { r.lines = append(r.lines, l) })
	require.NotNil(t, r.dh)
	return r
}

// TestALaneTakesNothingWhileAPushIsDown pins the friend's gate in her daemon
// (docs/SPEC-HOOK.md; tla/Hook.tla, GateMeansHooked): with her work push down
// (no beat answered yet) her lanes take nothing and report nothing; once the
// server answers a beat both pushes are proven and the take is sent; with her
// session's check unanswered the bus push is unproven and nothing is taken;
// unhooked, nothing is taken until she is hooked in again.
func TestALaneTakesNothingWhileAPushIsDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newHookRig(t, hook.Friend)
	sent := 0
	take := gated(r.dh, func(context.Context, []string) (string, error) { sent++; return "LANE OK", nil })

	r.dh.step(ctx)
	_, err := take(ctx, []string{"friend", "take"})
	require.ErrorIs(t, err, ErrUnhooked)
	assert.Contains(t, err.Error(), "live as friend (bus=proven work=armed)")
	assert.Zero(t, sent, "a lane with one push down takes nothing")
	assert.Equal(t, hook.StateLive, r.h.State(t, "bob").State)

	r.dh.beatAnswered(nil)
	r.dh.step(ctx)
	got, err := take(ctx, []string{"friend", "take"})
	require.NoError(t, err)
	assert.Equal(t, "LANE OK", got)
	assert.Equal(t, 1, sent)
	assert.Equal(t, hook.StateProven, r.h.State(t, "bob").State)

	r.session = false
	r.dh.step(ctx)
	_, err = take(ctx, nil)
	assert.ErrorIs(t, err, ErrUnhooked, "her session's check unanswered: the bus push is unproven")
	r.session = true

	r.now = r.now.Add(hook.LeaseTTL)
	r.dh.step(ctx)
	_, err = take(ctx, nil)
	assert.ErrorIs(t, err, ErrUnhooked, "no beat answered inside a lease: the work push is unproven")

	r.h.Unhook(t, "bob")
	r.dh.beatAnswered(nil)
	r.dh.step(ctx)
	_, err = take(ctx, nil)
	require.ErrorIs(t, err, ErrUnhooked)
	assert.Contains(t, err.Error(), "bob was unhooked (nova-hook unhook)")
	assert.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State, "an unhook stands against the running daemon")
	assert.Equal(t, 1, sent)

	_, _, err = r.h.Store.Arm(ctx, "bob", hook.Friend, "nova-hook", false)
	require.NoError(t, err)
	r.dh.step(ctx)
	_, err = take(ctx, nil)
	assert.NoError(t, err, "hooked in again, the daemon rejoins")
	assert.NotEmpty(t, r.lines, "every change of the gate's word is on the record")
}

// TestTheBeatSaysUnhooked pins her beat: while the gate says no her beat is a
// down beat with the reason unhooked, never an up beat that would show her
// alive; its answer is her work push's round trip; hooked in, the up beat goes.
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

	r.dh.step(ctx) // the down beat was answered: her work push is proven now
	_, err = beat(ctx, "srv", "bob", time.Time{}, friend.BeatWords{})
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, ups)

	r2 := newHookRig(t, hook.Friend)
	beatNoDown, _ := r2.dh.beats(func() time.Time { return r2.now }, up, nil)
	_, err = beatNoDown(ctx, "srv", "bob", time.Time{}, friend.BeatWords{})
	assert.ErrorIs(t, err, ErrUnhooked, "with no down beat an unhooked beat is not sent")
}

// TestNotificationsHoldTheBusAsComms pins the receiver's hold: as comms she
// needs only her bus push, held while the receiver runs.
func TestNotificationsHoldTheBusAsComms(t *testing.T) {
	t.Parallel()
	r := newHookRig(t, hook.Comms)
	r.dh.step(context.Background())
	ok, why := r.dh.ok()
	assert.True(t, ok, why)
	assert.Equal(t, hook.Comms, r.h.State(t, "bob").Role)
}

// TestAStoreWithNoHookRecordShutsTheGate pins fail closed: a bus store that
// keeps no hook record holds nothing and the gate stays shut.
func TestAStoreWithNoHookRecordShutsTheGate(t *testing.T) {
	t.Parallel()
	w := world{now: time.Now, hookStore: func(bus.Store) (hook.Store, error) {
		return hook.Store{}, errors.New("this bus store keeps no hook record")
	}}
	dh := w.newDaemonHook(nil, "bob", hook.Friend, func() bool { return true }, func(string) {})
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
		{"ping", "--as", "ada", "--to", "bob", "--nonce", "abc123"},
		{"watch", "--as", "ada", "--timeout", "1s"},
		{"serve", "--as", "ada", "--dry-run"},
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
	code, out := run1("ping", "--as", "ada", "--to", "bob", "--nonce", "abc123")
	assert.Equal(t, 0, code, out)
	assert.Contains(t, out, "PING OK nonce=abc123")
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
