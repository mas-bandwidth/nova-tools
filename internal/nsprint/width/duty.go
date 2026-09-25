package width

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
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
	PRs            deal.PRs

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

	// Round 1: Pipeline reading friends, sprints, sprint:order, TIME, and readers.
	pipe1 := client.Pipeline()
	friendsCmd := pipe1.SMembers(ctx, "friends")
	sprintsCmd := pipe1.SMembers(ctx, "sprints")
	orderCmd := pipe1.ZRange(ctx, "sprint:order", 0, -1)
	timeCmd := pipe1.Time(ctx)
	widthReadersCmd := pipe1.SMembers(ctx, "width:readers")
	readersCmd := pipe1.SMembers(ctx, "readers")
	if _, err := pipe1.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return reconcile.Counts{}, fmt.Errorf("width duty: round 1: %w", err)
	}

	friends := friendsCmd.Val()
	sort.Strings(friends)

	// Determine active readers for READ-BOUND evaluation
	var policyReaders []string
	var activeReaders []string
	if len(d.Policy.Readers) > 0 {
		policyReaders = d.Policy.Readers
		activeReaders = d.Policy.Readers
	} else {
		activeReaders = widthReadersCmd.Val()
		if len(activeReaders) == 0 {
			activeReaders = readersCmd.Val()
		}
	}

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
		held          *redis.StringSliceCmd
		beat          *redis.IntCmd
		fillstate     *redis.MapStringStringCmd
		openPerSprint map[string]*redis.StringSliceCmd
	}

	fcmds := make(map[string]*friendCmds, len(friends))
	pipe2 := client.Pipeline()
	for _, f := range friends {
		fc := &friendCmds{
			desired:       pipe2.HGet(ctx, "friend:"+f+":desired", "slots"),
			held:          pipe2.ZRange(ctx, "friend:"+f+":cards:working", 0, -1),
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
		leased        int
		working       int
		deficit       int
		up            bool
		prevPeak      int
		prevPeakAt    int64
		unfilledSince int64
		fillAt        int64
	}

	// Round 2b: the friend's one working set (#3998) holds its leases;
	// a friend-queue take is working once its task is working with a beat
	// inside 60 s, read in one pipeline (skipped when no friend holds one).
	// Since one task store (#3907) the take is the task card itself (task
	// <id>, its record carrying the take's attempt); an older lease member
	// <S>/<id>/<attempt> reads the same record. A copy, a sprint card or a
	// task card moved by the card verbs (no attempt) is working.
	beats := map[string][]*redis.SliceCmd{}
	pipe3 := client.Pipeline()
	for _, f := range friends {
		for _, m := range fcmds[f].held.Val() {
			id := m
			if parts := strings.SplitN(m, "/", 3); len(parts) == 3 {
				id = parts[1]
			} else if strings.HasPrefix(m, "s:") || copyMember.MatchString(m) {
				continue
			}
			beats[f] = append(beats[f], pipe3.HMGet(ctx, "task:"+id, "state", "beat_at", "attempt"))
		}
	}
	if len(beats) > 0 {
		if _, err := pipe3.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return reconcile.Counts{}, fmt.Errorf("width duty: round 2b: %w", err)
		}
	}

	computed := make(map[string]*friendComputed, len(friends))
	for _, f := range friends {
		fc := fcmds[f]
		desiredSlots, _ := strconv.Atoi(fc.desired.Val())
		held := len(fc.held.Val())
		w := task.WidthFrom(desiredSlots, held)
		working := held - len(beats[f])
		for _, b := range beats[f] {
			v := b.Val()
			state, _ := v[0].(string)
			raw, _ := v[1].(string)
			attempt, _ := v[2].(string)
			at, err := strconv.ParseInt(raw, 10, 64)
			if attempt == "" || (state == "working" && err == nil && at >= nowMs-60000) {
				working++
			}
		}

		fsMap := fc.fillstate.Val()
		pPeak, _ := strconv.Atoi(fsMap["peak"])
		pPeakAt, _ := strconv.ParseInt(fsMap["peak_at"], 10, 64)
		unfSince, _ := strconv.ParseInt(fsMap["unfilled_since"], 10, 64)
		fillAt, _ := strconv.ParseInt(fsMap["fill_at"], 10, 64)

		computed[f] = &friendComputed{
			slots:         w.Desired,
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

	hasOpenRead := false
	if len(allOpenTasks) > 0 {
		pipe3 := client.Pipeline()
		taskCmds := make([]*redis.SliceCmd, len(allOpenTasks))
		for i, t := range allOpenTasks {
			taskCmds[i] = pipe3.HMGet(ctx, "task:"+t.id, "state", "kind", "depends_on", "repo", "pr", "head", "blocked_reason", "base")
		}
		if _, err := pipe3.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return reconcile.Counts{}, fmt.Errorf("width duty: round 3: %w", err)
		}

		seenDedup := make(map[string]bool)
		for i, t := range allOpenTasks {
			val := taskCmds[i].Val()
			state := strVal(val, 0)
			kind := strVal(val, 1)
			dependsOn := strVal(val, 2)
			repo := strVal(val, 3)
			pr := strVal(val, 4)
			head := strVal(val, 5)
			reason := strVal(val, 6)
			base := strVal(val, 7)

			if state == "open" && (kind == "read" || kind == "review") {
				hasOpenRead = true
			}

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
				isLanded := true
				for _, dep := range splitDeps(dependsOn) {
					if dep == "" || dep == "-" || dep == "none" {
						continue
					}
					if d.PRs != nil {
						if r, n, ok := parseDepRef(dep); ok {
							ref, err := d.PRs.Ref(ctx, r, n)
							if why := deal.LandedPR(ref, err, dep, base); why != "" {
								isLanded = false
								break
							}
							continue
						}
					}
					isLanded = false
					break
				}
				if !isLanded {
					tc.deps++
				} else {
					ident := t.friend + "|" + repo + "|" + pr + "|" + head
					if repo != "" && pr != "" && head != "" && seenDedup[ident] {
						// dedup
					} else {
						if repo != "" && pr != "" && head != "" {
							seenDedup[ident] = true
						}
						tc.eligible++
					}
				}
			} else {
				ident := t.friend + "|" + repo + "|" + pr + "|" + head
				if repo != "" && pr != "" && head != "" && seenDedup[ident] {
					// dedup
				} else {
					if repo != "" && pr != "" && head != "" {
						seenDedup[ident] = true
					}
					tc.eligible++
				}
			}
		}
	}

	rebalAfter := d.RebalanceAfter
	if rebalAfter == 0 {
		rebalAfter = 30 * time.Second
	}
	rebalAfterMs := rebalAfter.Milliseconds()

	idleUnfilledMap := make(map[string]int)

	// READ-BOUND evaluation
	readBound := false
	if len(activeReaders) > 0 {
		allDeficitZero := true
		hasUpReader := false
		for _, r := range activeReaders {
			rc := computed[r]
			if rc != nil && rc.up {
				hasUpReader = true
				if rc.deficit > 0 {
					allDeficitZero = false
					break
				}
			}
		}
		if hasUpReader && allDeficitZero && hasOpenRead {
			readBound = true
		}
	}
	readBoundVal := "0"
	if readBound {
		readBoundVal = "1"
	}

	// Build arguments for ns_width_write:
	// fence, read_bound, num_policy_readers, [policy_readers...], num_open_sprints, [open_sprints...], num_friends, [friends_fields...]
	writeArgs := []any{
		l.Token(),
		readBoundVal,
		strconv.Itoa(len(policyReaders)),
	}
	for _, r := range policyReaders {
		writeArgs = append(writeArgs, r)
	}
	writeArgs = append(writeArgs, strconv.Itoa(len(openSprints)))
	for _, S := range openSprints {
		writeArgs = append(writeArgs, S)
	}
	writeArgs = append(writeArgs, strconv.Itoa(len(friends)))

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
		idleUnfilledMap[f] = idleUnfilled

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
		)
	}

	reply, err := client.FCall(ctx, FunctionWrite, nil, writeArgs...).Result()
	if err != nil {
		return reconcile.Counts{}, fmt.Errorf("width duty: write: %w", err)
	}
	if slice, ok := reply.([]any); ok && len(slice) > 0 && slice[0] == "FENCED" {
		return reconcile.Counts{}, reconcile.ErrFenced
	}

	// Rebalance eligible build/fix tasks from underfull friends
	actor := d.Actor
	if actor == "" {
		actor = DutyActor
	}
	for _, fromF := range friends {
		if idleUnfilledMap[fromF] > 0 {
			var targets []string
			if len(d.Policy.Builders) > 0 {
				targets = d.Policy.Builders
			} else {
				targets = friends
			}
			for _, toF := range targets {
				if toF == fromF {
					continue
				}
				toC := computed[toF]
				if toC != nil && toC.up && toC.deficit > 0 {
					for _, S := range openSprints {
						moveReply, err := client.FCall(ctx, "ns_width_move", nil,
							l.Token(), fromF, toF, S, strconv.FormatInt(rebalAfterMs, 10), actor, "").Result()
						if err != nil {
							return reconcile.Counts{}, fmt.Errorf("width duty: move: %w", err)
						}
						if slice, ok := moveReply.([]any); ok && len(slice) > 0 && slice[0] == "FENCED" {
							return reconcile.Counts{}, reconcile.ErrFenced
						}
					}
				}
			}
		}
	}

	return reconcile.Counts{}, nil
}

func parseDepRef(entry string) (repo string, n int, ok bool) {
	i := strings.LastIndexByte(entry, '#')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(entry[i+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return entry[:i], n, true
}

func splitDeps(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	}) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func strVal(val []any, idx int) string {
	if idx >= len(val) || val[idx] == nil {
		return ""
	}
	return fmt.Sprint(val[idx])
}

// copyMember is a consumer copy's id in a working set (<primary>~<n>).
var copyMember = regexp.MustCompile(`^\S+~\d+$`)
