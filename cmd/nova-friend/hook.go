package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/hookgate"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// The friend's hook (docs/SPEC-HOOK.md, holders; tla/Hook.tla, Renew and
// ProveDaemon). The daemon is its friend's hook: at start it hooks her in as
// a friend (her bus push and her work push) and holds both, renewing their
// leases every hook.RenewEvery. The bus push is proven while her session has
// answered its session check (the push proof); the work push while the sprint
// server answered her daemon's last beat inside a lease. While the gate says
// no, her lanes take nothing and report nothing, her reads begin nothing,
// and her beat says her down, unhooked, so the server deals her nothing.

// HookRetry is how soon the daemon looks again while its friend is not hooked in.
const HookRetry = 5 * time.Second

// BusGrace is how long a session harness's bus push stays proven after her
// session was last seen present (friend.SessionCheck.Present): one check
// cadence, so a check answered late, or a turn that runs past one check's
// bound, does not stop her lanes, and a session that is gone for longer does.
const BusGrace = friend.SessionQuiet

// ErrUnhooked is what a gated sprint verb answers while the friend is not hooked in.
var ErrUnhooked = errors.New("not hooked in")

// daemonHook is the daemon's hold on its friend's hook.
type daemonHook struct {
	holder   *hook.Holder
	now      func() time.Time
	record   func(string)
	busProof func(ctx context.Context) bool // the bus push's proof (sessionProof, or the daemon's own round trip)
	sprint   bool                           // the role carries the work push, proven by the server's answers

	hooked   atomic.Bool
	why      atomic.Pointer[string]
	answered atomic.Pointer[time.Time] // when the sprint server last answered her beat
	said     string                    // the gate's word last recorded
}

// newDaemonHook is the hold on name's hook as role over the bus store st,
// or nil when the world gives no hook store (a test's world that gates
// nothing). A store that keeps no hook record holds nothing and says why:
// the gate then stays shut (fail closed).
func (w world) newDaemonHook(st bus.Store, name string, role hook.Role, busProof func(context.Context) bool, record func(string)) *daemonHook {
	if w.hookStore == nil {
		return nil
	}
	h := &daemonHook{now: w.now, record: record, busProof: busProof, sprint: role == hook.Friend}
	why := "not hooked in yet: the daemon hooks in at its first step"
	h.why.Store(&why)
	hs, err := w.hookStore(st)
	if err != nil {
		why = err.Error()
		h.why.Store(&why)
		return h
	}
	hs.Timeout = bus.CallTimeout // each round trip of a step bounded on its own (hook.Store.Timeout)
	host, herr := os.Hostname()
	if herr != nil {
		host = "unknown-host" // ignored: the holder's name is a label on the lease, never a key
	}
	h.holder = &hook.Holder{Store: hs, Name: name, Role: role, Pushes: hook.PushesOf(role), By: fmt.Sprintf("nova-friend run %s pid %d", host, os.Getpid())}
	return h
}

// ok is whether the gate let her act at the last step, and why not.
func (h *daemonHook) ok() (bool, string) {
	if h == nil {
		return true, ""
	}
	if h.hooked.Load() {
		return true, ""
	}
	return false, *h.why.Load()
}

// beatAnswered records that the sprint server answered a beat of hers (up or
// down): her work push's own round trip.
func (h *daemonHook) beatAnswered(err error) {
	if h == nil || err != nil {
		return
	}
	at := h.now()
	h.answered.Store(&at)
}

// sessionProof is a session harness's bus push proof: proven while the push
// was proved this run (her session answered a check) and her session is
// present, or was within grace (the cold reads' finding 7): a proof proved
// once must not stand for the whole run after her session is gone.
func sessionProof(sc interface {
	Proof() (bool, string)
	Present() (bool, string)
}, now func() time.Time, grace time.Duration) func(context.Context) bool {
	var lastUp time.Time
	return func(context.Context) bool {
		if proved, _ := sc.Proof(); !proved {
			return false
		}
		if up, _ := sc.Present(); up {
			lastUp = now()
			return true
		}
		return !lastUp.IsZero() && now().Sub(lastUp) < grace
	}
}

// roundTripProof is a per-card harness's bus push proof (a claude friend,
// friend.SessionCheck.BeatAlways): her cards run as processes of their own, so
// no interactive session holds her bus push and none is waited for; the daemon
// reads her stream itself, and its own round trip to the bus store proves it.
func roundTripProof(st bus.Store) func(context.Context) bool {
	return func(ctx context.Context) bool {
		_, _, err := st.Roster(ctx)
		return err == nil
	}
}

