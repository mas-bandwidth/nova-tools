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

// showExec runs one pipelined round; a redis.Nil among the replies (an
// absent key) is not an error.
func showExec(ctx context.Context, pipe redis.Pipeliner) error {
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// Show is `ws show --order` (nova-tools #4318, #4322): every stream's cards
// in its computed work order (Order, order.go) with one reason per edge,
// read in four pipelined rounds (the streams; every set of every stream;
// every member's record; the records of dependencies that are in no
// stream). A stream's landed and done cards come first (the order is past
// them), then its live cards by rank, then its parked cards, and the
// sentinel last: it is the stream's stop, after every other card. A stream
// whose DEPENDS-ON closes a cycle carries the cycle in Cycle and lists its
// cards by score. Show never writes.

// ShowDep is one DEPENDS-ON entry of a card: the entry as written, and, for
// a task id, what its record says: Where (its set) and Landed; Known is
// false when no record has that id (an owner/repo#n reference is never
// looked up here: Known false, Ref true).
type ShowDep struct {
	Raw    string
	ID     string // the task id an entry names ("" for a repository reference)
	Ref    bool   // an owner/repo#n reference
	Known  bool
	Where  string
	Landed bool
}

// ShowCard is one card of a stream.
type ShowCard struct {
	ID       string
	Where    string
	Score    float64 // its score in its set
	Sentinel bool
	Rank     int // its rank in the stream's order; 0 when it is not ordered (landed, done, parked, or a cycle)
	Deps     []ShowDep
	Reasons  []Reason // its order edges that are not DEPENDS-ON entries (paths, issue)
}

// ShowStream is one stream: its cards in order, the sentinel last.
type ShowStream struct {
	Stream   string
	Rank     int
	Cards    []ShowCard
	Sentinel string // the sentinel's where, or "none" when the stream has no sentinel record
	Live     int    // cards in waiting, ready, working, review, merging or parked, the sentinel aside
	Landed   int    // cards in landed
	Cycle    string // the stream's DEPENDS-ON cycle (Order refused it); "" when it is ordered
}

// SplitDeps splits a stored blocked_on (DEPENDS-ON) value into its entries:
// separated by ',', ';' or white space, duplicates dropped; nil for "none",
// "-" or an empty value. It is the one splitter (the waiting resolver
// splits with it too); what an entry means is the reader's.
func SplitDeps(text string) []string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(parts) == 1 && (parts[0] == "none" || parts[0] == "-") {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		if p == "" || p == "none" || p == "-" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// DepID is the task id a DEPENDS-ON entry names: the entry, or what follows
// task:; "" for an owner/repo#n (or repo#n, or URL) reference.
func DepID(entry string) string {
	if strings.Contains(entry, "#") || strings.Contains(entry, "://") {
		return ""
	}
	if id, ok := strings.CutPrefix(entry, "task:"); ok {
		return id
	}
	return entry
}

// showLanded is whether a record's fields say landed (the waiting resolver's
// rule): where landed, or done with where_ok not fail; a stream's sentinel
// only when landed (its done is a rename's, never a landing).
func showLanded(where, whereOK, id string) bool {
	if IsSentinel(id) {
		return where == Landed
	}
	return where == Landed || (where == Done && whereOK != "fail")
}

// Show reads every stream's cards and edges. It returns the streams in rank
// order (ws:order, then unranked names by name).
func Show(ctx context.Context, c redis.Cmdable) ([]ShowStream, error) {
	// Round 1: the streams.
	pipe := c.Pipeline()
	orderCmd := pipe.ZRange(ctx, "ws:order", 0, -1)
	namesCmd := pipe.SMembers(ctx, "ws:names")
	if err := showExec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("ws show: streams: %w", err)
	}
	streams := orderCmd.Val()
	seen := map[string]bool{}
	for _, s := range streams {
		seen[s] = true
	}
	var rest []string
	for _, s := range namesCmd.Val() {
		if !seen[s] {
			seen[s] = true
			rest = append(rest, s)
		}
	}
	sort.Strings(rest)
	streams = append(streams, rest...)
	if len(streams) == 0 {
		return nil, nil
	}

	// Round 2: every set of every stream, with scores.
	pipe = c.Pipeline()
	setCmds := make([][]*redis.ZSliceCmd, len(streams))
	for i, s := range streams {
		setCmds[i] = make([]*redis.ZSliceCmd, len(Wheres))
		for j, w := range Wheres {
			setCmds[i][j] = pipe.ZRangeWithScores(ctx, Key(s, w), 0, -1)
		}
	}
	if err := showExec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("ws show: sets: %w", err)
	}
	out := make([]ShowStream, len(streams))
	var members []string
	whereOf := map[string]string{}
	for i, s := range streams {
		out[i] = ShowStream{Stream: s, Rank: i + 1, Sentinel: "none"}
		sid := SentinelID(s)
		for j, w := range Wheres {
			for _, z := range setCmds[i][j].Val() {
				id, _ := z.Member.(string)
				if id == "" {
					continue
				}
				card := ShowCard{ID: id, Where: w, Score: z.Score, Sentinel: id == sid}
				out[i].Cards = append(out[i].Cards, card)
				members = append(members, id)
				whereOf[id] = w
				switch {
				case card.Sentinel:
					out[i].Sentinel = w
				case w == Landed:
					out[i].Landed++
				case w != Done:
					out[i].Live++
				}
			}
		}
		cards := out[i].Cards
		sort.SliceStable(cards, func(a, b int) bool {
			if cards[a].Sentinel != cards[b].Sentinel {
				return !cards[a].Sentinel
			}
			if cards[a].Score != cards[b].Score {
				return cards[a].Score < cards[b].Score
			}
			return cards[a].ID < cards[b].ID
		})
	}

	// Round 3: every member's record: the order's fields and where_ok.
	pipe = c.Pipeline()
	recCmds := make([]*redis.SliceCmd, len(members))
	for i, id := range members {
		recCmds[i] = pipe.HMGet(ctx, RecordKey(id), append(append([]string{}, orderFields...), "where_ok")...)
	}
	if err := showExec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("ws show: records: %w", err)
	}
	blockedOn, okOf := map[string]string{}, map[string]string{}
	recs := map[string]orderRec{}
	for i, id := range members {
		v := recCmds[i].Val()
		recs[id] = readOrderRec(v)
		blockedOn[id] = recs[id].deps
		okOf[id] = showStr(v, len(orderFields))
	}

	// The edges, in two passes: the entries first, then (round 4) the
	// records of the dependencies that are in no stream, and every edge is
	// annotated from the one map (never a pointer into a slice still growing).
	var strays []string
	strayRead := map[string]bool{}
	for i := range out {
		for j := range out[i].Cards {
			card := &out[i].Cards[j]
			for _, raw := range SplitDeps(blockedOn[card.ID]) {
				dep := ShowDep{Raw: raw, ID: DepID(raw)}
				dep.Ref = dep.ID == ""
				if _, known := whereOf[dep.ID]; !dep.Ref && !known && !strayRead[dep.ID] {
					strayRead[dep.ID] = true
					strays = append(strays, dep.ID)
				}
				card.Deps = append(card.Deps, dep)
			}
		}
	}
	if len(strays) > 0 {
		pipe = c.Pipeline()
		strayCmds := make([]*redis.SliceCmd, len(strays))
		for i, id := range strays {
			strayCmds[i] = pipe.HMGet(ctx, "task:"+id, "where", "where_ok")
		}
		if err := showExec(ctx, pipe); err != nil {
			return nil, fmt.Errorf("ws show: dependencies: %w", err)
		}
		for i, id := range strays {
			v := strayCmds[i].Val()
			if w := showStr(v, 0); w != "" {
				whereOf[id], okOf[id] = w, showStr(v, 1)
			}
		}
	}
	for i := range out {
		for j := range out[i].Cards {
			deps := out[i].Cards[j].Deps
			for k := range deps {
				if w, ok := whereOf[deps[k].ID]; ok && !deps[k].Ref {
					deps[k].Known, deps[k].Where, deps[k].Landed = true, w, showLanded(w, okOf[deps[k].ID], deps[k].ID)
				}
			}
		}
		showOrder(&out[i], recs)
	}
	return out, nil
}

