package stream

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The work order gate (nova-tools #4322; Stella's read of #4410 at
// 1b3c3cb73): a landing's members are a prefix of each stream's COMPLETE
// computed live order (ws.ReadOrders: waiting, ready, working, review and
// merging, not the merging set alone), so a later card never passes an
// unfinished predecessor. Two lines, never one for both:
//
//	ORDER WAIT stream=<s> before=<id> where=<set>[:<skip why>] held=<ids>
//	ORDER CONFLICT stream=<s> why=cycle cycle=<a -> b -> a> held=<ids> escalate=coordinator
//	ORDER CONFLICT stream=<s> why=never-finishes before=<id> where=<set> dep=<id> dep_where=done/fail held=<ids> escalate=coordinator
//
// WAIT is ordinary: the predecessor is live and can still finish. CONFLICT
// is an order that cannot be met (a DEPENDS-ON cycle, or a predecessor
// that can no longer be satisfied: NeverFinishes): the coordinator's call.
// Neither reorders anything.
const (
	OrderWait     = "ORDER WAIT"
	OrderConflict = "ORDER CONFLICT"
)

// OrderHold is one stream's gate line: the members it holds back and why.
type OrderHold struct {
	Kind          string // OrderWait or OrderConflict
	Stream        string
	Why           string // CONFLICT: cycle | never-finishes
	Before, Where string // the first unfinished predecessor and its live set
	Cycle         string // why=cycle: the cycle ws.Order names
	Dep, DepWhere string // why=never-finishes: the edge that is never met
	Held          []string
}

// Line is the hold as the verbs print it.
func (h OrderHold) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s stream=%s", h.Kind, strconv.Quote(h.Stream))
	if h.Kind == OrderConflict {
		fmt.Fprintf(&b, " why=%s", h.Why)
	}
	if h.Cycle != "" {
		fmt.Fprintf(&b, " cycle=%s", strconv.Quote(h.Cycle))
	}
	if h.Before != "" {
		fmt.Fprintf(&b, " before=%s where=%s", h.Before, h.Where)
	}
	if h.Dep != "" {
		fmt.Fprintf(&b, " dep=%s dep_where=%s", h.Dep, h.DepWhere)
	}
	fmt.Fprintf(&b, " held=%s", strings.Join(h.Held, ","))
	if h.Kind == OrderConflict {
		b.WriteString(" escalate=coordinator")
	}
	return b.String()
}

// OrderGate keeps the members that are a prefix of their stream's live
// order and holds back the rest: each held member is a Skip (why
// order-wait:<before> or order-conflict:<why>) and each stream that holds
// any has one OrderHold. skips is Members' own (a merging predecessor's
// skip why is printed in where=). Three pipelined round trips at most
// (ReadOrders' two, then the blockers' edges).
func OrderGate(ctx context.Context, c redis.Cmdable, streams []string, members []Member, skips []Skip) ([]Member, []Skip, []OrderHold, error) {
	if len(members) == 0 {
		return members, nil, nil, nil
	}
	orders, err := ws.ReadOrders(ctx, c, streams)
	if err != nil {
		return nil, nil, nil, err
	}
	skipWhy := map[string]string{}
	for _, s := range skips {
		skipWhy[s.Task] = s.Why
	}
	sel := map[string]map[string]bool{}
	for _, m := range members {
		if sel[m.Stream] == nil {
			sel[m.Stream] = map[string]bool{}
		}
		sel[m.Stream][m.Task] = true
	}
	held := map[string]string{} // task -> skip why
	var holds []OrderHold
	for i, so := range orders {
		s := streams[i]
		if len(sel[s]) == 0 {
			continue
		}
		var ce *ws.CycleError
		if so.Err != nil {
			if !errors.As(so.Err, &ce) {
				return nil, nil, nil, so.Err
			}
			h := OrderHold{Kind: OrderConflict, Stream: s, Why: "cycle", Cycle: ce.Error()}
			for _, m := range members {
				if m.Stream == s {
					h.Held = append(h.Held, m.Task)
					held[m.Task] = "order-conflict:cycle"
				}
			}
			holds = append(holds, h)
			continue
		}
		before := ""
		var h OrderHold
		for _, o := range so.Order {
			switch {
			case o.Sentinel:
			case sel[s][o.ID]:
				if before != "" {
					h.Held = append(h.Held, o.ID)
				}
			case before == "":
				before = o.ID
			}
		}
		if len(h.Held) == 0 {
			continue
		}
		h.Kind, h.Stream, h.Before, h.Where = OrderWait, s, before, so.Where[before]
		if w := skipWhy[before]; w != "" {
			h.Where += ":" + w
		}
		holds = append(holds, h)
	}
	if err := neverFinishes(ctx, c, holds); err != nil {
		return nil, nil, nil, err
	}
	for _, h := range holds {
		why := "order-wait:" + h.Before
		if h.Kind == OrderConflict {
			why = "order-conflict:" + h.Why
		}
		for _, id := range h.Held {
			if held[id] == "" {
				held[id] = why
			}
		}
	}
	var kept []Member
	var out []Skip
	for _, m := range members {
		if why := held[m.Task]; why != "" {
			out = append(out, Skip{Task: m.Task, N: m.N, Why: why})
			continue
		}
		kept = append(kept, m)
	}
	return kept, out, holds, nil
}

