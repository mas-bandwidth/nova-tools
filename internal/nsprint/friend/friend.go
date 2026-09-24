// Package friend keeps friend:<f>:state, the friend ladders and the declared
// wake path (nova-tools #3101; spec #2756 v6 2.2, 4.4, 5.5).
//
// friend:<f>:state has one writer, the Redis Function ns_friend_state
// (internal/nsprint/fn/lua/friend.lua); the redistribute tick in the same
// library writes it only through the same two Lua functions. The ladder's
// rung and its tick count live in that hash, not in this process, so a
// reconciler that is killed at rung 2 and restarted continues at rung 2 and
// never repeats a rung's action (Stella's HOLD7 on #3058). Every call is zero
// tokens: no model and no coordinator is called.
package friend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Function names registered by internal/nsprint/fn/lua/friend.lua.
const (
	FunctionState    = "ns_friend_state"
	FunctionWakePath = "ns_friend_wakepath"
)

// OutboxKey is the zero-token outbox of rung actions (nudge, wake, notice,
// wake-missing) that a bus relay reads.
const OutboxKey = "friend:outbox"

// States kept in friend:<f>:state. An absent key is StateUp.
const (
	StateUp           = "up"
	StateUnderfull    = "underfull"
	StateIdle         = "idle"
	StateOutOfCredits = "out-of-credits"
	StateDown         = "down"
	StateAway         = "away"
)

// StateKey is the one key ns_friend_state writes for friend f.
func StateKey(f string) string { return "friend:" + f + ":state" }

// WakePathKey is the declared wake path of friend f.
func WakePathKey(f string) string { return "friend:" + f + ":wakepath" }

// ReportRequest is `friend report`: a state the friend (or its harness
// keeper) reports about itself. State is StateOutOfCredits (Until optional:
// the reset when the harness says), StateAway (Until required) or "clear".
type ReportRequest struct {
	Friend string
	State  string
	Until  time.Time
	Reason string
	Actor  string
	Idem   string
}

// Report writes a reported state through ns_friend_state. The reconciler's
// next sweep redistributes an out-of-credits or away friend's work.
func Report(ctx context.Context, st *store.Store, req ReportRequest) error {
	if st == nil || req.Friend == "" {
		return fmt.Errorf("friend report: store and friend are required")
	}
	switch req.State {
	case StateOutOfCredits, "clear":
	case StateAway:
		if req.Until.IsZero() {
			return fmt.Errorf("friend report %s: --away needs --until", req.Friend)
		}
	default:
		return fmt.Errorf("friend report %s: state must be %s, %s or clear, got %q", req.Friend, StateOutOfCredits, StateAway, req.State)
	}
	untilMS := ""
	if !req.Until.IsZero() {
		untilMS = strconv.FormatInt(req.Until.UnixMilli(), 10)
	}
	_, err := call(ctx, st, req.Friend, req.State, untilMS, req.Reason, req.Actor, req.Idem)
	return err
}

// Observation is one reconciler reading of an UP friend: State is
// StateIdle (living 0, open > 0), StateUnderfull (0 < living < slots, open >
// 0) or StateUp (nothing to climb; clears a ladder state). Ticks is the
// policy's sweeps per rung for that state.
type Observation struct {
	Friend string
	State  string
	Open   int
	Living int
	Ticks  int
}

// Step is ns_friend_state's answer to one observation.
type Step struct {
	Friend string
	// Status is OK, HELD (a reported or down state holds; the ladder does
	// not act) or DUP (this idem was already applied).
	Status string
	State  string
	Rung   int
	// Action is the rung action taken in this call: nudge, wake-unit,
	// notice, wake-missing, redistribute, or "".
	Action string
	// Repair is the Repairer's line for a wake-unit action.
	Repair string
}

// Line is the sweep's one line per friend that moved on the ladder.
func (s Step) Line() string {
	line := fmt.Sprintf("LADDER friend=%s state=%s rung=%d action=%s", s.Friend, s.State, s.Rung, dash(s.Action))
	if s.Repair != "" {
		line += " wake: " + s.Repair
	}
	return line
}