// showOrder ranks a stream's live cards (Order) and sorts its cards: landed
// and done by score, the live by rank, parked by score, the sentinel last.
func showOrder(st *ShowStream, recs map[string]orderRec) {
	var live []string
	seen := map[string]bool{}
	for _, c := range st.Cards {
		if isLive(c.Where) && !seen[c.ID] {
			seen[c.ID] = true
			live = append(live, c.ID)
		}
	}
	ord, reasons, err := Order(orderCards(st.Stream, live, recs))
	rank := map[string]int{}
	extra := map[string][]Reason{}
	if err != nil {
		st.Cycle = err.Error()
	} else {
		for _, o := range ord {
			rank[o.ID] = o.Rank
		}
		for _, r := range reasons {
			if r.Why == WhyPaths || r.Why == WhyIssue {
				extra[r.Card] = append(extra[r.Card], r)
			}
		}
	}
	group := func(c ShowCard) int {
		switch {
		case c.Sentinel:
			return 3
		case c.Where == Landed || c.Where == Done:
			return 0
		case c.Where == Parked:
			return 2
		}
		return 1
	}
	for j := range st.Cards {
		c := &st.Cards[j]
		c.Rank = rank[c.ID]
		if !c.Sentinel {
			c.Reasons = extra[c.ID]
		}
	}
	cards := st.Cards
	sort.SliceStable(cards, func(a, b int) bool {
		ga, gb := group(cards[a]), group(cards[b])
		if ga != gb {
			return ga < gb
		}
		if cards[a].Rank != cards[b].Rank {
			return cards[a].Rank < cards[b].Rank
		}
		if cards[a].Score != cards[b].Score {
			return cards[a].Score < cards[b].Score
		}
		return cards[a].ID < cards[b].ID
	})
}

