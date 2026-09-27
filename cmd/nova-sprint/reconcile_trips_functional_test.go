//go:build functional

package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// reconcileTripBudget is each duty's round trips per pass, the most it may
// make over a store shaped like the fleet's (below). Glenn 2026-09-27: "You
// always need to batch redis. This is standard." The store is 128 ms from
// the Studio, so a pass of 28 trips took 3.5 s on a 1 s tick. A duty's reads
// go in one pipeline (two when a read depends on a read) and its writes in
// one function or pipeline; the DUTY line prints trips=<n> and this test
// pins it. "pass" is the loop's own: the renew, the pit stop read and the
// record.
var reconcileTripBudget = map[string]int64{
	"pass":            3,
	"refill":          2,
	"dev-red":         1,
	"done-already":    1,
	"expire":          2,
	"fleet-deploy":    1,
	"fsck":            1,
	"land":            1,
	"land-watch":      1,
	"progress":        2,
	"route":           2,
	"card-deal":       2,
	"task-lease":      1,
	"waiting-resolve": 2,
}

// TestReconcilePassTrips runs one production pass (every duty, as
// nova-sprint reconcile --once runs them) over a store shaped like the
// fleet's: eight benches up, four friends declared, one open sprint, two
// streams with two waiting cards each, and reads every duty's trips off its
// DUTY line.
func TestReconcilePassTrips(t *testing.T) {
	addr, c := sprintRedis(t)
	t.Setenv("NOVA_SPRINT_REDIS", addr)
	t.Setenv("NOVA_REDIS_ADDR", "")
	t.Setenv("NOVA_FRIEND", "")
	ctx := context.Background()
	st := store.New(c)
	ssh := &verbSSH{}
	seams := reconcileSeams
	reconcileSeams = func(*store.Store) (deal.Dialer, deal.PRs) { return ssh, verbForge{} }
	t.Cleanup(func() { reconcileSeams = seams })

	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	for _, b := range []string{"batman", "captainamerica", "hetzner", "hulk", "space", "studio", "superman", "vision"} {
		c.SAdd(ctx, "benches", b)
		c.HSet(ctx, "machine:"+b+":ceiling", "slots", "64", "cores", "8")
		c.HSet(ctx, "bench:"+b+":desired", "slots", "8", "machine", b, "paused", "0", "legs", "go")
		c.HSet(ctx, "bench:"+b+":beat", "host", b, "user", "nova", "at", now)
		c.HSet(ctx, "bench:"+b+":state", "state", "UP", "at", now)
	}
	for _, f := range []string{"emma", "johnny", "rowan", "stella"} {
		c.SAdd(ctx, "friends", f)
		c.HSet(ctx, "friend:"+f+":desired", "slots", "32", "machine", "studio", "paused", "0")
	}
	const S = "trips-2026-09-27"
	c.SAdd(ctx, "sprints", S)
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	c.HSet(ctx, "s:"+S, "status", "open")
	c.HSet(ctx, "s:"+S+":policy", "share", "1", "backpressure_missing", "open")
	for i, s := range []string{"swarm: cards", "friends"} {
		for j := 0; j < 2; j++ {
			id := fmt.Sprintf("c%d%d", i, j)
			code, out, errOut := runTaskCLI("push", "--actor", "rowan", "--id", id, "--stream", s, "--waiting",
				"--kind", "build", "--repo", "mas-bandwidth/nova-tools", "--title", "t")
			if code != 0 {
				t.Fatalf("push %s: %d %q %q", id, code, out, errOut)
			}
		}
	}

	duties, names, stops, err := productionDuties(st, nil)
	if err != nil {
		t.Fatalf("productionDuties: %v", err)
	}
	lease, err := reconcile.Acquire(ctx, st, reconcile.AcquireOptions{Host: "test-host"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var out, errb bytes.Buffer
	named := &namedDuties{errOut: &errb, counter: st.CountTrips()}
	loop := &reconcile.Loop{
		Lease:        lease,
		Duties:       named.wrap(duties, names),
		Names:        names,
		Passes:       1,
		Out:          &out,
		StreamScoped: reconcileStreamScoped,
		AfterPass:    func(reconcile.PassResult) { named.report(&out, true) },
	}
	if err := loop.Run(ctx); err != nil {
		t.Fatalf("pass: %v\n%s", err, errb.String())
	}
	for _, s := range stops {
		s.Stop(ctx)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}

	// every duty's trips off its DUTY line, the pass's own off the counter
	line := regexp.MustCompile(`^DUTY (\S+) .* trips=(\d+) err=(.*)$`)
	got := map[string]int64{}
	for _, l := range strings.Split(out.String(), "\n") {
		if m := line.FindStringSubmatch(l); m != nil {
			n, _ := strconv.ParseInt(m[2], 10, 64)
			got[m[1]] = n
			if m[3] != "" {
				t.Errorf("duty %s: %s", m[1], m[3])
			}
		}
	}
	got["pass"] = named.counter.Of("pass")
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return got[keys[i]] > got[keys[j]] })
	var sum int64
	for _, k := range keys {
		sum += got[k]
		t.Logf("%-16s trips=%-3d budget=%d", k, got[k], reconcileTripBudget[k])
	}
	t.Logf("%-16s trips=%-3d (the counter's total, the heartbeat's included: %d)", "PASS", sum, named.counter.N())
	for _, name := range append(names, "pass") {
		budget, ok := reconcileTripBudget[name]
		if !ok {
			t.Errorf("duty %s has no trip budget; add it to reconcileTripBudget", name)
			continue
		}
		if got[name] > budget {
			t.Errorf("duty %s made %d round trips in one pass; its budget is %d", name, got[name], budget)
		}
	}
}
