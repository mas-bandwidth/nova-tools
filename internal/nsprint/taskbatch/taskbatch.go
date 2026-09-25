// Package taskbatch is the batch task verbs on the ws index (nova-tools
// #3661, sweep #3647; part of #3662): cancel, block, unblock, move and front
// over k ids, a whole stream or the members of one set, each ONE call into
// ns_task_batch (fn/lua/task_batch.lua), and sweep, one call into
// ns_task_sweep. The keys and invariants are rowan-new specs/ws-index.md.
// The Lua checks every row before it writes any, so a refusal changes
// nothing and names the first bad id.
package taskbatch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The two Redis Functions this package calls.
const (
	FnBatch = "ns_task_batch"
	FnSweep = "ns_task_sweep"
)

// The verbs ns_task_batch takes. move is spelled by its destination.
const (
	Cancel     = "cancel"
	Block      = "block"
	Unblock    = "unblock"
	Front      = "front"
	MoveStream = "move-stream"
	MoveState  = "move-state"
	MoveFriend = "move-friend"
)

// States are the ws states a task can be moved to (closed is in no ZSET).
var States = []string{"waiting", "ready", "working", "merging", "landed", "parked", "closed"}

// Caller runs one Redis Function with no keys. FCall is the production one; a
// test may run the same source another way.
type Caller func(ctx context.Context, fn string, args []any) (any, error)

// FCall calls the loaded nova_sprint library.
func FCall(c redis.Cmdable) Caller {
	return func(ctx context.Context, fn string, args []any) (any, error) {
		return c.FCall(ctx, fn, nil, args...).Result()
	}
}

// Request is one batch. Exactly one of IDs, Stream and Set is the source.
type Request struct {
	Verb   string
	Sprint string // names the legacy sprint:<S>:idx sets
	By     string
	Why    string // the receipt's why; cancel's evidence, block's reason
	Param  string // block: conditions; move-*: the destination
	IDs    []string
	Stream string
	Set    string
}

// Result is the function's answer. A refusal has OK false and names the first
// bad id (or key) and why.
type Result struct {
	OK        bool
	N         int
	Stream    string
	ID        string
	Why       string
	Cancelled int // sweep only
	Waiting   int // sweep only
	Skipped   int // sweep only
}

// Check refuses a request the function would refuse, before any dial.
func (r Request) Check() error {
	switch r.Verb {
	case Cancel, Block, Unblock, Front, MoveStream, MoveFriend:
	case MoveState:
		if !validState(r.Param) {
			return fmt.Errorf("--to-state %q: want one of %s", r.Param, strings.Join(States, ", "))
		}
	default:
		return fmt.Errorf("unknown verb %q", r.Verb)
	}
	if (r.Verb == MoveStream || r.Verb == MoveFriend) && r.Param == "" {
		return errors.New("move wants a destination")
	}
	if r.Sprint == "" || r.By == "" {
		return errors.New("want a sprint and an actor")
	}
	n := 0
	if len(r.IDs) > 0 {
		n++
	}
	if r.Stream != "" {
		n++
	}
	if r.Set != "" {
		n++
	}
	if n != 1 {
		return errors.New("want exactly one of --ids, --stream and --set")
	}
	return nil
}

func validState(s string) bool {
	for _, x := range States {
		if s == x {
			return true
		}
	}
	return false
}

// Args is the ns_task_batch argument list.
func (r Request) Args() []any {
	source, value := "ids", ""
	switch {
	case r.Stream != "":
		source, value = "stream", r.Stream
	case r.Set != "":
		source, value = "set", r.Set
	}
	args := make([]any, 0, 7+len(r.IDs))
	args = append(args, r.Verb, r.Sprint, r.By, r.Why, r.Param, source, value)
	for _, id := range r.IDs {
		args = append(args, id)
	}
	return args
}

// Batch runs one request as one function call.
func Batch(ctx context.Context, call Caller, r Request) (Result, error) {
	if err := r.Check(); err != nil {
		return Result{}, err
	}
	reply, err := call(ctx, FnBatch, r.Args())
	if err != nil {
		return Result{}, err
	}
	return parse(reply)
}

// SweepRequest is sweep --friend <f>.
type SweepRequest struct {
	Sprint string
	By     string
	Friend string
}

// Sweep runs ns_task_sweep for one friend's ready queue.
func Sweep(ctx context.Context, call Caller, r SweepRequest) (Result, error) {
	if r.Sprint == "" || r.By == "" || r.Friend == "" {
		return Result{}, errors.New("want a sprint, an actor and --friend")
	}
	reply, err := call(ctx, FnSweep, []any{r.Sprint, r.By, r.Friend})
	if err != nil {
		return Result{}, err
	}
	return parse(reply)
}

func parse(reply any) (Result, error) {
	row, ok := reply.([]any)
	if !ok || len(row) < 3 {
		return Result{}, fmt.Errorf("unexpected reply %v", reply)
	}
	word := fmt.Sprint(row[0])
	switch word {
	case "REFUSED":
		return Result{ID: fmt.Sprint(row[1]), Why: fmt.Sprint(row[2])}, nil
	case "OK":
		res := Result{OK: true, N: toInt(row[1]), Stream: fmt.Sprint(row[2])}
		if len(row) >= 6 {
			res.Cancelled, res.Waiting, res.Skipped = toInt(row[3]), toInt(row[4]), toInt(row[5])
		}
		return res, nil
	}
	return Result{}, fmt.Errorf("unexpected reply %v", reply)
}

func toInt(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case int:
		return x
	}
	var n int
	_, _ = fmt.Sscan(fmt.Sprint(v), &n)
	return n
}

// ReadIDs reads one id per line; blank lines and # comments are skipped.
func ReadIDs(r io.Reader) ([]string, error) {
	var ids []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	return ids, sc.Err()
}