// isLive is whether a set's cards are ordered (OrderLive).
func isLive(where string) bool {
	for _, w := range OrderLive {
		if w == where {
			return true
		}
	}
	return false
}

func showStr(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

// Line is the card's line as ws show prints it: its rank in the stream's
// order (- when it is not ordered), its where and id, then `<- ` and its
// edges, one reason each: every DEPENDS-ON entry as written, with the
// dependency's set in parentheses when it is not landed or (no record) when
// nothing has that id, `(reason: depends-on)`; then the order's other edges
// into it, `<id> (reason: paths <path>)` and `<id> (reason: issue)`; a
// sentinel's edge is every other card of its stream.
func (c ShowCard) Line(live int) string {
	var b strings.Builder
	rank := "-"
	if c.Rank > 0 {
		rank = strconv.Itoa(c.Rank)
	}
	fmt.Fprintf(&b, "%s %-7s %s", rank, c.Where, c.ID)
	if c.Sentinel {
		fmt.Fprintf(&b, " <- every other card of the stream (reason: sentinel; live %d)", live)
		return b.String()
	}
	if len(c.Deps)+len(c.Reasons) == 0 {
		return b.String()
	}
	b.WriteString(" <- ")
	for i, d := range c.Deps {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.Raw)
		switch {
		case d.Ref:
		case !d.Known:
			b.WriteString("(no record)")
		case !d.Landed:
			b.WriteString("(" + d.Where + ")")
		}
		b.WriteString(" (reason: " + WhyDependsOn + ")")
	}
	for i, r := range c.Reasons {
		if i > 0 || len(c.Deps) > 0 {
			b.WriteString(", ")
		}
		b.WriteString(r.String())
	}
	return b.String()
}
