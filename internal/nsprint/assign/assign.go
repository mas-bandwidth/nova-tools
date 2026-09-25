// Package assign moves many tasks in one Redis Function call (nova-tools
// #3103, spec #2756 v6 4.6, control 46): `assign --stdin` places many
// `<task> <friend>` lines with ns_assign_batch, and `redistribute --from`
// runs the reconciler's redistribution for one friend by hand with
// ns_redistribute_from. Both are one round trip per batch, whatever its
// size; the interim friend-queue took ~60 s per call and ten minutes for 50
// reads (rowan-tools #165).
//
// Every transfer of a read is fenced on the canonical identity (repo, PR,
// full head, friend) of spec 5.7 (6): a friend that holds a closed, working
// or open task for that identity, or has posted a typed line at that head,
// never receives it again, and the line says DEDUP with the reason.
package assign

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Function names registered by internal/nsprint/fn/lua/redistribute_assign.lua.
const (
	FunctionAssignBatch      = "ns_assign_batch"
	FunctionRedistributeFrom = "ns_redistribute_from"
)

// Status is the outcome of one assign line.
type Status string

const (
	// Moved placed the open task on the friend's queue.
	Moved Status = "MOVED"
	// Same found the task already on the friend's queue.
	Same Status = "SAME"
	// NotFound names no task in the sprint.
	NotFound Status = "NOTFOUND"
	// Unknown names a friend that is not registered.
	Unknown Status = "UNKNOWN"
	// Down is a friend with no beat, paused, or carrying a state.
	Down Status = "DOWN"
	// Live is a claimed or working task: moving it needs a fence (assign
	// --revoke, #2940), never a batch move.
	Live Status = "LIVE"
	// Closed is a closed, cancelled or reconcile-required task.
	Closed Status = "CLOSED"
	// Author is a read of the friend's own PR.
	Author Status = "AUTHOR"
	// Dedup is a read the friend already holds or has answered at that head.
	Dedup Status = "DEDUP"
	// NoRoute means the target lacks the Redis-configured role for the task.
	NoRoute Status = "NOROUTE"
)

// Line is one `<task> <friend>` line.
type Line struct {
	Task   string
	Friend string
}

// Receipt is the outcome of one line, in input order.
type Receipt struct {
	Task   string
	Friend string
	Status Status
	Detail string
}

// String is the verb's line for a receipt: `DEDUP <id>: <reason>` for a
// deduplicated read (spec 5.7 (6)), else `ASSIGN <status> <id> -> <friend>`.
func (r Receipt) String() string {
	if r.Status == Dedup {
		return fmt.Sprintf("DEDUP %s: %s", r.Task, r.Detail)
	}
	line := fmt.Sprintf("ASSIGN %s %s -> %s", r.Status, r.Task, r.Friend)
	if r.Detail != "" {
		line += " " + r.Detail
	}
	return line
}

// Refused reports whether the line did not move and was not already there.
func (r Receipt) Refused() bool { return r.Status != Moved && r.Status != Same }

