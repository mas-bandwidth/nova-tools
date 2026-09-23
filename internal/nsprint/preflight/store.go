// Package preflight holds the nova-sprint preflight checks (#2756 section 7).
// Each check prints one line, GREEN or RED with its number, and any RED
// exits 1. This file is the store checks (#2947): Redis and the function
// library (7.1), state files (7.2), leases against beats (7.3), the
// reconciler lease (7.7), states against their receipts (7.10) and width per
// machine (7.15). Every age is measured from Redis server time (2.1 rule 3),
// and every multi-key read is one pipelined exchange.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/redis/go-redis/v9"
)

// Windows from #2756: a starting reservation is released at 60 s (the start
// window), a living lease is stale at 120 s (2x the 60 s beat), the
// reconciler lease has a 6 s TTL and passes at least every 20 s, and a
// retired file touched inside 10 minutes means something still writes it.
const (
	StartWindow    = 60 * time.Second
	BeatStale      = 120 * time.Second
	ReconcilerTTL  = 6 * time.Second
	ReconcilerPass = 20 * time.Second
	RetiredWindow  = 10 * time.Minute
)

// Line is one preflight check's result.
type Line struct {
	N    string // the #2756 section 7 number, for example "7.3"
	Name string
	Red  bool
	Why  string
}

func (l Line) String() string {
	state := "GREEN"
	if l.Red {
		state = "RED"
	}
	return fmt.Sprintf("%s %s %s: %s", state, l.N, l.Name, l.Why)
}

// ExitCode is 1 when any line is RED, else 0.
func ExitCode(lines []Line) int {
	for _, l := range lines {
		if l.Red {
			return 1
		}
	}
	return 0
}

// Options names the sprint and the config files preflight reads for state
// file names. Files are read, never written.
type Options struct {
	Sprint         string   // empty means every sprint in the sprints set
	PolicyFile     string   // the etc/ policy file the sprint opens from
	UnitEnv        []string // unit environment files (launchd plist, systemd env)
	LauncherConfig string
	Retired        []string // retired state files; touched inside RetiredWindow is RED
	Now            func() time.Time
}

// StoreChecks runs the store checks in section order.
func StoreChecks(ctx context.Context, c *redis.Client, o Options) []Line {
	lines := []Line{checkRedis(ctx, c), checkStateFiles(ctx, c, o), checkLeases(ctx, c), checkReconciler(ctx, c)}
	sprints, err := sprintsFor(ctx, c, o.Sprint)
	if err != nil {
		lines = append(lines, redLine("7.10", "receipts", err))
	} else {
		lines = append(lines, checkReceipts(ctx, c, sprints...))
	}
	return append(lines, checkCeiling(ctx, c))
}

func redLine(n, name string, err error) Line {
	return Line{N: n, Name: name, Red: true, Why: "redis: " + err.Error()}
}

func verdict(n, name string, reds []string, green string) Line {
	if len(reds) == 0 {
		return Line{N: n, Name: name, Why: green}
	}
	return Line{N: n, Name: name, Red: true, Why: limit(reds, 6)}
}

func limit(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:n], "; ") + fmt.Sprintf("; +%d more", len(items)-n)
}

func sprintsFor(ctx context.Context, c *redis.Client, sprint string) ([]string, error) {
	if sprint != "" {
		return []string{sprint}, nil
	}
	names, err := c.SMembers(ctx, "sprints").Result()
	sort.Strings(names)
	return names, err
}

// stamp reads a Redis time as seconds, or milliseconds when it is that large.
func stamp(v float64) time.Time {
	if v > 1e12 {
		return time.UnixMilli(int64(v))
	}
	sec := int64(v)
	return time.Unix(sec, int64((v-float64(sec))*1e9))
}

func parseStamp(s string) (time.Time, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v <= 0 {
		return time.Time{}, false
	}
	return stamp(v), true
}

func secs(d time.Duration) string { return fmt.Sprintf("%ds", int64(d.Round(time.Second)/time.Second)) }

