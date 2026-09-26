package ws

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
	order                    float64 // its order_score; -1 when it has none
}

func readOrderRec(v []any) orderRec {
	r := orderRec{deps: showStr(v, 0), paths: showStr(v, 2), ref: showStr(v, 3), origin: showStr(v, 4)}
	if r.deps == "" {
		r.deps = showStr(v, 1)
	}
	r.created, _ = CreatedMS(showStr(v, 5))
	r.order = -1
	if n, err := strconv.ParseFloat(showStr(v, 6), 64); err == nil {
		r.order = n
	}
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
	// StoredScore is each waiting, ready and merging member's set score;
	// OrderScore each live record's order_score (absent: none).
	StoredScore, OrderScore map[string]float64
}

// Stale is whether any stored score is not the computed one: a member of
// waiting, ready or merging whose set score, or a live record whose
// order_score, differs from its rank's score. It is Drift and more: a base
// gone stale (the oldest card landed) or a card pushed and never ranked
// reads the same sequence but other scores.
func (s StreamOrder) Stale() bool {
	if s.Err != nil {
		return false
	}
	for r, o := range s.Order {
		if got, ok := s.OrderScore[o.ID]; !ok || got != s.Scores[r] {
			return true
		}
		if got, ok := s.StoredScore[o.ID]; ok && got != s.Scores[r] {
			return true
		}
	}
	return false
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
	members, recs, err := readLive(ctx, c, streams)
	if err != nil {
		return nil, err
	}
	out := make([]StreamOrder, len(streams))
	for i, s := range streams {
		if out[i], err = orderOf(s, members[i], recs); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// liveMember is one member of a stream's live sets.
type liveMember struct {
	id    string
	score float64
	where string
}

// readLive is ReadOrders' two pipelined round trips: each stream's live
// sets, then every member's order fields.
func readLive(ctx context.Context, c redis.Cmdable, streams []string) ([][]liveMember, map[string]orderRec, error) {
	if len(streams) == 0 {
		return nil, nil, nil
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
		return nil, nil, fmt.Errorf("ws order: sets: %w", err)
	}
	members := make([][]liveMember, len(streams))
	var ids []string
	for i := range streams {
		for j, w := range OrderLive {
			for _, z := range sets[i][j].Val() {
				id, _ := z.Member.(string)
				if id == "" {
					continue
				}
				members[i] = append(members[i], liveMember{id, z.Score, w})
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
			return nil, nil, fmt.Errorf("ws order: records: %w", err)
		}
		for k, id := range ids {
			recs[id] = readOrderRec(cmds[k].Val())
		}
	}
	return members, recs, nil
}

// orderOf computes one stream's order from its live members and their
// records. The score base is the created_at of the stream's oldest live
// card that is not its sentinel (the sentinel's only when it is the one
// live card), so the least order score reads the oldest live card's age
// and PROGRESS oldest= is that card's, never the stream's.
func orderOf(s string, members []liveMember, recs map[string]orderRec) (StreamOrder, error) {
	so := StreamOrder{Stream: s, Where: map[string]string{}, StoredScore: map[string]float64{}, OrderScore: map[string]float64{}}
	var live []string
	var stored []liveMember
	for _, m := range members {
		if _, twice := so.Where[m.id]; twice {
			continue // in two sets: ws.Check names it; order it once
		}
		so.Where[m.id] = m.where
		live = append(live, m.id)
		if r, ok := recs[m.id]; ok && r.order >= 0 {
			so.OrderScore[m.id] = r.order
		}
		if m.where == Waiting || m.where == Ready || m.where == Merging {
			stored = append(stored, m)
			so.StoredScore[m.id] = m.score
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
			return StreamOrder{}, fmt.Errorf("ws order: stream %s: %w", strconv.Quote(s), so.Err)
		}
		return so, nil
	}
	so.Scores = make([]float64, len(so.Order))
	base := Base(so.Order, func(id string) float64 { return recs[id].created })
	for r, o := range so.Order {
		so.Scores[r] = base + float64(r)
		switch so.Where[o.ID] {
		case Waiting, Ready, Merging:
			so.Computed = append(so.Computed, o.ID)
		}
	}
	return so, nil
}

// Base is the order's score base: the least created_at (ms, > 0) among
// its cards that are not the sentinel, else the sentinel's, else 0.
func Base(order []Ordered, created func(id string) float64) float64 {
	base, stop := 0.0, 0.0
	for _, o := range order {
		cr := created(o.ID)
		switch {
		case cr <= 0:
		case o.Sentinel:
			stop = cr
		case base == 0 || cr < base:
			base = cr
		}
	}
	if base == 0 {
		return stop
	}
	return base
}

// PushCard is one card a push is about to write onto a stream, as the
// order reads it: its id, ref and origin (the issue number), PATHS and
// DEPENDS-ON.
type PushCard struct {
	ID, Ref, Origin, Paths, DependsOn string
}

// WouldCycle is the push's check before any write (nova-tools #4322 fix
// round: a push that closes a DEPENDS-ON cycle is refused and writes
// nothing): the stream's live cards (ReadOrders' two round trips) with the
// pushed cards added (a pushed id already live is replaced) are ordered, and
// a cycle is returned as its *CycleError; nil when the order holds. A
// stream with no name is never checked.
func WouldCycle(ctx context.Context, c redis.Cmdable, stream string, adds []PushCard) error {
	if stream == "" || len(adds) == 0 {
		return nil
	}
	members, recs, err := readLive(ctx, c, []string{stream})
	if err != nil {
		return err
	}
	ms := members[0]
	for _, a := range adds {
		if _, ok := recs[a.ID]; !ok {
			ms = append(ms, liveMember{id: a.ID, where: Waiting})
		}
		recs[a.ID] = orderRec{deps: a.DependsOn, paths: a.Paths, ref: a.Ref, origin: a.Origin, created: 1}
	}
	so, err := orderOf(stream, ms, recs)
	if err != nil {
		return err
	}
	return so.Err
}

var titleStreamRE = regexp.MustCompile(`^\s*STREAM:\s*(.*?)\s*\|`)

// StreamOfTitle is the stream a friend-queue task push lands in: --stream
// when named, else the title's "STREAM: <s> |" prefix (TK.stream_of in
// fn/lua/02_card_move.lua), else "".
func StreamOfTitle(stream, title string) string {
	if stream != "" {
		return stream
	}
	if m := titleStreamRE.FindStringSubmatch(title); m != nil {
		return m[1]
	}
	return ""
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
// reads and the one write per attempt); Stale is the attempts refused ORDER
// STALE (a push or a move changed the stream between the read and the
// write) and read again.
type ReorderResult struct {
	StreamOrder
	Ranked, Rescored, Skipped, RoundTrips, Stale int
}

// ReorderAttempts bounds Reorder's reads after an ORDER STALE refusal.
const ReorderAttempts = 5

// IsStale is whether err is ns_ws_reorder's ORDER STALE refusal.
func IsStale(err error) bool {
	var r *Refused
	return errors.As(err, &r) && strings.HasPrefix(r.Why, "ORDER STALE")
}

// Reorder recomputes a stream's order and writes it, the scores of its
// waiting, ready and merging sets and every ranked record's order_score, in
// one FCALL of ns_ws_reorder (WriteOrder). A cycle is refused (*CycleError)
// and nothing is written; an unknown stream is a *Refused. The write is
// conditional: it names every live card the read saw, and the FCALL refuses
// ORDER STALE and writes nothing when the stream's live cards are no longer
// those (a concurrent push or move); Reorder then reads again, at most
// ReorderAttempts times.
//
// Why two round trips and not one: the order is ws.Order, a Go function
// (topological sort, paths edges, reasons); computing it inside the FCALL
// would be a second copy of it in Lua, and two copies drift. The read stays
// in Go and the write is guarded by the stream's live membership instead,
// so a stale order is never written.
func Reorder(ctx context.Context, c redis.Cmdable, stream, by string) (ReorderResult, error) {
	var r ReorderResult
	for attempt := 1; ; attempt++ {
		so, err := ReadOrder(ctx, c, stream)
		r.StreamOrder = so
		r.RoundTrips += 2
		if err != nil {
			return r, err
		}
		if so.Err != nil {
			return r, so.Err
		}
		w, err := WriteOrder(ctx, c, so, by)
		r.RoundTrips += w.RoundTrips
		r.Ranked, r.Rescored, r.Skipped = w.Ranked, w.Rescored, w.Skipped
		if IsStale(err) && attempt < ReorderAttempts {
			r.Stale++
			continue
		}
		if IsStale(err) {
			r.Stale++
		}
		return r, err
	}
}

// WriteOrder is Reorder's write of an order already read (so, from
// ReadOrders): one FCALL of ns_ws_reorder with the stream's live cards as
// read, each with its rank's score. It refuses a cycle (so.Err) before any
// call; the FCALL refuses ORDER STALE when the stream's live cards are no
// longer so's.
func WriteOrder(ctx context.Context, c redis.Cmdable, so StreamOrder, by string) (ReorderResult, error) {
	r := ReorderResult{StreamOrder: so}
	if so.Err != nil {
		return r, so.Err
	}
	args := make([]any, 0, 2+2*len(so.Order))
	args = append(args, so.Stream, by)
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
