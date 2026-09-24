package task

// The one task store (#3206 rev 5 PR A): the friend-queue verbs the task verb
// lacked, each one Redis Function call in internal/nsprint/fn/lua/
// task_queue.lua. There is no blocked state (ruling nova-tools#3516): a task
// whose DEPENDS-ON is unmet is waiting and is in no ready queue, count or
// fill; it becomes ready on its own queue when the last condition is met.

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Function names registered by task_queue.lua.
const (
	FunctionMove       = "ns_task_move"
	FunctionClose      = "ns_task_close"
	FunctionFront      = "ns_task_front"
	FunctionDepends    = "ns_task_depends"
	FunctionResolve    = "ns_task_resolve"
	FunctionFriendDown = "ns_friend_down"
)

// Reply is one function reply: the status word and the words after it.
type Reply struct {
	Status string
	Args   []string
}

// Arg returns the i-th word after the status, or "".
func (r Reply) Arg(i int) string {
	if i < len(r.Args) {
		return r.Args[i]
	}
	return ""
}

// ExitCode maps a queue reply to the CLI exit code: 0 for a done or no-op
// transition, 2 for a refused call, 3 FENCED, 4 a lease or state conflict,
// 6 DOWN (friend-queue's rule, rowan-tools #277), 1 NOTFOUND.
func (r Reply) ExitCode() int {
	switch r.Status {
	case "MOVED", "SAME", "CLOSED-NOW", "CLOSED", "FRONT", "WAITING", "MET", "READY", "UP":
		return 0
	case "DOWN":
		return 6
	case "FENCED":
		return 3
	case "LEASED":
		return 4
	case "NOTFOUND", "NONE":
		return 1
	default:
		return 2
	}
}

func call(ctx context.Context, st *store.Store, fn string, args ...any) (Reply, error) {
	if st == nil {
		return Reply{}, fmt.Errorf("%s: nil store", fn)
	}
	raw, err := st.Client().FCall(ctx, fn, nil, args...).Result()
	if err != nil {
		return Reply{}, fmt.Errorf("%s: %w", fn, err)
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return Reply{}, fmt.Errorf("%s: unexpected reply %T", fn, raw)
	}
	out := Reply{Status: fmt.Sprint(values[0])}
	for _, v := range values[1:] {
		out.Args = append(out.Args, fmt.Sprint(v))
	}
	return out, nil
}

func need(verb string, fields ...string) error {
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i+1] == "" {
			return fmt.Errorf("task %s: %s is required", verb, fields[i])
		}
	}
	return nil
}

// Move re-owns a task (push --move). An open task changes queue at its
// score, a waiting task changes dest, and a lease moves only off a down
// owner (fenced, reopened on open:<to>). DOWN when to is down.
func Move(ctx context.Context, st *store.Store, sprint, id, to, actor, idem string) (Reply, error) {
	if err := need("move", "sprint", sprint, "id", id, "to", to); err != nil {
		return Reply{}, err
	}
	return call(ctx, st, FunctionMove, sprint, id, to, actor, idem)
}

// Close closes an open or waiting task with cancelled=1 (friend-queue
// cancel). Waiters on task:<id> are released in the same call; Arg(0) is how
// many became ready.
func Close(ctx context.Context, st *store.Store, sprint, id, evidence, actor, idem string) (Reply, error) {
	if err := need("close", "sprint", sprint, "id", id); err != nil {
		return Reply{}, err
	}
	return call(ctx, st, FunctionClose, sprint, id, evidence, actor, idem)
}

// Front puts a task at the front of as's queue.
func Front(ctx context.Context, st *store.Store, sprint, id, as, actor, idem string) (Reply, error) {
	if err := need("front", "sprint", sprint, "id", id, "as", as); err != nil {
		return Reply{}, err
	}
	return call(ctx, st, FunctionFront, sprint, id, as, actor, idem)
}

// DependsRequest adds DEPENDS-ON conditions to a live task.
type DependsRequest struct {
	Sprint, ID, On string
	// As or Token proves the lease of a claimed or working task.
	As, Token   string
	Actor, Idem string
}

// Depends makes a task wait on conditions it did not carry at push. An open
// task leaves its queue; a lease is ended and fenced, and the task returns to
// its owner's queue when the conditions are met. MET when all already hold.
func Depends(ctx context.Context, st *store.Store, req DependsRequest) (Reply, error) {
	if err := need("depends", "sprint", req.Sprint, "id", req.ID, "on", req.On); err != nil {
		return Reply{}, err
	}
	return call(ctx, st, FunctionDepends, req.Sprint, req.ID, normalizeDepends(req.On), req.As, req.Token, req.Actor, req.Idem)
}

// Resolve settles one condition for every task waiting on it. task: and
// key: conditions are checked in the function; any other condition is met
// only when asserted (the caller read the fact). Arg(0) is how many became
// ready.
func Resolve(ctx context.Context, st *store.Store, sprint, cond string, asserted bool, actor, idem string) (Reply, error) {
	if err := need("resolve", "sprint", sprint, "on", cond); err != nil {
		return Reply{}, err
	}
	a := "0"
	if asserted {
		a = "1"
	}
	return call(ctx, st, FunctionResolve, sprint, cond, a, actor, idem)
}

// FriendDown sets (on) or clears friend:<f>:down, a HASH {reason, actor, at}.
// A down friend's push and take are refused and rebalance moves its ready
// work off on the next pass.
func FriendDown(ctx context.Context, st *store.Store, friend string, on bool, reason, actor, idem string) (Reply, error) {
	if err := need("friend down", "friend", friend, "actor", actor); err != nil {
		return Reply{}, err
	}
	flag := "0"
	if on {
		flag = "1"
	}
	return call(ctx, st, FunctionFriendDown, friend, flag, reason, actor, idem)
}
