// Package width keeps every friend working at its width while there is work
// (nova-tools #3071, with Stella's controls from its first comment).
//
// One formula, one writer: the tick (ns_width_tick in
// internal/nsprint/fn/lua/width.lua) writes friend:<f>:width on every tick:
//
//	desired  = min(slots, working + starting + ready_open)
//	deficit  = desired - working
//	fillable = desired - working - starting
//
// slots is the declared width (friend:<f>:desired, written by the capacity
// function from the friend's hello) lowered to a measured CAP when takes keep
// failing to raise working above the largest working count held in 24 hours.
// working counts only ACKed children: a task becomes WORKING on its child's
// first beat (ns_task_beat), never on a take, so a take with no ACK stays in
// starting and the deficit is computed from slots, never from "any WORKING".
// ready_open counts only tasks whose DEPENDS-ON are merged (#3066) and reads
// at the PR's head.
//
// The tick also rebalances: a deficit that persists rebalance_ticks (the
// measured p95 take latency, #3049's idle_ticks) moves the builds and fixes
// the friend cannot start now to the present friend with the most spare width,
// each with `[moved from <f>: underfull]` in its title; reads stay with their
// readers, and when every reader is at its slots the tick prints READ-BOUND and
// no PR-producing task is reserved. The coordinator's row gets an UNDERFULL
// wake carrying what `nova-sprint fill --as <f>` will print.
//
// Refill is the friend's own loop: Refill reserves exactly the fillable ready
// ids (atomically: ready, owner, dedup, slot generation, free slot) and
// launches one child per reservation; a pre-mutation wrapper failure (exit
// 125) returns before any reservation, and a launch failure after one gives
// the task back and reads its state back rather than retrying.
package width

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// Function names registered by internal/nsprint/fn/lua/width.lua.
const (
	FunctionTick    = "ns_width_tick"
	FunctionReady   = "ns_width_ready"
	FunctionReserve = "ns_width_reserve"
)

// Key names the width functions write. The wake stream is the assumed input
// of a harness with no loop (wake-serve-rowan reads friend:<f>:wake).
func Key(friend string) string     { return "friend:" + friend + ":width" }
func WakeKey(friend string) string { return "friend:" + friend + ":wake" }

// FleetKey is the fleet line's hash (working, desired, read_bound).
const FleetKey = "sprint:width"

// LogKey is the per-tick sample stream the fold integrates.
const LogKey = "width:log"

// Policy is what one tick reads from config, never from state.
type Policy struct {
	// RebalanceTicks is how long a deficit (or a stuck cap) persists before
	// the tick acts. Set it from the measured p95 take latency
	// (life.IdleTicksFromTakeLatency in #3049); it must be at least 1.
	RebalanceTicks int
	// Readers are the friends who read PRs; all of them at their slots is
	// READ-BOUND. Empty disables the check.
	Readers []string
	// Builders may receive moved builds and fixes; empty means every friend.
	Builders []string
	// Coordinator is the friend whose children are session agents; its row
	// gets an UNDERFULL wake instead of a loop.
	Coordinator string
}

// Row is one friend's width as the tick wrote it.
type Row struct {
	Friend    string
	Slots     int
	Cap       int
	Working   int
	Starting  int
	ReadyOpen int
	Desired   int
	Deficit   int
	Fillable  int
	// Idle is the typed reason of every slot not working, for example
	// "starting=1 fillable=2 deps=1 no-work=4 capped=5".
	Idle string
	// Waiting is the tasks the friend owns that wait on CI, a read, a
	// dependency or a person with no child (#3090). It holds no slot and is
	// counted in neither Working nor ReadyOpen.
	Waiting int
}

