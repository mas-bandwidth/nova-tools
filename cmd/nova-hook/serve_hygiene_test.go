package main

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOnlyAChangeOfLevelIsPushed pins the push rule (tla/HookBeat.tla, PS,
// NoPushWithinDown and ArmingPushedAtPass): the levels are down, live and
// proven. A name coming up from down is FRIEND UP at the pass that finds it,
// armed or proven (2026-10-18: a friend armed at 15:57:25 and nothing said so
// until her proof a minute later); its fall back is FRIEND STATE; a change
// between two down states is no message; a mark written before the rule reads
// by its level, so an old expired mark is down and pushes nothing.
func TestOnlyAChangeOfLevelIsPushed(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.step(t, []string{"ada"}, "ada", "bob")
	r.step(t, []string{"ada"}, "ada", "bob") // the baseline is written: nothing pushed
	require.Zero(t, r.count(t, "ada", "FRIEND STATE "))

	r.h.Unhook(t, "bob")
	r.step(t, []string{"ada"}, "ada")
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND STATE bob proven->down"), "proven to unhooked is a change of level")

	_, _, err := r.h.Store.HookAs(ctx, "bob", hook.Comms, "hooktest") // an arming, its proof not yet in: live
	require.NoError(t, err)
	require.NoError(t, (&hook.Holder{Store: r.h.Store, Name: "bob", Role: hook.Comms, Pushes: hook.PushesOf(hook.Comms), By: "hooktest"}).Start(ctx))
	require.Equal(t, hook.StateLive, r.h.State(t, "bob").State)
	r.step(t, []string{"ada"}, "ada")
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND UP bob live at="), "armed is up at the pass that finds it; a comms name has no sprint row")
	r.h.Unhook(t, "bob")
	r.step(t, []string{"ada"}, "ada")
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND STATE bob live->down"))
	r.step(t, []string{"ada"}, "ada")
	assert.Equal(t, 2, r.count(t, "ada", "FRIEND STATE "), "down stays down: no push")

	// a mark from before the rule: expired reads as down, so bob down is no change
	require.NoError(t, r.h.Client.HSet(ctx, hook.ServeKey, "pushed:bob", "expired:7").Err())
	r.step(t, []string{"ada"}, "ada")
	assert.Equal(t, 2, r.count(t, "ada", "FRIEND STATE "), "an old expired mark is down")
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND UP "))

	r.h.Hook(t, "bob", hook.Comms)
	r.step(t, []string{"ada"}, "ada", "bob")
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND UP bob proven at="), "down to proven is up too")
	assert.Equal(t, 2, r.count(t, "ada", "FRIEND UP "))
}

// TestTheCoordinatorsOwnStateIsNeverPushedToHer pins NeverPushedSelf: the
// heartbeat server runs as the coordinator and pushes to her, and her own
// state against her mark (rowan's proven->partial->proven, pushed to rowan 60
// times in 34 hours, 2026-10-17) is never a message; a friend's still is.
func TestTheCoordinatorsOwnStateIsNeverPushedToHer(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.step(t, []string{"ada"}, "ada", "bob")
	r.step(t, []string{"ada"}, "ada", "bob")
	other := "down"
	if pushLevel(hook.PushState(r.h.State(t, "ada"))) != hook.StateProven {
		other = hook.StateProven
	}
	// her mark says the other side of proven: the rule before pushed that to her
	require.NoError(t, r.h.Client.HSet(ctx, hook.ServeKey, "pushed:ada", other+":3", "pushed:bob", "down:3").Err())
	for range 3 {
		r.step(t, []string{"ada"}, "ada", "bob")
	}
	assert.Zero(t, r.count(t, "ada", "FRIEND STATE ada"), "her own state is never pushed to her")
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND UP bob proven"), "a friend's change still is")
}

// TestANameWithNoRosterRowUnhooksOnTheUsualMissCount pins finding 4 of
// 2026-10-17: friend-stella, in hook:names with no nova-config row, was
// challenged every pass for ever, each send refused as no known name and
// never counted as a miss (1,443 fault lines). A refused unknown-name
// challenge now counts as sent, so the name misses and unhooks on the usual
// count, and is challenged no more.
func TestANameWithNoRosterRowUnhooksOnTheUsualMissCount(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "zed", hook.Comms) // hooked, and no roster row: every send to zed is refused
	var out string
	for i := 0; i < 40 && r.h.State(t, "zed").State != hook.StateUnhooked; i++ {
		out += r.step(t, []string{"ada"}, "ada")
	}
	require.Equal(t, hook.StateUnhooked, r.h.State(t, "zed").State, out)
	assert.Contains(t, r.h.State(t, "zed").Reason, "challenges missed")
	faults := strings.Count(out, "zed is no known name")
	for range 10 {
		out += r.step(t, []string{"ada"}, "ada")
	}
	assert.Equal(t, faults, strings.Count(out, "zed is no known name"), "unhooked, it is challenged no more")
}

// TestOnlyTheUnknownNameRefusalIsAMiss pins the cold read of #5954: a send
// that fails for another reason is not the name's miss. A challenge whose send
// times out, with the name off the roster, is sent again and never unhooks the
// name; a roster that answers empty (restored empty, not yet synced) refuses
// the server's own name too, and is a store fault, not a miss.
func TestOnlyTheUnknownNameRefusalIsAMiss(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, r *rig, w *world)
	}{
		{"timeout", func(t *testing.T, r *rig, w *world) {
			deliver := w.deliver
			w.deliver = func(b *bus.Bus, m bus.Message) (bus.Message, error) {
				if slices.Contains(m.To, "zed") {
					return bus.Message{}, context.DeadlineExceeded
				}
				return deliver(b, m)
			}
		}},
		{"empty roster", func(t *testing.T, r *rig, w *world) {
			require.NoError(t, r.h.Client.Del(context.Background(), "friends", "machines").Err())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.h.Hook(t, "ada", hook.Coordinator)
			r.h.Hook(t, "zed", hook.Comms) // no roster row
			w := r.world()
			tc.setup(t, r, &w)
			cli := testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
				return run(args, stdin, stdout, stderr, w)
			})
			var out string
			for range 40 {
				res := cli.Run("serve", "--as", "ada", "--once", "--down-after", stepDownAfter)
				require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
				out += res.Stdout
				r.h.Pass(hook.BeatEvery)
				r.h.Hook(t, "ada", hook.Coordinator)
			}
			assert.Contains(t, out, "event=fault", "the send failed every pass")
			assert.NotEqual(t, hook.StateUnhooked, r.h.State(t, "zed").State, "and zed was never unhooked for it: %s", out)
		})
	}
}
