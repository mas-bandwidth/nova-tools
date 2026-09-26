// Package ws is the typed face of the ws index, the sprint's work-stream data
// structure (nova-tools #3662; contract: rowan-new specs/ws-index.md). Every
// function here is one round trip: one FCALL (or FCALL_RO) into the
// nova_sprint library functions registered by fn/lua/ws.lua, never a scan of
// task:* (Migrate's one SCAN page per call is the exception the spec names).
//
// Keys: ws:names (SET), ws:order (ZSET rank), ws:<stream>:<state> (ZSET per
// state in States), task:<id> (HASH: stream, state, created_at, ...; every set scores a task by its created_at ms), ws:log
// (STREAM, one entry per move), ws:checkpoint (STRING, the last checkpoint
// receipt).
package ws

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// The library functions (fn/lua/ws.lua). The coordinator seat FCALLs every one;
// FnCounts is registered no-writes and is called with FCALL_RO.
const (
	FnMove         = "ns_ws_move"
	FnMoveMany     = "ns_ws_move_many"
	FnParkStream   = "ns_ws_park_stream"
	FnUnparkStream = "ns_ws_unpark_stream"
	FnKeep         = "ns_ws_keep"
	FnRename       = "ns_ws_rename"
	FnOrder        = "ns_ws_order"
	FnCounts       = "ns_ws_counts"
)

// The stream line (Glenn 2026-09-26): waiting -> ready -> working -> review
// -> merging -> landed, then done and parked as the two sets a card ends in.
// This is THE list. taskcard.Wheres, table.WSStates and taskbatch.States are
// views of it, and 02_card_move.lua's WHERE is the same eight in the same
// order; a state added here is added there in the same change.
const (
	Waiting = "waiting"
	Ready   = "ready"
	Working = "working"
	Review  = "review"
	Merging = "merging"
	Landed  = "landed"
	Done    = "done"
	Parked  = "parked"
)

// Stream is the six live states, in the table's column order.
var Stream = []string{Waiting, Ready, Working, Review, Merging, Landed}

// Terminal are the two sets a card ends in.
var Terminal = []string{Done, Parked}

// Wheres is every set a stream has: Stream then Terminal, in the fsck reply's
// order (ns_task_fsck counts them in this order).
var Wheres = append(append([]string{}, Stream...), Terminal...)

// States are the sets ns_ws_move moves a task between: Stream and parked.
// Done is the verbs' closed, which no move names.
var States = append(append([]string{}, Stream...), Parked)

// Closed is the state in no set.
const Closed = "closed"

// key0 is the ZSET holding a stream's tasks in one state at epoch 0, the
// name before the first sprint clear; every reader keys by the epoch
// through KeyAt (epoch.go, nova-tools#4238).
func key0(stream, state string) string { return "ws:" + stream + ":" + state }

// Refused is a function's REFUSED reply: the call wrote nothing (for a batch,
// see ManyResult.Refused instead).
type Refused struct{ Why string }

func (r *Refused) Error() string { return "REFUSED " + r.Why }

// IsRefused reports whether err is a Refused reply.
func IsRefused(err error) bool {
	var r *Refused
	return errors.As(err, &r)
}

func strs(reply any) ([]string, error) {
	rows, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected reply %T", reply)
	}
	out := make([]string, len(rows))
	for i, v := range rows {
		out[i] = fmt.Sprint(v)
	}
	return out, nil
}

// call runs one FCALL and returns its reply as strings, turning a REFUSED
// reply into a *Refused error.
func call(ctx context.Context, c redis.Cmdable, fn string, ro bool, args ...any) ([]string, error) {
	var reply any
	var err error
	if ro {
		reply, err = c.FCallRO(ctx, fn, nil, args...).Result()
	} else {
		reply, err = c.FCall(ctx, fn, nil, args...).Result()
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}
	out, err := strs(reply)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fn, err)
	}
	if len(out) >= 2 && out[0] == "REFUSED" {
		return nil, &Refused{Why: out[1]}
	}
	if len(out) == 0 && !ro {
		return nil, fmt.Errorf("%s: empty reply", fn)
	}
	return out, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// MoveResult is ns_ws_move's reply. Status is MOVED or SAME (the task was
// already in To: nothing written).
type MoveResult struct {
	Status, Stream, From, To string
}

// Move is ns_ws_move: one task to state `to`, receipted in ws:log.
func Move(ctx context.Context, c redis.Cmdable, id, to, by, why string) (MoveResult, error) {
	out, err := call(ctx, c, FnMove, false, id, to, by, why)
	if err != nil {
		return MoveResult{}, err
	}
	switch {
	case out[0] == "MOVED" && len(out) == 4:
		return MoveResult{Status: out[0], Stream: out[1], From: out[2], To: out[3]}, nil
	case out[0] == "SAME" && len(out) == 3:
		return MoveResult{Status: out[0], Stream: out[1], From: out[2], To: out[2]}, nil
	}
	return MoveResult{}, fmt.Errorf("%s: unexpected reply %v", FnMove, out)
}