// Line is the friend's width line: working/desired first.
func (r Row) Line() string {
	line := fmt.Sprintf("WIDTH %s %d/%d", r.Friend, r.Working, r.Desired)
	if r.Waiting > 0 {
		// Beside working (#3090): owned builds holding no child.
		line += fmt.Sprintf(" waiting=%d", r.Waiting)
	}
	line += fmt.Sprintf(" slots=%d cap=%d deficit=%d starting=%d ready=%d",
		r.Slots, r.Cap, r.Deficit, r.Starting, r.ReadyOpen)
	if r.Idle != "" {
		line += " idle=" + strings.ReplaceAll(r.Idle, " ", ",")
	}
	return line
}

// Result is one tick: a row per friend and the event lines (CAP, MOVE,
// READ-BOUND, UNDERFULL) in the order they happened.
type Result struct {
	Rows   []Row
	Events []string
}

// Fleet is the fleet line `width <sum working>/<sum desired>`.
func (r Result) Fleet() string {
	w, d := 0, 0
	for _, row := range r.Rows {
		w += row.Working
		d += row.Desired
	}
	return fmt.Sprintf("width %d/%d", w, d)
}

// ReadBound reports whether this tick printed READ-BOUND.
func (r Result) ReadBound() bool {
	for _, e := range r.Events {
		if strings.HasPrefix(e, "READ-BOUND") {
			return true
		}
	}
	return false
}

// Tick runs one width tick. It is one atomic Redis Function call fenced by
// lease:reconciler: fence is the holder's token (reconcile.Lease.Token), and
// any other token refuses with an error wrapping reconcile.ErrFenced before
// anything is written. The reconciler pass runs it through Duty once per pass;
// the `nova-sprint width` verb takes the lease for its one tick.
func Tick(ctx context.Context, st *store.Store, p Policy, fence, actor, idem string) (Result, error) {
	if st == nil {
		return Result{}, fmt.Errorf("width tick: nil store")
	}
	if fence == "" {
		return Result{}, fmt.Errorf("width tick: %w: no lease:reconciler token", reconcile.ErrFenced)
	}
	if p.RebalanceTicks < 1 {
		return Result{}, fmt.Errorf("width tick: rebalance_ticks must be >= 1; measure it from the p95 take latency")
	}
	for _, list := range [][]string{p.Readers, p.Builders, {p.Coordinator}} {
		for _, name := range list {
			if strings.Contains(name, ",") {
				return Result{}, fmt.Errorf("width tick: friend name %q contains a comma", name)
			}
		}
	}
	reply, err := st.Client().FCall(ctx, FunctionTick, nil,
		strconv.Itoa(p.RebalanceTicks), strings.Join(p.Readers, ","),
		strings.Join(p.Builders, ","), p.Coordinator, actor, idem, fence).Result()
	if err != nil {
		return Result{}, fmt.Errorf("width tick: %w", err)
	}
	if err := fencedReply("width tick", reply); err != nil {
		return Result{}, err
	}
	top, ok := reply.([]any)
	if !ok || len(top) != 2 {
		return Result{}, fmt.Errorf("width tick: unexpected reply %T", reply)
	}
	rows, _ := top[0].([]any)
	events, _ := top[1].([]any)
	var res Result
	for _, raw := range rows {
		rec, ok := raw.([]any)
		if !ok || len(rec) != 11 {
			return Result{}, fmt.Errorf("width tick: malformed row %v", raw)
		}
		s := make([]string, len(rec))
		for i, v := range rec {
			s[i] = fmt.Sprint(v)
		}
		row := Row{Friend: s[0], Idle: s[9]}
		if row.Waiting, err = strconv.Atoi(s[10]); err != nil {
			return Result{}, fmt.Errorf("width tick: %s waiting: %w", row.Friend, err)
		}
		for i, dst := range []*int{&row.Slots, &row.Cap, &row.Working, &row.Starting, &row.ReadyOpen, &row.Desired, &row.Deficit, &row.Fillable} {
			n, err := strconv.Atoi(s[1+i])
			if err != nil {
				return Result{}, fmt.Errorf("width tick: %s field %d: %w", row.Friend, 1+i, err)
			}
			*dst = n
		}
		res.Rows = append(res.Rows, row)
	}
	for _, e := range events {
		res.Events = append(res.Events, fmt.Sprint(e))
	}
	return res, nil
}

