package task

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionExpire is registered by internal/nsprint/fn/lua/task_beat.lua. It is
// the reconciler-only expiry transition (spec 3.1 rows 4-6).
const FunctionExpire = "ns_task_expire"

// ExpireRequest is one reconciler expiry attempt for one task. Age is measured
// against Redis server TIME inside the function; the caller only names the
// task (spec 2.1 rule 3, no client-supplied wall time).
type ExpireRequest struct {
	Sprint string
	ID     string
	Actor  string
	Idem   string
}

// ExpireStatus is the outcome of one expiry attempt.
type ExpireStatus string

const (
	// ExpireReopened is a claimed task whose start ack never came within the
	// 60 s spawn window: reason spawn-timeout, reservation freed (row 4).
	ExpireReopened ExpireStatus = "REOPENED"
	// ExpireExpired is a working task with no beat for 180 s and effects none
	// or idempotent: it reopens keeping its owner (row 5).
	ExpireExpired ExpireStatus = "EXPIRED"
	// ExpireReconcile is a working task with no beat for 180 s and effects
	// external: reconcile-required with an unresolved item, nothing replayed
	// (row 6).
	ExpireReconcile ExpireStatus = "RECONCILE"
	// ExpireFresh is a task that is not yet due; nothing was written.
	ExpireFresh ExpireStatus = "FRESH"
	// ExpireNotFound is an unknown task id.
	ExpireNotFound ExpireStatus = "NOTFOUND"
	// ExpireInvalid is a terminal or otherwise non-expirable task.
	ExpireInvalid ExpireStatus = "INVALID"
)

// ExitCode maps an expiry outcome to its CLI exit code. Expiry is a
// reconciler pass; a task that is not due is not an error.
func (s ExpireStatus) ExitCode() int {
	switch s {
	case ExpireNotFound, ExpireInvalid:
		return 2
	default:
		return 0
	}
}

// Expire runs the reconciler expiry guard for one task. The guard, the fence,
// the lease and index moves, the unresolved evidence for external effects,
// Redis TIME and the one receipt are a single atomic Redis Function call.
func Expire(ctx context.Context, st *store.Store, req ExpireRequest) (ExpireStatus, error) {
	if st == nil {
		return "", fmt.Errorf("task expire: nil store")
	}
	if req.Sprint == "" || req.ID == "" {
		return "", fmt.Errorf("task expire: sprint and id are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionExpire, nil,
		req.Sprint, req.ID, req.Actor, req.Idem).Result()
	if err != nil {
		return "", fmt.Errorf("task expire %s: %w", req.ID, err)
	}
	status, err := firstString(reply)
	if err != nil {
		return "", fmt.Errorf("task expire %s: %w", req.ID, err)
	}
	switch ExpireStatus(status) {
	case ExpireReopened, ExpireExpired, ExpireReconcile, ExpireFresh, ExpireNotFound, ExpireInvalid:
		return ExpireStatus(status), nil
	default:
		return "", fmt.Errorf("task expire %s: unexpected status %q", req.ID, status)
	}
}

// ExpireSprint is one reconciler pass over a sprint: it reads the claimed and
// working index sets (the only count of work in flight, spec 2.1 rule 5) and
// runs Expire once per id. Every expiry is still its own atomic Function call
// with its own receipt; the pass only chooses which tasks to try. It returns
// the number of tasks that actually transitioned.
func ExpireSprint(ctx context.Context, st *store.Store, sprint, actor, idem string) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("task expire: nil store")
	}
	if sprint == "" {
		return 0, fmt.Errorf("task expire: sprint is required")
	}
	client := st.Client()
	seen := map[string]struct{}{}
	for _, state := range []string{"claimed", "working"} {
		ids, err := client.SMembers(ctx, "s:"+sprint+":idx:task:"+state).Result()
		if err != nil {
			return 0, fmt.Errorf("task expire: %s index: %w", state, err)
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	expired := 0
	for id := range seen {
		status, err := Expire(ctx, st, ExpireRequest{Sprint: sprint, ID: id, Actor: actor, Idem: idem})
		if err != nil {
			return expired, err
		}
		switch status {
		case ExpireReopened, ExpireExpired, ExpireReconcile:
			expired++
		}
	}
	return expired, nil
}