// 7.1: reachable, standalone, AOF on, nova_sprint loaded at the binary's
// version. The ACL half of 7.1 is the deployment test of #2937.
func checkRedis(ctx context.Context, c *redis.Client) Line {
	const n, name = "7.1", "redis"
	if err := c.Ping(ctx).Err(); err != nil {
		return Line{N: n, Name: name, Red: true, Why: "unreachable: " + err.Error()}
	}
	pipe := c.Pipeline()
	server := pipe.Info(ctx, "server")
	persistence := pipe.Info(ctx, "persistence")
	libs := pipe.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library, WithCode: true})
	_, _ = pipe.Exec(ctx)
	var reds []string
	if info, err := server.Result(); err != nil {
		reds = append(reds, "cannot read INFO server: "+err.Error())
	} else if mode := infoField(info, "redis_mode"); mode != "standalone" {
		reds = append(reds, fmt.Sprintf("redis_mode %q, not the standalone fleet instance", mode))
	}
	if info, err := persistence.Result(); err != nil {
		reds = append(reds, "cannot read INFO persistence: "+err.Error())
	} else if infoField(info, "aof_enabled") != "1" {
		reds = append(reds, "AOF off")
	}
	want, err := fn.Source()
	if err != nil {
		reds = append(reds, "the binary's library does not build: "+err.Error())
	}
	if got, lerr := libs.Result(); lerr != nil {
		reds = append(reds, "cannot list functions: "+lerr.Error())
	} else if lib, ok := library(got, fn.Library); !ok {
		reds = append(reds, "library "+fn.Library+" not loaded")
	} else if err == nil && strings.TrimSpace(lib.Code) != strings.TrimSpace(want) {
		reds = append(reds, "library "+fn.Library+" is not the binary's version")
	}
	return verdict(n, name, reds, "standalone, AOF on, "+fn.Library+" at the binary's version")
}

func library(libs []redis.Library, name string) (redis.Library, bool) {
	for _, l := range libs {
		if l.Name == name {
			return l, true
		}
	}
	return redis.Library{}, false
}

func infoField(info, field string) string {
	for _, line := range strings.Split(info, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == field {
			return v
		}
	}
	return ""
}

