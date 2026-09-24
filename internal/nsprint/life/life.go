// Package life implements the global presence verbs of #2756 v5: a friend's
// hello/beat/bye/wake and a bench's beat. Every state change is one Redis
// Function call (internal/nsprint/fn/lua/presence.lua), which reads server
// TIME, writes the presence keys and appends one cap:log receipt atomically.
//
// Hello also runs the automatic return path: a friend that comes back takes
// its own assigned ready work through the existing task.TakeAvailable, with no
// manual deal. Polling is done by the functions, not by the model, so it
// spends no model tokens.
package life

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// Function names registered by internal/nsprint/fn/lua/presence.lua. The
// library is nova_sprint and the loader prepends its single header.
const (
	FunctionHello        = "ns_friend_hello"
	FunctionBeat         = "ns_friend_beat"
	FunctionBye          = "ns_friend_bye"
	FunctionWake         = "ns_friend_wake"
	FunctionPollWake     = "ns_friend_poll_wake"
	FunctionBenchBeat    = "ns_bench_beat"
	FunctionBenchRelease = "ns_bench_release"
)

// BeatInterval is the one-second presence cadence of #2756 v5.
const BeatInterval = time.Second

// liveSeparator joins a bench's card identities, which Lua cannot receive as a
// native array through FCALL.
const liveSeparator = "\x1f"

// HelloRequest is one friend hello. Slots is the desired capacity; -1 keeps
// the current value. A raise beyond the machine ceiling is refused.
type HelloRequest struct {
	Sprint  string
	As      string
	Slots   int
	Harness string
	Host    string
	Machine string
	Session string
	Actor   string
	Idem    string
	// Logins are `--login` aliases (#3092 rev 6); ns_friend_hello is the
	// only writer of friends:login and refuses a clashing alias.
	Logins []string
}

// HelloResult is one friend hello. Claims are the friend's own queued tasks
// taken on return without any manual deal; the fence tokens stay with the
// caller so a child can complete them later.
type HelloResult struct {
	Up     bool
	Slots  int
	Claims []task.Claim
}

