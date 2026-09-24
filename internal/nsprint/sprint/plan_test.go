package sprint

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// planRedis starts a throwaway loopback redis-server with the nova_sprint
// library loaded and returns its address and an admin client (the default
// user) that the fixtures write through. The admin client carries no write
// hook: the hook counts only what the verb under test sends.
func planRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load Redis Functions: %v", err)
	}
	return addr, client
}

// planFixture is the fake fleet: three benches and two friends on machines
// m1 and m2, each with a ceiling of 32. The records are fixtures, not hosts.
func planFixture(t *testing.T, c *redis.Client) {
	t.Helper()
	ctx := context.Background()
	for _, m := range []string{"m1", "m2"} {
		c.HSet(ctx, capacity.MachineCeilingKey(m), "slots", 32, "at", 1)
	}
	c.SAdd(ctx, "benches", "a", "b", "c")
	c.SAdd(ctx, "friends", "x", "y")
	c.HSet(ctx, "bench:a:desired", "slots", 2, "machine", "m1", "paused", 0, "at", 1)
	c.HSet(ctx, "bench:b:desired", "slots", 2, "machine", "m1", "paused", 0, "at", 1)
	c.HSet(ctx, "bench:c:desired", "slots", 2, "machine", "m2", "paused", 0, "at", 1)
	c.HSet(ctx, "friend:x:desired", "slots", 4, "machine", "m1", "paused", 0, "at", 1)
	c.HSet(ctx, "friend:y:desired", "slots", 4, "machine", "m2", "paused", 0, "at", 1)
}

// writeHook counts the write commands a client sends. FCALL is counted apart
// (fcalls): whether the function wrote is proved by the server's own dirty
// counter and a DUMP of every key, so a refused FCALL is not a write here.
type writeHook struct{ writes, fcalls atomic.Int64 }

var writeCommands = map[string]bool{
	"hset": true, "hmset": true, "hdel": true, "set": true, "del": true, "unlink": true,
	"sadd": true, "srem": true, "zadd": true, "zrem": true, "xadd": true, "xtrim": true,
	"expire": true, "pexpire": true, "incr": true, "incrby": true, "hincrby": true,
	"rename": true, "lpush": true, "rpush": true, "restore": true, "copy": true,
}

func (h *writeHook) count(cmd redis.Cmder) {
	name := strings.ToLower(cmd.Name())
	switch {
	case name == "fcall":
		h.fcalls.Add(1)
	case writeCommands[name]:
		h.writes.Add(1)
	}
}

func (h *writeHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *writeHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.count(cmd)
		return next(ctx, cmd)
	}
}

func (h *writeHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			h.count(cmd)
		}
		return next(ctx, cmds)
	}
}

// seat opens a hooked store as one ACL user ("" is the default user).
func seat(t *testing.T, addr, user, password string) (*store.Store, *writeHook) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: password})
	t.Cleanup(func() { _ = client.Close() })
	hook := &writeHook{}
	client.AddHook(hook)
	return store.New(client), hook
}

// dirty is the server's count of writes since the last save (saving is off),
// which includes every write a Redis Function makes.
func dirty(t *testing.T, c *redis.Client) int64 {
	t.Helper()
	info, err := c.Info(context.Background(), "persistence").Result()
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(strings.NewReader(info))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "rdb_changes_since_last_save:"); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatal("INFO persistence has no rdb_changes_since_last_save")
	return 0
}

// dumpAll is DUMP of every key, the byte-identical witness of zero writes.
func dumpAll(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, k := range keys {
		v, err := c.Dump(ctx, k).Result()
		if err != nil {
			t.Fatalf("DUMP %s: %v", k, err)
		}
		out[k] = v
	}
	return out
}

