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
//	ORDER CONFLICT stream=<s> why=sequence-changed was=<a,b> now=<b,a> escalate=coordinator (land merge: MergeGate)
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
	Why           string   // CONFLICT: cycle | never-finishes | sequence-changed
	Before, Where string   // the first unfinished predecessor and its live set
	Cycle         string   // why=cycle: the cycle ws.Order names
	Dep, DepWhere string   // why=never-finishes: the edge that is never met
	Was, Now      []string // why=sequence-changed: the recorded and the current sequence
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
	if h.Why == "sequence-changed" {
		fmt.Fprintf(&b, " was=%s now=%s", strings.Join(h.Was, ","), strings.Join(h.Now, ","))
	} else {
		fmt.Fprintf(&b, " held=%s", strings.Join(h.Held, ","))
	}
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
	return orderGate(ctx, c, streams, orders, members, skips)
}

// orderGate is OrderGate on orders already read. The kept members come
// back as the computed SEQUENCE: streams in the order given, each stream's
// members in its computed order (never the input slice's order), so the
// build merges them, and the landing records them, in that sequence.
func orderGate(ctx context.Context, c redis.Cmdable, streams []string, orders []ws.StreamOrder, members []Member, skips []Skip) ([]Member, []Skip, []OrderHold, error) {
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
	type pending struct {
		stream        string
		so            ws.StreamOrder
		prefix, after []string
		before        string
	}
	var pend []pending
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
		// the prefix: the selected members before the first unselected
		// live card (before); every selected member after it is held
		var prefix, after []string
		before := ""
		for _, o := range so.Order {
			switch {
			case o.Sentinel:
			case sel[s][o.ID] && before == "":
				prefix = append(prefix, o.ID)
			case sel[s][o.ID]:
				after = append(after, o.ID)
			case before == "":
				before = o.ID
			}
		}
		pend = append(pend, pending{stream: s, so: so, prefix: prefix, after: after, before: before})
	}
	// never-finishes, walked from every prefix member and every blocker at
	// once: a member whose own DEPENDS-ON can no longer be met is held with
	// everything after it, as a predecessor that cannot finish is
	var starts []string
	for _, p := range pend {
		starts = append(starts, p.prefix...)
		if p.before != "" && len(p.after) > 0 {
			starts = append(starts, p.before)
		}
	}
	nf, err := NeverFinishesAll(starts, readDepRecs(ctx, c))
	if err != nil {
		return nil, nil, nil, err
	}
	for _, p := range pend {
		h := OrderHold{Stream: p.stream}
		for k, id := range p.prefix {
			if nf[id] != "" {
				h.Kind, h.Why, h.Before, h.Where, h.Dep, h.DepWhere = OrderConflict, "never-finishes", id, p.so.Where[id], nf[id], "done/fail"
				h.Held = append(append([]string(nil), p.prefix[k:]...), p.after...)
				break
			}
		}
		if h.Kind == "" && len(p.after) > 0 {
			h.Kind, h.Before, h.Where, h.Held = OrderWait, p.before, p.so.Where[p.before], p.after
			if w := skipWhy[p.before]; w != "" {
				h.Where += ":" + w
			}
			if nf[p.before] != "" {
				h.Kind, h.Why, h.Dep, h.DepWhere = OrderConflict, "never-finishes", nf[p.before], "done/fail"
			}
		}
		if h.Kind != "" {
			holds = append(holds, h)
		}
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
	var out []Skip
	byID := map[string]Member{}
	for _, m := range members {
		if why := held[m.Task]; why != "" {
			out = append(out, Skip{Task: m.Task, N: m.N, Why: why})
			continue
		}
		byID[m.Stream+"\x00"+m.Task] = m
	}
	var kept []Member
	for i, so := range orders {
		for _, o := range so.Order {
			k := streams[i] + "\x00" + o.ID
			if m, ok := byID[k]; ok {
				kept = append(kept, m)
				delete(byID, k)
			}
		}
	}
	for _, m := range members { // a member no live order ranks (none today) keeps its place after them
		if _, ok := byID[m.Stream+"\x00"+m.Task]; ok {
			kept = append(kept, m)
		}
	}
	return kept, out, holds, nil
}

