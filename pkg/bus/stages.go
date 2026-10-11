package bus

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// StagePrefix is the hash of a recipient's receipts: one field per message
// id, its value the state and the store's time it was reached, in Unix
// seconds ("read 1791288000"); written only by Store.Forward (SPEC-BUS.md,
// message-receipts; tla/Bus2Receipts.tla), by the receipt rule the store's
// script keeps (redis.go, forwardLua) and each fake keeps beside it: a
// receipt moves only forward, and only delivered starts one.
const StagePrefix = "bus2:receipt:"

// StagesOf is the recipient's receipts hash.
func StagesOf(name string) string { return StagePrefix + name }

// The states of a receipt, in the one order it moves: delivered (the
// recipient's reader took the message off its stream), read (the turn
// carrying it started), acted (the turn ended at exit 0, or the recipient
// sent a message naming it by re).
const (
	Delivered = "delivered"
	Read      = "read"
	Acted     = "acted"
)

// StageStates is every state, in order.
var StageStates = []string{Delivered, Read, Acted}

// rank is the state's place in StageStates from 1, 0 for none.
func rank(state string) int { return slices.Index(StageStates, state) + 1 }

// Stage is one message's receipt: its state ("" none) and when the store
// reached it.
type Stage struct {
	ID    string
	State string
	At    time.Time
}

// Age is how long the receipt has stood at now; zero with none.
func (s Stage) Age(now time.Time) time.Duration {
	if s.State == "" {
		return 0
	}
	return now.Sub(s.At)
}

// parseStage is the receipt of id that the value v holds.
func parseStage(id, v string) Stage {
	state, secs, _ := strings.Cut(v, " ")
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return Stage{ID: id, State: state} // a stamp that is no number reads as no time, never a refusal
	}
	return Stage{ID: id, State: state, At: time.Unix(n, 0).UTC()}
}

// Stamp moves the receipts of ids on as's hash to state, each only forward
// (Forward), at the store's time, in one trip, and answers each id's state
// before it ("" none). It is the daemon's word that a turn started (read) or
// ended at exit 0 (acted); recv stamps delivered, and a send naming a message
// (re) stamps it acted in its own transaction (owe).
func (b *Bus) Stamp(ctx context.Context, as, state string, ids ...string) ([]string, error) {
	var problems []string
	if p := CheckName(as); p != "" {
		problems = append(problems, p)
	}
	if rank(state) == 0 {
		problems = append(problems, fmt.Sprintf("the receipt state %q is not one of %s", state, strings.Join(StageStates, ", ")))
	}
	if len(problems) > 0 {
		return nil, &Refusal{problems}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return b.Store.Forward(ctx, StagesOf(as), state, ids...)
}

// stamp is a stamp the bus makes on its own beside a verb (recv's
// delivered): one that fails goes to OnStampError and never fails the verb,
// and the message shows in Overdue until a later stamp lands.
func (b *Bus) stamp(ctx context.Context, as, state string, id string) string {
	prior, err := b.Stamp(ctx, as, state, id)
	if err != nil {
		if b.OnStampError != nil {
			b.OnStampError(fmt.Errorf("receipt %s of %s for %s not written: %w", state, id, as, err))
		}
		return ""
	}
	return prior[0]
}

// Stages is as's receipts and the store's time, in two trips: those of ids in
// their order (State "" for one with none), or with no ids every receipt as
// holds, oldest first.
func (b *Bus) Stages(ctx context.Context, as string, ids ...string) ([]Stage, time.Time, error) {
	if p := CheckName(as); p != "" {
		return nil, time.Time{}, &Refusal{[]string{p}}
	}
	_, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	hashes, err := b.Store.Marks(ctx, StagesOf(as))
	if err != nil {
		return nil, time.Time{}, err
	}
	h := hashes[0]
	if len(ids) == 0 {
		for id := range h {
			ids = append(ids, id)
		}
		slices.Sort(ids) // a ULID sorts by the store's time
	}
	out := make([]Stage, len(ids))
	for i, id := range ids {
		out[i] = Stage{ID: id}
		if v, ok := h[id]; ok {
			out[i] = parseStage(id, v)
		}
	}
	return out, now, nil
}

// Late is a message still short of delivered: on name's stream, new (never
// delivered) or pending with no receipt, and how long since it was sent.
type Late struct {
	Name, ID, From, Subject, State string
	At                             time.Time
	Age                            time.Duration
}

// Overdue is every message on every known name's stream still short of
// delivered older than older, oldest first, and the store's time: the
// alarm the coordinator's loop runs. A message on a stream is short of
// delivered when it is new, or pending with no receipt (its reader's stamp
// did not land); one acked is never listed. One trip for the roster, a peek
// of each stream, and one for every receipts hash.
func (b *Bus) Overdue(ctx context.Context, older time.Duration) ([]Late, time.Time, error) {
	names, now, err := b.Store.Roster(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	slices.Sort(names)
	names = slices.Compact(names)
	keys := make([]string, len(names))
	waiting := make([][]Late, len(names))
	for i, n := range names {
		keys[i] = StagesOf(n)
		pending, fresh, err := b.Peek(ctx, n)
		if err != nil {
			return nil, time.Time{}, err
		}
		for _, s := range []struct {
			state string
			es    []Entry
		}{{"pending", pending}, {"new", fresh}} {
			for _, e := range s.es {
				m := e.Message()
				waiting[i] = append(waiting[i], Late{Name: n, ID: m.ID, From: m.From, Subject: m.Subject, State: s.state, At: m.At, Age: now.Sub(m.At)})
			}
		}
	}
	hashes, err := b.Store.Marks(ctx, keys...)
	if err != nil {
		return nil, time.Time{}, err
	}
	var late []Late
	for i := range names {
		for _, l := range waiting[i] {
			if _, stamped := hashes[i][l.ID]; !stamped && l.Age >= older {
				late = append(late, l)
			}
		}
	}
	slices.SortStableFunc(late, func(a, b Late) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return late, now, nil
}