// Hello registers a friend (cap:log friend-up on a return), refreshes
// its beat and then immediately takes the friend's own assigned open work. The
// take uses the guarded task.TakeAvailable, so capacity is enforced by the
// same Redis Function that grants the fence token; no model tokens are spent
// looking for work.
func Hello(ctx context.Context, st *store.Store, req HelloRequest) (HelloResult, error) {
	if st == nil || req.As == "" {
		return HelloResult{}, fmt.Errorf("friend hello: store and as are required")
	}
	if req.Slots < -1 {
		return HelloResult{}, fmt.Errorf("friend hello: slots must be nonnegative or -1 to keep current")
	}
	// #2929 rev 4: hello is a take, so its actor must be the friend itself.
	if req.Actor != req.As {
		return HelloResult{}, fmt.Errorf("friend hello %s: actor %q is not --as; want --as equal to NOVA_FRIEND", req.As, req.Actor)
	}
	fargs := []any{req.As, req.Slots, req.Harness, req.Host, req.Session,
		req.Machine, req.Actor, req.Idem}
	for _, alias := range req.Logins {
		fargs = append(fargs, alias)
	}
	reply, err := st.Client().FCall(ctx, FunctionHello, nil, fargs...).Result()
	if err != nil {
		return HelloResult{}, fmt.Errorf("friend hello %s: %w", req.As, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return HelloResult{}, fmt.Errorf("friend hello %s: unexpected reply %T", req.As, reply)
	}
	status := fmt.Sprint(values[0])
	if status != "UP" {
		words := make([]string, 0, len(values))
		for _, v := range values {
			words = append(words, fmt.Sprint(v))
		}
		return HelloResult{}, fmt.Errorf("friend hello %s: %s", req.As, strings.Join(words, " "))
	}
	if len(values) < 2 {
		return HelloResult{}, fmt.Errorf("friend hello %s: short UP reply", req.As)
	}
	slots, err := strconv.Atoi(fmt.Sprint(values[1]))
	if err != nil {
		return HelloResult{}, fmt.Errorf("friend hello %s: slots %v: %w", req.As, values[1], err)
	}
	if len(values) > 2 && fmt.Sprint(values[2]) == "0" {
		if err := Wake(ctx, st, req.As, "friend-return", req.Actor, req.Idem); err != nil {
			return HelloResult{Up: true, Slots: slots}, err
		}
	}

	claims, err := task.TakeAvailable(ctx, st, req.As, req.Sprint, "", 0, req.Actor, req.Idem)
	if err != nil {
		return HelloResult{Up: true, Slots: slots}, err
	}
	return HelloResult{Up: true, Slots: slots, Claims: claims}, nil
}

// Presence identifies a live friend beat.
type Presence struct {
	Friend  string
	Harness string
	Host    string
	Session string
}

// Beat refreshes one live friend's beat. A friend that is not registered
// returns an error; use Hello to register.
func Beat(ctx context.Context, st *store.Store, p Presence) error {
	if st == nil || p.Friend == "" {
		return fmt.Errorf("friend beat: store and friend are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionBeat, nil,
		p.Friend, p.Harness, p.Host, p.Session).Result()
	if err != nil {
		return fmt.Errorf("friend beat %s: %w", p.Friend, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("friend beat %s: unexpected reply %T", p.Friend, reply)
	}
	if status := fmt.Sprint(values[0]); status != "OK" {
		return fmt.Errorf("friend beat %s: %s", p.Friend, status)
	}
	return nil
}

// Bye stops a friend's presence. Registration and assigned work persist for
// a later hello.
func Bye(ctx context.Context, st *store.Store, as, actor, idem string) (bool, error) {
	if st == nil || as == "" {
		return false, fmt.Errorf("friend bye: store and as are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionBye, nil, as, "", actor, idem).Result()
	if err != nil {
		return false, fmt.Errorf("friend bye %s: %w", as, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) < 2 {
		return false, fmt.Errorf("friend bye %s: unexpected reply %T", as, reply)
	}
	if status := fmt.Sprint(values[0]); status != "DOWN" {
		return false, fmt.Errorf("friend bye %s: %s", as, status)
	}
	return fmt.Sprint(values[1]) == "1", nil
}

// Wake routes one wake for a friend through the reconciler's wake list. The
// list holds at most one entry and expires after 600 s; the harness consumes
// it at zero model tokens.
func Wake(ctx context.Context, st *store.Store, friend, reason, actor, idem string) error {
	if st == nil || friend == "" {
		return fmt.Errorf("friend wake: store and friend are required")
	}
	if reason == "" {
		reason = "reconciler"
	}
	reply, err := st.Client().FCall(ctx, FunctionWake, nil, friend, reason, actor, idem).Result()
	if err != nil {
		return fmt.Errorf("friend wake %s: %w", friend, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("friend wake %s: unexpected reply %T", friend, reply)
	}
	if status := fmt.Sprint(values[0]); status != "WAKE" {
		return fmt.Errorf("friend wake %s: %s", friend, status)
	}
	return nil
}

// PollWake consumes the most recent wake in one Redis Function. The presence
// loop uses this receipt without calling a model, then looks for assigned work.
func PollWake(ctx context.Context, st *store.Store, friend string) (string, error) {
	if st == nil || friend == "" {
		return "", fmt.Errorf("friend poll wake: store and friend are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionPollWake, nil, friend).Result()
	if err != nil {
		return "", fmt.Errorf("friend poll wake %s: %w", friend, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return "", fmt.Errorf("friend poll wake %s: unexpected reply %T", friend, reply)
	}
	switch fmt.Sprint(values[0]) {
	case "NONE":
		return "", nil
	case "WAKE":
		if len(values) < 2 {
			return "", fmt.Errorf("friend poll wake %s: short WAKE reply", friend)
		}
		return fmt.Sprint(values[1]), nil
	default:
		return "", fmt.Errorf("friend poll wake %s: %s", friend, fmt.Sprint(values[0]))
	}
}

// BenchRequest is one bench beat. Live is the set of card identities renewed
// with the beat. Session is the fenced owner identity of the one-second loop.
type BenchRequest struct {
	Bench    string
	Host     string
	User     string
	Load1    string
	SSH      string
	Probe    string
	Launcher string
	Why      string
	Session  string
	Live     []string
	Actor    string
	Idem     string
}

// BenchResult is one bench beat. Accepted is false when another live session
// owns the bench; Owner is the session that holds it.
type BenchResult struct {
	Accepted bool
	Owner    string
}

// BenchBeat writes one bench beat and its live row, and refuses when a
// different live session owns the bench. Ownership is fenced in Redis with the
// beat TTL, so it works across processes; once the owner and beat expire the
// bench is free again (stale expiry recovery). Absence-to-presence is logged
// to cap:log as bench-up.
func BenchBeat(ctx context.Context, st *store.Store, req BenchRequest) (BenchResult, error) {
	if st == nil || req.Bench == "" || req.Session == "" {
		return BenchResult{}, fmt.Errorf("bench beat: store, bench and session are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionBenchBeat, nil,
		req.Bench, req.Host, req.User, req.Load1, req.SSH, req.Probe,
		req.Launcher, strings.Join(req.Live, liveSeparator), req.Why,
		req.Session, req.Actor, req.Idem).Result()
	if err != nil {
		return BenchResult{}, fmt.Errorf("bench beat %s: %w", req.Bench, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return BenchResult{}, fmt.Errorf("bench beat %s: unexpected reply %T", req.Bench, reply)
	}
	owner := ""
	if len(values) > 1 {
		owner = fmt.Sprint(values[1])
	}
	switch fmt.Sprint(values[0]) {
	case "OK":
		return BenchResult{Accepted: true, Owner: owner}, nil
	case "BUSY":
		return BenchResult{Accepted: false, Owner: owner}, nil
	default:
		return BenchResult{}, fmt.Errorf("bench beat %s: %s", req.Bench, fmt.Sprint(values[0]))
	}
}

// BenchRelease stops a bench's owned loop. Only the owning session may release.
func BenchRelease(ctx context.Context, st *store.Store, bench, session, actor, idem string) error {
	if st == nil || bench == "" || session == "" {
		return fmt.Errorf("bench release: store, bench and session are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionBenchRelease, nil, bench, session, actor, idem).Result()
	if err != nil {
		return fmt.Errorf("bench release %s: %w", bench, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return fmt.Errorf("bench release %s: unexpected reply %T", bench, reply)
	}
	if status := fmt.Sprint(values[0]); status != "DOWN" {
		return fmt.Errorf("bench release %s: %s", bench, status)
	}
	return nil
}
