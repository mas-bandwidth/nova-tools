package task

// rebalance (#3206 rev 5 PR A): a port of bash friend-queue's RB_PLAN rules,
// one for one, onto the one store. The friend list is SMEMBERS friends (never
// KEYS); moves go through ns_task_move.
//
//   - A target is UP (desired slots > 0, beat present, no down marker) with
//     ready < floor; it wants floor - ready (capped by MaxMove). It draws from
//     the fullest ready queue with ready > floor, recomputed after every move;
//     a donor never drops below the floor, so a second pass moves nothing.
//   - Only kinds read, fix, rebase and recut move; a read only when its
//     author is known and is not the target (hash field `author`, else exactly
//     one known name as a branch prefix "<name>/" in the title or ref).
//   - A hold release naming a holder other than the target never moves,
//     except a down holder's release to Releaser; a title that says hold any
//     other way is unsure and never moves.
//   - (spec (1), down row) A down friend's ready work moves off it in the same
//     pass, every kind, each task to the UP friend with the fewest ready that
//     the author and hold rules allow. A down friend is never a target.
//
// Waiting tasks are on no queue, so they are never counted or moved.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// RebalanceFriend is one friend's facts for the plan.
type RebalanceFriend struct {
	Name string
	Up   bool
	Down bool
	// Ready is the friend's ready queue in order (front first).
	Ready []RebalanceTask
}

// RebalanceTask is one ready task's facts.
type RebalanceTask struct {
	ID, Kind, Author, Title, Ref string
}

// RebalanceMove is one planned move.
type RebalanceMove struct {
	ID, From, To string
}

// RebalancePlanInput is everything the plan decides from.
type RebalancePlanInput struct {
	Friends  []RebalanceFriend
	Floor    int
	MaxMove  int // 0 = no cap
	Releaser string
	Known    []string // names the author and holder rules may match
}

var (
	holdNamedRx = regexp.MustCompile(`([a-z][a-z0-9_-]*)'s (typed )?hold`)
	holdYourRx  = regexp.MustCompile(`your (typed )?hold`)
	holdAnyRx   = regexp.MustCompile(`(^|[^a-z])holds?([^a-z]|$)`)
)

// PlanRebalance is the pure decision: the moves, down friends first, then
// the floor pass in friend-name order.
func PlanRebalance(in RebalancePlanInput) []RebalanceMove {
	friends := append([]RebalanceFriend(nil), in.Friends...)
	sort.Slice(friends, func(i, j int) bool { return friends[i].Name < friends[j].Name })
	known := map[string]bool{}
	for _, k := range in.Known {
		known[strings.ToLower(k)] = true
	}
	down := map[string]bool{}
	ready := map[string]int{}
	queue := map[string][]RebalanceTask{}
	for _, f := range friends {
		down[f.Name] = f.Down
		ready[f.Name] = len(f.Ready)
		queue[f.Name] = f.Ready
	}
	used := map[string]bool{}
	var moves []RebalanceMove

	holder := func(t RebalanceTask, donor string) string {
		s := strings.ToLower(t.Title)
		if m := holdNamedRx.FindStringSubmatch(s); m != nil {
			return m[1]
		}
		if holdYourRx.MatchString(s) {
			return donor
		}
		if strings.HasPrefix(t.ID, "release-") {
			parts := strings.Split(t.ID, "-")
			if last := parts[len(parts)-1]; known[last] {
				return last
			}
			return donor
		}
		if holdAnyRx.MatchString(s) {
			return "?"
		}
		return ""
	}
	author := func(t RebalanceTask) string {
		if t.Author != "" {
			return strings.ToLower(t.Author)
		}
		s := " " + strings.ToLower(t.Title) + " " + strings.ToLower(t.Ref) + " "
		found, n := "", 0
		for k := range known {
			if regexp.MustCompile(`[^a-z0-9_-]` + regexp.QuoteMeta(k) + `/`).MatchString(s) {
				found, n = k, n+1
			}
		}
		if n == 1 {
			return found
		}
		return ""
	}
	allowed := func(t RebalanceTask, donor, target string, anyKind bool) bool {
		if used[t.ID] {
			return false
		}
		switch Kind(t.Kind) {
		case KindRead, KindFix, KindRebase, KindRecut:
		default:
			if !anyKind {
				return false
			}
		}
		h := holder(t, donor)
		if h == "?" {
			return false
		}
		if h != "" && h != target && (!down[h] || target != in.Releaser) {
			return false
		}
		if Kind(t.Kind) == KindRead {
			a := author(t)
			if a == "" || a == target {
				return false
			}
		}
		return true
	}
	move := func(t RebalanceTask, from, to string) {
		used[t.ID] = true
		ready[from]--
		ready[to]++
		moves = append(moves, RebalanceMove{ID: t.ID, From: from, To: to})
	}

	// Down friends first: every ready task goes to the least-loaded UP friend
	// the rules allow.
	for _, g := range friends {
		if !g.Down {
			continue
		}
		for _, t := range queue[g.Name] {
			best := ""
			for _, f := range friends {
				if !f.Up || f.Down || f.Name == g.Name || !allowed(t, g.Name, f.Name, true) {
					continue
				}
				if best == "" || ready[f.Name] < ready[best] {
					best = f.Name
				}
			}
			if best != "" {
				move(t, g.Name, best)
			}
		}
	}

	// The floor pass (RB_PLAN).
	for _, tf := range friends {
		t := tf.Name
		if !tf.Up || tf.Down || ready[t] >= in.Floor {
			continue
		}
		want := in.Floor - ready[t]
		if in.MaxMove > 0 && in.MaxMove < want {
			want = in.MaxMove
		}
		dry := map[string]bool{}
		for moved := 0; moved < want; {
			best := ""
			for _, g := range friends {
				if g.Name == t || dry[g.Name] || ready[g.Name] <= in.Floor {
					continue
				}
				if best == "" || ready[g.Name] > ready[best] {
					best = g.Name
				}
			}
			if best == "" {
				break
			}
			picked := false
			for _, cand := range queue[best] {
				if allowed(cand, best, t, false) {
					move(cand, best, t)
					moved++
					picked = true
					break
				}
			}
			if !picked {
				dry[best] = true
			}
		}
	}
	return moves
}