// fencedReply maps a FENCED function reply to an error wrapping
// reconcile.ErrFenced, naming the holder.
func fencedReply(what string, reply any) error {
	vals, ok := reply.([]any)
	if !ok || len(vals) == 0 || fmt.Sprint(vals[0]) != "FENCED" {
		return nil
	}
	holder := ""
	if len(vals) >= 3 {
		holder = fmt.Sprintf(" (holder instance=%v host=%v)", vals[1], vals[2])
	}
	return fmt.Errorf("%s%s: %w", what, holder, reconcile.ErrFenced)
}

// Ready is one id a fill may start now, with the brief the child reads.
type Ready struct {
	Sprint string
	ID     string
	Kind   string
	Brief  string
}

// ReadyIDs lists, read only, the ready ids friend may start now: at most the
// live fillable count, in queue order, without PR-producing tasks while the
// fleet is READ-BOUND. gen is the slot generation a reservation must present.
func ReadyIDs(ctx context.Context, st *store.Store, friend string) (gen string, fillable int, ids []Ready, err error) {
	if st == nil || friend == "" {
		return "", 0, nil, fmt.Errorf("width ready: store and friend are required")
	}
	reply, err := st.Client().FCallRO(ctx, FunctionReady, nil, friend).Result()
	if err != nil {
		return "", 0, nil, fmt.Errorf("width ready %s: %w", friend, err)
	}
	vals, ok := reply.([]any)
	if !ok || len(vals) < 2 || (len(vals)-2)%4 != 0 {
		return "", 0, nil, fmt.Errorf("width ready %s: unexpected reply %v", friend, reply)
	}
	gen = fmt.Sprint(vals[0])
	fillable, err = strconv.Atoi(fmt.Sprint(vals[1]))
	if err != nil {
		return "", 0, nil, fmt.Errorf("width ready %s: fillable: %w", friend, err)
	}
	for i := 2; i < len(vals); i += 4 {
		ids = append(ids, Ready{
			Sprint: fmt.Sprint(vals[i]), ID: fmt.Sprint(vals[i+1]),
			Kind: fmt.Sprint(vals[i+2]), Brief: fmt.Sprint(vals[i+3]),
		})
	}
	return gen, fillable, ids, nil
}

// Reservation outcomes of ns_width_reserve other than CLAIMED.
var (
	// ErrStale: another dispatcher reserved a slot since gen was read.
	ErrStale = errors.New("STALE")
	// ErrFull: no free effective slot.
	ErrFull = errors.New("FULL")
	// ErrNotReady: a dependency is not merged, or a read's head is missing
	// (head:missing) or moved (head:moved). The error names the reason.
	ErrNotReady = errors.New("NOTREADY")
	// ErrReadBound: a PR-producing task while every reader is at its slots.
	ErrReadBound = errors.New("READBOUND")
	// ErrGone: the task is not open on this friend's queue (taken, moved).
	ErrGone = errors.New("NONE")
	// ErrDown: the friend has no presence or is paused.
	ErrDown = errors.New("DOWN")
)

// Reserve atomically claims one ready task for friend at slot generation
// gen. The task is claimed (starting); it becomes WORKING only when the
// child's first beat arrives. It returns the claim and the next generation.
func Reserve(ctx context.Context, st *store.Store, r Ready, friend, gen, actor, idem string) (task.Claim, string, error) {
	return reserve(ctx, st, r, friend, gen, "", actor, idem)
}

