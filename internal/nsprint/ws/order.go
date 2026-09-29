package ws

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/redis/go-redis/v9"
)

var (
	issueFromRefRx    = regexp.MustCompile(`#([0-9]+)$`)
	issueFromOriginRx = regexp.MustCompile(`[#/]([0-9]+)$`)
	issueFromIDRx     = regexp.MustCompile(`-([0-9]+)$`)
)

// ParseIssueNumber extracts the integer issue number from ref, origin, or id.
func ParseIssueNumber(id, ref, origin string) int {
	for _, s := range []string{ref, origin} {
		if m := issueFromRefRx.FindStringSubmatch(s); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				return n
			}
		}
		if m := issueFromOriginRx.FindStringSubmatch(s); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				return n
			}
		}
	}
	if m := issueFromIDRx.FindStringSubmatch(id); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	if n, err := strconv.Atoi(id); err == nil && n > 0 {
		return n
	}
	return 0
}

// CardOrderInfo carries the card metadata needed to compute stream order.
type CardOrderInfo struct {
	ID        string
	Issue     int
	Paths     []string
	DependsOn []string
}

// OrderMiss represents one learned miss read from ws:order:misses.
type OrderMiss struct {
	Stream  string
	Card    string
	Missing string
	Why     string
}

// ReadOrderMisses reads all rows from the order misses stream for stream.
func ReadOrderMisses(ctx context.Context, c redis.Cmdable, stream string) ([]OrderMiss, error) {
	msgs, err := c.XRange(ctx, OrderMissesKey(ctx, c), "-", "+").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var out []OrderMiss
	for _, m := range msgs {
		st, _ := m.Values["stream"].(string)
		if stream != "" && st != stream {
			continue
		}
		card, _ := m.Values["card"].(string)
		missing, _ := m.Values["missing"].(string)
		why, _ := m.Values["why"].(string)
		out = append(out, OrderMiss{
			Stream:  st,
			Card:    card,
			Missing: missing,
			Why:     why,
		})
	}
	return out, nil
}

// OrderStream computes the stream's work order across all cards in
// ws:<stream>:waiting:
// 1) topological order of DEPENDS-ON
// 2) ties broken by learned order misses (ws:order:misses)
// 3) ties broken by PATHS overlap (a card whose paths overlap an earlier card's comes after it)
// 4) then by issue number.
//
// The computed order is stored as the ZSET score on ws:<stream>:waiting (and task:<id> order).
func OrderStream(ctx context.Context, c redis.Cmdable, stream string) error {
	if stream == "" {
		return nil
	}
	epoch, err := Epoch(ctx, c)
	if err != nil {
		return fmt.Errorf("order stream %s: epoch: %w", stream, err)
	}
	waitKey := KeyAt(epoch, stream, Waiting)
	members, err := c.ZRange(ctx, waitKey, 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("order stream %s: zrange %s: %w", stream, waitKey, err)
	}
	if len(members) == 0 {
		return nil
	}
	sid := SentinelID(stream)
	var cardIDs []string
	for _, id := range members {
		if id != sid && !IsSentinel(id) {
			cardIDs = append(cardIDs, id)
		}
	}
	if len(cardIDs) == 0 {
		return nil
	}

	pipe := c.Pipeline()
	cmds := make([]*redis.SliceCmd, len(cardIDs))
	for i, id := range cardIDs {
		cmds[i] = pipe.HMGet(ctx, "task:"+id, "blocked_on", "paths", "stream_paths", "ref", "origin")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("order stream %s: task reads: %w", stream, err)
	}

	cards := make(map[string]*CardOrderInfo, len(cardIDs))
	for i, id := range cardIDs {
		val := cmds[i].Val()
		blockedOn := showStr(val, 0)
		pathsStr := showStr(val, 1)
		if pathsStr == "" {
			pathsStr = showStr(val, 2)
		}
		ref := showStr(val, 3)
		origin := showStr(val, 4)

		paths := SplitPaths(pathsStr)
		var deps []string
		for _, raw := range SplitDeps(blockedOn) {
			if did := DepID(raw); did != "" {
				deps = append(deps, did)
			}
		}
		cards[id] = &CardOrderInfo{
			ID:        id,
			Issue:     ParseIssueNumber(id, ref, origin),
			Paths:     paths,
			DependsOn: deps,
		}
	}

	misses, err := ReadOrderMisses(ctx, c, stream)
	if err != nil {
		return fmt.Errorf("order stream %s: read misses: %w", stream, err)
	}

	ranked := ComputeOrder(cardIDs, cards, misses)

	// Write computed scores into ws:<stream>:waiting and task:<id> order.
	pipe = c.Pipeline()
	for rank, id := range ranked {
		score := float64(rank + 1)
		pipe.ZAdd(ctx, waitKey, redis.Z{Score: score, Member: id})
		pipe.HSet(ctx, fmt.Sprintf("task:%s", id), "order", strconv.FormatInt(int64(rank+1), 10))
	}
	// Sentinel is always last.
	sentinelScore := float64(len(ranked) + 1000)
	pipe.ZAdd(ctx, waitKey, redis.Z{Score: sentinelScore, Member: sid})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("order stream %s: write scores: %w", stream, err)
	}
	return nil
}