// 7.2: a state file named anywhere the tool is configured from, or a retired
// state file still being written.
func checkStateFiles(ctx context.Context, c *redis.Client, o Options) Line {
	const n, name = "7.2", "state-files"
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	var reds []string
	sources := 0
	for _, f := range append(append([]string{o.PolicyFile}, o.UnitEnv...), o.LauncherConfig) {
		if f == "" {
			continue
		}
		sources++
		body, err := os.ReadFile(f)
		if err != nil {
			reds = append(reds, "cannot read "+f+": "+err.Error())
			continue
		}
		for _, p := range stateFiles(string(body)) {
			reds = append(reds, f+" names "+p)
		}
	}
	for _, f := range o.Retired {
		sources++
		st, err := os.Stat(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			reds = append(reds, "cannot stat retired "+f+": "+err.Error())
			continue
		}
		if age := now().Sub(st.ModTime()); age < RetiredWindow {
			reds = append(reds, fmt.Sprintf("retired %s modified %s ago", f, secs(age)))
		}
	}
	sprints, err := sprintsFor(ctx, c, o.Sprint)
	if err != nil {
		return redLine(n, name, err)
	}
	pipe := c.Pipeline()
	policies := make([]*redis.MapStringStringCmd, len(sprints))
	queued := make([]*redis.StringSliceCmd, len(sprints))
	for i, s := range sprints {
		policies[i] = pipe.HGetAll(ctx, "s:"+s+":policy")
		queued[i] = pipe.SMembers(ctx, "s:"+s+":idx:card:queued")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return redLine(n, name, err)
	}
	type cardRef struct{ sprint, label string }
	var cards []cardRef
	for i, s := range sprints {
		policy := policies[i].Val()
		sources++
		fields := make([]string, 0, len(policy))
		for k := range policy {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		for _, k := range fields {
			for _, p := range stateFiles(policy[k]) {
				reds = append(reds, fmt.Sprintf("policy s:%s:policy %s names %s", s, k, p))
			}
		}
		labels := queued[i].Val()
		sort.Strings(labels)
		for _, l := range labels {
			cards = append(cards, cardRef{s, l})
		}
	}
	if len(cards) > 0 {
		pipe = c.Pipeline()
		results := make([]*redis.StringCmd, len(cards))
		for i, cr := range cards {
			results[i] = pipe.HGet(ctx, "s:"+cr.sprint+":card:"+cr.label, "results")
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return redLine(n, name, err)
		}
		for i, cr := range cards {
			for _, p := range stateFiles(results[i].Val()) {
				reds = append(reds, fmt.Sprintf("card %s results names %s", cr.label, p))
			}
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d sources and %d queued cards name no state file", sources, len(cards)))
}

// stateFiles returns the path-like tokens in text that name a file the tool
// would read or write as state: a TSV, lock or pid file, a BEAT file, a
// backpressure file or a control file.
func stateFiles(text string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r <= ' ' || strings.ContainsRune("=,;:\"'()[]{}<>", r)
	}) {
		if isStateFile(tok) {
			out = append(out, tok)
		}
	}
	return out
}

func isStateFile(tok string) bool {
	base := path.Base(tok)
	if !strings.Contains(tok, "/") && !strings.Contains(base, ".") {
		return false // a word, not a path
	}
	lower := strings.ToLower(base)
	stem := strings.TrimSuffix(lower, path.Ext(lower))
	switch path.Ext(lower) {
	case ".tsv", ".lock", ".pid":
		return true
	}
	return strings.HasPrefix(base, "BEAT") || strings.Contains(lower, "backpressure") || stem == "control"
}

type consumer struct{ kind, name string }

func consumers(ctx context.Context, c *redis.Client) ([]consumer, error) {
	pipe := c.Pipeline()
	friends := pipe.SMembers(ctx, "friends")
	benches := pipe.SMembers(ctx, "benches")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	var out []consumer
	for _, set := range []struct {
		kind  string
		names []string
	}{{"bench", benches.Val()}, {"friend", friends.Val()}} {
		sort.Strings(set.names)
		for _, name := range set.names {
			out = append(out, consumer{set.kind, name})
		}
	}
	return out, nil
}

// 7.3: leased is not equal to living beats past the start window (Johnny 10).
func checkLeases(ctx context.Context, c *redis.Client) Line {
	const n, name = "7.3", "leases-vs-beats"
	now, err := c.Time(ctx).Result()
	if err != nil {
		return redLine(n, name, err)
	}
	cs, err := consumers(ctx, c)
	if err != nil {
		return redLine(n, name, err)
	}
	pipe := c.Pipeline()
	starting := make([]*redis.ZSliceCmd, len(cs))
	living := make([]*redis.ZSliceCmd, len(cs))
	for i, k := range cs {
		starting[i] = pipe.ZRangeWithScores(ctx, k.kind+":"+k.name+":starting", 0, -1)
		living[i] = pipe.ZRangeWithScores(ctx, k.kind+":"+k.name+":living", 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return redLine(n, name, err)
	}
	var reds []string
	leased, beats := 0, 0
	for i, k := range cs {
		late, stale := 0, 0
		for _, z := range starting[i].Val() {
			if now.Sub(stamp(z.Score)) > StartWindow {
				late++
			}
		}
		for _, z := range living[i].Val() {
			if now.Sub(stamp(z.Score)) > BeatStale {
				stale++
			}
		}
		leased += len(starting[i].Val()) + len(living[i].Val())
		beats += len(living[i].Val()) - stale
		var why []string
		if late > 0 {
			why = append(why, fmt.Sprintf("%d starting past %s", late, secs(StartWindow)))
		}
		if stale > 0 {
			why = append(why, fmt.Sprintf("%d living beat past %s", stale, secs(BeatStale)))
		}
		if len(why) > 0 {
			reds = append(reds, fmt.Sprintf("%s %s %s (leased %d, living beats %d)", k.kind, k.name,
				strings.Join(why, ", "), len(starting[i].Val())+len(living[i].Val()), len(living[i].Val())-stale))
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d consumers, leased %d, living beats %d, no reservation past %s and no beat past %s",
		len(cs), leased, beats, secs(StartWindow), secs(BeatStale)))
}

// 7.7: the reconciler lease is missing or stale, or its last pass is old. A
// second instance is refused by the lease itself (#2726); the store shows
// only the holder.
func checkReconciler(ctx context.Context, c *redis.Client) Line {
	const n, name = "7.7", "reconciler"
	pipe := c.Pipeline()
	lease := pipe.HGetAll(ctx, "lease:reconciler")
	proc := pipe.HGetAll(ctx, "proc:reconciler")
	clock := pipe.Time(ctx)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return redLine(n, name, err)
	}
	now := clock.Val()
	var reds []string
	l := lease.Val()
	if len(l) == 0 {
		reds = append(reds, "lease:reconciler missing")
	} else {
		if l["instance"] == "" || l["token"] == "" {
			reds = append(reds, "lease:reconciler has no instance or token")
		}
		if at, ok := parseStamp(l["at"]); !ok {
			reds = append(reds, "lease:reconciler has no at")
		} else if age := now.Sub(at); age > ReconcilerTTL {
			reds = append(reds, fmt.Sprintf("lease:reconciler stale (renewed %s ago, ttl %s)", secs(age), secs(ReconcilerTTL)))
		}
	}
	passAt, ok := parseStamp(proc.Val()["pass_at"])
	if !ok {
		reds = append(reds, "proc:reconciler has no pass_at")
	} else if age := now.Sub(passAt); age > ReconcilerPass {
		reds = append(reds, fmt.Sprintf("last pass %s ago (limit %s)", secs(age), secs(ReconcilerPass)))
	}
	return verdict(n, name, reds, fmt.Sprintf("instance %s on %s, last pass %s ago", l["instance"], l["host"], secs(now.Sub(passAt))))
}

// 7.10: an id in an index set whose latest receipt names a different state.
func checkReceipts(ctx context.Context, c *redis.Client, sprints ...string) Line {
	const n, name = "7.10", "receipts"
	var reds []string
	ids := 0
	for _, s := range sprints {
		prefix := "s:" + s + ":idx:"
		var keys []string
		iter := c.Scan(ctx, 0, prefix+"*", 1000).Iterator()
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			return redLine(n, name, err)
		}
		sort.Strings(keys)
		pipe := c.Pipeline()
		members := make([]*redis.StringSliceCmd, len(keys))
		for i, k := range keys {
			members[i] = pipe.SMembers(ctx, k)
		}
		log := pipe.XRevRange(ctx, "s:"+s+":log", "+", "-")
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return redLine(n, name, err)
		}
		latest := map[string]string{}
		for _, m := range log.Val() {
			key := fmt.Sprint(m.Values["kind"]) + " " + fmt.Sprint(m.Values["id"])
			if _, seen := latest[key]; !seen {
				latest[key] = fmt.Sprint(m.Values["to"])
			}
		}
		for i, k := range keys {
			kind, state, ok := strings.Cut(strings.TrimPrefix(k, prefix), ":")
			if !ok {
				continue
			}
			list := members[i].Val()
			sort.Strings(list)
			for _, id := range list {
				ids++
				got, seen := latest[kind+" "+id]
				if !seen {
					got = "none"
				}
				if got != state {
					reds = append(reds, fmt.Sprintf("%s %s %s, receipt %s", kind, id, state, got))
				}
			}
		}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d sprints, %d indexed ids, each at its latest receipt", len(sprints), ids))
}