// reserve is Reserve with an optional lease:reconciler fence: set, the
// reservation is the reconciler dealing a replacement, refused FENCED unless
// the token holds the lease, and the spawn line lands on friend:<f>:wake in
// the same call.
func reserve(ctx context.Context, st *store.Store, r Ready, friend, gen, fence, actor, idem string) (task.Claim, string, error) {
	if st == nil || friend == "" || r.Sprint == "" || r.ID == "" {
		return task.Claim{}, "", fmt.Errorf("width reserve: store, friend, sprint and id are required")
	}
	for tries := 0; tries < 3; tries++ {
		prev, err := st.Client().HGet(ctx, "s:"+r.Sprint+":task:"+r.ID, "attempt").Int()
		if err != nil && !errors.Is(err, redis.Nil) {
			return task.Claim{}, "", fmt.Errorf("width reserve %s: read attempt: %w", r.ID, err)
		}
		attempt := prev + 1
		random, err := task.RandomToken()
		if err != nil {
			return task.Claim{}, "", err
		}
		token := fmt.Sprintf("%d.%s", attempt, random)
		sum := sha256.Sum256([]byte(token))
		reply, err := st.Client().FCall(ctx, FunctionReserve, nil,
			r.Sprint, r.ID, friend, gen, attempt, token, hex.EncodeToString(sum[:])[:12], actor, idem, fence).Result()
		if err != nil {
			return task.Claim{}, "", fmt.Errorf("width reserve %s: %w", r.ID, err)
		}
		if err := fencedReply("width reserve "+r.ID, reply); err != nil {
			return task.Claim{}, "", err
		}
		vals, ok := reply.([]any)
		if !ok || len(vals) == 0 {
			return task.Claim{}, "", fmt.Errorf("width reserve %s: unexpected reply %T", r.ID, reply)
		}
		switch status := fmt.Sprint(vals[0]); status {
		case "CLAIMED":
			if len(vals) < 6 {
				return task.Claim{}, "", fmt.Errorf("width reserve %s: short reply", r.ID)
			}
			n, err := strconv.Atoi(fmt.Sprint(vals[3]))
			if err != nil {
				return task.Claim{}, "", fmt.Errorf("width reserve %s: attempt: %w", r.ID, err)
			}
			return task.Claim{Sprint: fmt.Sprint(vals[1]), ID: fmt.Sprint(vals[2]), Attempt: n, Token: fmt.Sprint(vals[4])},
				fmt.Sprint(vals[5]), nil
		case "RETRY":
			continue
		case "STALE":
			return task.Claim{}, "", ErrStale
		case "FULL":
			return task.Claim{}, "", ErrFull
		case "NOTREADY":
			why := "unknown"
			if len(vals) >= 2 {
				why = fmt.Sprint(vals[1])
			}
			return task.Claim{}, "", fmt.Errorf("width reserve %s: %w %s", r.ID, ErrNotReady, why)
		case "READBOUND":
			return task.Claim{}, "", ErrReadBound
		case "NONE", "NOTFOUND":
			return task.Claim{}, "", ErrGone
		case "DOWN":
			return task.Claim{}, "", ErrDown
		default:
			return task.Claim{}, "", fmt.Errorf("width reserve %s: unexpected status %q", r.ID, status)
		}
	}
	return task.Claim{}, "", fmt.Errorf("width reserve %s: attempt moved during three reservations", r.ID)
}

// Reserved is one reservation a fill made: the ready id and its claim.
type Reserved struct {
	Ready
	Claim task.Claim
}

// Line is one `nova-sprint fill` output line: the id and its brief first, so
// the spawner starts one child per line with no judgement; the token is what
// the child's first beat (its ACK) presents.
func (r Reserved) Line() string {
	brief := r.Brief
	if brief == "" {
		brief = "-"
	}
	return fmt.Sprintf("%s %s sprint=%s attempt=%d token=%s", r.ID, brief, r.Sprint, r.Claim.Attempt, r.Claim.Token)
}

// Fill reserves exactly the ready ids friend may start now, one reservation
// per free slot. A reservation that loses its slot generation to another
// dispatcher re-reads once per loss; a FULL stops (the other dispatcher has
// the slot). It never takes more than the fillable count it read.
func Fill(ctx context.Context, st *store.Store, friend, actor, idem string) ([]Reserved, error) {
	return fill(ctx, st, friend, "", actor, idem)
}

