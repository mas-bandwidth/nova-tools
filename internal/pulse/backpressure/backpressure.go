// Package backpressure is the reading-debt brake as one key on the fleet store:
// `bp`, written by the counter, read by the dealer, expired by the counter's own
// death.
//
// bin/backpressure (bash-to-verb item 24, nova-tools next-sprint report
// 2026-09-22) counted the fleet's reading debt and marked it in
// queue/control/BACKPRESSURE, and that file was the whole control. A file is one
// machine's local truth: the dealer on another bench cannot read it, a counter
// that dies leaves it lying at its last word forever, and a control that says
// one thing everywhere and cannot expire is not a control. The pipeline's own
// cut of the same idea (#2509) kept the file and only clamped the dealer to one
// card per bench -- bulk and priority slowed together, which is the debt
// punishing the work that pays it down.
//
// The verb form is one string key on the fleet Redis every bench already
// reaches (the client comes from internal/pulse's DialStore; this package takes
// it ready-dialled so the library never owns the connection):
//
//   - the counter beats: it counts the pending read requests, and every beat
//     writes the key -- `state=ON` over the cap, `state=OFF` under it -- with a
//     TTL. OFF is written, never removed: only the counter's death may make the
//     key vanish, because a vanished key and a written OFF must never read the
//     same.
//   - the dealer consults once per tick: GET bp, bounded at Budget. An absent
//     key is the counter dead, and expiry is ON -- the dealer fails closed,
//     because the one state it may not guess is "nobody is minding the debt, so
//     deal as if it were paid".
//   - the gate is by class, not by clamp: a card that names no priority is BULK
//     and is held while the key reads ON; a card with a `PRIORITY: <why>` line
//     (the same shape as the LANE: line fill already reads) still flows, so the
//     fleet keeps its emergency lane under backpressure. #2517 wires this into
//     fillTick.
package backpressure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Key is the fleet-store key the whole control lives on: `bp`, one string,
	// written by the counter and read by every dealer that shares the store.
	Key = "bp"

	// Cap is the default reading-debt cap: ten pending reads. The pipeline's
	// own control (#2509) used the same number; the cap is a default and not a
	// law, because a fleet that grows its friends grows its readable debt.
	Cap = 10

	// Budget bounds the dealer's whole consult at one second: a tick asks once,
	// and whatever the store answers -- or fails to answer -- the answer is
	// folded in within the second, so a dealer never spends its tick waiting on
	// a brake and never deals bulk past a store it could not ask in time.
	Budget = time.Second

	// Lease is the default TTL the counter writes under: three beats of a
	// one-minute counter. The lease is what turns a dead counter into an
	// expired key rather than a stale file: the key outlives a missed beat, and
	// a counter that is gone takes its last word with it.
	Lease = 3 * time.Minute
)

// The reasons a state reads the way it does. Reason key is the ordinary case:
// the key said it. Everything else is the fail-closed fold -- expired, a key
// the counter's death took away; unreadable, a store that could not be asked;
// unknown, a value the reader does not recognise.
const (
	ReasonKey        = "key"
	ReasonExpired    = "expired"
	ReasonUnreadable = "unreadable"
	ReasonUnknown    = "unknown"
)

// State is what the dealer learns from one consult: whether backpressure is on,
// the debt and the cap that decided it, the counter's stamp, and why the state
// reads the way it does when the key itself did not say.
type State struct {
	On      bool
	Pending int
	Cap     int
	At      time.Time
	Reason  string
}

// Line is the state as one scannable line for the dealer to print beside its
// own FILL lines: the key's fields, the word BACKPRESSURE in front, and the
// reason the state reads the way it does.
func (s State) Line() string {
	return "BACKPRESSURE " + s.fields() + " reason=" + s.Reason
}

// fields is the state as the key's own `k=v` fields: state, the debt, the cap
// and the counter's stamp, in that order, with no prefix and no reason -- the
// reason is what the reader learned, not what the counter wrote.
func (s State) fields() string {
	var b strings.Builder
	b.WriteString("state=")
	if s.On {
		b.WriteString("ON")
	} else {
		b.WriteString("OFF")
	}
	b.WriteString(" pending=")
	b.WriteString(strconv.Itoa(s.Pending))
	b.WriteString(" cap=")
	b.WriteString(strconv.Itoa(s.Cap))
	b.WriteString(" at=")
	b.WriteString(s.At.UTC().Format(time.RFC3339))
	return b.String()
}

// Debt counts the reading debt: the pending read requests under the reads
// directory, one `*.json` file per request, counted while its status is
// "pending" (the request shape the pipeline writes, #2509). A reads directory
// that is not there is zero debt -- no read has landed, and an empty queue is
// not backpressure -- while an empty NAME is a refusal, because a counter that
// guesses a directory counts a debt nobody owes. A file that is not a request
// is stepped over, not fatal: one stranger file must not stop the counter.
func Debt(reads string) (int, error) {
	dir := strings.TrimSpace(reads)
	if dir == "" {
		return 0, errors.New("no reads directory; pass the queue's reads/ where the pending read requests live")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0, nil
	}
	pending := 0
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var req struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &req) == nil && req.Status == "pending" {
			pending++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("read requests under %s: %w", dir, err)
	}
	return pending, nil
}

