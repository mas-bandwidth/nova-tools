package width

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// DutyActor is the actor the width duty writes on receipts.
const DutyActor = "reconciler-width"

// Duty is the reconciler's width duty.
type Duty struct {
	Store          *store.Store
	Policy         Policy
	Actor          string
	RebalanceAfter time.Duration

	mu sync.Mutex
}

// Run executes one width duty pass under the lease.
func (d *Duty) Run(ctx context.Context, l *reconcile.Lease) (reconcile.Counts, error) {
	if d.Store == nil || l == nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: store and lease are required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	client := d.Store.Client()

	// Round 1: Pipeline reading friends, sprints, sprint:order, and TIME.
	pipe1 := client.Pipeline()
	friendsCmd := pipe1.SMembers(ctx, "friends")
	sprintsCmd := pipe1.SMembers(ctx, "sprints")
	orderCmd := pipe1.ZRange(ctx, "sprint:order", 0, -1)
	timeCmd := pipe1.Time(ctx)
	if _, err := pipe1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return reconcile.Counts{}, fmt.Errorf("width duty: round 1: %w", err)
	}

	friends := friendsCmd.Val()
	sort.Strings(friends)

	nowMs := timeCmd.Val().UnixMilli()

	sprintsSet := make(map[string]bool, len(sprintsCmd.Val()))
	for _, s := range sprintsCmd.Val() {
		sprintsSet[s] = true
	}
	var openSprints []string
	for _, s := range orderCmd.Val() {
		if sprintsSet[s] {
			openSprints = append(openSprints, s)
		}
	}
	for _, s := range sprintsCmd.Val() {
		found := false
		for _, os := range openSprints {
			if os == s {
				found = true
				break
			}
		}
		if !found {
			openSprints = append(openSprints, s)
		}
	}

	if len(friends) == 0 {
		return reconcile.Counts{}, nil
	}

	// Round 2: One pipeline over all friends.
	type friendCmds struct {
		desired       *redis.StringCmd
		starting      *redis.IntCmd
		living        *redis.IntCmd
		livingScores  *redis.ZSliceCmd
		beat          *redis.IntCmd
		fillstate     *redis.MapStringStringCmd
		openPerSprint map[string]*redis.StringSliceCmd
	}

	fcmds := make(map[string]*friendCmds, len(friends))
	pipe2 := client.Pipeline()
	for _, f := range friends {
		fc := &friendCmds{
			desired:       pipe2.HGet(ctx, "friend:"+f+":desired", "slots"),
			starting:      pipe2.ZCard(ctx, "friend:"+f+":starting"),
			living:        pipe2.ZCard(ctx, "friend:"+f+":living"),
			livingScores:  pipe2.ZRangeWithScores(ctx, "friend:"+f+":living", 0, -1),
			beat:          pipe2.Exists(ctx, "friend:"+f+":beat"),
			fillstate:     pipe2.HGetAll(ctx, "friend:"+f+":fillstate"),
			openPerSprint: make(map[string]*redis.StringSliceCmd, len(openSprints)),
		}
		for _, S := range openSprints {
			fc.openPerSprint[S] = pipe2.ZRange(ctx, "s:"+S+":open:"+f, 0, -1)
		}
		fcmds[f] = fc
	}
	if _, err := pipe2.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return reconcile.Counts{}, fmt.Errorf("width duty: round 2: %w", err)
	}

	// Process Round 2 replies and gather open tasks for Round 3.
	type taskRef struct {
		sprint string
		id     string
		friend string
	}
	var allOpenTasks []taskRef

	type friendComputed struct {
		slots         int
		starting      int
		living        int
		leased        int
		working       int
		deficit       int
		up            bool
		prevPeak      int
		prevPeakAt    int64
		unfilledSince int64
		fillAt        int64
	}

	computed := make(map[string]*friendComputed, len(friends))
	for _, f := range friends {
		fc := fcmds[f]
		desiredSlots, _ := strconv.Atoi(fc.desired.Val())
		starting := int(fc.starting.Val())
		living := int(fc.living.Val())
		w := task.WidthFrom(desiredSlots, starting, living)

		working := 0
		for _, z := range fc.livingScores.Val() {
			if int64(z.Score) >= nowMs-60000 {
				working++
			}
		}
		if working > living {
			working = living
		}

		fsMap := fc.fillstate.Val()
		pPeak, _ := strconv.Atoi(fsMap["peak"])
		pPeakAt, _ := strconv.ParseInt(fsMap["peak_at"], 10, 64)
		unfSince, _ := strconv.ParseInt(fsMap["unfilled_since"], 10, 64)
		fillAt, _ := strconv.ParseInt(fsMap["fill_at"], 10, 64)

		computed[f] = &friendComputed{
			slots:         w.Desired,
			starting:      w.Starting,
			living:        w.Living,
			leased:        w.Leased,
			working:       working,
			deficit:       w.Free,
			up:            fc.beat.Val() == 1,
			prevPeak:      pPeak,
			prevPeakAt:    pPeakAt,
			unfilledSince: unfSince,
			fillAt:        fillAt,
		}

		for _, S := range openSprints {
			for _, id := range fc.openPerSprint[S].Val() {
				allOpenTasks = append(allOpenTasks, taskRef{sprint: S, id: id, friend: f})
			}
		}
	}

	// Round 3: One pipeline of HMGET on all open tasks across all friends.
	type friendTaskCounts struct {
		eligible int
		deps     int
		input    int
	}
	taskCounts := make(map[string]*friendTaskCounts, len(friends))
	for _, f := range friends {
		taskCounts[f] = &friendTaskCounts{}
	}

	if len(allOpenTasks) > 0 {
		pipe3 := client.Pipeline()
		taskCmds := make([]*redis.SliceCmd, len(allOpenTasks))
		for i, t := range allOpenTasks {
			taskCmds[i] = pipe3.HMGet(ctx, "s:"+t.sprint+":task:"+t.id, "state", "kind", "depends_on", "repo", "pr", "head", "blocked_reason")
		}
		if _, err := pipe3.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return reconcile.Counts{}, fmt.Errorf("width duty: round 3: %w", err)
		}

		for i, t := range allOpenTasks {
			val := taskCmds[i].Val()
			state := strVal(val, 0)
			dependsOn := strVal(val, 2)
			reason := strVal(val, 6)

			tc := taskCounts[t.friend]
			if state == "blocked" || state == "input" {
				if reason == "deps" {
					tc.deps++
				} else {
					tc.input++
				}
			} else if reason == "deps" || state == "deps" {
				tc.deps++
			} else if dependsOn != "" && dependsOn != "-" && dependsOn != "none" {
				// Task has unmerged dependencies
				tc.deps++
			} else {
				tc.eligible++
			}
		}
	}

	rebalAfter := d.RebalanceAfter
	if rebalAfter == 0 {
		rebalAfter = 30 * time.Second
	}
	rebalAfterMs := rebalAfter.Milliseconds()

	// Build arguments for ns_width_write
	writeArgs := []any{l.Token()}
	for _, f := range friends {
		c := computed[f]
		tc := taskCounts[f]

		eligible := tc.eligible
		if eligible > c.deficit {
			eligible = c.deficit
		}
		rem := c.deficit - eligible

		idleDeps := tc.deps
		if idleDeps > rem {
			idleDeps = rem
		}
		rem -= idleDeps

		idleInput := tc.input
		if idleInput > rem {
			idleInput = rem
		}
		rem -= idleInput

		idleNoReady := rem

		idleUnfilled := 0
		unfilledSince := c.unfilledSince
		if c.up && eligible > 0 {
			if c.fillAt >= unfilledSince && c.fillAt > 0 {
				unfilledSince = nowMs
				idleUnfilled = 0
			} else if unfilledSince == 0 {
				unfilledSince = nowMs
				idleUnfilled = 0
			} else if nowMs-unfilledSince > rebalAfterMs {
				idleUnfilled = eligible
			} else {
				idleUnfilled = 0
			}
		} else {
			unfilledSince = 0
			idleUnfilled = 0
		}

		peak := c.prevPeak
		peakAt := c.prevPeakAt
		if c.working > peak || peak == 0 {
			peak = c.working
			peakAt = nowMs
		} else if nowMs-peakAt > 24*3600*1000 {
			peak = c.working
			peakAt = nowMs
		}

		writeArgs = append(writeArgs,
			f,
			strconv.Itoa(c.slots),
			strconv.Itoa(c.leased),
			strconv.Itoa(c.working),
			strconv.Itoa(c.deficit),
			strconv.Itoa(eligible),
			strconv.Itoa(idleNoReady),
			strconv.Itoa(idleDeps),
			strconv.Itoa(idleInput),
			strconv.Itoa(idleUnfilled),
			strconv.Itoa(peak),
			strconv.FormatInt(peakAt, 10),
			strconv.FormatInt(unfilledSince, 10),
			strconv.Itoa(c.starting),
			strconv.Itoa(c.living),
		)
	}

	reply, err := client.FCall(ctx, FunctionWrite, nil, writeArgs...).Result()
	if err != nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: write: %w", err)
	}
	if slice, ok := reply.([]any); ok && len(slice) > 0 && slice[0] == "FENCED" {
		return reconcile.Counts{}, reconcile.ErrFenced
	}

	return reconcile.Counts{}, nil
}

func strVal(val []any, idx int) string {
	if idx >= len(val) || val[idx] == nil {
		return ""
	}
	return fmt.Sprint(val[idx])
}