// ComputeOrder computes the topological order of cards:
// - DEPENDS-ON edges
// - ws:order:misses (learned misses: missing -> card)
// - PATHS overlap ties (earlier card by issue number comes first)
// - tie-break by issue number, then ID.
func ComputeOrder(cardIDs []string, cards map[string]*CardOrderInfo, misses []OrderMiss) []string {
	inWaiting := map[string]bool{}
	for _, id := range cardIDs {
		inWaiting[id] = true
	}

	// Adjacency list: u -> v means u must come before v.
	adj := map[string]map[string]bool{}
	for _, id := range cardIDs {
		adj[id] = map[string]bool{}
	}

	// 1. Explicit DEPENDS-ON edges: if v depends on u, then u -> v.
	for _, id := range cardIDs {
		info := cards[id]
		if info == nil {
			continue
		}
		for _, dep := range info.DependsOn {
			if inWaiting[dep] && dep != id {
				adj[dep][id] = true
			}
		}
	}

	// Helper to check reachability in adj (transitive closure).
	reachable := func(from, to string) bool {
		visited := map[string]bool{from: true}
		queue := []string{from}
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			if curr == to {
				return true
			}
			for next := range adj[curr] {
				if !visited[next] {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
		return false
	}

	// 2. Learned order misses: missing -> card.
	for _, m := range misses {
		if inWaiting[m.Card] && inWaiting[m.Missing] && m.Card != m.Missing {
			// missing must precede card
			if !reachable(m.Card, m.Missing) {
				adj[m.Missing][m.Card] = true
			}
		}
	}

	// 3. PATHS overlap ties:
	// A card whose paths overlap an earlier card's comes after it.
	// Earlier card is determined by issue number (or ID).
	for i := 0; i < len(cardIDs); i++ {
		idA := cardIDs[i]
		infoA := cards[idA]
		if infoA == nil || len(infoA.Paths) == 0 {
			continue
		}
		for j := i + 1; j < len(cardIDs); j++ {
			idB := cardIDs[j]
			infoB := cards[idB]
			if infoB == nil || len(infoB.Paths) == 0 {
				continue
			}
			// Check if already ordered
			if reachable(idA, idB) || reachable(idB, idA) {
				continue
			}
			if len(OverlappingPaths(infoA.Paths, infoB.Paths)) > 0 {
				// Ties broken by PATHS overlap: earlier card comes first.
				// Compare by issue number.
				aBeforeB := infoA.Issue < infoB.Issue
				if infoA.Issue == infoB.Issue {
					aBeforeB = idA < idB
				}
				if aBeforeB {
					adj[idA][idB] = true
				} else {
					adj[idB][idA] = true
				}
			}
		}
	}

	// 4. Topological sort with Kahn's algorithm, using issue number ascending as tie-breaker.
	inDegree := map[string]int{}
	for _, id := range cardIDs {
		inDegree[id] = 0
	}
	for u := range adj {
		for v := range adj[u] {
			inDegree[v]++
		}
	}

	less := func(a, b string) bool {
		ia := 0
		if info := cards[a]; info != nil {
			ia = info.Issue
		}
		ib := 0
		if info := cards[b]; info != nil {
			ib = info.Issue
		}
		if ia != ib {
			return ia < ib
		}
		return a < b
	}

	var available []string
	for _, id := range cardIDs {
		if inDegree[id] == 0 {
			available = append(available, id)
		}
	}
	sort.Slice(available, func(i, j int) bool {
		return less(available[i], available[j])
	})

	var out []string
	visited := map[string]bool{}

	for len(available) > 0 {
		curr := available[0]
		available = available[1:]
		if visited[curr] {
			continue
		}
		visited[curr] = true
		out = append(out, curr)

		for next := range adj[curr] {
			inDegree[next]--
			if inDegree[next] == 0 && !visited[next] {
				available = append(available, next)
				sort.Slice(available, func(i, j int) bool {
					return less(available[i], available[j])
				})
			}
		}
	}

	// Handle any cycles or disconnected nodes
	if len(out) < len(cardIDs) {
		var rest []string
		for _, id := range cardIDs {
			if !visited[id] {
				rest = append(rest, id)
			}
		}
		sort.Slice(rest, func(i, j int) bool {
			return less(rest[i], rest[j])
		})
		out = append(out, rest...)
	}

	return out
}
