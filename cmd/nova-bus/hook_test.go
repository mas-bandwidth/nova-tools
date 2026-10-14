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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookedRig is the rig whose gate and names reader are the real hook record, on
// a hooktest store beside the bus's fake: nothing is hooked until a test hooks it.
func hookedRig(t *testing.T) (*rig, *hooktest.Rig) {
	t.Helper()
	r := newRig("ada", "bob")
	h := hooktest.New(t)
	r.gate = func(ctx context.Context, _ bus.Store, want hook.Want) error { return h.Store.Gate(ctx, want) }
	r.hooks = func(ctx context.Context, _ bus.Store, names ...string) ([]hook.Status, time.Time, error) {
		return h.Store.Read(ctx, names...)
	}
	return r, h
}

// verbCalls is one call of every verb as ada: each acts as a named identity.
var verbCalls = []struct {
	verb string
	args []string
}{
	{"wait", []string{"wait", "--as", "ada", "--timeout", "1s"}},
	{"send", []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b"}},
	{"peek", []string{"peek", "--as", "ada"}},
	{"recv", []string{"recv", "--as", "ada"}},
	{"ack", []string{"ack", "--as", "ada", "--id", "01ARZ3NDEKTSV4RRFFQ69G5FAV"}},
	{"receipts", []string{"receipts", "--as", "ada"}},
	{"overdue", []string{"overdue", "--as", "ada"}},
	{"log", []string{"log", "--as", "ada"}},
	{"names", []string{"names", "--as", "ada"}},
}

// TestEveryVerbRefusesUntilHooked pins the gate on every verb (docs/SPEC-HOOK.md;
// tla/Hook.tla, GateMeansHooked): an identity never hooked in is FAILED at exit 1
// with the breadcrumb and nothing is written; hooked in, the same call runs;
// unhooked, it is refused again.
func TestEveryVerbRefusesUntilHooked(t *testing.T) {
	t.Parallel()
	for _, c := range verbCalls {
		t.Run(c.verb, func(t *testing.T) {
			t.Parallel()
			r, h := hookedRig(t)
			r.wireClock()
			res := r.cli().Run(c.args...)
			assert.Equal(t, 1, res.Code, res.Stdout+res.Stderr)
			out := res.Stdout + res.Stderr
			assert.Contains(t, out, "FAILED reason=unhooked: ada is not hooked in: no hook record")
			assert.Contains(t, out, "; run: nova-hook status --as ada", "no role known: the remedy looks, it never guesses the coordinator")
			assert.NotContains(t, out, "run: nova-hook hook --as ada --role coordinator")
			assert.Less(t, strings.Index(out, "NOTE hook in as comms"), strings.Index(out, "NOTE hook in as friend"), "comms is offered first")
			assert.Less(t, strings.Index(out, "NOTE hook in as friend"), strings.Index(out, "NOTE hook in as coordinator"), "then friend, then coordinator")
			assert.Contains(t, out, "NOTE hook in as comms (on the bus, taking no sprint work): nova-hook hook --as ada --role comms")
			assert.Empty(t, r.execIn)

			h.Hook(t, "ada", hook.Comms)
			res = r.cli().Run(c.args...)
			assert.NotContains(t, res.Stdout+res.Stderr, "unhooked", "hooked in, the verb runs")
			assert.NotEqual(t, 2, res.Code, res.Stdout+res.Stderr)

			h.Unhook(t, "ada")
			res = r.cli().Run(c.args...)
			assert.Equal(t, 1, res.Code)
			assert.Contains(t, res.Stdout+res.Stderr, "ada is not hooked in: unhooked at")
		})
	}
}

// TestASendUnhookedWritesNothing pins that a refused send writes no entry: the
// gate stands before the write, and the same send lands once ada hooks in.
func TestASendUnhookedWritesNothing(t *testing.T) {
	t.Parallel()
	r, h := hookedRig(t)
	send := []string{"send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b"}
	res := r.cli().Run(send...)
	require.Equal(t, 1, res.Code)
	got, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
	require.NoError(t, err)
	assert.Empty(t, got, "nothing on the log")

	h.Hook(t, "ada", hook.Friend)
	res = r.cli().Run(send...)
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout, "SEND OK id=")
}

