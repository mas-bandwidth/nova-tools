package ws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// The stream order in the store (nova-tools #4322, #4324; card
// land-order-1): Order's sequence is the score of every card in the
// stream's waiting, ready and merging sets, so the lander (land/stream
// Members reads merging by score) and the dealer (ready and waiting by
// score) take the cards in work order. The score of rank r (1-based) is the
// stream's oldest ranked card's created_at ms plus r-1: the order, and still
// an age (the progress duty reads the least score as the oldest card). The
// one writer is ns_ws_reorder (TK.reorder in fn/lua/02_card_move.lua), which
// also keeps the score as the record's order_score so every later move into
// one of the three sets carries it.

// FnReorder writes a stream's computed order (fn/lua/ws.lua).
const FnReorder = "ns_ws_reorder"

// OrderedSets are the sets the order scores.
var OrderedSets = []string{Waiting, Ready, Merging}

// OrderLive are the sets whose cards the order ranks: the stream's live
// cards (a landed, done or parked card is no longer ordered; a DEPENDS-ON on
// one is met or out of scope).
var OrderLive = []string{Waiting, Ready, Working, Review, Merging}

// orderFields are the record fields the order reads.
var orderFields = []string{"blocked_on", "depends_on", "paths", "ref", "origin", "created_at", "order_score"}

// RecordKey is the hash of a set member: a card id (s:<S>:card:<label>) is
// its own key, a task id's is task:<id>.
func RecordKey(id string) string {
	if isCardID(id) {
		return id
	}
	return "task:" + id
}

// orderRec is one member's order fields.
type orderRec struct {
	deps, paths, ref, origin string
	created                  float64
}

func readOrderRec(v []any) orderRec {
	r := orderRec{deps: showStr(v, 0), paths: showStr(v, 2), ref: showStr(v, 3), origin: showStr(v, 4)}
	if r.deps == "" {
		r.deps = showStr(v, 1)
	}
	r.created, _ = CreatedMS(showStr(v, 5))
	return r
}

// orderCards builds Order's input for one stream: its live members and its
// sentinel. A DEPENDS-ON entry of a card id that names a bare label is read
// as that sprint's card id.
func orderCards(stream string, live []string, recs map[string]orderRec) []OrderCard {
	sid := SentinelID(stream)
	in := map[string]bool{}
	for _, id := range live {
		in[id] = true
	}
	cards := make([]OrderCard, 0, len(live))
	for _, id := range live {
		r := recs[id]
		c := OrderCard{ID: id, Issue: IssueOf(r.ref, r.origin), Paths: SplitPaths(r.paths), Sentinel: id == sid}
		for _, raw := range SplitDeps(r.deps) {
			d := DepID(raw)
			if d == "" {
				continue
			}
			if !in[d] && isCardID(id) {
				if s, _, ok := cutCardID(id); ok && in["s:"+s+":card:"+d] {
					d = "s:" + s + ":card:" + d
				}
			}
			c.Deps = append(c.Deps, d)
		}
		cards = append(cards, c)
	}
	return cards
}

// cutCardID splits s:<S>:card:<label>.
func cutCardID(id string) (sprint, label string, ok bool) {
	rest, ok := strings.CutPrefix(id, "s:")
	if !ok {
		return "", "", false
	}
	return strings.Cut(rest, ":card:")
}

// StreamOrder is one stream's computed order beside its stored one.
type StreamOrder struct {
	Stream   string
	Order    []Ordered
	Reasons  []Reason
	Scores   []float64         // the score of each rank, Order's index
	Where    map[string]string // each live member's set
	Stored   []string          // the members of waiting, ready and merging by stored score, then id
	Computed []string          // Order's sequence over those same members
	Err      error             // a *CycleError: nothing is ordered
}

// Drift is whether the stored scores read another sequence than the order.
func (s StreamOrder) Drift() bool {
	if s.Err != nil {
		return false // a cycle is its own finding
	}
	if len(s.Stored) != len(s.Computed) {
		return true
	}
	for i := range s.Stored {
		if s.Stored[i] != s.Computed[i] {
			return true
		}
	}
	return false
}