// IDWhy is one id a batch refused and the reason.
type IDWhy struct{ ID, Why string }

// ManyResult is ns_ws_move_many's reply: the refused ids stayed where they were.
type ManyResult struct {
	Moved, Same int
	Refused     []IDWhy
}

// MoveMany is ns_ws_move_many(to, by, why, id...): k ids in one call.
func MoveMany(ctx context.Context, c redis.Cmdable, to, by, why string, ids []string) (ManyResult, error) {
	args := make([]any, 0, len(ids)+3)
	args = append(args, to, by, why)
	for _, id := range ids {
		args = append(args, id)
	}
	out, err := call(ctx, c, FnMoveMany, false, args...)
	if err != nil {
		return ManyResult{}, err
	}
	if out[0] != "MOVED" || len(out) < 4 || len(out) != 4+2*atoi(out[3]) {
		return ManyResult{}, fmt.Errorf("%s: unexpected reply %v", FnMoveMany, out)
	}
	r := ManyResult{Moved: atoi(out[1]), Same: atoi(out[2])}
	for i := 4; i+1 < len(out); i += 2 {
		r.Refused = append(r.Refused, IDWhy{ID: out[i], Why: out[i+1]})
	}
	return r, nil
}

func one(ctx context.Context, c redis.Cmdable, fn, want string, args ...any) (int, error) {
	out, err := call(ctx, c, fn, false, args...)
	if err != nil {
		return 0, err
	}
	if out[0] != want || len(out) != 2 {
		return 0, fmt.Errorf("%s: unexpected reply %v", fn, out)
	}
	return atoi(out[1]), nil
}

// ParkStream is ns_ws_park_stream: the stream's waiting and ready sets move
// into parked with their scores. It returns how many tasks it parked.
func ParkStream(ctx context.Context, c redis.Cmdable, stream, by, why string) (int, error) {
	return one(ctx, c, FnParkStream, "PARKED", stream, by, why)
}

// UnparkStream is ns_ws_unpark_stream, the inverse of ParkStream: each parked
// task returns to the set it was parked from.
func UnparkStream(ctx context.Context, c redis.Cmdable, stream, by, why string) (int, error) {
	return one(ctx, c, FnUnparkStream, "UNPARKED", stream, by, why)
}

// KeepResult is ns_ws_keep's reply.
type KeepResult struct {
	Kept, ParkedStreams, ParkedTasks int
}

// Keep is ns_ws_keep: park every stream not named.
func Keep(ctx context.Context, c redis.Cmdable, by, why string, streams []string) (KeepResult, error) {
	args := []any{by, why}
	for _, s := range streams {
		args = append(args, s)
	}
	out, err := call(ctx, c, FnKeep, false, args...)
	if err != nil {
		return KeepResult{}, err
	}
	if out[0] != "KEPT" || len(out) != 4 {
		return KeepResult{}, fmt.Errorf("%s: unexpected reply %v", FnKeep, out)
	}
	return KeepResult{Kept: atoi(out[1]), ParkedStreams: atoi(out[2]), ParkedTasks: atoi(out[3])}, nil
}

// Rename is ns_ws_rename: the six sets, ws:names, ws:order (the old rank) and
// every member's stream field. It returns how many members it rewrote.
func Rename(ctx context.Context, c redis.Cmdable, old, new, by string) (int, error) {
	return one(ctx, c, FnRename, "RENAMED", old, new, by)
}

// Order is ns_ws_order: the named streams take ranks 1..k; the rest follow in
// their old order. It returns how many streams are ranked.
func Order(ctx context.Context, c redis.Cmdable, streams []string) (int, error) {
	args := make([]any, len(streams))
	for i, s := range streams {
		args[i] = s
	}
	return one(ctx, c, FnOrder, "ORDERED", args...)
}

// Count is one stream's row of ns_ws_counts.
type Count struct {
	Stream                                           string
	Rank                                             int // 1-based position in the reply (ws:order, then unranked names)
	Waiting, Ready, Working, Merging, Landed, Parked int
}

// Active is the stream's tasks that are not parked, landed or closed.
func (c Count) Active() int { return c.Waiting + c.Ready + c.Working + c.Merging }

// Counts is ns_ws_counts (FCALL_RO): every stream's six ZCARDs in rank order.
func Counts(ctx context.Context, c redis.Cmdable) ([]Count, error) {
	out, err := call(ctx, c, FnCounts, true)
	if err != nil {
		return nil, err
	}
	const stride = 7
	if len(out)%stride != 0 {
		return nil, fmt.Errorf("%s: reply of %d cells is not rows of %d", FnCounts, len(out), stride)
	}
	rows := make([]Count, 0, len(out)/stride)
	for i := 0; i < len(out); i += stride {
		rows = append(rows, Count{Stream: out[i], Rank: i/stride + 1,
			Waiting: atoi(out[i+1]), Ready: atoi(out[i+2]), Working: atoi(out[i+3]),
			Merging: atoi(out[i+4]), Landed: atoi(out[i+5]), Parked: atoi(out[i+6])})
	}
	return rows, nil
}
