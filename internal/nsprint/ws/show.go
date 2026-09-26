package ws

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// Show is `ws show --order` (nova-tools #4318): every stream's cards in
// order with their DEPENDS-ON edges, read in four pipelined rounds (the
// streams; every set of every stream; every member's blocked_on and where;
// the records of dependencies that are in no stream). The order within a
// stream is the sets' one order, created_at then id (the seam with #4342,
// whose computed order is the waiting set's score), with the sentinel last:
// it is the stream's stop, after every other card. Show never writes.

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
	Score    float64 // its created_at ms, the sets' order
	Sentinel bool
	Deps     []ShowDep
}

// ShowStream is one stream: its cards in order, the sentinel last.
type ShowStream struct {
	Stream   string
	Rank     int
	Cards    []ShowCard
	Sentinel string // the sentinel's where, or "none" when the stream has no sentinel record
	Live     int    // cards in waiting, ready, working, review, merging or parked, the sentinel aside
	Landed   int    // cards in landed
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
// rule): where landed, or done with where_ok not fail.
func showLanded(where, whereOK string) bool {
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

	// Round 3: every member's blocked_on and where_ok.
	pipe = c.Pipeline()
	recCmds := make([]*redis.SliceCmd, len(members))
	for i, id := range members {
		recCmds[i] = pipe.HMGet(ctx, "task:"+id, "blocked_on", "where_ok")
	}
	if err := showExec(ctx, pipe); err != nil {
		return nil, fmt.Errorf("ws show: records: %w", err)
	}
	blockedOn, okOf := map[string]string{}, map[string]string{}
	for i, id := range members {
		v := recCmds[i].Val()
		blockedOn[id] = showStr(v, 0)
		okOf[id] = showStr(v, 1)
	}

	// The edges; a dependency in no stream is read in round 4.
	var strays []string
	strayDeps := map[string][]*ShowDep{}
	for i := range out {
		for j := range out[i].Cards {
			card := &out[i].Cards[j]
			for _, raw := range SplitDeps(blockedOn[card.ID]) {
				dep := ShowDep{Raw: raw, ID: DepID(raw)}
				dep.Ref = dep.ID == ""
				if w, ok := whereOf[dep.ID]; ok {
					dep.Known, dep.Where, dep.Landed = true, w, showLanded(w, okOf[dep.ID])
				}
				card.Deps = append(card.Deps, dep)
				if !dep.Ref && !dep.Known {
					if _, listed := strayDeps[dep.ID]; !listed {
						strays = append(strays, dep.ID)
					}
					strayDeps[dep.ID] = append(strayDeps[dep.ID], &card.Deps[len(card.Deps)-1])
				}
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
			w := showStr(v, 0)
			if w == "" {
				continue
			}
			for _, dep := range strayDeps[id] {
				dep.Known, dep.Where, dep.Landed = true, w, showLanded(w, showStr(v, 1))
			}
		}
	}
	return out, nil
}

func showStr(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

// Line is the card's line as ws show prints it: its where and id, then
// `<- ` and its edges: each entry as written, with the dependency's set in
// parentheses when it is not landed, or (no record) when nothing has that
// id; a sentinel's edge is every other card of its stream.
func (c ShowCard) Line(live int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-7s %s", c.Where, c.ID)
	if c.Sentinel {
		fmt.Fprintf(&b, " <- every other card of the stream (live %d)", live)
		return b.String()
	}
	if len(c.Deps) == 0 {
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
	}
	return b.String()
}
