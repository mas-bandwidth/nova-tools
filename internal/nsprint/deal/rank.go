package deal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// TaskRank represents the computed rank and ordering details for a task.
type TaskRank struct {
	Sprint   string
	ID       string
	Owner    string
	Front    bool
	Rank     int
	Est      string // formatted estimate: "30" or "?"
	EstVal   int    // numeric minutes (0 if unknown / out-of-domain)
	Chain    string // e.g. "X1>X2>X3"
	Score    float64
	Priority int
	PushedAt int64
	State    string
}

var (
	estRx   = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)
	parenRx = regexp.MustCompile(`\([^)]*\)`)
	splitRx = regexp.MustCompile(`[\s,;]+`)
)

// parseEst returns the numeric estimate in minutes (or 0) and the formatted string ("?" or "N").
// Domain: whole minutes 1 <= est <= 10080. Stored est outside domain or empty counts as 0 and "?".
func parseEst(storedEst, title string) (int, string) {
	if storedEst != "" {
		if estRx.MatchString(storedEst) {
			v, err := strconv.Atoi(storedEst)
			if err == nil && v >= 1 && v <= 10080 {
				return v, storedEst
			}
		}
		return 0, "?"
	}
	if title != "" {
		for _, seg := range strings.Split(title, "|") {
			seg = strings.TrimSpace(seg)
			seg = strings.TrimPrefix(seg, "**")
			if rest, ok := strings.CutPrefix(seg, "est:"); ok {
				rest = strings.TrimSpace(strings.ReplaceAll(rest, "**", ""))
				parts := strings.Fields(rest)
				if len(parts) > 0 {
					valStr := parts[0]
					if estRx.MatchString(valStr) {
						v, err := strconv.Atoi(valStr)
						if err == nil && v >= 1 && v <= 10080 {
							return v, strconv.Itoa(v)
						}
					}
				}
			}
		}
	}
	return 0, "?"
}

// parseDependsOn reads DEPENDS-ON entries from a task title.
// #n and owner/repo#n entries add no edge because readiness is #3109's job.
func parseDependsOn(title string) []string {
	var deps []string
	for _, seg := range strings.Split(title, "|") {
		seg = strings.TrimSpace(seg)
		seg = strings.TrimPrefix(seg, "**")
		if rest, ok := strings.CutPrefix(seg, "DEPENDS-ON:"); ok {
			rest = strings.TrimSpace(strings.ReplaceAll(rest, "**", ""))
			for _, tok := range splitRx.Split(parenRx.ReplaceAllString(rest, " "), -1) {
				tok = strings.Trim(tok, "`'\".")
				if tok == "" || tok == "-" || strings.EqualFold(tok, "none") || strings.Contains(tok, "#") {
					continue
				}
				deps = append(deps, tok)
			}
		}
	}
	return deps
}

func matchDep(dep, sprint, targetID string) bool {
	if dep == targetID || dep == sprint+"/"+targetID {
		return true
	}
	if !strings.Contains(dep, "/") && dep == targetID {
		return true
	}
	s, id, ok := strings.Cut(dep, "/")
	if ok && s == sprint && id == targetID {
		return true
	}
	return false
}

type taskRecord struct {
	ID       string
	Title    string
	Owner    string
	Priority int
	PushedAt int64
	Score    float64
	State    string
	EstStr   string
	EstVal   int
	Deps     []string
}

// RankTasks computes upward rank for tasks in sprint and returns open tasks
// (for friend as, or all open tasks if as is empty) in take order.
func RankTasks(ctx context.Context, st *store.Store, sprint, as string, stderr io.Writer) ([]TaskRank, error) {
	if st == nil {
		return nil, fmt.Errorf("deal rank: nil store")
	}
	return RankTasksWithClient(ctx, st.Client(), sprint, as, stderr)
}