// DepRec is one record's fields the never-finishes walk reads.
type DepRec struct {
	Where, OK, State string
	Deps             []string // its DEPENDS-ON ids (DepID, a card's bare label as its sprint's card id)
}

// Retired is a record that can no longer be satisfied: it ended done/fail
// (retired fail, or cancelled, which is done/fail with state cancelled) and
// holds no live place, so nothing will finish it under that id. done/ok
// and landed are satisfied; done/abstain may still become done/ok.
func (r DepRec) Retired() bool {
	return r.Where == ws.Done && (r.OK == "fail" || r.State == "cancelled")
}

// satisfied is a record whose edge is met: landed, or done and not retired.
func (r DepRec) satisfied() bool {
	return r.Where == ws.Landed || (r.Where == ws.Done && !r.Retired())
}

// NeverFinishes is the predecessor start's never-finishes test: the first
// record, start itself or one reached through DEPENDS-ON edges that are not
// yet met, that is Retired ("" when none). read returns the records of one
// level of the walk (one pipelined round trip each); a satisfied record's
// own edges are not walked.
func NeverFinishes(start string, read func(ids []string) (map[string]DepRec, error)) (string, error) {
	seen := map[string]bool{start: true}
	level := []string{start}
	for len(level) > 0 {
		recs, err := read(level)
		if err != nil {
			return "", err
		}
		var next []string
		for _, id := range level {
			r := recs[id]
			if r.Retired() {
				return id, nil
			}
			if r.satisfied() {
				continue
			}
			for _, d := range r.Deps {
				if !seen[d] {
					seen[d] = true
					next = append(next, d)
				}
			}
		}
		level = next
	}
	return "", nil
}

// readDepRecs is NeverFinishes' read on the store: one pipeline per level.
func readDepRecs(ctx context.Context, c redis.Cmdable) func([]string) (map[string]DepRec, error) {
	return func(ids []string) (map[string]DepRec, error) {
		pipe := c.Pipeline()
		cmds := make([]*redis.SliceCmd, len(ids))
		for i, id := range ids {
			cmds[i] = pipe.HMGet(ctx, ws.RecordKey(id), "where", "where_ok", "state", "blocked_on", "depends_on")
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		out := make(map[string]DepRec, len(ids))
		for i, id := range ids {
			v := cmds[i].Val()
			str := func(k int) string { s, _ := v[k].(string); return s }
			r := DepRec{Where: str(0), OK: str(1), State: str(2)}
			text := str(3)
			if text == "" {
				text = str(4)
			}
			sprint, _, isCard := strings.Cut(strings.TrimPrefix(id, "s:"), ":card:")
			isCard = isCard && strings.HasPrefix(id, "s:")
			for _, raw := range ws.SplitDeps(text) {
				d := ws.DepID(raw)
				if d == "" {
					continue
				}
				if isCard && !strings.HasPrefix(d, "s:") {
					d = "s:" + sprint + ":card:" + d
				}
				r.Deps = append(r.Deps, d)
			}
			out[id] = r
		}
		return out, nil
	}
}

// neverFinishes turns a WAIT whose predecessor can never finish into a
// CONFLICT (NeverFinishes on the store).
func neverFinishes(ctx context.Context, c redis.Cmdable, holds []OrderHold) error {
	read := readDepRecs(ctx, c)
	for i := range holds {
		h := &holds[i]
		if h.Kind != OrderWait {
			continue
		}
		dep, err := NeverFinishes(h.Before, read)
		if err != nil {
			return err
		}
		if dep != "" {
			h.Kind, h.Why, h.Dep, h.DepWhere = OrderConflict, "never-finishes", dep, "done/fail"
		}
	}
	return nil
}

// orderRefusal is the gate's refusal at land merge: the landing is no
// longer a prefix of the live order.
func orderRefusal(h OrderHold) *Refusal {
	if h.Kind == OrderConflict {
		return &Refusal{Why: h.Line(), Remedy: "the coordinator resolves the order (the cycle, or the predecessor that cannot finish); nothing is merged or reordered"}
	}
	return &Refusal{Why: h.Line(), Remedy: "finish " + h.Before + " first (it is ahead in the work order), then land stream again"}
}