// TestAHalfHookedCoordinatorIsRefused pins the role's pushes: a coordinator
// whose sprint push is down is refused, the breadcrumb naming its holder.
func TestAHalfHookedCoordinatorIsRefused(t *testing.T) {
	t.Parallel()
	r, h := hookedRig(t)
	ctx := context.Background()
	hd := &hook.Holder{Store: h.Store, Name: "ada", Role: hook.Coordinator, Pushes: []hook.Push{hook.Bus}, By: "session"}
	require.NoError(t, hd.Start(ctx))
	require.NoError(t, hd.Beat(ctx, map[hook.Push]bool{hook.Bus: true}))
	res := r.cli().Run("peek", "--as", "ada")
	assert.Equal(t, 1, res.Code)
	out := res.Stdout + res.Stderr
	assert.Contains(t, out, "partial as coordinator (bus=proven sprint=down)")
	assert.Contains(t, out, "the sprint push of a coordinator is held by: nova-sprint hook --as ada")
}

// TestTheGateFailsClosed pins that a hook record that cannot be read refuses
// at exit 2 and never lets the verb act.
func TestTheGateFailsClosed(t *testing.T) {
	t.Parallel()
	r := newRig("ada", "bob")
	r.gate = func(context.Context, bus.Store, hook.Want) error { return errors.New("LOADING the store is loading") }
	res := r.cli().Run("send", "--as", "ada", "--to", "bob", "--subject", "s", "--body", "b")
	assert.Equal(t, 2, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "the hook gate could not decide, so nothing runs: LOADING")
	got, err := (&bus.Bus{Store: r.store}).Log(context.Background(), "-")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestNamesReadsTheHookRecord pins that nova-bus names reads each known name's
// presence from the hook record: proven when hooked with its pushes proven and
// heartbeat evidence fresh, none when never hooked, unhooked with the heartbeat's
// reason when unhooked, and down when the holders are proven and the heartbeat
// evidence is not.
func TestNamesReadsTheHookRecord(t *testing.T) {
	t.Parallel()
	r, h := hookedRig(t)
	cli := r.cli()
	h.Hook(t, "ada", hook.Friend)
	cli.Do(t, "names", "--as", "ada").Exit(0).Out(
		"NAMES OK count=2 proven=1",
		"NAMES NAME name=ada state=proven role=friend reason=-",
		"NAMES NAME name=bob state=none role=- reason=-",
	)

	h.Hook(t, "bob", hook.Comms)
	cli.Do(t, "names", "--as", "ada").Exit(0).Out(
		"NAMES OK count=2 proven=2",
		"NAMES NAME name=ada state=proven role=friend reason=-",
		"NAMES NAME name=bob state=proven role=comms reason=-",
	)

	h.Unhook(t, "bob")
	require.NoError(t, h.Client.HSet(context.Background(), hook.RecordKey("bob"), "reason", "missed 2 challenges").Err())
	cli.Do(t, "names", "--as", "ada").Exit(0).Out(
		"NAMES OK count=2 proven=1",
		`NAMES NAME name=bob state=unhooked role=comms reason="missed 2 challenges"`,
	)

	// bob hooks in again with holders proven, then loses its challenge answer
	h.Hook(t, "bob", hook.Comms)
	require.NoError(t, h.Client.HDel(context.Background(), hook.RecordKey("bob"), "answered_at").Err())
	cli.Do(t, "names", "--as", "ada").Exit(0).Out(
		"NAMES OK count=2 proven=1",
		`NAMES NAME name=bob state=down role=comms reason="no answer to a heartbeat challenge within the window"`,
	)
}