func fill(ctx context.Context, st *store.Store, friend, fence, actor, idem string) ([]Reserved, error) {
	var out []Reserved
	for rounds := 0; rounds < 64; rounds++ {
		gen, _, ids, err := ReadyIDs(ctx, st, friend)
		if err != nil {
			return out, err
		}
		if len(ids) == 0 {
			return out, nil
		}
		restart := false
		for _, r := range ids {
			claim, next, err := reserve(ctx, st, r, friend, gen, fence, actor, idem)
			switch {
			case err == nil:
				out = append(out, Reserved{Ready: r, Claim: claim})
				gen = next
			case errors.Is(err, ErrStale):
				restart = true
			case errors.Is(err, ErrFull), errors.Is(err, ErrDown):
				return out, nil
			case errors.Is(err, ErrGone), errors.Is(err, ErrNotReady), errors.Is(err, ErrReadBound):
				continue
			default:
				return out, err
			}
			if restart {
				break
			}
		}
		if !restart {
			return out, nil
		}
	}
	return out, fmt.Errorf("width fill %s: slot generation kept moving", friend)
}

// Launcher starts children for a friend's harness. Preflight runs before
// any reservation (the nova-secrets exec wrapper, for example); its failure
// leaves every task OPEN. Launch starts one child for a reservation; the
// child ACKs with its first beat.
type Launcher interface {
	Preflight(ctx context.Context) error
	Launch(ctx context.Context, r Reserved) error
}

// Idle reasons Refill returns when it started nothing.
const (
	IdleNone     = ""
	IdleWrapper  = "wrapper"
	IdleNoReady  = "no-ready"
	IdleLaunched = "launched"
)

// ExitError is a wrapper failure with its exit status (125 is nova-secrets
// exec failing before the command ran).
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit %d: %v", e.Code, e.Err) }
func (e *ExitError) Unwrap() error { return e.Err }

// RefillResult is one step of the friend's loop.
type RefillResult struct {
	Launched []Reserved
	// Returned are reservations whose launch failed and were given back,
	// each with the state read back after the give-back.
	Returned map[string]string
	Reason   string
}

// Refill is one step of the friend's own loop (#3071 item 3): it runs when
// working < desired, and on every child completion (one-for-one). Preflight
// first; a failure there is a pre-mutation failure and returns with nothing
// reserved. Each launch failure gives its task back (task cancel with reason
// launch-failed) and reads its state back; it is never retried blind.
func Refill(ctx context.Context, st *store.Store, friend string, l Launcher, actor, idem string) (RefillResult, error) {
	res := RefillResult{Returned: map[string]string{}}
	if l == nil {
		return res, fmt.Errorf("width refill %s: nil launcher", friend)
	}
	if err := l.Preflight(ctx); err != nil {
		res.Reason = IdleWrapper
		return res, fmt.Errorf("width refill %s: preflight: %w", friend, err)
	}
	reserved, err := Fill(ctx, st, friend, actor, idem)
	if err != nil {
		return res, err
	}
	for _, r := range reserved {
		if err := l.Launch(ctx, r); err != nil {
			if _, cerr := task.Cancel(ctx, st, task.CancelRequest{
				Sprint: r.Sprint, ID: r.ID, Token: r.Claim.Token, Reason: "launch-failed", Actor: actor, Idem: idem,
			}); cerr != nil {
				return res, fmt.Errorf("width refill %s: give back %s: %w", friend, r.ID, cerr)
			}
			state, rerr := st.Client().HGet(ctx, "s:"+r.Sprint+":task:"+r.ID, "state").Result()
			if rerr != nil {
				return res, fmt.Errorf("width refill %s: read back %s: %w", friend, r.ID, rerr)
			}
			res.Returned[r.ID] = state
			continue
		}
		res.Launched = append(res.Launched, r)
	}
	switch {
	case len(res.Launched) > 0:
		res.Reason = IdleLaunched
	case len(reserved) == 0:
		res.Reason = IdleNoReady
	}
	return res, nil
}
