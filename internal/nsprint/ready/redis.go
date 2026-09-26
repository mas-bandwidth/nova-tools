package ready

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// The state indexes ready reads (#2756 2.2): candidates, then in flight. The
// index only names candidates; the hash's state decides.
var (
	cardCandidate = []string{"queued"}
	cardInFlight  = []string{"dealt", "running"}
	taskCandidate = []string{"open", "waiting"}
	taskInFlight  = []string{"claimed", "working"}
)

var itemFields = []string{"state", "depends_on", "paths", "repo", "base", "priority", "blocked_on", "wait_on"}

// Read takes one Snapshot from Redis in four pipelined rounds: the sprints,
// the state indexes, the item hashes (a queued card's priority is its
// record's field; the pool is scored by age, #3692), then the cards and
// tasks named in DEPENDS-ON. sprint limits it to one
// sprint (read whatever its status); empty reads every open sprint in
// sprint:order. Nothing is read with SCAN or KEYS.
func Read(ctx context.Context, c *redis.Client, sprint string) (Snapshot, error) {
	if c == nil {
		return Snapshot{}, errors.New("ready: nil redis client")
	}
	sprints := []string{sprint}
	if sprint == "" {
		var err error
		if sprints, err = openSprints(ctx, c); err != nil {
			return Snapshot{}, err
		}
	}

	type idx struct {
		sprint, kind string
		inFlight     bool
		cmd          *redis.StringSliceCmd
	}
	var idxs []idx
	pipe := c.Pipeline()
	for _, s := range sprints {
		add := func(kind string, states []string, inFlight bool) {
			for _, st := range states {
				idxs = append(idxs, idx{s, kind, inFlight, pipe.SMembers(ctx, "s:"+s+":idx:"+kind+":"+st)})
			}
		}
		add(KindCard, cardCandidate, false)
		add(KindCard, cardInFlight, true)
		add(KindTask, taskCandidate, false)
		add(KindTask, taskInFlight, true)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Snapshot{}, fmt.Errorf("ready: state indexes: %w", err)
	}

	type itemCmd struct {
		sprint, kind, id string
		inFlight         bool
		hash             *redis.SliceCmd
	}
	var items []itemCmd
	seen := map[string]bool{}
	pipe = c.Pipeline()
	for _, x := range idxs {
		ids := x.cmd.Val()
		sort.Strings(ids)
		for _, id := range ids {
			k := x.sprint + "/" + x.kind + "/" + id
			if id == "" || seen[k] {
				continue
			}
			seen[k] = true
			ic := itemCmd{sprint: x.sprint, kind: x.kind, id: id, inFlight: x.inFlight,
				hash: pipe.HMGet(ctx, itemKey(x.sprint, x.kind, id), itemFields...)}
			items = append(items, ic)
		}
	}
	if len(items) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return Snapshot{}, fmt.Errorf("ready: item hashes: %w", err)
		}
	}

	order := map[string]int{}
	for i, s := range sprints {
		order[s] = i
	}
	var snap Snapshot
	for _, ic := range items {
		v := ic.hash.Val()
		deps := str(v, 1)
		if deps == "" {
			deps = str(v, 6) // a stream card's DEPENDS-ON is its blocked_on
		}
		it := Item{
			Sprint: ic.sprint, ID: ic.id, Kind: ic.kind, State: str(v, 0),
			DependsOn: ws.SplitDeps(deps), Paths: splitList(str(v, 2)),
			Repo: str(v, 3), Base: str(v, 4), WaitOn: str(v, 7),
		}
		it.Priority, _ = strconv.ParseFloat(str(v, 5), 64)
		switch {
		case ic.inFlight && in(it.State, cardInFlight, taskInFlight):
			snap.InFlight = append(snap.InFlight, it)
		case !ic.inFlight && in(it.State, cardCandidate, taskCandidate):
			snap.Candidates = append(snap.Candidates, it)
		}
	}
	sort.SliceStable(snap.Candidates, func(i, j int) bool {
		a, b := snap.Candidates[i], snap.Candidates[j]
		if order[a.Sprint] != order[b.Sprint] {
			return order[a.Sprint] < order[b.Sprint]
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})

	deps, tasks, err := readDeps(ctx, c, snap.Candidates)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Deps, snap.Tasks = deps, tasks
	return snap, nil
}