func sameDump(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	var diff []string
	for k, v := range before {
		if after[k] != v {
			diff = append(diff, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			diff = append(diff, k+" (new)")
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Fatalf("%s: keys changed: %s", what, strings.Join(diff, ", "))
	}
}

func planFile(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "plan", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

type run struct {
	code        int
	out, errOut string
}

func applyPlan(t *testing.T, a Applier, s, file string) run {
	t.Helper()
	var out, errOut bytes.Buffer
	code := a.Apply(context.Background(), s, planFile(t, file), &out, &errOut)
	return run{code, out.String(), errOut.String()}
}

func showPlan(t *testing.T, st *store.Store, s string) run {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Show(context.Background(), st, s, &out, &errOut)
	return run{code, out.String(), errOut.String()}
}

// goldenShow is control 2's show text for good.tsv, with the three values a
// run cannot fix in advance read back from the plan hash.
func goldenShow(t *testing.T, c *redis.Client, s, by string) string {
	t.Helper()
	plan := c.HGetAll(context.Background(), "s:"+s+":plan").Val()
	ms, err := strconv.ParseInt(plan["applied_at"], 10, 64)
	if err != nil {
		t.Fatalf("applied_at %q: %v", plan["applied_at"], err)
	}
	routes, err := RoutesSHA()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{
		"PLAN " + s + " sha=" + plan["sha"][:12] + " rows=4 applied_at=" + time.UnixMilli(ms).In(time.Local).Format(time.RFC3339) +
			" applied_by=" + by + " routes_sha=" + routes[:12],
		"policy backpressure_missing plan=closed store=closed",
		"policy ci_reruns plan=1 store=1",
		"policy readers plan=1 store=1",
		"policy absent_after plan=60m store=60m",
		"bench a m1 plan=6 store=6",
		"bench b m1 plan=4 store=4",
		"friend x m1 plan=8 store=8",
		"bench c m2 plan=8 store=8",
		"RED 7.7 reconciler: lease:reconciler missing; proc:reconciler has no pass_at",
		"DRIFT none",
	}, "\n") + "\n"
}

func desired(t *testing.T, c *redis.Client, kind, name string) map[string]string {
	t.Helper()
	return c.HGetAll(context.Background(), capacity.DesiredKey(kind, name)).Val()
}

func dealSlots(t *testing.T, c *redis.Client, bench string) int {
	t.Helper()
	in, err := deal.RedisSource{Client: c}.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range in.Benches {
		if b.Name == bench {
			return b.Slots
		}
	}
	t.Fatalf("deal pass read no bench %s", bench)
	return 0
}

// TestPlanControls is the #2380 rev 4 DONE-WHEN: one plan file, validated in
// full, applied in one atomic step by an authorised seat, readable by any.
func TestPlanControls(t *testing.T) {
	ctx := context.Background()
	const s = "control-2380"

	t.Run("1_bad_plan_zero_writes", func(t *testing.T) {
		addr, admin := planRedis(t)
		planFixture(t, admin)
		st, hook := seat(t, addr, "", "")
		a := Applier{Store: st, User: "default"}
		cases := []struct{ file, want string }{
			{"bad_header.tsv", `PLAN-REFUSED line 1: header "#nova-sprint-plan v2" is not "#nova-sprint-plan v1"`},
			{"bad_policy_key.tsv", "PLAN-REFUSED line 7: unknown policy key max_load_per_core"},
			{"bad_readers.tsv", "PLAN-REFUSED line 5: policy readers 3 is not 1 or 2"},
			{"bad_unknown_bench.tsv", "PLAN-REFUSED line 7: bench zz is not in benches (a plan never registers a name)"},
			{"bad_repeat.tsv", "PLAN-REFUSED line 8: bench a repeats line 7"},
			{"bad_ceiling.tsv", "PLAN-REFUSED line 7: CEILING m1 46/32"},
		}
		for _, tc := range cases {
			before, d0, logs := dumpAll(t, admin), dirty(t, admin), admin.XLen(ctx, capacity.LogKey).Val()
			r := applyPlan(t, a, s, tc.file)
			if r.code != 1 || r.errOut != tc.want+"\n" || r.out != "" {
				t.Fatalf("%s: code=%d out=%q err=%q; want 1 and %q", tc.file, r.code, r.out, r.errOut, tc.want)
			}
			if w, f := hook.writes.Load(), hook.fcalls.Load(); w != 0 || f != 0 {
				t.Fatalf("%s: hook counted writes=%d fcalls=%d, want 0 0", tc.file, w, f)
			}
			if d := dirty(t, admin) - d0; d != 0 {
				t.Fatalf("%s: server dirty grew by %d", tc.file, d)
			}
			if admin.XLen(ctx, capacity.LogKey).Val() != logs {
				t.Fatalf("%s: cap:log grew", tc.file)
			}
			sameDump(t, tc.file, before, dumpAll(t, admin))
		}
	})

	t.Run("2_good_plan_visible", func(t *testing.T) {
		addr, admin := planRedis(t)
		planFixture(t, admin)
		st, _ := seat(t, addr, "", "")
		logs := admin.XLen(ctx, capacity.LogKey).Val()
		r := applyPlan(t, Applier{Store: st, User: "default"}, s, "good.tsv")
		sha := admin.HGet(ctx, "s:"+s+":plan", "sha").Val()
		if r.code != 0 || r.out != "PLAN-APPLIED "+s+" rows=4 sha="+sha[:min(12, len(sha))]+"\n" {
			t.Fatalf("apply: code=%d out=%q err=%q", r.code, r.out, r.errOut)
		}
		if grew := admin.XLen(ctx, capacity.LogKey).Val() - logs; grew != 4 {
			t.Fatalf("cap:log grew by %d, want rows=4", grew)
		}
		show := showPlan(t, st, s)
		if want := goldenShow(t, admin, s, "default"); show.code != 0 || show.out != want {
			t.Fatalf("show: code=%d err=%q\n got:\n%s\nwant:\n%s", show.code, show.errOut, show.out, want)
		}
	})

	t.Run("3_no_partial_apply", func(t *testing.T) {
		addr, admin := planRedis(t)
		planFixture(t, admin)
		st, hook := seat(t, addr, "", "")
		var before map[string]string
		var d0, logs int64
		a := Applier{Store: st, User: "default", afterValidate: func(context.Context) {
			// Go validation has passed; the store changes under it.
			admin.Del(ctx, "bench:c:desired")
			admin.Set(ctx, "bench:c:desired", "not a hash", 0)
			before, d0, logs = dumpAll(t, admin), dirty(t, admin), admin.XLen(ctx, capacity.LogKey).Val()
		}}
		r := applyPlan(t, a, s, "partial.tsv")
		if r.code != 1 || r.errOut != "PLAN-REFUSED check: WRONGTYPE bench:c:desired\n" {
			t.Fatalf("partial: code=%d out=%q err=%q", r.code, r.out, r.errOut)
		}
		if w := hook.writes.Load(); w != 0 {
			t.Fatalf("hook counted %d writes", w)
		}
		if d := dirty(t, admin) - d0; d != 0 {
			t.Fatalf("the function wrote %d times before refusing", d)
		}
		if admin.XLen(ctx, capacity.LogKey).Val() != logs {
			t.Fatal("cap:log grew")
		}
		sameDump(t, "partial", before, dumpAll(t, admin))
		for _, b := range []string{"a", "b"} {
			if got := desired(t, admin, "bench", b)["slots"]; got != "2" {
				t.Fatalf("earlier row bench %s changed to %s", b, got)
			}
		}

		// Transfer: raising ta first alone would sum to 40 on mt and be
		// refused by a per-row check; the plan substitutes both rows at once.
		admin.Del(ctx, "bench:c:desired")
		admin.HSet(ctx, "bench:c:desired", "slots", 2, "machine", "m2", "paused", 0, "at", 1)
		admin.HSet(ctx, capacity.MachineCeilingKey("mt"), "slots", 32, "at", 1)
		admin.SAdd(ctx, "benches", "ta", "tb")
		admin.HSet(ctx, "bench:ta:desired", "slots", 4, "machine", "mt", "paused", 1, "at", 1)
		admin.HSet(ctx, "bench:tb:desired", "slots", 20, "machine", "mt", "paused", 0, "at", 1)
		logs = admin.XLen(ctx, capacity.LogKey).Val()
		r = applyPlan(t, Applier{Store: st, User: "default"}, s, "transfer.tsv")
		if r.code != 0 || !strings.HasPrefix(r.out, "PLAN-APPLIED "+s+" rows=2 ") {
			t.Fatalf("transfer: code=%d out=%q err=%q", r.code, r.out, r.errOut)
		}
		ta, tb := desired(t, admin, "bench", "ta"), desired(t, admin, "bench", "tb")
		if ta["slots"] != "20" || ta["paused"] != "1" || tb["slots"] != "4" || tb["paused"] != "0" {
			t.Fatalf("transfer read back ta=%v tb=%v", ta, tb)
		}
		entries := admin.XRange(ctx, capacity.LogKey, "-", "+").Val()
		if int64(len(entries))-logs != 2 {
			t.Fatalf("cap:log grew by %d, want 2", int64(len(entries))-logs)
		}
		for _, e := range entries[logs:] {
			if e.Values["actor"] != "plan:"+s {
				t.Fatalf("cap:log entry %v, want actor=plan:%s", e.Values, s)
			}
		}
	})

	t.Run("4_live_set_no_restart", func(t *testing.T) {
		addr, admin := planRedis(t)
		planFixture(t, admin)
		st, hook := seat(t, addr, "", "")
		a := Applier{Store: st, User: "default"}
		if r := applyPlan(t, a, s, "live_a.tsv"); r.code != 0 {
			t.Fatalf("plan A: %+v", r)
		}
		if got := dealSlots(t, admin, "a"); got != 4 {
			t.Fatalf("deal pass after plan A sees %d, want 4", got)
		}
		if r := applyPlan(t, a, s, "live_b.tsv"); r.code != 0 {
			t.Fatalf("plan B: %+v", r)
		}
		if got := dealSlots(t, admin, "a"); got != 6 {
			t.Fatalf("next deal pass after plan B sees %d, want 6", got)
		}
		d0, w0 := dirty(t, admin), hook.writes.Load()
		r := applyPlan(t, a, s, "live_b.tsv")
		if r.code != 0 || !strings.HasPrefix(r.out, "PLAN-UNCHANGED "+s+" sha=") {
			t.Fatalf("re-apply B: %+v", r)
		}
		if d := dirty(t, admin) - d0; d != 0 || hook.writes.Load() != w0 {
			t.Fatalf("PLAN-UNCHANGED wrote: dirty=%d hook=%d", d, hook.writes.Load()-w0)
		}
	})

	t.Run("5_drift", func(t *testing.T) {
		addr, admin := planRedis(t)
		planFixture(t, admin)
		st, _ := seat(t, addr, "", "")
		if r := applyPlan(t, Applier{Store: st, User: "default"}, s, "good.tsv"); r.code != 0 {
			t.Fatalf("apply: %+v", r)
		}
		if _, err := capacity.SetBench(ctx, st, "a", "m1", 8, "test", ""); err != nil {
			t.Fatal(err)
		}
		r := showPlan(t, st, s)
		if r.code != 1 || !strings.Contains(r.out, "bench a m1 plan=6 store=8\n") || !strings.HasSuffix(r.out, "\nDRIFT bench a slots plan=6 store=8\n") {
			t.Fatalf("drift: code=%d out=\n%s", r.code, r.out)
		}
	})

	t.Run("6_authority", func(t *testing.T) {
		addr, admin := planRedis(t)
		planFixture(t, admin)
		acl := planFile(t, "acl.txt")
		for _, line := range strings.Split(string(acl), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, rules, ok := strings.Cut(line, "\t")
			if !ok {
				t.Fatalf("acl.txt line %q", line)
			}
			args := []any{"ACL", "SETUSER", name, "reset", "on", ">pw-" + name}
			for _, r := range strings.Fields(rules) {
				args = append(args, r)
			}
			if err := admin.Do(ctx, args...).Err(); err != nil {
				t.Fatalf("ACL SETUSER %s: %v", name, err)
			}
		}
		for _, who := range []struct{ user, want string }{
			{"bench", "PLAN-REFUSED authority: redis user bench may not apply a sprint plan (needs HSET on authz:sprint-plan)\n"},
			{"viewer", "PLAN-REFUSED authority: redis user viewer may not apply a sprint plan (needs HSET on authz:sprint-plan): NOPERM"},
		} {
			st, hook := seat(t, addr, who.user, "pw-"+who.user)
			before, d0, logs := dumpAll(t, admin), dirty(t, admin), admin.XLen(ctx, capacity.LogKey).Val()
			r := applyPlan(t, Applier{Store: st, User: who.user}, s, "good.tsv")
			if r.code != 1 || !strings.HasPrefix(r.errOut, who.want) || r.out != "" {
				t.Fatalf("%s apply: code=%d out=%q err=%q; want 1 and %q", who.user, r.code, r.out, r.errOut, who.want)
			}
			if w := hook.writes.Load(); w != 0 {
				t.Fatalf("%s: hook counted %d writes", who.user, w)
			}
			if d := dirty(t, admin) - d0; d != 0 {
				t.Fatalf("%s: server dirty grew by %d", who.user, d)
			}
			if admin.XLen(ctx, capacity.LogKey).Val() != logs {
				t.Fatalf("%s: cap:log grew", who.user)
			}
			sameDump(t, who.user, before, dumpAll(t, admin))
		}
		if admin.Exists(ctx, AuthzKey).Val() != 0 {
			t.Fatalf("%s exists; it names a right and is never written", AuthzKey)
		}
		coord, _ := seat(t, addr, "coordinator", "pw-coordinator")
		if r := applyPlan(t, Applier{Store: coord, User: "coordinator"}, s, "good.tsv"); r.code != 0 || !strings.HasPrefix(r.out, "PLAN-APPLIED "+s+" rows=4 ") {
			t.Fatalf("coordinator apply: %+v", r)
		}
		want := goldenShow(t, admin, s, "coordinator")
		bench, _ := seat(t, addr, "bench", "pw-bench")
		for _, who := range []string{"bench", "viewer"} {
			st, _ := seat(t, addr, who, "pw-"+who)
			if r := showPlan(t, st, s); r.code != 0 || r.out != want {
				t.Fatalf("%s show: code=%d err=%q\n got:\n%s\nwant:\n%s", who, r.code, r.errOut, r.out, want)
			}
		}
		if err := bench.Client().HSet(ctx, "s:"+s+":policy", "readers", "2").Err(); err != nil {
			t.Fatalf("bench raw HSET: %v", err)
		}
		r := showPlan(t, bench, s)
		if r.code != 1 || !strings.Contains(r.out, "policy readers plan=1 store=2\n") || !strings.HasSuffix(r.out, "\nDRIFT policy readers plan=1 store=2\n") {
			t.Fatalf("bench drift: code=%d out=\n%s", r.code, r.out)
		}
	})
}
