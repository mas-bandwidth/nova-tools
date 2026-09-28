package life

// Friend lifecycle events (nova-tools #3153). A shell can beat every second
// with no model behind it, so process life (beat) and model life (a turn)
// are separate signals on one stream, friend:<f>:events, whose one writer
// is the Redis Function ns_friend_event (internal/nsprint/fn/lua/presence.lua).
// Classify derives one state from those events and a clock; it reads only
// its two arguments (no Redis, no network, no model), so a classification
// costs zero tokens and replays byte for byte from a fixture.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Function names registered by presence.lua and friend.lua for #3153.
const (
	FunctionEvent    = "ns_friend_event"
	FunctionWakeMode = "ns_friend_wakemode"
)

// The six event kinds friend:<f>:events accepts.
const (
	EventBeat       = "beat"
	EventDeliver    = "deliver"
	EventTurnStart  = "turn-start"
	EventTurnEnd    = "turn-end"
	EventTurnError  = "turn-error"
	EventUsageLimit = "usage-limit"
)

// EventKinds lists the six kinds in the order the verb names them.
var EventKinds = []string{EventBeat, EventDeliver, EventTurnStart, EventTurnEnd, EventTurnError, EventUsageLimit}

// ValidEventKind reports whether k is one of the six kinds.
func ValidEventKind(k string) bool {
	for _, kind := range EventKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Declared config, not code (#3153): the classifier's window is two
// idle-watch cycles of about 10 min each (measured 2026-09-22), and a
// delivered wake must start a turn within WakeTurnBound. A sprint fold may
// re-measure both; nothing else changes them.
var (
	OfflineModelWindow = 20 * time.Minute
	WakeTurnBound      = 120 * time.Second
)

// EventsReadBound caps one read of friend:<f>:events: the newest entries in
// append order, which at one beat per second cover the 20 min window.
const EventsReadBound = 2000

// WakeModeScheduled is the one wake mode a friend may declare, and only on a
// firing receipt; an absent friend:<f>:wakemode means active-turn/tool-return.
const WakeModeScheduled = "scheduled-model-turn"

// EventsKey is the lifecycle stream of friend f.
func EventsKey(f string) string { return "friend:" + f + ":events" }

// WakeModeKey is the declared wake mode of friend f.
func WakeModeKey(f string) string { return "friend:" + f + ":wakemode" }

// Event is one entry of friend:<f>:events: its stream id, kind, the
// producer's UTC milliseconds and, for a turn-start, the deliver id that
// caused it. Reset is a usage-limit's reset (ms) when the producer gives one.
type Event struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	At    int64  `json:"at"`
	Cause string `json:"cause,omitempty"`
	Reset int64  `json:"reset,omitempty"`
}

// The classifier's states, first match wins (#3153 rev 7).
const (
	ClassOutOfCredits = "OUT-OF-CREDITS"
	ClassDown         = "DOWN"
	ClassWakeMissed   = "WAKE-MISSED"
	ClassWakePending  = "WAKE-PENDING"
	ClassOfflineModel = "OFFLINE-MODEL"
	ClassUp           = "UP"
)

// Class is one classification. Basis is the event the state rests on: the
// usage-limit for OUT-OF-CREDITS, the newest delivery for WAKE-MISSED and
// WAKE-PENDING, else the newest event in the window ("" when it is empty).
// Until is the out-of-credits reset in ms (the event's, else now + window).
type Class struct {
	State string
	Basis string
	Until int64
}

func windowOf(events []Event, now time.Time) []Event {
	nowMS := now.UnixMilli()
	from := nowMS - OfflineModelWindow.Milliseconds()
	var in []Event
	for _, e := range events {
		if e.At >= from && e.At <= nowMS {
			in = append(in, e)
		}
	}
	return in
}

// Classify derives a friend's state at now from its events in append order:
//  1. OUT-OF-CREDITS: a usage-limit in the window with no later turn-start.
//  2. DOWN: no beat in the window.
//  3. WAKE-MISSED: D, the newest deliver in the window, has no turn-start
//     with cause D and now - D.at > WakeTurnBound (strictly).
//  4. WAKE-PENDING: D has no caused turn-start and is inside the bound.
//  5. OFFLINE-MODEL: beats in the window and no turn-start.
//  6. UP otherwise.
//
// Older deliveries are never consulted: any later delivery supersedes them.
func Classify(events []Event, now time.Time) Class {
	in := windowOf(events, now)
	nowMS := now.UnixMilli()
	newest := ""
	if len(in) > 0 {
		newest = in[len(in)-1].ID
	}
	limit := -1
	beats, turns := false, false
	deliver := -1
	for i, e := range in {
		switch e.Kind {
		case EventUsageLimit:
			limit = i
		case EventBeat:
			beats = true
		case EventTurnStart:
			turns = true
			if limit >= 0 {
				limit = -1 // a later turn-start answers the usage limit
			}
		case EventDeliver:
			deliver = i
		}
	}
	if limit >= 0 {
		until := in[limit].Reset
		if until <= 0 {
			until = nowMS + OfflineModelWindow.Milliseconds()
		}
		return Class{State: ClassOutOfCredits, Basis: in[limit].ID, Until: until}
	}
	if !beats {
		return Class{State: ClassDown, Basis: newest}
	}
	if deliver >= 0 {
		d := in[deliver]
		answered := false
		for _, e := range in[deliver+1:] {
			if e.Kind == EventTurnStart && e.Cause == d.ID {
				answered = true
				break
			}
		}
		if !answered {
			if nowMS-d.At > WakeTurnBound.Milliseconds() {
				return Class{State: ClassWakeMissed, Basis: d.ID}
			}
			return Class{State: ClassWakePending, Basis: d.ID}
		}
	}
	if !turns {
		return Class{State: ClassOfflineModel, Basis: newest}
	}
	return Class{State: ClassUp, Basis: newest}
}

// Receipt is a firing receipt: a deliver D followed by a turn-start T with
// cause D at most WakeTurnBound later (exactly 120,000 ms is a receipt).
type Receipt struct {
	Deliver Event
	Turn    Event
	LagMS   int64
}

// FindReceipt returns the newest firing receipt among events, in append
// order. A bare turn-start, or a caused one later than the bound, is not one.
func FindReceipt(events []Event) (Receipt, bool) {
	delivers := map[string]Event{}
	var best Receipt
	found := false
	for _, e := range events {
		switch e.Kind {
		case EventDeliver:
			delivers[e.ID] = e
		case EventTurnStart:
			d, ok := delivers[e.Cause]
			if e.Cause == "" || !ok {
				continue
			}
			lag := e.At - d.At
			if lag < 0 || lag > WakeTurnBound.Milliseconds() {
				continue
			}
			best, found = Receipt{Deliver: d, Turn: e, LagMS: lag}, true
		}
	}
	return best, found
}

// EventInvalidError is ns_friend_event's INVALID answer with its reason.
type EventInvalidError struct{ Reason string }

func (e *EventInvalidError) Error() string { return "INVALID " + e.Reason }

// AppendEvent appends one event through ns_friend_event, the one writer of
// friend:<f>:events, and returns its stream id. actor is the seat the caller
// already checked (NOVA_FRIEND); the Function refuses actor != friend.
func AppendEvent(ctx context.Context, st *store.Store, friend, kind, cause string, at time.Time, actor string) (string, error) {
	if st == nil || friend == "" {
		return "", fmt.Errorf("friend event: store and friend are required")
	}
	reply, err := st.Client().FCall(ctx, FunctionEvent, nil,
		friend, kind, cause, strconv.FormatInt(at.UTC().UnixMilli(), 10), actor).Slice()
	if err != nil {
		return "", fmt.Errorf("friend event %s: %w", friend, err)
	}
	if len(reply) == 0 {
		return "", fmt.Errorf("friend event %s: empty reply", friend)
	}
	switch status := fmt.Sprint(reply[0]); status {
	case "OK":
		if len(reply) < 2 {
			return "", fmt.Errorf("friend event %s: short OK reply", friend)
		}
		return fmt.Sprint(reply[1]), nil
	case "INVALID":
		reason := ""
		if len(reply) > 1 {
			reason = fmt.Sprint(reply[1])
		}
		return "", &EventInvalidError{Reason: reason}
	default:
		return "", fmt.Errorf("friend event %s: %s", friend, status)
	}
}

// ParseEvents turns stream entries into events, skipping none: an entry
// with an unreadable at keeps At 0, which falls outside any window.
func ParseEvents(msgs []StreamEntry) []Event {
	out := make([]Event, 0, len(msgs))
	for _, m := range msgs {
		e := Event{ID: m.ID, Kind: m.Values["kind"], Cause: m.Values["cause"]}
		e.At, _ = strconv.ParseInt(m.Values["at"], 10, 64)
		e.Reset, _ = strconv.ParseInt(m.Values["reset"], 10, 64)
		out = append(out, e)
	}
	return out
}

// StreamEntry is one stream entry with string values.
type StreamEntry struct {
	ID     string
	Values map[string]string
}

// ReadEvents reads the newest EventsReadBound entries of friend:<f>:events
// in append order (one XREVRANGE).
func ReadEvents(ctx context.Context, st *store.Store, f string) ([]Event, error) {
	if st == nil || f == "" {
		return nil, fmt.Errorf("friend events: store and friend are required")
	}
	msgs, err := st.Client().XRevRangeN(ctx, EventsKey(f), "+", "-", EventsReadBound).Result()
	if err != nil {
		return nil, fmt.Errorf("friend events %s: %w", f, err)
	}
	entries := make([]StreamEntry, len(msgs))
	for i, m := range msgs {
		vals := map[string]string{}
		for k, v := range m.Values {
			vals[k] = fmt.Sprint(v)
		}
		entries[len(msgs)-1-i] = StreamEntry{ID: m.ID, Values: vals}
	}
	return ParseEvents(entries), nil
}

// ErrNoReceipt is the wake-mode refusal: no firing receipt in the window, or
// the Function's own re-read of the pair said no.
var ErrNoReceipt = errors.New("no firing receipt")

// WakeModeResult is one accepted declaration.
type WakeModeResult struct {
	Deliver string
	Turn    string
	LagMS   int64
}

// DeclareWakeMode reads f's events in the classifier window at now, finds
// the newest firing receipt and asks ns_friend_wakemode to declare
// scheduled-model-turn; the Function re-reads both entries, so a caller
// cannot forge a receipt. ErrNoReceipt when there is none.
func DeclareWakeMode(ctx context.Context, st *store.Store, f string, now time.Time, actor, idem string) (WakeModeResult, error) {
	events, err := ReadEvents(ctx, st, f)
	if err != nil {
		return WakeModeResult{}, err
	}
	rc, ok := FindReceipt(windowOf(events, now))
	if !ok {
		return WakeModeResult{}, ErrNoReceipt
	}
	reply, err := st.Client().FCall(ctx, FunctionWakeMode, nil, f, rc.Deliver.ID, rc.Turn.ID, actor, idem).Slice()
	if err != nil {
		return WakeModeResult{}, fmt.Errorf("friend wake mode %s: %w", f, err)
	}
	if len(reply) == 0 {
		return WakeModeResult{}, fmt.Errorf("friend wake mode %s: empty reply", f)
	}
	switch status := fmt.Sprint(reply[0]); status {
	case "OK":
		return WakeModeResult{Deliver: rc.Deliver.ID, Turn: rc.Turn.ID, LagMS: rc.LagMS}, nil
	case "REFUSED":
		return WakeModeResult{}, ErrNoReceipt
	default:
		return WakeModeResult{}, fmt.Errorf("friend wake mode %s: %s", f, status)
	}
}