// RebalanceRequest is one rebalance pass.
type RebalanceRequest struct {
	Sprint   string
	Floor    int // 0 = s:<S>:policy rebalance_floor
	MaxMove  int
	Releaser string
	Authors  []string // extra known author names beside `friends`
	Actor    string
	Idem     string
}

// Rebalance reads the friends and their ready queues, plans, and sends one
// ns_task_move per move in one pipeline. It returns the moves that the
// function accepted (MOVED).
func Rebalance(ctx context.Context, st *store.Store, req RebalanceRequest) ([]RebalanceMove, error) {
	if err := need("rebalance", "sprint", req.Sprint); err != nil {
		return nil, err
	}
	client := st.Client()
	S := req.Sprint

	pipe := client.Pipeline()
	membersCmd := pipe.SMembers(ctx, "friends")
	floorCmd := pipe.HGet(ctx, "s:"+S+":policy", "rebalance_floor")
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("task rebalance: read friends: %w", err)
	}
	names := membersCmd.Val()
	sort.Strings(names)
	floor := req.Floor
	if floor == 0 {
		floor, _ = strconv.Atoi(floorCmd.Val())
	}
	// With no floor only a down friend's ready work moves.
	if floor < 0 {
		floor = 0
	}
	if len(names) == 0 {
		return nil, nil
	}

	pipe = client.Pipeline()
	type fcmds struct {
		slots *redis.StringCmd
		beat  *redis.IntCmd
		down  *redis.IntCmd
		open  *redis.StringSliceCmd
	}
	cmds := make([]fcmds, len(names))
	for i, f := range names {
		cmds[i] = fcmds{
			slots: pipe.HGet(ctx, "friend:"+f+":desired", "slots"),
			beat:  pipe.Exists(ctx, "friend:"+f+":beat"),
			down:  pipe.Exists(ctx, "friend:"+f+":down"),
			open:  pipe.ZRange(ctx, "s:"+S+":open:"+f, 0, -1),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("task rebalance: read queues: %w", err)
	}

	pipe = client.Pipeline()
	hashes := map[string]*redis.SliceCmd{}
	for i := range names {
		for _, id := range cmds[i].open.Val() {
			hashes[id] = pipe.HMGet(ctx, "s:"+S+":task:"+id, "state", "kind", "author", "title", "ref")
		}
	}
	if len(hashes) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return nil, fmt.Errorf("task rebalance: read tasks: %w", err)
		}
	}

	in := RebalancePlanInput{Floor: floor, MaxMove: req.MaxMove, Releaser: req.Releaser}
	in.Known = append(append(in.Known, names...), req.Authors...)
	for i, f := range names {
		slots, _ := strconv.Atoi(cmds[i].slots.Val())
		isDown := cmds[i].down.Val() == 1
		rf := RebalanceFriend{
			Name: f,
			Down: isDown,
			Up:   slots > 0 && cmds[i].beat.Val() == 1 && !isDown,
		}
		for _, id := range cmds[i].open.Val() {
			v := hashes[id].Val()
			if str(v, 0) != "open" {
				continue
			}
			rf.Ready = append(rf.Ready, RebalanceTask{ID: id, Kind: str(v, 1), Author: str(v, 2), Title: str(v, 3), Ref: str(v, 4)})
		}
		in.Friends = append(in.Friends, rf)
	}
	plan := PlanRebalance(in)
	if len(plan) == 0 {
		return nil, nil
	}

	pipe = client.Pipeline()
	replies := make([]*redis.Cmd, len(plan))
	for i, m := range plan {
		replies[i] = pipe.FCall(ctx, FunctionMove, nil, S, m.ID, m.To, req.Actor, req.Idem)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("task rebalance: move: %w", err)
	}
	var done []RebalanceMove
	for i, m := range plan {
		values, _ := replies[i].Val().([]any)
		if len(values) > 0 && fmt.Sprint(values[0]) == "MOVED" {
			done = append(done, m)
		}
	}
	return done, nil
}
