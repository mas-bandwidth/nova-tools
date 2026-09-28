package task

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionCancel is registered by internal/nsprint/fn/lua/task_beat.lua.
const FunctionCancel = "ns_task_cancel"

// CancelRequest is one task cancel (spec 4.2): the owner gives a claimed or
// working task back. Reason is recorded in the receipt and in the unresolved
// item for an external-effect task.
type CancelRequest struct {
	Sprint string
	ID     string
	Token  string
	Reason string
	Actor  string
	Idem   string
}

// CancelStatus is the outcome of one cancel.
type CancelStatus string

const (
	// CancelOpen is claimed/working -> open for effects none or idempotent,
	// with the reservation freed (spec 3.1 row 8).
	CancelOpen CancelStatus = "OPEN"
	// CancelReconcile is claimed/working -> reconcile-required for effects
	// external: slot freed and an unresolved item written, nothing replayed
	// (spec 3.1 row 9).
	CancelReconcile CancelStatus = "RECONCILE"
	// CancelFenced is a missing or superseded token (exit 3).
	CancelFenced CancelStatus = "FENCED"
	// CancelNotFound is an unknown task id.
	CancelNotFound CancelStatus = "NOTFOUND"
	// CancelInvalid is a cancel of a task that is not claimed or working.
	CancelInvalid CancelStatus = "INVALID"
)

// ExitCode maps a cancel outcome to its CLI exit code (spec 2.1 rule 8).
func (s CancelStatus) ExitCode() int {
	switch s {
	case CancelFenced:
		return 3
	case CancelNotFound, CancelInvalid:
		return 2
	default:
		return 0
	}
}

// Cancel gives a claimed or working task back. The token check, the lease and
// index moves, the unresolved evidence for external effects, Redis TIME and
// the one receipt are a single atomic Redis Function call. The stored token is
// replaced with the fence marker before the reopen, so the old token's Done
// refuses with exit 3 (spec 2.1 rules 2 and 8).
func Cancel(ctx context.Context, st *store.Store, req CancelRequest) (CancelStatus, error) {
	if st == nil {
		return "", fmt.Errorf("task cancel: nil store")
	}
	if req.Sprint == "" || req.ID == "" || req.Token == "" || req.Reason == "" {
		return "", fmt.Errorf("task cancel: sprint, id, token and reason are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionCancel, nil,
		req.Sprint, req.ID, req.Token, req.Reason, req.Actor, req.Idem).Result()
	if err != nil {
		return "", fmt.Errorf("task cancel %s: %w", req.ID, err)
	}
	status, err := firstString(reply)
	if err != nil {
		return "", fmt.Errorf("task cancel %s: %w", req.ID, err)
	}
	switch CancelStatus(status) {
	case CancelOpen, CancelReconcile, CancelFenced, CancelNotFound, CancelInvalid:
		return CancelStatus(status), nil
	default:
		return "", fmt.Errorf("task cancel %s: unexpected status %q", req.ID, status)
	}
}
