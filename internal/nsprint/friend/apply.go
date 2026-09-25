package friend

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// ActorLife is the actor of every classifier write; ns_friend_state requires
// the read state and idem (args 7-8) from it and re-checks them atomically.
const ActorLife = "life"

// Apply statuses beside ns_friend_state's OK, DUP, HELD and STALE.
const (
	// ApplyPending: a delivery is inside its 120 s bound; nothing written.
	ApplyPending = "PENDING"
	// ApplyNothing: UP over a state the classifier does not own (absent,
	// idle or underfull); nothing to clear, no FCALL.
	ApplyNothing = "NOTHING"
)

// ApplyResult is one ApplyLife: the class, what was sent (State: the
// friend:<f>:state value asked for, "clear" for UP) and the answer.
type ApplyResult struct {
	Friend string
	Class  string
	State  string
	Status string
	// Values is ns_friend_state's whole reply when an FCALL was made.
	Values []string
}

// Line is the sweep's line for one applied classification.
func (r ApplyResult) Line() string {
	return fmt.Sprintf("LIFE friend=%s class=%s state=%s status=%s", r.Friend, r.Class, dash(r.State), r.Status)
}

// lifeState maps a class to the friend:<f>:state value the classifier asks
// for; "" means no write (WAKE-PENDING).
func lifeState(class string) string {
	switch class {
	case life.ClassDown:
		return StateDown
	case life.ClassOutOfCredits:
		return StateOutOfCredits
	case life.ClassOfflineModel:
		return StateOfflineModel
	case life.ClassWakeMissed:
		return StateWakeMissed
	case life.ClassUp:
		return "clear"
	}
	return ""
}

// ApplyLife classifies friend f at now and writes the result through the one
// writer, ns_friend_state (ReportIf), conditionally on what it read. One
// pipelined round trip reads friend:<f>:events (the newest
// life.EventsReadBound entries) and the stored state and idem; hook, nil in
// production, runs between that read and the FCALL (the test seam for an
// interleaved report). The classifier may overwrite an absent, idle or
// underfull state or its own (idem life:<f>:...); any other state is a
// report and answers HELD with no FCALL, and the writer re-checks both the
// read and the ownership itself. WAKE-PENDING writes nothing (PENDING).
func ApplyLife(ctx context.Context, st *store.Store, f string, now time.Time, hook func()) (ApplyResult, error) {
	if st == nil || f == "" {
		return ApplyResult{}, fmt.Errorf("friend apply life: store and friend are required")
	}
	pipe := st.Client().Pipeline()
	evCmd := pipe.XRevRangeN(ctx, EventsKey(f), "+", "-", life.EventsReadBound)
	stCmd := pipe.HMGet(ctx, StateKey(f), "state", "idem")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return ApplyResult{}, fmt.Errorf("friend apply life %s: read: %w", f, err)
	}
	msgs := evCmd.Val()
	entries := make([]life.StreamEntry, len(msgs))
	for i, m := range msgs {
		vals := map[string]string{}
		for k, v := range m.Values {
			vals[k] = fmt.Sprint(v)
		}
		entries[len(msgs)-1-i] = life.StreamEntry{ID: m.ID, Values: vals}
	}
	class := life.Classify(life.ParseEvents(entries), now)
	stored, storedIdem := StateUp, ""
	if vals := stCmd.Val(); len(vals) == 2 {
		if s, ok := vals[0].(string); ok && s != "" {
			stored = s
		}
		if s, ok := vals[1].(string); ok {
			storedIdem = s
		}
	}
	if hook != nil {
		hook()
	}
	res := ApplyResult{Friend: f, Class: class.State, State: lifeState(class.State)}
	if res.State == "" {
		res.Status = ApplyPending
		return res, nil
	}
	ladder := stored == StateUp || stored == StateIdle || stored == StateUnderfull
	owned := strings.HasPrefix(storedIdem, "life:"+f+":")
	if !ladder && !owned {
		res.Status = "HELD"
		return res, nil
	}
	if res.State == "clear" && !owned {
		res.Status = ApplyNothing
		return res, nil
	}
	req := ReportRequest{
		Friend: f, State: res.State, Reason: "life: " + class.State,
		Actor: ActorLife, Idem: "life:" + f + ":" + dash(class.Basis) + ":" + res.State,
		IfState: stored, IfIdem: storedIdem,
	}
	if class.State == life.ClassOutOfCredits {
		req.Until = time.UnixMilli(class.Until)
	}
	values, err := ReportIf(ctx, st, req)
	if err != nil {
		return res, err
	}
	res.Status, res.Values = values[0], values
	return res, nil
}