// Observe applies one observation. idem makes a replayed call (a lost
// response) an answer from the record instead of a second step.
func Observe(ctx context.Context, st *store.Store, obs Observation, actor, idem string) (Step, error) {
	if st == nil || obs.Friend == "" {
		return Step{}, fmt.Errorf("friend observe: store and friend are required")
	}
	switch obs.State {
	case StateIdle, StateUnderfull, StateUp:
	default:
		return Step{}, fmt.Errorf("friend observe %s: state must be idle, underfull or up, got %q", obs.Friend, obs.State)
	}
	if obs.Ticks < 1 {
		return Step{}, fmt.Errorf("friend observe %s: ticks must be at least 1", obs.Friend)
	}
	values, err := call(ctx, st, obs.Friend, obs.State, "", "", actor, idem,
		strconv.Itoa(obs.Open), strconv.Itoa(obs.Living), strconv.Itoa(obs.Ticks))
	if err != nil {
		return Step{}, err
	}
	step := Step{Friend: obs.Friend, Status: values[0]}
	if len(values) > 1 {
		step.State = values[1]
	}
	if len(values) > 2 {
		step.Rung, _ = strconv.Atoi(values[2])
	}
	if len(values) > 3 {
		step.Action = values[3]
	}
	return step, nil
}

// call runs ns_friend_state and returns its reply as strings; OK, HELD and
// DUP are answers, anything else is an error.
func call(ctx context.Context, st *store.Store, f string, args ...string) ([]string, error) {
	argv := make([]any, 0, len(args)+1)
	argv = append(argv, f)
	for _, a := range args {
		argv = append(argv, a)
	}
	reply, err := st.Client().FCall(ctx, FunctionState, nil, argv...).Result()
	if err != nil {
		return nil, fmt.Errorf("friend state %s: %w", f, err)
	}
	raw, ok := reply.([]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("friend state %s: unexpected reply %T", f, reply)
	}
	values := make([]string, len(raw))
	for i, v := range raw {
		values[i] = fmt.Sprint(v)
	}
	switch values[0] {
	case "OK", "HELD", "DUP":
		return values, nil
	}
	return nil, fmt.Errorf("friend state %s: %s", f, values[0])
}

// WakePath is a friend's declared wake path: a launchd/systemd unit on a
// host, or a human reached on a notify channel.
type WakePath struct {
	Kind   string // unit or human
	Unit   string
	Host   string
	Notify string
}

// ParseWakePath reads `unit:<label>@<host>` or `human` (with notify).
func ParseWakePath(spec, notify string) (WakePath, error) {
	if spec == "human" {
		if notify == "" {
			return WakePath{}, fmt.Errorf("wake human needs --notify <channel>")
		}
		return WakePath{Kind: "human", Notify: notify}, nil
	}
	rest, ok := strings.CutPrefix(spec, "unit:")
	if !ok {
		return WakePath{}, fmt.Errorf("wake %q: want unit:<label>@<host> or human", spec)
	}
	label, host, ok := strings.Cut(rest, "@")
	if !ok || label == "" || host == "" || strings.ContainsAny(label+host, " \t@") {
		return WakePath{}, fmt.Errorf("wake %q: want unit:<label>@<host>", spec)
	}
	if notify != "" {
		return WakePath{}, fmt.Errorf("wake %q: --notify is for a human wake path", spec)
	}
	return WakePath{Kind: "unit", Unit: label, Host: host}, nil
}

func (w WakePath) String() string {
	switch w.Kind {
	case "unit":
		return "unit:" + w.Unit + "@" + w.Host
	case "human":
		return "human:" + w.Notify
	}
	return "none"
}

// SetWakePath declares f's wake path (`capacity friend <f> --wake`).
func SetWakePath(ctx context.Context, st *store.Store, f string, wp WakePath, actor, idem string) error {
	if st == nil || f == "" {
		return fmt.Errorf("friend wake path: store and friend are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionWakePath, nil, f, wp.Kind, wp.Unit, wp.Host, wp.Notify, actor, idem).Result()
	if err != nil {
		return fmt.Errorf("friend wake path %s: %w", f, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("friend wake path %s: unexpected reply %T", f, reply)
	}
	if status := fmt.Sprint(values[0]); status != "OK" {
		return fmt.Errorf("friend wake path %s: %s", f, status)
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
