package pitstop

// held.go is the one gate every automatic actor on the sprint table reads
// (Glenn 2026-09-25 5:50 PM ET: "pit stop should enable/disable all automatic
// activity related to the sprint table"). On 09-25 s:<S>:pitstop was set at
// 5:05 PM and only the dealer honoured it; the land duty kept rebuilding and
// force-pushing every stream branch each tick. Now the reconciler reads Held
// once per pass and skips its duties, and the route loop reads it before it
// dispatches; beats, the table and `pitstop status` do not.
//
// A stop is held while its sprint is not closed. A scope=all stop holds every
// stream (Whole); a scope=streams stop holds the streams it names, and only a
// stream-aware actor can honour it (Holds.Stream). A key of another type at
// s:<S>:pitstop (the 09-23 string) and the table's legacy
// sprint:<S>:pitstop both hold the whole sprint: a stop we cannot read is a
// stop, never a go.

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// LegacyKey is the table's older pit stop key, read as a whole stop.
func LegacyKey(sprint string) string { return "sprint:" + sprint + ":pitstop" }

// Hold is one held sprint: its stop, and the words a receipt prints.
type Hold struct {
	Sprint string
	Stop   Stop
}

// Whole is a scope=all stop: every stream but the lifted ones, and every
// actor that cannot tell streams apart.
func (h Hold) Whole() bool { return h.Stop.Set && h.Stop.Streams == nil }

// Scope is the receipt's scope word: `all` or the named streams, quoted and
// comma-joined (the pitstop verb's scope field).
func (h Hold) Scope() string {
	if h.Whole() {
		return "all"
	}
	return quoteList(h.Stop.Streams)
}

// Why is the stop's why, quoted for a receipt.
func (h Hold) Why() string { return oneline.Quote(h.Stop.Why) }

// Words are the receipt words every PITSTOP idle line carries:
// `sprint=<S> scope=<scope>[ lifted=<streams>]`.
func (h Hold) Words() string {
	w := "sprint=" + oneline.Field(h.Sprint) + " scope=" + h.Scope()
	if h.Whole() && len(h.Stop.Lifted) > 0 {
		w += " lifted=" + quoteList(h.Stop.Lifted)
	}
	return w
}

// Holds are the held sprints, in name order.
type Holds []Hold

// Whole is the first scope=all hold, if any: the one that idles an actor
// that cannot tell streams apart.
func (hs Holds) Whole() (Hold, bool) {
	for _, h := range hs {
		if h.Whole() {
			return h, true
		}
	}
	return Hold{}, false
}

// Stream is the first hold that holds the stream, if any.
func (hs Holds) Stream(stream string) (Hold, bool) {
	for _, h := range hs {
		if h.Stop.InScope(stream) {
			return h, true
		}
	}
	return Hold{}, false
}

// Held is one sprint's gate: the hold and true while the sprint is not
// closed and a stop is set (s:<S>:pitstop, a key of another type there, or
// the legacy sprint:<S>:pitstop). One pipelined round trip.
func Held(ctx context.Context, c redis.Cmdable, sprint string) (Hold, bool, error) {
	hs, err := read(ctx, c, []string{sprint})
	if err != nil || len(hs) == 0 {
		return Hold{}, false, err
	}
	return hs[0], true, nil
}

// HeldOpen is the gate of an actor that serves every sprint (the
// reconciler, a serve with no --sprint): the holds of every member of
// `sprints` and sprint:order that is not closed. Two pipelined round trips.
func HeldOpen(ctx context.Context, c redis.Cmdable) (Holds, error) {
	pipe := c.Pipeline()
	members := pipe.SMembers(ctx, "sprints")
	order := pipe.ZRange(ctx, "sprint:order", 0, -1)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, s := range append(members.Val(), order.Val()...) {
		if s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	sort.Strings(names)
	return read(ctx, c, names)
}

func read(ctx context.Context, c redis.Cmdable, names []string) (Holds, error) {
	if len(names) == 0 {
		return nil, nil
	}
	type cmds struct {
		status *redis.StringCmd
		stop   *redis.MapStringStringCmd
		legacy *redis.IntCmd
	}
	pipe := c.Pipeline()
	cs := make([]cmds, len(names))
	for i, s := range names {
		cs[i] = cmds{
			status: pipe.HGet(ctx, "s:"+s, "status"),
			stop:   pipe.HGetAll(ctx, Key(s)),
			legacy: pipe.Exists(ctx, LegacyKey(s)),
		}
	}
	// A WRONGTYPE on one stop fails Exec; each command keeps its own answer.
	_, _ = pipe.Exec(ctx)
	var out Holds
	for i, s := range names {
		if err := cs[i].status.Err(); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if err := cs[i].legacy.Err(); err != nil {
			return nil, err
		}
		if cs[i].status.Val() == "closed" {
			continue
		}
		h, err := cs[i].stop.Result()
		switch {
		case err != nil && strings.HasPrefix(err.Error(), "WRONGTYPE"):
			out = append(out, Hold{Sprint: s, Stop: Stop{Sprint: s, Set: true, By: "wrongtype",
				Why: Key(s) + " is not a hash; nova-sprint pitstop clear repairs it"}})
		case err != nil:
			return nil, err
		case len(h) > 0:
			out = append(out, Hold{Sprint: s, Stop: FromHash(s, h)})
		case cs[i].legacy.Val() > 0:
			out = append(out, Hold{Sprint: s, Stop: Stop{Sprint: s, Set: true, By: "legacy",
				Why: LegacyKey(s) + " is set"}})
		}
	}
	return out, nil
}

type holdsKey struct{}

// WithHolds is ctx carrying the holds one reconciler pass read, for the
// stream-aware duties it runs.
func WithHolds(ctx context.Context, hs Holds) context.Context {
	return context.WithValue(ctx, holdsKey{}, hs)
}

// FromContext is the holds the pass put on ctx, and whether it did.
func FromContext(ctx context.Context) (Holds, bool) {
	hs, ok := ctx.Value(holdsKey{}).(Holds)
	return hs, ok
}

// Current is the holds on ctx when a pass put them there, else a fresh
// HeldOpen read: a stream-aware duty run outside the reconciler still
// honours the stop.
func Current(ctx context.Context, c redis.Cmdable) (Holds, error) {
	if hs, ok := FromContext(ctx); ok {
		return hs, nil
	}
	return HeldOpen(ctx, c)
}
