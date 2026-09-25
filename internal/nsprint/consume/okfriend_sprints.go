package consume

// ok-to-friend over every open sprint in one round trip (nova-tools #3831).
//
// The reconcile duty ran one OkFriend pass per open sprint: two XREADGROUPs
// and the proc line, three round trips a sprint every pass (20 at the
// fleet's six open sprints) against a one second pass on a store 83 ms away.
// OkFriendSprints reads every open sprint's log in ONE pipeline: the sprint
// index and each known sprint's status, one XREADGROUP of this consumer's
// pending entries and one of the new entries over every open sprint's log,
// the acks of the events the last pass read and did not handle (any event
// that is not a card ended or harvested), and the proc line of the last pass.
// An idle pass is that one round trip. Only a sprint with card events costs
// more: its handling is OkFriend's, per event, as before. A sprint new to the
// index is started (group and reclaim, OkFriend.Start) once, and read in one
// more round trip that pass.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// OkFriendSprints is ok-to-friend over every open sprint, as the reconcile
// duty runs it. One value serves one consumer name; a new name (a new lease
// instance) starts every sprint again.
type OkFriendSprints struct {
	Store    *store.Store
	Consumer string
	Actor    string
	Count    int64 // entries per stream per read; 0 means 100

	known   []string            // `sprints` as the last pass read it
	open    []string            // the open sprints whose logs the last pass read
	started map[string]bool     // sprints whose group this consumer made and reclaimed
	acks    map[string][]string // log key -> unhandled entries to ack with the next read
	retry   map[string]okRetry  // sprint -> backoff after a retryable deferral
	proc    []any               // the last pass's proc line (took_ms, n, err), sent with the next read
}

// okRetry is one sprint's backoff: its pending entries are handled again
// at `at`, as OkFriend.Run pauses after a retryable pass.
type okRetry struct {
	at   time.Time
	wait time.Duration
}

// OkSprintResult is one sprint's handling in one pass.
type OkSprintResult struct {
	Sprint string
	N      int
	Err    error
}

func (a *OkFriendSprints) count() int64 {
	if a.Count > 0 {
		return a.Count
	}
	return 100
}

func logKey(sprint string) string { return "s:" + sprint + ":log" }

// okRead is one read round's commands.
type okRead struct {
	status  map[string]*redis.StringCmd
	pending *redis.XStreamSliceCmd
	fresh   *redis.XStreamSliceCmd
	streams []string // the sprints the two XREADGROUPs name, in order
}

// queueReads adds the two XREADGROUPs over sprints to pipe.
func (a *OkFriendSprints) queueReads(ctx context.Context, pipe redis.Pipeliner, sprints []string, r *okRead) {
	if len(sprints) == 0 {
		return
	}
	r.streams = sprints
	zero := make([]string, 0, 2*len(sprints))
	gt := make([]string, 0, 2*len(sprints))
	for _, s := range sprints {
		zero = append(zero, logKey(s))
		gt = append(gt, logKey(s))
	}
	for range sprints {
		zero = append(zero, "0")
		gt = append(gt, ">")
	}
	r.pending = pipe.XReadGroup(ctx, &redis.XReadGroupArgs{Group: GroupOkFriend, Consumer: a.Consumer, Streams: zero, Count: a.count(), Block: -1})
	r.fresh = pipe.XReadGroup(ctx, &redis.XReadGroupArgs{Group: GroupOkFriend, Consumer: a.Consumer, Streams: gt, Count: a.count(), Block: -1})
}

func streamsOf(cmd *redis.XStreamSliceCmd) (map[string][]redis.XMessage, error) {
	out := map[string][]redis.XMessage{}
	if cmd == nil {
		return out, nil
	}
	res, err := cmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	for _, s := range res {
		out[s.Stream] = append(out[s.Stream], s.Messages...)
	}
	return out, nil
}