// step hooks in on the first call and renews and proves after: one beat of
// the holder, then the gate, its word recorded when it changes. Each round
// trip is bounded on its own (the hook store's Timeout, bus.CallTimeout), so a
// slow one never spends the time of the next.
func (h *daemonHook) step(ctx context.Context) {
	if h == nil || h.holder == nil {
		return
	}
	var err error
	if h.holder.Epoch() == 0 {
		err = h.holder.Start(ctx)
	}
	if err == nil {
		err = h.holder.Beat(ctx, nil)
	}
	if err == nil {
		err = h.holder.Gate(ctx, hook.PushesOf(h.holder.Role)...)
	}
	word := "hooked in as " + string(h.holder.Role)
	if err != nil {
		word = err.Error()
		var r *hook.Refusal
		if errors.As(err, &r) {
			word = r.Error()
		}
		if errors.Is(err, hook.ErrNotHooked) {
			word = h.holder.Name + " was unhooked (nova-hook unhook), or hooked as another role since: this daemon holds nothing, and a restart of it does not hook " + h.holder.Name + " in, until " + h.holder.Name + " is hooked in as " + string(h.holder.Role) + " again; run: " + hook.HookCommand(h.holder.Name, h.holder.Role)
		}
		if errors.Is(err, hook.ErrOtherRole) {
			word = h.holder.Name + " is hooked as another role (nova-hook status --as " + h.holder.Name + "): this daemon never takes the record over, so it holds nothing and its lanes take nothing; to work as a friend, run: " + hook.HookCommand(h.holder.Name, h.holder.Role)
		}
	}
	h.hooked.Store(err == nil)
	h.why.Store(&word)
	if word != h.said {
		h.said = word
		h.record(h.now().UTC().Format(time.RFC3339) + " hook: " + word)
	}
}

// loop steps the hold every hook.RenewEvery until ctx ends.
func (h *daemonHook) loop(ctx context.Context, sleep func(context.Context, time.Duration)) {
	if h == nil || h.holder == nil {
		return
	}
	for ctx.Err() == nil {
		h.step(ctx)
		every := hook.RenewEvery
		if !h.hooked.Load() {
			every = HookRetry // not hooked yet: look again soon, inside the lease
		}
		sleep(ctx, every)
	}
}

// upBeat and downBeat are her beat to the sprint server, up and down (world.beat, world.beatDown).
type (
	upBeat   func(ctx context.Context, server, name string, active time.Time, proof friend.BeatWords) (string, error)
	downBeat func(ctx context.Context, server, name string, active, until time.Time, reason string, proof friend.BeatWords) error
)

// beats wraps her beats: while the gate says no, her beat is a down beat until a
// lease from now, its reason "unhooked: " and why, so the sprint server shows her
// down and deals her nothing; every answer of the server, up or down, is her work
// push's round trip. With no down beat, an unhooked beat is not sent at all.
func (h *daemonHook) beats(now func() time.Time, up upBeat, down downBeat) (upBeat, downBeat) {
	if h == nil || up == nil {
		return up, down
	}
	gatedUp := func(ctx context.Context, server, name string, active time.Time, proof friend.BeatWords) (string, error) {
		if ok, why := h.ok(); !ok {
			if down == nil {
				return "", fmt.Errorf("%w: %s", ErrUnhooked, why)
			}
			err := down(ctx, server, name, active, now().Add(hook.LeaseTTL), "unhooked: "+why, proof)
			h.beatAnswered(err)
			return "", err
		}
		answer, err := up(ctx, server, name, active, proof)
		h.beatAnswered(err)
		return answer, err
	}
	if down == nil {
		return gatedUp, nil
	}
	return gatedUp, func(ctx context.Context, server, name string, active, until time.Time, reason string, proof friend.BeatWords) error {
		err := down(ctx, server, name, active, until, reason, proof)
		h.beatAnswered(err)
		return err
	}
}

// gated is a sprint verb that runs only while she is hooked in; while she is
// not it answers ErrUnhooked with why, and nothing is sent: a lane takes
// nothing and reports nothing.
func gated[T any](h *daemonHook, verb func(ctx context.Context, argv []string) (T, error)) func(ctx context.Context, argv []string) (T, error) {
	if verb == nil || h == nil {
		return verb
	}
	return func(ctx context.Context, argv []string) (T, error) {
		if ok, why := h.ok(); !ok {
			var zero T
			return zero, fmt.Errorf("%w: %s", ErrUnhooked, why)
		}
		return verb(ctx, argv)
	}
}

// gateVerb is the gate a verb of this tool meets before it sends or reads the
// bus as name (docs/SPEC-HOOK.md): nil when name is hooked in as a role the
// verb is for (actsFor) that carries need, else the verb's result with the
// breadcrumb (FAILED at exit 1, or REFUSED at exit 2 when the record cannot be
// read).
func (w world) gateVerb(st bus.Store, name string, role hook.Role, need ...hook.Push) *tool.Out {
	if w.gate == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), bus.CallTimeout)
	defer cancel()
	return hookgate.Out(w.gate(ctx, st, hook.Want{Name: name, Need: need, Roles: actsFor(role), Role: role}))
}

// actsFor is the roles a verb meant for role accepts: its own, and for the
// coordinator's verbs comms too until nova-sprint hook ships
// (hook.SprintHookPR): until then a coordinator cannot be proven, and the seat
// hooks in as comms. A role that merely carries the push is not enough: a
// friend daemon arming the seat's name as friend opens none of these verbs.
func actsFor(role hook.Role) []hook.Role {
	if role == hook.Coordinator {
		return []hook.Role{hook.Coordinator, hook.Comms}
	}
	return []hook.Role{role}
}
