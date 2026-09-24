package task

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Durable WAITING (nova-tools #3090): a WORKING task that only waits on CI, a
// read, a dependency or a person gives its child slot back and keeps its
// owner. The functions are registered by internal/nsprint/fn/lua/task.lua.
const (
	FunctionWait      = "ns_task_wait"
	FunctionWake      = "ns_task_wake"
	FunctionWaitSweep = "ns_task_wait_sweep"
)

// StateWaiting is the task state of a build that holds ownership without a
// child: not working (its identity left friend:<f>:living) and not ready (it
// is on no open queue), so #3071's desired never counts it.
const StateWaiting = "waiting"

// WaitingKey is the friend's global waiting set (S/id -> wait_since), the
// count the width line prints beside working.
func WaitingKey(friend string) string { return "friend:" + friend + ":waiting" }

// waitKey is the typed wait_on grammar the Lua function enforces as well.
var waitKey = regexp.MustCompile(`^(ci:[A-Za-z0-9._-]+:[0-9a-f]+|read:[0-9]+@[0-9a-f]+|dep:\S+|human:\S+)$`)

// WaitOnCI, WaitOnRead, WaitOnDep and WaitOnHuman build typed wait_on keys.
func WaitOnCI(repo, head string) string     { return "ci:" + repo + ":" + head }
func WaitOnRead(pr int, head string) string { return "read:" + strconv.Itoa(pr) + "@" + head }
func WaitOnDep(id string) string            { return "dep:" + id }
func WaitOnHuman(channel string) string     { return "human:" + channel }

// ValidWaitKey reports whether on is a typed wait_on key.
func ValidWaitKey(on string) bool { return waitKey.MatchString(on) }

// WaitRequest is one `task wait --id <t> --on <key>`: the child holding the
// current token gives its slot back and the task keeps its owner.
type WaitRequest struct {
	Sprint string
	ID     string
	Token  string
	On     string
	Actor  string
	Idem   string
}

// WaitStatus is the outcome of one wait.
type WaitStatus string

const (
	// WaitWaiting is working -> waiting: the lease closed, the slot freed,
	// the owner kept.
	WaitWaiting WaitStatus = "WAITING"
	// WaitFenced is a missing or superseded token (exit 3).
	WaitFenced WaitStatus = "FENCED"
	// WaitNotFound is an unknown task id.
	WaitNotFound WaitStatus = "NOTFOUND"
	// WaitInvalid is a wait on a task that is not WORKING.
	WaitInvalid WaitStatus = "INVALID"
	// WaitBadKey is a wait_on key outside the typed grammar.
	WaitBadKey WaitStatus = "BADKEY"
)

// ExitCode maps a wait outcome to its CLI exit code.
func (s WaitStatus) ExitCode() int {
	switch s {
	case WaitFenced:
		return 3
	case WaitNotFound, WaitInvalid, WaitBadKey:
		return 2
	default:
		return 0
	}
}

// Wait moves a WORKING task to waiting on a typed key in one atomic Redis
// Function call: the token check, the fenced token (the old child's beat and
// done refuse FENCED), the living lease removed, the slot-freed event, the
// wake index and the one receipt.
func Wait(ctx context.Context, st *store.Store, req WaitRequest) (WaitStatus, error) {
	if st == nil {
		return "", fmt.Errorf("task wait: nil store")
	}
	if req.Sprint == "" || req.ID == "" || req.Token == "" || req.On == "" {
		return "", fmt.Errorf("task wait: sprint, id, token and on are required")
	}
	if !ValidWaitKey(req.On) {
		return WaitBadKey, nil
	}
	reply, err := st.Client().FCall(ctx, FunctionWait, nil,
		req.Sprint, req.ID, req.Token, req.On, req.Actor, req.Idem).Result()
	if err != nil {
		return "", fmt.Errorf("task wait %s: %w", req.ID, err)
	}
	status, err := firstString(reply)
	if err != nil {
		return "", fmt.Errorf("task wait %s: %w", req.ID, err)
	}
	switch WaitStatus(status) {
	case WaitWaiting, WaitFenced, WaitNotFound, WaitInvalid, WaitBadKey:
		return WaitStatus(status), nil
	default:
		return "", fmt.Errorf("task wait %s: unexpected status %q", req.ID, status)
	}
}

// Wake outcomes.
const (
	WakeOK   = "ok"
	WakeDead = "dead"
)

// WakeRequest settles one key: the CI end at a head, a person answering.
// Fence is the lease:reconciler token when the reconciler calls it, empty for
// a direct caller.
type WakeRequest struct {
	Sprint  string
	On      string
	Outcome string
	Reason  string
	Fence   string
	Actor   string
	Idem    string
}

// Woke counts what one wake or sweep moved.
type Woke struct {
	Resumed int // waiting -> open at the front of the owner's queue
	Dead    int // waiting -> reconcile-required with an unresolved item
}

// Wake puts every task waiting on req.On back open at the front of its own
// owner's queue (outcome ok) or sends it to unresolved (outcome dead).
func Wake(ctx context.Context, st *store.Store, req WakeRequest) (Woke, error) {
	if st == nil {
		return Woke{}, fmt.Errorf("task wake: nil store")
	}
	if req.Sprint == "" || !ValidWaitKey(req.On) || (req.Outcome != WakeOK && req.Outcome != WakeDead) {
		return Woke{}, fmt.Errorf("task wake: sprint, a typed key and outcome ok|dead are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionWake, nil,
		req.Sprint, req.On, req.Outcome, req.Reason, req.Actor, req.Idem, req.Fence).Result()
	if err != nil {
		return Woke{}, fmt.Errorf("task wake %s: %w", req.On, err)
	}
	return wokeReply("task wake "+req.On, "WOKE", reply)
}

// ErrWaitFenced is a sweep or wake presenting a token that does not hold
// lease:reconciler.
var ErrWaitFenced = errors.New("FENCED")

// WaitSweep resolves the sprint's waiting dep: and read: keys from Redis
// state (a dependency closed and merged, a typed read at head; a dependency
// gone or a moved head is dead). ci: and human: keys wait for Wake.
func WaitSweep(ctx context.Context, st *store.Store, sprint, fence, actor, idem string) (Woke, error) {
	if st == nil || sprint == "" {
		return Woke{}, fmt.Errorf("task wait sweep: store and sprint are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionWaitSweep, nil, sprint, actor, idem, fence).Result()
	if err != nil {
		return Woke{}, fmt.Errorf("task wait sweep %s: %w", sprint, err)
	}
	return wokeReply("task wait sweep "+sprint, "SWEPT", reply)
}

func wokeReply(what, ok string, reply any) (Woke, error) {
	vals, isList := reply.([]any)
	if !isList || len(vals) == 0 {
		return Woke{}, fmt.Errorf("%s: unexpected reply %T", what, reply)
	}
	status := fmt.Sprint(vals[0])
	if status == "FENCED" {
		return Woke{}, fmt.Errorf("%s: %w", what, ErrWaitFenced)
	}
	if status != ok || len(vals) != 3 {
		return Woke{}, fmt.Errorf("%s: status %v", what, vals)
	}
	var w Woke
	var err error
	if w.Resumed, err = strconv.Atoi(fmt.Sprint(vals[1])); err != nil {
		return Woke{}, fmt.Errorf("%s: resumed: %w", what, err)
	}
	if w.Dead, err = strconv.Atoi(fmt.Sprint(vals[2])); err != nil {
		return Woke{}, fmt.Errorf("%s: dead: %w", what, err)
	}
	return w, nil
}
