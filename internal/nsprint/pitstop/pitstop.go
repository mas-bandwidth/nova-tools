// Package pitstop is the sprint pit stop (nova-tools #3371): one Redis hash
// per sprint, s:<S>:pitstop {by, reason, scope, at}. The deal pass reads it; the feed
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
	"strconv"
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
	Reason string
	Scope  string
	At     int64 // Redis TIME, ms
}

// Line is the one status line: `PITSTOP sprint=<S> none`, or
// `PITSTOP sprint=<S> set by=<who> at=<ms> (<UTC>) reason="<text>" scope=<scope>`.
func (s Stop) Line() string {
	if !s.Set {
		return fmt.Sprintf("PITSTOP sprint=%s none", oneline.Field(s.Sprint))
	}
	return fmt.Sprintf("PITSTOP sprint=%s set by=%s at=%d (%s) reason=%s scope=%s",
		oneline.Field(s.Sprint), oneline.Field(s.By), s.At, utc(s.At), oneline.Quote(s.Reason), oneline.Field(s.Scope))
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
	return Stop{Sprint: sprint, Set: true, By: h["by"], Reason: h["reason"], Scope: h["scope"], At: at}
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
)

// Result is one Set or Clear. Prior is the stop that was there before the
// call (Set: the one refused or replaced; Clear: the one lifted).
type Result struct {
	Outcome Outcome
	At      int64 // Done: when the write happened (Redis TIME, ms)
	Prior   Stop
}

// Set sets the sprint's stop in one ns_pitstop_set call. by is required.
func Set(ctx context.Context, c redis.Cmdable, sprint, by, reason, scope string, force bool, idem string) (Result, error) {
	f := "0"
	if force {
		f = "1"
	}
	reply, err := c.FCall(ctx, "ns_pitstop_set", nil, sprint, by, reason, scope, f, idem).StringSlice()
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
		if len(reply) != 5 {
			return Result{}, fmt.Errorf("ns_pitstop_set: malformed REFUSED %v", reply)
		}
		at, _ := strconv.ParseInt(reply[4], 10, 64)
		return Result{Outcome: Exists, Prior: Stop{Sprint: sprint, Set: true, By: reply[1], Reason: reply[2], Scope: reply[3], At: at}}, nil
	case "SET":
		if len(reply) != 5 {
			return Result{}, fmt.Errorf("ns_pitstop_set: malformed SET %v", reply)
		}
		at, _ := strconv.ParseInt(reply[1], 10, 64)
		r := Result{Outcome: Done, At: at, Prior: Stop{Sprint: sprint}}
		if reply[2] != "" {
			r.Prior = Stop{Sprint: sprint, Set: true, By: reply[2], Reason: reply[3], Scope: reply[4]}
		}
		return r, nil
	default:
		return Result{}, fmt.Errorf("ns_pitstop_set: unexpected reply %v", reply)
	}
}

// Clear lifts the sprint's stop in one ns_pitstop_clear call. by is required.
func Clear(ctx context.Context, c redis.Cmdable, sprint, by, scope, idem string) (Result, error) {
	reply, err := c.FCall(ctx, "ns_pitstop_clear", nil, sprint, by, scope, idem).StringSlice()
	if err != nil {
		return Result{}, err
	}
	if len(reply) == 0 {
		return Result{}, fmt.Errorf("ns_pitstop_clear: empty reply")
	}
	switch reply[0] {
	case "NONE":
		return Result{Outcome: None}, nil
	case "CLEARED":
		if len(reply) != 6 {
			return Result{}, fmt.Errorf("ns_pitstop_clear: malformed CLEARED %v", reply)
		}
		at, _ := strconv.ParseInt(reply[1], 10, 64)
		was, _ := strconv.ParseInt(reply[5], 10, 64)
		return Result{Outcome: Done, At: at, Prior: Stop{Sprint: sprint, Set: true, By: reply[2], Reason: reply[3], Scope: reply[4], At: was}}, nil
	default:
		return Result{}, fmt.Errorf("ns_pitstop_clear: unexpected reply %v", reply)
	}
}