// Pass reads every open sprint's log once, handles the card events, and
// returns what it handled per sprint with events, in sprint order. bound,
// when set, is asked before the pass and before each sprint's handling; its
// error stops the pass there and is returned. A sprint's error (its handling
// or its start) is in its result; the returned error is the read's or the
// bound's.
func (a *OkFriendSprints) Pass(ctx context.Context, bound func() error) ([]OkSprintResult, error) {
	if a.Store == nil || a.Consumer == "" {
		return nil, fmt.Errorf("ok-to-friend: store and consumer are required")
	}
	if a.started == nil {
		a.started, a.acks, a.retry = map[string]bool{}, map[string][]string{}, map[string]okRetry{}
	}
	if bound != nil {
		if err := bound(); err != nil {
			return nil, fmt.Errorf("pass not started, no sprint read: %w", err)
		}
	}
	began := time.Now()
	client := a.Store.Client()

	// The one round trip: last pass's acks and proc line, the index, and
	// the reads over the sprints the last pass found open and started.
	pipe := client.Pipeline()
	for key, ids := range a.acks {
		pipe.XAck(ctx, key, GroupOkFriend, ids...)
	}
	if a.proc != nil {
		pipe.FCall(ctx, FunctionOkFriendPass, nil, append([]any{GroupOkFriend}, a.proc...)...)
	}
	members := pipe.SMembers(ctx, "sprints")
	r := okRead{status: map[string]*redis.StringCmd{}}
	for _, s := range a.known {
		r.status[s] = pipe.HGet(ctx, "s:"+s, "status")
	}
	var reading []string
	for _, s := range a.open {
		if a.started[s] {
			reading = append(reading, s)
		}
	}
	a.queueReads(ctx, pipe, reading, &r)
	_, err := pipe.Exec(ctx)
	a.acks, a.proc = map[string][]string{}, nil
	if err != nil && !errors.Is(err, redis.Nil) {
		if strings.HasPrefix(err.Error(), "NOGROUP") {
			// A log lost its group: start every sprint again next pass.
			a.started = map[string]bool{}
		}
		return nil, fmt.Errorf("ok-to-friend: read: %w", err)
	}
	pending, err := streamsOf(r.pending)
	if err != nil {
		return nil, fmt.Errorf("ok-to-friend: read pending: %w", err)
	}
	fresh, err := streamsOf(r.fresh)
	if err != nil {
		return nil, fmt.Errorf("ok-to-friend: read: %w", err)
	}

	// The index as it is now: a sprint new to it has its status read, and
	// an open sprint not started is started and read, in one more round.
	names := members.Val()
	sort.Strings(names)
	var fresher []string
	for _, s := range names {
		if _, ok := r.status[s]; !ok {
			fresher = append(fresher, s)
		}
	}
	if len(fresher) > 0 {
		pipe := client.Pipeline()
		for _, s := range fresher {
			r.status[s] = pipe.HGet(ctx, "s:"+s, "status")
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("ok-to-friend: sprint status: %w", err)
		}
	}
	a.known = names
	var open, unstarted []string
	var results []OkSprintResult
	read := map[string]bool{}
	for _, s := range reading {
		read[s] = true
	}
	for _, s := range names {
		if r.status[s].Val() != "open" {
			continue
		}
		open = append(open, s)
		if read[s] {
			continue
		}
		unstarted = append(unstarted, s)
	}
	a.open = open
	if len(unstarted) > 0 {
		var failed []OkSprintResult
		unstarted, failed = a.start(ctx, unstarted)
		results = append(results, failed...)
	}
	if len(unstarted) > 0 {
		pipe := client.Pipeline()
		var more okRead
		a.queueReads(ctx, pipe, unstarted, &more)
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return results, fmt.Errorf("ok-to-friend: read new sprints: %w", err)
		}
		p, err := streamsOf(more.pending)
		if err != nil {
			return results, fmt.Errorf("ok-to-friend: read pending: %w", err)
		}
		f, err := streamsOf(more.fresh)
		if err != nil {
			return results, fmt.Errorf("ok-to-friend: read: %w", err)
		}
		for k, v := range p {
			pending[k] = v
		}
		for k, v := range f {
			fresh[k] = v
		}
	}

	// The card events, per sprint; the rest are acked with the next read.
	now := time.Now()
	total := 0
	for i, s := range open {
		key := logKey(s)
		var wantP, wantF []redis.XMessage
		for _, m := range pending[key] {
			if okWanted(m) {
				wantP = append(wantP, m)
			} else {
				a.acks[key] = append(a.acks[key], m.ID)
			}
		}
		for _, m := range fresh[key] {
			if okWanted(m) {
				wantF = append(wantF, m)
			} else {
				a.acks[key] = append(a.acks[key], m.ID)
			}
		}
		full := int64(len(fresh[key])) >= a.count()
		if rt, ok := a.retry[s]; ok && now.Before(rt.at) {
			// Backing off after a retryable deferral: the pending entries
			// wait; new card events still run.
			wantP = nil
		}
		if len(wantP) == 0 && len(wantF) == 0 && !full {
			continue
		}
		if bound != nil {
			if err := bound(); err != nil {
				err = fmt.Errorf("stopped before %s, %d of %d open sprint(s) not handled: %w", s, len(open)-i, len(open), err)
				a.proc = []any{strconv.FormatInt(time.Since(began).Milliseconds(), 10), strconv.Itoa(total), err.Error()}
				return results, err
			}
		}
		o := &OkFriend{Store: a.Store, Sprint: s, Consumer: a.Consumer, Actor: a.Actor, Count: a.count()}
		n, err := o.Drain(ctx, wantP, wantF, full)
		total += n
		switch {
		case retryable(err):
			rt := a.retry[s]
			rt.wait = o.nextBackoff(rt.wait)
			rt.at = now.Add(rt.wait)
			a.retry[s] = rt
		case err == nil && len(wantP) > 0:
			delete(a.retry, s)
		}
		results = append(results, OkSprintResult{Sprint: s, N: n, Err: err})
	}
	var errs []string
	for _, res := range results {
		if res.Err != nil && !retryable(res.Err) {
			errs = append(errs, res.Sprint+": "+res.Err.Error())
		}
	}
	a.proc = []any{strconv.FormatInt(time.Since(began).Milliseconds(), 10), strconv.Itoa(total), strings.Join(errs, "; ")}
	return results, nil
}