// MergeGate is land merge's check immediately before the merge: the
// landing's recorded SEQUENCE (its members in the order the build merged
// them, the landing record's tasks field) against each stream's current
// complete computed order, one read. The selected members' relative order
// must be the same, element by element; any difference is
// `ORDER CONFLICT stream=<s> why=sequence-changed was=<a,b> now=<b,a>
// escalate=coordinator` (the branch was built in the old sequence: rebuild
// or review, never merge it). Then the unselected-predecessor check
// (orderGate): ORDER WAIT or ORDER CONFLICT. nil when the landing may merge.
func MergeGate(ctx context.Context, c redis.Cmdable, streams []string, recorded []Member) (*OrderHold, error) {
	if len(recorded) == 0 {
		return nil, nil
	}
	orders, err := ws.ReadOrders(ctx, c, streams)
	if err != nil {
		return nil, err
	}
	for i, so := range orders {
		var was []string
		in := map[string]bool{}
		for _, m := range recorded {
			if m.Stream == streams[i] {
				was = append(was, m.Task)
				in[m.Task] = true
			}
		}
		if len(was) == 0 || so.Err != nil {
			continue // a cycle is orderGate's CONFLICT below
		}
		var now []string
		for _, o := range so.Order {
			if in[o.ID] {
				now = append(now, o.ID)
			}
		}
		if strings.Join(now, ",") != strings.Join(was, ",") {
			return &OrderHold{Kind: OrderConflict, Stream: streams[i], Why: "sequence-changed", Was: was, Now: now}, nil
		}
	}
	_, _, holds, err := orderGate(ctx, c, streams, orders, recorded, nil)
	if err != nil || len(holds) == 0 {
		return nil, err
	}
	return &holds[0], nil
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
	out, err := NeverFinishesAll([]string{start}, read)
	return out[start], err
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

// NeverFinishesAll is NeverFinishes for many starts at once, one read per
// level of the joint walk (each record read once): start -> the retired
// record it reaches ("" when none).
func NeverFinishesAll(starts []string, read func(ids []string) (map[string]DepRec, error)) (map[string]string, error) {
	out := map[string]string{}
	cache := map[string]DepRec{}
	type walk struct {
		start string
		seen  map[string]bool
		level []string
	}
	var ws []*walk
	for _, st := range starts {
		if _, dup := out[st]; dup {
			continue
		}
		out[st] = ""
		ws = append(ws, &walk{start: st, seen: map[string]bool{st: true}, level: []string{st}})
	}
	for {
		var need []string
		asked := map[string]bool{}
		for _, w := range ws {
			for _, id := range w.level {
				if _, ok := cache[id]; !ok && !asked[id] {
					asked[id] = true
					need = append(need, id)
				}
			}
		}
		if len(need) > 0 {
			recs, err := read(need)
			if err != nil {
				return nil, err
			}
			for _, id := range need {
				cache[id] = recs[id]
			}
		}
		live := ws[:0]
		for _, w := range ws {
			var next []string
			done := false
			for _, id := range w.level {
				r := cache[id]
				if r.Retired() {
					out[w.start], done = id, true
					break
				}
				if r.satisfied() {
					continue
				}
				for _, d := range r.Deps {
					if !w.seen[d] {
						w.seen[d] = true
						next = append(next, d)
					}
				}
			}
			if !done && len(next) > 0 {
				w.level = next
				live = append(live, w)
			}
		}
		ws = live
		if len(ws) == 0 {
			return out, nil
		}
	}
}

// orderRefusal is the gate's refusal at land merge: the landing is no
// longer a prefix of the live order.
func orderRefusal(h OrderHold) *Refusal {
	if h.Why == "sequence-changed" {
		return &Refusal{Why: h.Line(), Remedy: "the stream branch was built in the old sequence: land stream again to rebuild it in the current order, or the coordinator reviews the order change; nothing is merged"}
	}
	if h.Kind == OrderConflict {
		return &Refusal{Why: h.Line(), Remedy: "the coordinator resolves the order (the cycle, or the predecessor that cannot finish); nothing is merged or reordered"}
	}
	return &Refusal{Why: h.Line(), Remedy: "finish " + h.Before + " first (it is ahead in the work order), then land stream again"}
}

// StreamLandGate is `card land --stream` and `task card land --stream`'s
// check before ns_tcard_land_stream moves anyone: the stream's merging
// members (the tasks the call would land) must be a prefix of the stream's
// whole live order, none of them unable to finish (orderGate). The first
// hold, or nil. Nothing is written.
func StreamLandGate(ctx context.Context, c redis.Cmdable, stream string) (*OrderHold, error) {
	epoch, err := ws.Epoch(ctx, c)
	if err != nil {
		return nil, err
	}
	ids, err := c.ZRange(ctx, WSKeyAt(epoch, stream, "merging"), 0, -1).Result()
	if err != nil {
		return nil, err
	}
	var members []Member
	for _, id := range ids {
		if !strings.HasPrefix(id, "s:") { // the call lands tasks; a card keeps its own door
			members = append(members, Member{Task: id, Stream: stream})
		}
	}
	if len(members) == 0 {
		return nil, nil
	}
	_, _, holds, err := OrderGate(ctx, c, []string{stream}, members, nil)
	if err != nil || len(holds) == 0 {
		return nil, err
	}
	return &holds[0], nil
}

// MemberHeadGate is `land pr <n>`'s check immediately before its merge: a
// PR whose record makes it a stream MEMBER (a stream and a task, kind not
// stream) merges only when its task is the head of the stream's current
// live order and can finish; the stream PR itself (kind=stream) and a PR
// with no stream pass as before. The member's task ("" when the PR is no
// member) and the hold (ORDER WAIT before=<head>, or ORDER CONFLICT), or
// nil. The store runs the same check again in the landing's own call
// (cm_order_head) and land pr claims the slot first (taskcard.LandClaim).
func MemberHeadGate(ctx context.Context, c redis.Cmdable, repo string, n int) (string, *OrderHold, error) {
	recs, err := LoadPRs(ctx, c, repo, []int{n})
	if err != nil || len(recs) == 0 {
		return "", nil, err
	}
	r := recs[0]
	if !r.Exists || r.Kind == "stream" || r.Stream == "" || r.Task == "" {
		return "", nil, nil
	}
	h, err := TaskHeadGate(ctx, c, r.Stream, r.Task, n)
	return r.Task, h, err
}

// TaskHeadGate is `task land --id <task>`'s check before taskcard.Land (and
// MemberHeadGate's): task lands only when it is the head of stream's
// current live order and can finish (OrderGate over the one member). The
// hold, or nil; a task with no stream passes. The store runs the same
// check in the move's own call (cm_order_head in ns_tcard_move).
func TaskHeadGate(ctx context.Context, c redis.Cmdable, stream, task string, n int) (*OrderHold, error) {
	if stream == "" || task == "" {
		return nil, nil
	}
	_, _, holds, err := OrderGate(ctx, c, []string{stream}, []Member{{Task: task, Stream: stream, N: n}}, nil)
	if err != nil || len(holds) == 0 {
		return nil, err
	}
	return &holds[0], nil
}

// OrderRefusal is the gate's refusal for a hold (land merge, land pr, card
// land --stream): the line as the reason, the remedy by its kind.
func OrderRefusal(h OrderHold) *Refusal { return orderRefusal(h) }
