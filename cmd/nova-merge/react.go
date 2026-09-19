package main

// react is this lane's subscriber (docs/SPEC-JOBS.md, "Events, not ticks"). It holds no
// timer: it blocks on the pub/sub channels until a message lands or its --deadline is
// reached, and it acts once per message. A pr-checks-done success enqueues the PR unless
// the lane's skip set or the lane's hold stops it; a dev-moved asks for a rebase unit for
// every PR the move made DIRTY; a card-done does nothing, because the recorder and the
// harvester read the stream directly.
//
// --lane IS REQUIRED, AND IT IS THE QUEUE (edge 17, 2026-09-18). The reactor used to run
// without one and enqueue into `merge:queue`, a redis set nothing in this tree reads:
// `REACT enqueue pr=N`, exit 0, and nothing reachable. The lane's `queue.json` is the one
// door the tree has -- it is what `nova-merge queue` writes and what `nova-merge run`
// walks -- so react writes it, through the same read-modify-write under the same lock.
//
// ONE HOLD, ONE SKIP SET (edge 18). The skip set react obeys is `queue.json`'s `skipped`
// (and a park, which is a skip carrying a record), and the hold is `<lane>/hold`. There
// is no second pair in redis any more.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// reactOnceDeadline is how long a --once reactor waits for its one message when the
// caller named no --deadline. The LOOP form gets no such default (edge 19).
const reactOnceDeadline = 60

// laneQueue is the reactor's gate and its door in one: the lane's own two files.
type laneQueue struct {
	lane    string
	st      *merge.State
	timeout time.Duration
}

// Skipped is queue.json's `skipped`, plus a park -- which is a skip carrying the record
// that says why. It is the same answer `nova-merge queue skip` wrote and the same one the
// sweep reads.
func (q laneQueue) Skipped(pr int) (bool, error) {
	loaded, err := merge.LoadQueue(q.lane, q.st)
	if err != nil {
		return false, err
	}
	return loaded.HasSkip(pr) || loaded.IsParked(pr), nil
}

// Held is `<lane>/hold`, the file `nova-merge queue hold` writes and `release` removes.
func (q laneQueue) Held() (bool, string, error) {
	h, present, err := merge.ReadHold(q.lane)
	if err != nil {
		return false, "", err
	}
	return present, h.Reason, nil
}

// Enqueue appends the pull request to the lane's order, once, under the state lock --
// the same read-modify-write `queue skip`, `queue unskip` and `queue front` make. An
// entry already queued is left where it is: a repeated event must not reorder the queue.
func (q laneQueue) Enqueue(_ context.Context, pr int, _ string) error {
	_, err := merge.UpdateQueue(q.lane, q.st, q.timeout, func(queue *merge.Queue) error {
		if queue.HasSkip(pr) || queue.IsParked(pr) {
			return nil
		}
		if !hasInt(queue.Queued, pr) {
			queue.Queued = append(queue.Queued, pr)
		}
		return nil
	})
	return err
}

func cmdReact(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("react")
	addr := f.fs.String("redis", "", "")
	lane := f.fs.String("lane", "", "")
	deadline := f.fs.Int("deadline", 0, "")
	timeout := f.fs.Int("timeout", 120, "")
	once := f.fs.Bool("once", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("redis", *addr, "the address of the pub/sub instance this reactor subscribes to")
	// The lane is where an enqueue GOES. Without it this verb has nowhere to put a green
	// pull request, and the version that ran without one reported enqueues nobody could
	// reach (edge 17).
	f.require("lane", *lane, "the lane whose queue.json this reactor enqueues into, which is the queue `nova-merge queue` writes and `nova-merge run` walks")
	// EVERY LOOP ENDS ON ITS OWN, and this one said so in its help and then ran a 60 s
	// loop anyway when neither form was given (edge 19). It is refused by name, the way
	// `nova-work events` refuses it.
	switch {
	case *deadline < 0:
		f.problem(fmt.Sprintf("--deadline is a whole number of seconds the reactor waits before returning, got %d", *deadline))
	case !*once && *deadline == 0:
		f.problem("the loop form requires --deadline <seconds>; a loop with no deadline is a process nobody can tell from a stuck one, and --once is the one-message form")
	}
	if *timeout < 1 {
		f.problem(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if !f.done(stderr) {
		return 2
	}
	bound := *deadline
	if bound == 0 {
		bound = reactOnceDeadline
	}

	st, code := openLane("react", *lane, stderr)
	if st == nil {
		return code
	}

	// EDGE 16: go-redis writes its own connection-pool chatter to a package-global
	// logger, and five raw `redis: ... pool.go` lines landed on stderr ahead of this
	// verb's clean refusal. This tool's stderr is a grammar; the library's is not part
	// of it, and a reader of five pool lines is reading the library, not the tool.
	silenceRedis()

	rdb := deps.Dial(*addr)
	defer rdb.Close()

	var forge ci.Forge
	if deps.Forge != nil {
		forge = deps.Forge(st.Repo, st.Base, time.Duration(*timeout)*time.Second)
	}

	q := laneQueue{lane: *lane, st: st, timeout: time.Duration(*timeout) * time.Second}
	r := ci.NewReactor(rdb, forge, q, q.Enqueue, stdout)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(bound)*time.Second)
	defer cancel()

	if *once {
		if err := r.RunOnce(ctx); err != nil && !isDeadline(err) {
			fmt.Fprintf(stderr, "REACT FAIL: %s\n", oneline.Err(err))
			return 1
		}
		fmt.Fprintf(stdout, "REACT OK once=true dropped=%d\n", r.Dropped)
		return 0
	}
	if err := r.Run(ctx); err != nil && !isDeadline(err) {
		fmt.Fprintf(stderr, "REACT FAIL: %s\n", oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "REACT OK once=false deadline=%ds dropped=%d\n", bound, r.Dropped)
	return 0
}

// quietRedis is the logger go-redis's internals write through. It drops every line: a
// library's pool diagnostics are not this tool's output grammar, and five of them ahead
// of a one-line refusal is a reader reading the library instead of the tool (edge 16).
// A connection this tool could not make is reported by the verb, on the verb's own line.
type quietRedis struct{}

func (quietRedis) Printf(context.Context, string, ...interface{}) {}

// silenceRedis installs it, before the first dial of every verb that dials -- and
// ONCE FOR THE PROCESS (#1609). `redis.SetLogger` writes a package-level variable
// inside go-redis, so a second verb in the same process writing it again is a data
// race with the first: `go test -race ./cmd/nova-merge/` caught two `react` runs on
// it, one run in six on vision, and ten tests failed behind that one race. The Once
// also orders the write before every dial: a caller returns from Do only after the
// first caller's write has completed.
var silenceRedisOnce sync.Once

func silenceRedis() { silenceRedisOnce.Do(func() { redis.SetLogger(quietRedis{}) }) }

// isDeadline is the reactor's quiet deadline: a watch that never changed returns once at
// its bound, and returning at the bound is the design and not a failure.
func isDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