// start is OkFriend.Start for every sprint not started, in one pipeline
// (#3831): the group, then the first reclaim page of each; a sprint with
// more pending than a page is reclaimed on by its own Start. It returns the
// sprints started and a result for each that failed.
func (a *OkFriendSprints) start(ctx context.Context, sprints []string) ([]string, []OkSprintResult) {
	var todo []string
	for _, s := range sprints {
		if !a.started[s] {
			todo = append(todo, s)
		}
	}
	client := a.Store.Client()
	pipe := client.Pipeline()
	groups := make([]*redis.StatusCmd, len(todo))
	claims := make([]*redis.XAutoClaimCmd, len(todo))
	for i, s := range todo {
		groups[i] = pipe.XGroupCreateMkStream(ctx, logKey(s), GroupOkFriend, "0")
		claims[i] = pipe.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: logKey(s), Group: GroupOkFriend,
			Consumer: a.Consumer, MinIdle: 0, Start: "0-0", Count: 1000})
	}
	if len(todo) > 0 {
		_, _ = pipe.Exec(ctx)
	}
	var failed []OkSprintResult
	bad := map[string]bool{}
	for i, s := range todo {
		if err := groups[i].Err(); err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			failed = append(failed, OkSprintResult{Sprint: s, Err: fmt.Errorf("ok-to-friend: group: %w", err)})
			bad[s] = true
			continue
		}
		_, next, err := claims[i].Result()
		if err == nil && next != "" && next != "0-0" {
			// More than a page pending: the rest by the sprint's own Start.
			o := &OkFriend{Store: a.Store, Sprint: s, Consumer: a.Consumer, Actor: a.Actor}
			err = o.Start(ctx)
		}
		if err != nil {
			failed = append(failed, OkSprintResult{Sprint: s, Err: fmt.Errorf("ok-to-friend: reclaim pending: %w", err)})
			bad[s] = true
			continue
		}
		a.started[s] = true
	}
	var ok []string
	for _, s := range sprints {
		if !bad[s] {
			ok = append(ok, s)
		}
	}
	return ok, failed
}

// Drain handles one sprint's card events a batched read already delivered
// (OkFriendSprints): the pending ones, then the new ones; when the read came
// back full (more), it reads on without blocking until the log is drained,
// as a pass does. An event left pending on a retryable error is the
// deferred error, as a pass's is.
func (o *OkFriend) Drain(ctx context.Context, pending, fresh []redis.XMessage, more bool) (int, error) {
	if err := o.check(); err != nil {
		return 0, err
	}
	count := o.Count
	if count <= 0 {
		count = 100
	}
	handled := 0
	var deferred error
	for _, batch := range [][]redis.XMessage{pending, fresh} {
		if len(batch) == 0 {
			continue
		}
		n, err := o.handleBatch(ctx, batch)
		handled += n
		if retryable(err) {
			deferred = err
		} else if err != nil {
			return handled, err
		}
	}
	for more {
		msgs, err := o.read(ctx, ">", count, -1)
		if err != nil {
			return handled, err
		}
		more = int64(len(msgs)) >= count
		if len(msgs) == 0 {
			break
		}
		n, err := o.handleBatch(ctx, msgs)
		handled += n
		if retryable(err) {
			deferred = err
		} else if err != nil {
			return handled, err
		}
	}
	return handled, deferred
}