// ParseLines reads `<task> <friend>` lines. Blank lines and lines starting
// with # are skipped; any other line must have exactly two fields.
func ParseLines(r io.Reader) ([]Line, error) {
	var lines []Line
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d: want `<task> <friend>`, got %d fields", n, len(fields))
		}
		lines = append(lines, Line{Task: fields[0], Friend: fields[1]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// Batch applies every line in ONE ns_assign_batch call. reason is the word in
// `[moved from <f>: <reason>]` on a task moved off another friend ("assign"
// when empty). A batch of one is the single assign.
func Batch(ctx context.Context, st *store.Store, sprint string, lines []Line, reason, actor, idem string) ([]Receipt, error) {
	if st == nil {
		return nil, fmt.Errorf("assign: nil store")
	}
	if sprint == "" {
		return nil, fmt.Errorf("assign: sprint is required")
	}
	if len(lines) == 0 {
		return nil, nil
	}
	args := make([]any, 0, 4+2*len(lines))
	args = append(args, sprint, reason, actor, idem)
	for i, l := range lines {
		if l.Task == "" || l.Friend == "" {
			return nil, fmt.Errorf("assign: line %d needs a task and a friend", i+1)
		}
		args = append(args, l.Task, l.Friend)
	}
	reply, err := st.Client().FCall(ctx, FunctionAssignBatch, nil, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("assign: %w", err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("assign: unexpected reply %T", reply)
	}
	if status := fmt.Sprint(values[0]); status != "OK" {
		return nil, fmt.Errorf("assign %s: %s", sprint, status)
	}
	values = values[1:]
	if len(values) != 4*len(lines) {
		return nil, fmt.Errorf("assign: %d values for %d lines", len(values), len(lines))
	}
	out := make([]Receipt, len(lines))
	for i := range out {
		v := values[4*i : 4*i+4]
		out[i] = Receipt{Task: fmt.Sprint(v[0]), Friend: fmt.Sprint(v[1]), Status: Status(fmt.Sprint(v[2])), Detail: fmt.Sprint(v[3])}
	}
	return out, nil
}

// FromRequest is one hand redistribution of friend From.
type FromRequest struct {
	From   string
	Reason string
	// To, when set, is the only friend that may receive; a task it cannot
	// receive stays on From (KEPT).
	To string
	// Kinds, when set, limits the move to these task kinds (for example
	// work and fix for an underfull rebalance, which never moves reads).
	Kinds []string
	// Roster is retained for source compatibility and ignored. Every routing
	// FCALL reads friend:<f>:roles for the members of friends atomically.
	Roster life.Roster
	Actor  string
	Idem   string
}

// Event is one line of what the redistribution did: MOVED id to marker,
// DEDUP id friend reason, KEPT id from why.
type Event struct {
	What string
	Task string
	A, B string
}

// String is the verb's line for an event.
func (e Event) String() string {
	switch e.What {
	case "DEDUP":
		return fmt.Sprintf("DEDUP %s: %s", e.Task, e.B)
	case "MOVED":
		return fmt.Sprintf("MOVED %s -> %s %s", e.Task, e.A, e.B)
	default:
		return fmt.Sprintf("%s %s on %s: %s", e.What, e.Task, e.A, e.B)
	}
}

// FromResult is the redistribution's summary and its events.
type FromResult struct {
	Friend   string
	State    string
	Moved    int
	Leases   int
	Released int
	Unrouted int
	Kept     int
	Events   []Event
}

// Line is the summary line.
func (r FromResult) Line() string {
	state := r.State
	if state == "" {
		state = "-"
	}
	return fmt.Sprintf("REDISTRIBUTE friend=%s state=%s moved=%d leases=%d released=%d unrouted=%d kept=%d",
		r.Friend, state, r.Moved, r.Leases, r.Released, r.Unrouted, r.Kept)
}

// From runs one hand redistribution in ONE ns_redistribute_from call.
func From(ctx context.Context, st *store.Store, req FromRequest) (FromResult, error) {
	if st == nil {
		return FromResult{}, fmt.Errorf("redistribute: nil store")
	}
	if req.From == "" || req.Reason == "" {
		return FromResult{}, fmt.Errorf("redistribute: --from and --reason are required")
	}
	for _, list := range [][]string{req.Kinds} {
		for _, name := range list {
			if strings.Contains(name, ",") {
				return FromResult{}, fmt.Errorf("redistribute: name %q contains a comma", name)
			}
		}
	}
	reply, err := st.Client().FCall(ctx, FunctionRedistributeFrom, nil,
		req.From, req.Reason, req.To, strings.Join(req.Kinds, ","),
		req.Actor, req.Idem).Result()
	if err != nil {
		return FromResult{}, fmt.Errorf("redistribute %s: %w", req.From, err)
	}
	values, ok := reply.([]any)
	if !ok || len(values) == 0 {
		return FromResult{}, fmt.Errorf("redistribute %s: unexpected reply %T", req.From, reply)
	}
	if status := fmt.Sprint(values[0]); status != "OK" {
		// REFUSED carries its reason (#4145): a down friend's queue that
		// would not move is never a silent moved=0.
		words := make([]string, len(values))
		for i, v := range values {
			words[i] = fmt.Sprint(v)
		}
		return FromResult{}, fmt.Errorf("redistribute %s: %s", req.From, strings.Join(words, " "))
	}
	if len(values) != 3 {
		return FromResult{}, fmt.Errorf("redistribute %s: reply of %d values", req.From, len(values))
	}
	sum, ok := values[1].([]any)
	if !ok || len(sum) != 7 {
		return FromResult{}, fmt.Errorf("redistribute %s: bad summary", req.From)
	}
	res := FromResult{Friend: fmt.Sprint(sum[0]), State: fmt.Sprint(sum[1])}
	for k, dst := range []*int{&res.Moved, &res.Leases, &res.Released, &res.Unrouted, &res.Kept} {
		n, err := strconv.Atoi(fmt.Sprint(sum[2+k]))
		if err != nil {
			return FromResult{}, fmt.Errorf("redistribute %s: summary field %d: %w", req.From, 2+k, err)
		}
		*dst = n
	}
	evs, _ := values[2].([]any)
	if len(evs)%4 != 0 {
		return FromResult{}, fmt.Errorf("redistribute %s: events are not whole records", req.From)
	}
	for i := 0; i < len(evs); i += 4 {
		res.Events = append(res.Events, Event{What: fmt.Sprint(evs[i]), Task: fmt.Sprint(evs[i+1]),
			A: fmt.Sprint(evs[i+2]), B: fmt.Sprint(evs[i+3])})
	}
	return res, nil
}