// ReadOrders reads each stream's live members and their records, in two
// pipelined round trips for all the streams, and computes each order. A
// stream whose DEPENDS-ON closes a cycle carries the *CycleError in Err.
func ReadOrders(ctx context.Context, c redis.Cmdable, streams []string) ([]StreamOrder, error) {
	if len(streams) == 0 {
		return nil, nil
	}
	pipe := c.Pipeline()
	sets := make([][]*redis.ZSliceCmd, len(streams))
	for i, s := range streams {
		sets[i] = make([]*redis.ZSliceCmd, len(OrderLive))
		for j, w := range OrderLive {
			sets[i][j] = pipe.ZRangeWithScores(ctx, Key(s, w), 0, -1)
		}
	}
	if err := showExec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("ws order: sets: %w", err)
	}
	type member struct {
		id    string
		score float64
		where string
	}
	members := make([][]member, len(streams))
	var ids []string
	for i := range streams {
		for j, w := range OrderLive {
			for _, z := range sets[i][j].Val() {
				id, _ := z.Member.(string)
				if id == "" {
					continue
				}
				members[i] = append(members[i], member{id, z.Score, w})
				ids = append(ids, id)
			}
		}
	}
	recs := map[string]orderRec{}
	if len(ids) > 0 {
		pipe = c.Pipeline()
		cmds := make([]*redis.SliceCmd, len(ids))
		for k, id := range ids {
			cmds[k] = pipe.HMGet(ctx, RecordKey(id), orderFields...)
		}
		if err := showExec(ctx, pipe); err != nil {
			return nil, fmt.Errorf("ws order: records: %w", err)
		}
		for k, id := range ids {
			recs[id] = readOrderRec(cmds[k].Val())
		}
	}
	out := make([]StreamOrder, len(streams))
	for i, s := range streams {
		so := StreamOrder{Stream: s, Where: map[string]string{}}
		var live []string
		var stored []member
		for _, m := range members[i] {
			if _, twice := so.Where[m.id]; twice {
				continue // in two sets: ws.Check names it; order it once
			}
			so.Where[m.id] = m.where
			live = append(live, m.id)
			if m.where == Waiting || m.where == Ready || m.where == Merging {
				stored = append(stored, m)
			}
		}
		sort.SliceStable(stored, func(a, b int) bool {
			if stored[a].score != stored[b].score {
				return stored[a].score < stored[b].score
			}
			return stored[a].id < stored[b].id
		})
		for _, m := range stored {
			so.Stored = append(so.Stored, m.id)
		}
		so.Order, so.Reasons, so.Err = Order(orderCards(s, live, recs))
		if so.Err != nil {
			var ce *CycleError
			if !errors.As(so.Err, &ce) {
				return nil, fmt.Errorf("ws order: stream %s: %w", strconv.Quote(s), so.Err)
			}
			out[i] = so
			continue
		}
		base := 0.0
		for _, o := range so.Order {
			if cr := recs[o.ID].created; cr > 0 && (base == 0 || cr < base) {
				base = cr
			}
		}
		so.Scores = make([]float64, len(so.Order))
		for r, o := range so.Order {
			so.Scores[r] = base + float64(r)
			switch so.Where[o.ID] {
			case Waiting, Ready, Merging:
				so.Computed = append(so.Computed, o.ID)
			}
		}
		out[i] = so
	}
	return out, nil
}

// ReadOrder is ReadOrders for one stream.
func ReadOrder(ctx context.Context, c redis.Cmdable, stream string) (StreamOrder, error) {
	out, err := ReadOrders(ctx, c, []string{stream})
	if err != nil {
		return StreamOrder{}, err
	}
	return out[0], nil
}

// ReorderResult is what Reorder wrote: Ranked records took an order_score,
// Rescored set members a new score, Skipped ids were no record of the
// stream by the write; RoundTrips is the round trips it took (two pipelined
// reads and the one write; no write when the stream has no live card).
type ReorderResult struct {
	StreamOrder
	Ranked, Rescored, Skipped, RoundTrips int
}

// Reorder recomputes a stream's order and writes it, the scores of its
// waiting, ready and merging sets and every ranked record's order_score, in
// one FCALL of ns_ws_reorder. A cycle is refused (*CycleError) and nothing
// is written; an unknown stream is a *Refused.
func Reorder(ctx context.Context, c redis.Cmdable, stream, by string) (ReorderResult, error) {
	so, err := ReadOrder(ctx, c, stream)
	r := ReorderResult{StreamOrder: so, RoundTrips: 2}
	if err != nil {
		return r, err
	}
	if so.Err != nil {
		return r, so.Err
	}
	args := make([]any, 0, 2+2*len(so.Order))
	args = append(args, stream, by)
	for k, o := range so.Order {
		args = append(args, o.ID, strconv.FormatFloat(so.Scores[k], 'f', 0, 64))
	}
	r.RoundTrips++
	out, err := call(ctx, c, FnReorder, false, args...)
	if err != nil {
		return r, err
	}
	if out[0] != "REORDERED" || len(out) != 4 {
		return r, fmt.Errorf("%s: unexpected reply %v", FnReorder, out)
	}
	r.Ranked, r.Rescored, r.Skipped = atoi(out[1]), atoi(out[2]), atoi(out[3])
	return r, nil
}