func openSprints(ctx context.Context, c *redis.Client) ([]string, error) {
	pipe := c.Pipeline()
	order := pipe.ZRange(ctx, "sprint:order", 0, -1)
	open := pipe.SMembers(ctx, "sprints")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("ready: sprints: %w", err)
	}
	openSet := map[string]bool{}
	for _, s := range open.Val() {
		openSet[s] = true
	}
	var names []string
	for _, s := range order.Val() {
		if openSet[s] {
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	pipe = c.Pipeline()
	status := make([]*redis.StringCmd, len(names))
	for i, s := range names {
		status[i] = pipe.HGet(ctx, "s:"+s, "status")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("ready: sprint status: %w", err)
	}
	var out []string
	for i, s := range names {
		if status[i].Val() == "open" {
			out = append(out, s)
		}
	}
	return out, nil
}

// readDeps reads each sprint card and task record a candidate names, in one
// pipelined round. A card hash wins over a task hash of the same id; a
// task:<id> entry, or a stream's <slug>:sentinel, is a task record alone.
func readDeps(ctx context.Context, c *redis.Client, cands []Item) (map[string]deal.DepCard, map[string]TaskDep, error) {
	var keys []string
	seen := map[string]bool{}
	for _, it := range cands {
		for _, e := range it.DependsOn {
			if isNone(e) {
				continue
			}
			if _, _, ok := parseRef(e); ok {
				continue
			}
			k := it.Sprint + "/" + e
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	out, tasks := map[string]deal.DepCard{}, map[string]TaskDep{}
	if len(keys) == 0 {
		return out, tasks, nil
	}
	sort.Strings(keys)
	pipe := c.Pipeline()
	cards := make([]*redis.SliceCmd, len(keys))
	recs := make([]*redis.SliceCmd, len(keys))
	for i, k := range keys {
		slash := strings.IndexByte(k, '/')
		S, e := k[:slash], k[slash+1:]
		id, isTask := strings.CutPrefix(e, "task:")
		if !isTask && !ws.IsSentinel(id) {
			cards[i] = pipe.HMGet(ctx, "s:"+S+":card:"+id, "state", "outcome", "repo", "base", "pr", "pushed_sha")
		}
		recs[i] = pipe.HMGet(ctx, "task:"+id, "state", "where", "where_ok")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, fmt.Errorf("ready: dependencies: %w", err)
	}
	for i, k := range keys {
		if cards[i] != nil {
			if v := cards[i].Val(); str(v, 0) != "" {
				pr, _ := strconv.Atoi(str(v, 4))
				out[k] = deal.DepCard{Found: true, State: str(v, 0), Outcome: str(v, 1), Repo: str(v, 2), Base: str(v, 3), PR: pr, PushedSHA: str(v, 5)}
				continue
			}
		}
		if v := recs[i].Val(); str(v, 0) != "" || str(v, 1) != "" {
			id := strings.TrimPrefix(k[strings.IndexByte(k, '/')+1:], "task:")
			tasks[id] = TaskDep{State: str(v, 0), Where: str(v, 1), WhereOK: str(v, 2)}
		}
	}
	return out, tasks, nil
}

func in(s string, lists ...[]string) bool {
	for _, l := range lists {
		for _, x := range l {
			if x == s {
				return true
			}
		}
	}
	return false
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func str(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

// itemKey is a card's record (s:<S>:card:<id>) or a task's (task:<id>: one
// task store, nova-tools #3778).
func itemKey(sprint, kind, id string) string {
	if kind == "task" {
		return "task:" + id
	}
	return "s:" + sprint + ":" + kind + ":" + id
}
