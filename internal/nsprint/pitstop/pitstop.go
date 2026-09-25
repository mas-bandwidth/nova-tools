// Package pitstop is the sprint pit stop (nova-tools #3371): one Redis hash
// per sprint, s:<S>:pitstop {by, why, at, scope}. The scope is all (every
// stream but the lifted ones) or a named set of streams; InScope is the
// question a take asks before it claims a task of a stream. The deal pass reads it; the feed
// and the table read it through Read. While it exists the sprint deals nothing (the deal pass's
// plan skips the sprint and ns_card_deal refuses its cards). It replaces the
// pit stop as a bus note: a friend reads a key, never judges a note.
//
// Set and Clear are one FCALL each (ns_pitstop_set, ns_pitstop_clear in the
// nova_sprint library), the check and the write and the s:<S>:log receipt in
// one step. Read is one HGETALL.
package pitstop

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Key is the sprint's pit stop hash.
func Key(sprint string) string { return "s:" + sprint + ":pitstop" }

// LogKey is the sprint log the receipts go to.
func LogKey(sprint string) string { return "s:" + sprint + ":log" }

// Stop is one sprint's pit stop as stored.
type Stop struct {
	Sprint string
	Set    bool // false: no stop; the other fields are empty
	By     string
	Why    string
	At     int64 // Redis TIME, ms
	// Streams is the scope when the stop names streams (scope=streams); nil
	// is scope=all. Lifted is the streams a narrowing clear took out of an
	// all-scope stop. Both sorted.
	Streams []string
	Lifted  []string
}

// InScope is whether the stop holds the stream: a scope=all stop holds every
// stream it has not lifted, a scope=streams stop only the streams it names,
// and no stop holds none. ns_pitstop_clear asks the same question in Lua
// (NS.pitstop.in_scope).
func (s Stop) InScope(stream string) bool {
	if !s.Set {
		return false
	}
	if s.Streams != nil {
		return contains(s.Streams, stream)
	}
	return !contains(s.Lifted, stream)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func quoteList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = oneline.Quote(x)
	}
	return strings.Join(q, ",")
}

// Line is the one status line: `PITSTOP sprint=<S> none`, or
// `PITSTOP sprint=<S> set by=<who> at=<ms> (<UTC>) why="<text>"`, followed by
// ` streams="<a>","<b>"` for a scope=streams stop or ` lifted="<a>"` for an
// all-scope stop with streams lifted (nothing more for a plain all stop).
func (s Stop) Line() string {
	if !s.Set {
		return fmt.Sprintf("PITSTOP sprint=%s none", oneline.Field(s.Sprint))
	}
	line := fmt.Sprintf("PITSTOP sprint=%s set by=%s at=%d (%s) why=%s",
		oneline.Field(s.Sprint), oneline.Field(s.By), s.At, utc(s.At), oneline.Quote(s.Why))
	if s.Streams != nil {
		line += " streams=" + quoteList(s.Streams)
	} else if len(s.Lifted) > 0 {
		line += " lifted=" + quoteList(s.Lifted)
	}
	return line
}

func utc(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05Z")
}

// Read is the stop for one sprint, one HGETALL.
func Read(ctx context.Context, c redis.Cmdable, sprint string) (Stop, error) {
	h, err := c.HGetAll(ctx, Key(sprint)).Result()
	if err != nil {
		return Stop{}, err
	}
	return FromHash(sprint, h), nil
}

// FromHash reads a stop from its hash as HGETALL answered it; an empty hash
// is no stop. A reader that pipelines the HGETALL with other reads uses it.
func FromHash(sprint string, h map[string]string) Stop {
	if len(h) == 0 {
		return Stop{Sprint: sprint}
	}
	at, _ := strconv.ParseInt(h["at"], 10, 64)
	st := Stop{Sprint: sprint, Set: true, By: h["by"], Why: h["why"], At: at}
	if h["scope"] == "streams" {
		st.Streams = []string{}
	}
	for f := range h {
		if name, ok := strings.CutPrefix(f, "stream:"); ok && st.Streams != nil {
			st.Streams = append(st.Streams, name)
		} else if name, ok := strings.CutPrefix(f, "lifted:"); ok && st.Streams == nil {
			st.Lifted = append(st.Lifted, name)
		}
	}
	sort.Strings(st.Streams)
	sort.Strings(st.Lifted)
	return st
}

// Outcome is what a Set or Clear did.
type Outcome int