// 7.15: width per machine (2.4).
func checkCeiling(ctx context.Context, c *redis.Client) Line {
	const n, name = "7.15", "machine-ceiling"
	cs, err := consumers(ctx, c)
	if err != nil {
		return redLine(n, name, err)
	}
	pipe := c.Pipeline()
	desired := make([]*redis.SliceCmd, len(cs))
	beat := make([]*redis.StringCmd, len(cs))
	interim := make([]*redis.IntCmd, len(cs))
	for i, k := range cs {
		desired[i] = pipe.HMGet(ctx, k.kind+":"+k.name+":desired", "slots", "machine")
		beat[i] = pipe.HGet(ctx, k.kind+":"+k.name+":beat", "host")
		if k.kind == "friend" {
			interim[i] = pipe.Exists(ctx, "friend:"+k.name+":slots")
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return redLine(n, name, err)
	}
	var reds []string
	sum := map[string]int{}
	for i, k := range cs {
		v := desired[i].Val()
		slots, machine := str(v, 0), str(v, 1)
		if interim[i] != nil && interim[i].Val() > 0 {
			reds = append(reds, fmt.Sprintf("friend:%s:slots exists beside friend:%s:desired (two writers)", k.name, k.name))
		}
		if machine == "" {
			reds = append(reds, fmt.Sprintf("%s %s has no desired machine", k.kind, k.name))
			continue
		}
		width, err := strconv.Atoi(slots)
		if err != nil && slots != "" {
			reds = append(reds, fmt.Sprintf("%s %s desired slots %q is not a number", k.kind, k.name, slots))
		}
		sum[machine] += width
		if host := beat[i].Val(); host != "" && host != machine {
			reds = append(reds, fmt.Sprintf("%s %s beats on %s, desired %s", k.kind, k.name, host, machine))
		}
	}
	machines := make([]string, 0, len(sum))
	for m := range sum {
		machines = append(machines, m)
	}
	sort.Strings(machines)
	pipe = c.Pipeline()
	ceilings := make([]*redis.StringCmd, len(machines))
	for i, m := range machines {
		ceilings[i] = pipe.HGet(ctx, "machine:"+m+":ceiling", "slots")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return redLine(n, name, err)
	}
	var green []string
	for i, m := range machines {
		ceiling, err := strconv.Atoi(ceilings[i].Val())
		switch {
		case ceilings[i].Val() == "":
			reds = append(reds, m+" has no ceiling")
		case err != nil:
			reds = append(reds, fmt.Sprintf("%s ceiling %q is not a number", m, ceilings[i].Val()))
		case sum[m] > ceiling:
			reds = append(reds, fmt.Sprintf("%s %d/%d over its ceiling", m, sum[m], ceiling))
		default:
			green = append(green, fmt.Sprintf("%s %d/%d", m, sum[m], ceiling))
		}
	}
	if len(green) == 0 {
		green = []string{"no consumer has a desired machine"}
	}
	return verdict(n, name, reds, fmt.Sprintf("%d machines: %s", len(machines), strings.Join(green, ", ")))
}

func str(v []any, i int) string {
	if i < len(v) && v[i] != nil {
		return fmt.Sprint(v[i])
	}
	return ""
}
