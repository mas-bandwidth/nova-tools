//go:build functional

package reconcile_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// commandLog records every command a client sends, grouped by round trip: a
// single command is a trip of one, a pipeline one trip of all its commands.
type commandLog struct {
	mu    sync.Mutex
	trips [][]string
}

func (h *commandLog) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}

func (h *commandLog) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.add([]redis.Cmder{cmd})
		return next(ctx, cmd)
	}
}

func (h *commandLog) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.add(cmds)
		return next(ctx, cmds)
	}
}

func (h *commandLog) add(cmds []redis.Cmder) {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = strings.ToLower(c.FullName())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trips = append(h.trips, names)
}

func (h *commandLog) reset() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.trips
	h.trips = nil
	return t
}

// TestExpireDutyUsesLibraryOnly is nova-tools #3620. The expire duty read each
// card index with SORT <idx> BY nosort GET ..., and SORT is @dangerous: the
// Studio's reconciler logged "NOPERM User coordinator has no permissions to
// run the 'sort' command" on every pass. The duty now reads through the
// library (FCALL_RO ns_expire_read). The control runs the duty as a user with
// the coordinator seat's rules WITHOUT +sort (rowan-tools fleet/redis.yml
// grants +sort only as a stopgap) over 200 expired cards: the sweep expires
// all 200, reads them in its one read round trip, sends no SORT, and the
// server counts no SORT call.
func TestExpireDutyUsesLibraryOnly(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := fn.Load(ctx, admin); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	const user, pass = "coordinator", "ctl-3620"
	if err := admin.Do(ctx, "ACL", "SETUSER", user, "reset", "on", ">"+pass,
		"~*", "&*", "+@all", "-@dangerous", "+info", "+config|get").Err(); err != nil {
		t.Fatalf("ACL SETUSER: %v", err)
	}
	if v, err := admin.Do(ctx, "ACL", "DRYRUN", user, "SORT", "k").Text(); err != nil || v == "OK" {
		t.Fatalf("the control user may SORT (%q); the control needs the seat without +sort", v)
	}
	c := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: pass})
	t.Cleanup(func() { _ = c.Close() })
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	const S, bench = "expire-lib-3620", "exp-bench"
	must(admin.SAdd(ctx, "sprints", S).Err())
	must(admin.HSet(ctx, "s:"+S, "status", "open").Err())
	must(admin.HSet(ctx, reconcile.PolicyKey(S), "share", "1").Err())
	must(admin.SAdd(ctx, "benches", bench).Err())
	must(admin.HSet(ctx, "bench:"+bench+":beat", "host", bench, "at", "1").Err())

	l, err := reconcile.Acquire(ctx, store.New(c), reconcile.AcquireOptions{Host: "ctl-3620"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	duty := &reconcile.Expire{Client: c}
	// A first sweep over the empty sprint learns the sprint index.
	if _, err := duty.Run(ctx, l); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	now, err := admin.Time(ctx).Result()
	must(err)
	ms := now.UnixMilli()
	for i := range 200 {
		label := fmt.Sprintf("card-%03d", i)
		h := map[string]any{"attempt": "1", "retries": "0", "priority": "5", "bench": bench,
			"token_sha": "sha-" + label, "identity": S + "/" + label + "/0123abcd/" + bench + "/1"}
		state := "running"
		if i%2 == 0 {
			h["beat_at"] = strconv.FormatInt(ms-reconcile.DefaultPolicy.Beat.Milliseconds()-1000, 10)
		} else {
			state = "dealt"
			h["dealt_at"] = strconv.FormatInt(ms-reconcile.DefaultPolicy.Start.Milliseconds()-1000, 10)
		}
		h["state"] = state
		must(admin.HSet(ctx, card.CardKey(S, label), h).Err())
		must(admin.SAdd(ctx, card.IdxKey(S, state), label).Err())
	}
	must(admin.HDel(ctx, reconcile.ProcKey, reconcile.ExpireStampField(S)).Err())
	must(admin.ConfigResetStat(ctx).Err())

	log := &commandLog{}
	c.AddHook(log)
	counts, err := duty.Run(ctx, l)
	if err != nil {
		t.Fatalf("sweep as the seat without +sort: %v", err)
	}
	if counts.Expired != 200 {
		t.Fatalf("sweep expired %d cards (%+v), want 200", counts.Expired, counts)
	}
	for _, state := range []string{"dealt", "running"} {
		if n, _ := admin.SCard(ctx, card.IdxKey(S, state)).Result(); n != 0 {
			t.Fatalf("%d cards still %s after the sweep, want 0", n, state)
		}
	}

	trips := log.reset()
	// gate, read, transitions: three round trips, whatever the card count.
	if len(trips) != 3 {
		t.Fatalf("sweep made %d round trips, want 3 (gate, read, transitions): %v", len(trips), trips)
	}
	read := trips[1]
	if n := strings.Count(strings.Join(read, " "), "fcall_ro"); n != 5 {
		t.Fatalf("read trip has %d FCALL_RO, want 5 (one per card index): %v", n, read)
	}
	for i, trip := range trips {
		for _, name := range trip {
			if strings.HasPrefix(name, "sort") {
				t.Fatalf("round trip %d sent %s: %v", i, name, trip)
			}
		}
	}
	for _, name := range read {
		if name != "hgetall" && name != "fcall_ro" && name != "zrange" && name != "hmget" {
			t.Fatalf("read trip sent %s, want only hgetall, fcall_ro, zrange and hmget: %v", name, read)
		}
	}
	stats, err := admin.Info(ctx, "commandstats").Result()
	must(err)
	if strings.Contains(stats, "cmdstat_sort") {
		t.Fatalf("the server counted a SORT call:\n%s", stats)
	}
}

// TestReconcilerFunctionsInLibrary is nova-tools #3620: the refill duty's
// fleet step logged "ERR Function not found". The name is not drifted:
// ns_fleet_step (internal/nsprint/fleet) is registered in fleet.lua. The
// library the store held lacked it because a verb on an older binary REPLACEd
// it (card push, the reconciler's calls; fn.LoadMissing now never replaces).
// This control loads the embedded library and holds every ns_* function the
// reconciler calls (the reconcile and fleet packages' Go) to FUNCTION LIST,
// so a name that drifts from its Lua registration fails here, not on a bench.
func TestReconcilerFunctionsInLibrary(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	libs, err := c.FunctionList(ctx, redis.FunctionListQuery{LibraryNamePattern: fn.Library}).Result()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, lib := range libs {
		for _, f := range lib.Functions {
			have[f.Name] = true
		}
	}
	called := map[string]bool{fleet.FunctionStep: true, reconcile.ExpireReadFunction: true}
	name := regexp.MustCompile(`"(ns_[a-z0-9_]+)"`)
	for _, dir := range []string{".", "../fleet"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range name.FindAllStringSubmatch(string(src), -1) {
				called[m[1]] = true
			}
		}
	}
	var missing []string
	for f := range called {
		if !have[f] {
			missing = append(missing, f)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("the reconciler calls %v, which FUNCTION LIST of the embedded library does not hold", missing)
	}
	if len(called) < 10 {
		t.Fatalf("found only %d called functions; the scan is broken", len(called))
	}
}