// Beat is one beat of the counter: it compares the debt to the cap and writes
// the key -- state=ON over the cap, state=OFF under or at it -- under a TTL
// lease. The OFF beat is written and not removed, so the key can only vanish by
// the lease lapsing: a counter that is alive says its word every beat, and a
// counter that has died says nothing, which the dealer reads as ON. cap 0 takes
// Cap, ttl 0 takes Lease, and a zero now takes the clock: the counter's loop
// passes its own clock so a test drives a whole lease without waiting one.
func Beat(ctx context.Context, store *redis.Client, debt, cap int, ttl time.Duration, now time.Time) (State, error) {
	if store == nil {
		return State{}, errors.New("no store; the counter dials the fleet store once and passes the client to every beat")
	}
	if cap <= 0 {
		cap = Cap
	}
	if ttl <= 0 {
		ttl = Lease
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	st := State{
		On:      debt > cap,
		Pending: debt,
		Cap:     cap,
		At:      now.UTC(),
		Reason:  ReasonKey,
	}
	value := st.fields()
	if err := store.Set(ctx, Key, value, ttl).Err(); err != nil {
		return st, fmt.Errorf("fleet store: %w", err)
	}
	return st, nil
}

// Consult is the dealer's one ask of the brake: GET bp, answered within Budget
// whatever the store does. An absent key is the counter dead -- the lease took
// the key -- and expiry is ON. A store that cannot be asked in the budget is ON
// too, with the error named beside it: the dealer fails closed, and the one
// state it never answers is a guess. The state it returns is always safe to act
// on; the error, when it is not nil, is the reason to print, never a licence to
// deal.
//
// THE BOUND IS THIS PACKAGE'S, NOT THE CLIENT'S. go-redis carries a context to
// the socket only when the client was built with ContextTimeoutEnabled, and the
// fleet's DialStore builds its client without it: a context deadline alone
// would let a stalled store hold the tick until the client's own read timeout.
// So the ask runs beside the clock and whichever answers first wins, and the
// abandoned ask is left to the client's read timeout, which bounds it: the
// dealer pays at most one second on a store that will not speak.
func Consult(ctx context.Context, store *redis.Client) (State, error) {
	if store == nil {
		return closed(ReasonUnreadable), errors.New("no store; the dealer dials the fleet store once and passes the client to every consult")
	}
	if Budget > 0 {
		c, cancel := context.WithTimeout(ctx, Budget)
		defer cancel()
		ctx = c
	}
	type reply struct {
		st  State
		err error
	}
	ch := make(chan reply, 1)
	go func() {
		v, err := store.Get(ctx, Key).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				ch <- reply{closed(ReasonExpired), nil}
				return
			}
			ch <- reply{closed(ReasonUnreadable), fmt.Errorf("fleet store: %w", err)}
			return
		}
		ch <- reply{parse(v), nil}
	}()
	select {
	case r := <-ch:
		return r.st, r.err
	case <-ctx.Done():
		return closed(ReasonUnreadable), fmt.Errorf("fleet store: %w", ctx.Err())
	}
}

// closed is the fail-closed state: backpressure ON, the debt unknown, the
// reason naming what was read instead of the key.
func closed(reason string) State {
	return State{On: true, Reason: reason}
}

// parse reads the key's value into a state. A value whose state= is neither ON
// nor OFF -- a value a future counter wrote and this reader was never given --
// is ON under reason unknown: the dealer does not act on a word it does not
// know. Every other field is carried when it parses and left at zero when it
// does not; the verdict never rests on them.
func parse(v string) State {
	st := closed(ReasonUnknown)
	for _, tok := range strings.Fields(v) {
		k, val, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch k {
		case "state":
			switch strings.ToUpper(strings.TrimSpace(val)) {
			case "ON":
				st.On, st.Reason = true, ReasonKey
			case "OFF":
				st.On, st.Reason = false, ReasonKey
			}
		case "pending":
			st.Pending, _ = strconv.Atoi(val)
		case "cap":
			st.Cap, _ = strconv.Atoi(val)
		case "at":
			if t, terr := time.Parse(time.RFC3339, val); terr == nil {
				st.At = t
			}
		}
	}
	return st
}

// Priority says whether a card names itself a priority card: a `PRIORITY: <why>`
// line, the same one-line shape as the LANE: line fill already reads, with the
// why free-form because the reason is for the person, not the tool. Only the
// exact field prefix counts, a PRIORITY line with nothing after the colon names
// no priority, and a card that cannot be read is not special: a dealer that
// cannot ask a card what it is does not promote it past a brake it cannot read.
func Priority(card string) bool {
	raw, err := os.ReadFile(card)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PRIORITY:"); ok {
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	return false
}

// Holds says whether the dealer holds this card under this state: a bulk card
// is held while backpressure is on, and a priority card flows whatever the
// debt, because the emergency lane is the part of the fleet that clears the
// brake's own cause. A state that is off holds nothing.
func Holds(st State, card string) bool {
	return st.On && !Priority(card)
}