const (
	// Done: the stop was set (or replaced under force), or cleared.
	Done Outcome = iota
	// Unknown: the sprint has no s:<S> status (Set only).
	Unknown
	// Exists: a stop is already set and force was not given (Set only).
	Exists
	// None: no stop was set (Clear only).
	None
	// Narrowed: a clear naming streams lifted them and a stop remains
	// (Clear only).
	Narrowed
	// NotIn: a clear named a stream the stop does not hold; nothing was
	// written (Clear only; Result.Stream names it).
	NotIn
)

// Result is one Set or Clear. Prior is the stop that was there before the
// call (Set: the one refused or replaced; Clear: the one lifted).
type Result struct {
	Outcome Outcome
	At      int64 // Done, Narrowed: when the write happened (Redis TIME, ms)
	Prior   Stop
	Stream  string // NotIn: the stream the stop does not hold
}

// Set sets the sprint's stop in one ns_pitstop_set call. by is required. No
// stream is scope=all; streams name the scope exactly.
func Set(ctx context.Context, c redis.Cmdable, sprint, by, why string, force bool, idem string, streams ...string) (Result, error) {
	f := "0"
	if force {
		f = "1"
	}
	args := []any{sprint, by, why, f, idem}
	for _, s := range streams {
		args = append(args, s)
	}
	reply, err := c.FCall(ctx, "ns_pitstop_set", nil, args...).StringSlice()
	if err != nil {
		return Result{}, err
	}
	if len(reply) == 0 {
		return Result{}, fmt.Errorf("ns_pitstop_set: empty reply")
	}
	switch reply[0] {
	case "UNKNOWN":
		return Result{Outcome: Unknown}, nil
	case "REFUSED":
		if len(reply) != 4 {
			return Result{}, fmt.Errorf("ns_pitstop_set: malformed REFUSED %v", reply)
		}
		at, _ := strconv.ParseInt(reply[3], 10, 64)
		return Result{Outcome: Exists, Prior: Stop{Sprint: sprint, Set: true, By: reply[1], Why: reply[2], At: at}}, nil
	case "SET":
		if len(reply) != 4 {
			return Result{}, fmt.Errorf("ns_pitstop_set: malformed SET %v", reply)
		}
		at, _ := strconv.ParseInt(reply[1], 10, 64)
		r := Result{Outcome: Done, At: at, Prior: Stop{Sprint: sprint}}
		if reply[2] != "" {
			r.Prior = Stop{Sprint: sprint, Set: true, By: reply[2], Why: reply[3]}
		}
		return r, nil
	default:
		return Result{}, fmt.Errorf("ns_pitstop_set: unexpected reply %v", reply)
	}
}

// Clear lifts the sprint's stop in one ns_pitstop_clear call. by is required.
// No stream lifts it whole; streams narrow it by exactly those (Narrowed while
// a stop remains, Done when the last scoped stream goes, NotIn with nothing
// written when one of them is not held).
func Clear(ctx context.Context, c redis.Cmdable, sprint, by, idem string, streams ...string) (Result, error) {
	args := []any{sprint, by, idem}
	for _, s := range streams {
		args = append(args, s)
	}
	reply, err := c.FCall(ctx, "ns_pitstop_clear", nil, args...).StringSlice()
	if err != nil {
		return Result{}, err
	}
	if len(reply) == 0 {
		return Result{}, fmt.Errorf("ns_pitstop_clear: empty reply")
	}
	switch reply[0] {
	case "NONE":
		return Result{Outcome: None}, nil
	case "NOTIN":
		if len(reply) != 2 {
			return Result{}, fmt.Errorf("ns_pitstop_clear: malformed NOTIN %v", reply)
		}
		return Result{Outcome: NotIn, Stream: reply[1]}, nil
	case "CLEARED", "NARROWED":
		if len(reply) != 5 {
			return Result{}, fmt.Errorf("ns_pitstop_clear: malformed %s %v", reply[0], reply)
		}
		at, _ := strconv.ParseInt(reply[1], 10, 64)
		was, _ := strconv.ParseInt(reply[4], 10, 64)
		o := Done
		if reply[0] == "NARROWED" {
			o = Narrowed
		}
		return Result{Outcome: o, At: at, Prior: Stop{Sprint: sprint, Set: true, By: reply[2], Why: reply[3], At: was}}, nil
	default:
		return Result{}, fmt.Errorf("ns_pitstop_clear: unexpected reply %v", reply)
	}
}