// RankTasksWithClient computes upward rank using a redis.Cmdable client.
// Reads cost exactly 2 round trips per sprint.
func RankTasksWithClient(ctx context.Context, client redis.Cmdable, sprint, as string, stderr io.Writer) ([]TaskRank, error) {
	if client == nil {
		return nil, fmt.Errorf("deal rank: nil redis client")
	}
	if sprint == "" {
		return nil, fmt.Errorf("deal rank: sprint is required")
	}
	if stderr == nil {
		stderr = io.Discard
	}

	// Round trip 1: ZRANGE WITHSCORES on open zset(s) + SMEMBERS on the 3 live idx sets.
	pipe := client.Pipeline()
	var openCmd *redis.ZSliceCmd
	var readyCmd *redis.ZSliceCmd
	friends := []string{"rowan", "stella", "johnny", "freddy", "emma"}
	var friendOpenCmds map[string]*redis.ZSliceCmd

	if as != "" {
		openCmd = pipe.ZRangeWithScores(ctx, "s:"+sprint+":open:"+as, 0, -1)
	} else {
		readyCmd = pipe.ZRangeWithScores(ctx, "s:"+sprint+":ready", 0, -1)
		friendOpenCmds = make(map[string]*redis.ZSliceCmd, len(friends))
		for _, f := range friends {
			friendOpenCmds[f] = pipe.ZRangeWithScores(ctx, "s:"+sprint+":open:"+f, 0, -1)
		}
	}
	idxOpenCmd := pipe.SMembers(ctx, "s:"+sprint+":idx:task:open")
	idxClaimedCmd := pipe.SMembers(ctx, "s:"+sprint+":idx:task:claimed")
	idxWorkingCmd := pipe.SMembers(ctx, "s:"+sprint+":idx:task:working")

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("deal rank: round trip 1: %w", err)
	}

	openScores := make(map[string]float64)
	openOwners := make(map[string]string)

	if as != "" {
		for _, z := range openCmd.Val() {
			id, _ := z.Member.(string)
			if id != "" {
				openScores[id] = z.Score
				openOwners[id] = as
			}
		}
	} else {
		for _, z := range readyCmd.Val() {
			id, _ := z.Member.(string)
			if id != "" {
				openScores[id] = z.Score
				openOwners[id] = ""
			}
		}
		for f, cmd := range friendOpenCmds {
			for _, z := range cmd.Val() {
				id, _ := z.Member.(string)
				if id != "" {
					openScores[id] = z.Score
					openOwners[id] = f
				}
			}
		}
	}

	allIDsMap := make(map[string]bool)
	for id := range openScores {
		allIDsMap[id] = true
	}
	for _, id := range idxOpenCmd.Val() {
		if id != "" {
			allIDsMap[id] = true
		}
	}
	for _, id := range idxClaimedCmd.Val() {
		if id != "" {
			allIDsMap[id] = true
		}
	}
	for _, id := range idxWorkingCmd.Val() {
		if id != "" {
			allIDsMap[id] = true
		}
	}

	if len(allIDsMap) == 0 {
		return []TaskRank{}, nil
	}

	allIDs := make([]string, 0, len(allIDsMap))
	for id := range allIDsMap {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)

	// Round trip 2: pipelined HMGET (title est priority pushed_at owner state) over every id.
	pipe2 := client.Pipeline()
	hmgetCmos := make(map[string]*redis.SliceCmd, len(allIDs))
	for _, id := range allIDs {
		hmgetCmos[id] = pipe2.HMGet(ctx, "s:"+sprint+":task:"+id,
			"title", "est", "priority", "pushed_at", "owner", "state")
	}
	if _, err := pipe2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("deal rank: round trip 2: %w", err)
	}

	liveTasks := make(map[string]*taskRecord)
	var liveIDs []string

	for _, id := range allIDs {
		vals, err := hmgetCmos[id].Result()
		if err != nil || len(vals) < 6 {
			continue
		}
		title, _ := vals[0].(string)
		storedEst, _ := vals[1].(string)
		priorityStr, _ := vals[2].(string)
		pushedAtStr, _ := vals[3].(string)
		owner, _ := vals[4].(string)
		state, _ := vals[5].(string)

		// Live means exactly open, claimed, or working.
		if state != "open" && state != "claimed" && state != "working" {
			continue
		}

		prio, _ := strconv.Atoi(priorityStr)
		pushedAt, _ := strconv.ParseInt(pushedAtStr, 10, 64)
		estVal, estStr := parseEst(storedEst, title)

		score, hasScore := openScores[id]
		if !hasScore {
			score = float64(prio)
		}

		if owner == "" {
			owner = openOwners[id]
		}

		rec := &taskRecord{
			ID:       id,
			Title:    title,
			Owner:    owner,
			Priority: prio,
			PushedAt: pushedAt,
			Score:    score,
			State:    state,
			EstStr:   estStr,
			EstVal:   estVal,
			Deps:     parseDependsOn(title),
		}
		liveTasks[id] = rec
		liveIDs = append(liveIDs, id)
	}

	// Build dependency edges: if task d has DEPENDS-ON naming t, then t has dependent d.
	dependents := make(map[string][]string)
	for _, dID := range liveIDs {
		d := liveTasks[dID]
		for _, dep := range d.Deps {
			for _, tID := range liveIDs {
				if matchDep(dep, sprint, tID) {
					dependents[tID] = append(dependents[tID], dID)
				}
			}
		}
	}

	// Cycle detection with DFS.
	stateMap := make(map[string]int) // 0: unvisited, 1: visiting, 2: visited
	inCycle := make(map[string]bool)
	reportedCycles := make(map[string]bool)
	var stack []string

	var detectCycles func(u string)
	detectCycles = func(u string) {
		stateMap[u] = 1
		stack = append(stack, u)
		for _, v := range dependents[u] {
			if stateMap[v] == 1 {
				idx := -1
				for i, id := range stack {
					if id == v {
						idx = i
						break
					}
				}
				if idx >= 0 {
					cycleNodes := append([]string(nil), stack[idx:]...)
					cycleNodes = append(cycleNodes, v)
					cycleStr := strings.Join(cycleNodes, ">")
					if !reportedCycles[cycleStr] {
						reportedCycles[cycleStr] = true
						fmt.Fprintf(stderr, "CYCLE %s\n", cycleStr)
					}
					for _, id := range stack[idx:] {
						inCycle[id] = true
					}
				}
			} else if stateMap[v] == 0 {
				detectCycles(v)
			}
		}
		stack = stack[:len(stack)-1]
		stateMap[u] = 2
	}

	for _, id := range liveIDs {
		if stateMap[id] == 0 {
			detectCycles(id)
		}
	}

	// Compute upward rank with memoization.
	rankMemo := make(map[string]int)
	chainMemo := make(map[string]string)
	computed := make(map[string]bool)

	var computeRank func(u string) (int, string)
	computeRank = func(u string) (int, string) {
		if computed[u] {
			return rankMemo[u], chainMemo[u]
		}
		task := liveTasks[u]
		if inCycle[u] {
			computed[u] = true
			rankMemo[u] = task.EstVal
			chainMemo[u] = u
			return task.EstVal, u
		}
		maxDepRank := 0
		var bestDep string
		var bestChain string
		hasDep := false
		for _, v := range dependents[u] {
			if inCycle[v] {
				// Don't follow into cycles
				continue
			}
			r, c := computeRank(v)
			if !hasDep || r > maxDepRank {
				hasDep = true
				maxDepRank = r
				bestDep = v
				bestChain = c
			} else if r == maxDepRank {
				bestTask := liveTasks[bestDep]
				curTask := liveTasks[v]
				if curTask.Score < bestTask.Score ||
					(curTask.Score == bestTask.Score && curTask.PushedAt < bestTask.PushedAt) ||
					(curTask.Score == bestTask.Score && curTask.PushedAt == bestTask.PushedAt && v < bestDep) {
					bestDep = v
					bestChain = c
				}
			}
		}
		totalRank := task.EstVal + maxDepRank
		chain := u
		if bestDep != "" {
			chain = u + ">" + bestChain
		}
		computed[u] = true
		rankMemo[u] = totalRank
		chainMemo[u] = chain
		return totalRank, chain
	}

	for _, id := range liveIDs {
		computeRank(id)
	}

	// Filter candidate tasks for open queue.
	var candidates []TaskRank
	for _, id := range liveIDs {
		task := liveTasks[id]
		if task.State != "open" {
			continue
		}
		if as != "" {
			if _, inOpen := openScores[id]; !inOpen {
				continue
			}
		}
		candidates = append(candidates, TaskRank{
			Sprint:   sprint,
			ID:       task.ID,
			Owner:    task.Owner,
			Front:    task.Score < 0,
			Rank:     rankMemo[task.ID],
			Est:      task.EstStr,
			EstVal:   task.EstVal,
			Chain:    chainMemo[task.ID],
			Score:    task.Score,
			Priority: task.Priority,
			PushedAt: task.PushedAt,
			State:    task.State,
		})
	}

	// Sort candidates in Take Order:
	// 1. front (score < 0) first;
	// 2. then rank, descending;
	// 3. then score, ascending (priority);
	// 4. then pushed_at, ascending;
	// 5. then id.
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Front != b.Front {
			return a.Front
		}
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		if a.PushedAt != b.PushedAt {
			return a.PushedAt < b.PushedAt
		}
		return a.ID < b.ID
	})

	return candidates, nil
}
