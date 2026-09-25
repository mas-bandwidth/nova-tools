package task

import (
	"context"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionBeat is registered by internal/nsprint/fn/lua/task_beat.lua. The
// library is `nova_sprint` (fn.Library); the loader prepends its header.
const FunctionBeat = "ns_task_beat"

// FunctionBeatN is Beat for many tasks in one call (task_beat.lua, #3915).
const FunctionBeatN = "ns_task_beat_n"

// BeatRequest is one task beat (spec 4.2). The process that started the child
// sends it, every 60 s, never the model (Johnny 2). The first beat of a
// claimed task is the start acknowledgement (spec 3.1).
type BeatRequest struct {
	Sprint string
	ID     string
	Token  string
	Actor  string
	Idem   string
}

// BeatStatus is the outcome of one beat.
type BeatStatus string

const (
	// BeatWorking is the first beat: claimed -> working (start ack). It moves
	// the identity from the friend's global starting lease to its living one.
	BeatWorking BeatStatus = "WORKING"
	// BeatBeating refreshes the lease and beat_at of a working task.
	BeatBeating BeatStatus = "BEAT"
	// BeatFenced is a missing or superseded token (exit 3).
	BeatFenced BeatStatus = "FENCED"
	// BeatNotFound is an unknown task id.
	BeatNotFound BeatStatus = "NOTFOUND"
	// BeatInvalid is a beat on a task that is not claimed or working.
	BeatInvalid BeatStatus = "INVALID"
)

// ExitCode maps a beat outcome to its CLI exit code (spec 2.1 rule 8: a
// superseded or missing token refuses with exit 3 FENCED).
func (s BeatStatus) ExitCode() int {
	switch s {
	case BeatFenced:
		return 3
	case BeatNotFound, BeatInvalid:
		return 2
	default:
		return 0
	}
}

// Beat acknowledges the start of a claimed task or refreshes a working one.
// The token check, the index and global lease moves, Redis TIME and the one
// receipt are a single atomic Redis Function call. The token is checked first,
// so a beat from an attempt the reconciler already superseded refuses FENCED
// and never renews the new attempt (spec 2.1 rules 2 and 8).
func Beat(ctx context.Context, st *store.Store, req BeatRequest) (BeatStatus, error) {
	if st == nil {
		return "", fmt.Errorf("task beat: nil store")
	}
	if req.Sprint == "" || req.ID == "" || req.Token == "" {
		return "", fmt.Errorf("task beat: sprint, id and token are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionBeat, nil,
		req.Sprint, req.ID, req.Token, req.Actor, req.Idem).Result()
	if err != nil {
		return "", fmt.Errorf("task beat %s: %w", req.ID, err)
	}
	status, err := firstString(reply)
	if err != nil {
		return "", fmt.Errorf("task beat %s: %w", req.ID, err)
	}
	switch BeatStatus(status) {
	case BeatWorking, BeatBeating, BeatFenced, BeatNotFound, BeatInvalid:
		return BeatStatus(status), nil
	default:
		return "", fmt.Errorf("task beat %s: unexpected status %q", req.ID, status)
	}
}

// BeatMany is Beat for every request in one Redis Function call (#3915: a
// harness renews every live child's lease once per tick). The statuses are
// in request order; each request is fenced on its own token. Actor and Idem
// are the first request's.
func BeatMany(ctx context.Context, st *store.Store, reqs []BeatRequest) ([]BeatStatus, error) {
	if st == nil {
		return nil, fmt.Errorf("task beat: nil store")
	}
	if len(reqs) == 0 {
		return nil, nil
	}
	args := []any{reqs[0].Actor, reqs[0].Idem}
	for _, r := range reqs {
		if r.Sprint == "" || r.ID == "" || r.Token == "" {
			return nil, fmt.Errorf("task beat: sprint, id and token are required (%s)", r.ID)
		}
		args = append(args, r.Sprint, r.ID, r.Token)
	}
	reply, err := st.Client().FCall(ctx, FunctionBeatN, nil, args...).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("task beat: %w", err)
	}
	if len(reply) != len(reqs) {
		return nil, fmt.Errorf("task beat: %d statuses for %d tasks", len(reply), len(reqs))
	}
	out := make([]BeatStatus, len(reply))
	for i, v := range reply {
		switch BeatStatus(v) {
		case BeatWorking, BeatBeating, BeatFenced, BeatNotFound, BeatInvalid:
			out[i] = BeatStatus(v)
		default:
			return nil, fmt.Errorf("task beat %s: unexpected status %q", reqs[i].ID, v)
		}
	}
	return out, nil
}
