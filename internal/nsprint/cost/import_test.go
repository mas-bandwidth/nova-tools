package cost_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cost"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

var t0 = time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC)

// multiHook counts MULTI pipelines and lets a case act just before and just after one runs,
// the way internal/benchcount's test counts HGETs.
type multiHook struct {
	n      int
	before func(n int)
	after  func(n int, err error) error
}

func (h *multiHook) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (h *multiHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *multiHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if len(cmds) == 0 || cmds[0].Name() != "multi" {
			return next(ctx, cmds)
		}
		h.n++
		if h.before != nil {
			h.before(h.n)
		}
		err := next(ctx, cmds)
		if h.after != nil {
			return h.after(h.n, err)
		}
		return err
	}
}

type rig struct {
	mr     *miniredis.Miniredis
	client *redis.Client
	hook   *multiHook
}

func newRig(t *testing.T) *rig {
	t.Helper()
	mr := miniredis.RunT(t)
	st := store.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = st.Close() })
	hook := &multiHook{}
	st.Client().AddHook(hook)
	return &rig{mr: mr, client: st.Client(), hook: hook}
}

type result struct {
	code        int
	out, errOut string
}

// run imports one export file the way the verb does after its flags: load, then import.
func (r *rig) run(t *testing.T, provider, path string, now time.Time) result {
	t.Helper()
	e, err := cost.Load(provider, path)
	if err != nil {
		return result{code: cost.Code(err), errOut: err.Error()}
	}
	var out, errOut bytes.Buffer
	code := cost.Import(context.Background(), r.client, e, now, &out, &errOut)
	return result{code: code, out: out.String(), errOut: errOut.String()}
}

func (r *rig) hash(t *testing.T, key string) map[string]string {
	t.Helper()
	h, err := r.client.HGetAll(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("HGETALL %s: %v", key, err)
	}
	return h
}

func (r *rig) score(t *testing.T, member string) float64 {
	t.Helper()
	z, err := r.client.ZScore(context.Background(), cost.IndexKey, member).Result()
	if err != nil {
		t.Fatalf("ZSCORE %s %s: %v", cost.IndexKey, member, err)
	}
	return z
}

func withoutAt(h map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if k != "at" {
			out[k] = v
		}
	}
	return out
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func fixture(name string) string { return filepath.Join("testdata", name) }

// writeFile writes a variant export into the test's own temp directory.
func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// rowSums is the test's own reading of an export: each day's data rows summed in
// micro-dollars, from the day cell's first ten characters and the last column.
func rowSums(t *testing.T, path string) map[string]int64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, rec := range recs[1:] {
		if strings.EqualFold(rec[0], "total") {
			continue
		}
		v, err := strconv.ParseFloat(rec[len(rec)-1], 64)
		if err != nil {
			t.Fatal(err)
		}
		out[rec[0][:10]] += int64(math.Round(v * 1e6))
	}
	return out
}

func micro(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("%q is not a dollar amount: %v", s, err)
	}
	return int64(math.Round(v * 1e6))
}

func TestImportedRowsReconcileToProviderDailyTotal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		provider, file string
		fields         map[string]map[string]string // day -> field -> USD
		unrouted       map[string]string            // day -> unrouted_rows
	}{
		{"anthropic", "anthropic.csv",
			map[string]map[string]string{
				"2026-09-21": {"nova|model:claude-opus-5-5": "12.345678", "nova|model:claude-sonnet-5": "3.210000", "rocketnet|model:claude-opus-5-5": "1.000001"},
				"2026-09-22": {"nova|model:claude-opus-5-5": "20.750000", "nova|model:claude-fable-5-1": "4.125000"},
			},
			map[string]string{"2026-09-21": "3", "2026-09-22": "3"}},
		{"openrouter", "openrouter.csv",
			map[string]map[string]string{
				"2026-09-21": {"swarm|orqwen38": "0.512345", "swarm|orglmflash": "0.730000"},
				"2026-09-22": {"swarm|orqwen38": "0.050505", "probe|model:mystery/model-x": "1.250000"},
			},
			map[string]string{"2026-09-21": "0", "2026-09-22": "1"}},
		{"oc", "oc.csv",
			map[string]map[string]string{
				"2026-09-21": {"swarm|ocglmflash": "2.833333"},
				"2026-09-22": {"swarm|ocglmflash": "1.000000", "swarm|model:unlisted-flash-9": "0.666667"},
			},
			map[string]string{"2026-09-21": "0", "2026-09-22": "1"}},
		{"anthropic", "anthropic-total.csv",
			map[string]map[string]string{
				"2026-09-23": {"nova|model:claude-opus-5-5": "7.500000", "nova|model:claude-sonnet-5": "1.250000"},
			},
			map[string]string{"2026-09-23": "2"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			r := newRig(t)
			res := r.run(t, tc.provider, fixture(tc.file), t0)
			if res.code != cost.ExitOK {
				t.Fatalf("import exit %d, want 0\nstdout %s\nstderr %s", res.code, res.out, res.errOut)
			}
			sums := rowSums(t, fixture(tc.file))
			if len(sums) != len(tc.fields) {
				t.Fatalf("the fixture has %d days, the case names %d", len(sums), len(tc.fields))
			}
			for day, ref := range sums {
				h := r.hash(t, "cost:"+tc.provider+":"+day)
				var fields int64
				n := 0
				for k, v := range h {
					if strings.Contains(k, "|") {
						fields += micro(t, v)
						n++
					}
				}
				if d := fields - ref; d > 10_000 || d < -10_000 {
					t.Errorf("%s: fields sum to %d micro-dollars, the rows to %d", day, fields, ref)
				}
				if got := micro(t, h["total"]); got != ref {
					t.Errorf("%s: total %s, want the rows' sum %d micro-dollars", day, h["total"], ref)
				}
				if n != len(tc.fields[day]) {
					t.Errorf("%s: %d fields, want %d: %v", day, n, len(tc.fields[day]), h)
				}
				for f, usd := range tc.fields[day] {
					if h[f] != usd {
						t.Errorf("%s: field %s = %q, want %s", day, f, h[f], usd)
					}
				}
				if h["unrouted_rows"] != tc.unrouted[day] {
					t.Errorf("%s: unrouted_rows = %q, want %s", day, h["unrouted_rows"], tc.unrouted[day])
				}
				if h["writer"] != cost.Writer || h["source_name"] != tc.file || len(h["source_sha256"]) != 64 {
					t.Errorf("%s: provenance fields wrong: %v", day, h)
				}
				if z := r.score(t, tc.provider+":"+day); z != float64(mustAtoi(t, strings.ReplaceAll(day, "-", ""))) {
					t.Errorf("%s: cost:idx score %v", day, z)
				}
			}
		})
	}

	t.Run("a bad total writes nothing", func(t *testing.T) {
		r := newRig(t)
		res := r.run(t, "openrouter", fixture("openrouter-bad-total.csv"), t0)
		if res.code != cost.ExitReconcile {
			t.Fatalf("exit %d, want 4: %s", res.code, res.errOut)
		}
		if keys := r.mr.Keys(); len(keys) != 0 {
			t.Fatalf("a export that does not reconcile left keys: %v", keys)
		}
		if r.hook.n != 0 {
			t.Fatalf("%d MULTI pipelines on an export that does not reconcile", r.hook.n)
		}
	})

	t.Run("a total row in a multi-day file is a bad export", func(t *testing.T) {
		b, err := os.ReadFile(fixture("oc.csv"))
		if err != nil {
			t.Fatal(err)
		}
		r := newRig(t)
		res := r.run(t, "oc", writeFile(t, "oc-total.csv", string(b)+"total,,,4.5\n"), t0)
		if res.code != cost.ExitExport {
			t.Fatalf("exit %d, want 3: %s", res.code, res.errOut)
		}
	})
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCostImportReimport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const day, key, mem = "2026-09-21", "cost:anthropic:2026-09-21", "anthropic:2026-09-21"
	r := newRig(t)
	first := r.run(t, "anthropic", fixture("anthropic.csv"), t0)
	if first.code != 0 || strings.Count(first.out, "state=new") != 2 {
		t.Fatalf("first import: exit %d\n%s%s", first.code, first.out, first.errOut)
	}
	clean := r.hash(t, key)

	r.hook.n = 0
	again := r.run(t, "anthropic", fixture("anthropic.csv"), t0.Add(time.Hour))
	if again.code != 0 || strings.Count(again.out, "state=same") != 2 ||
		!strings.Contains(again.out, "COST IMPORT DONE provider=anthropic days=2 written=0 same=2 repaired=0 recovered=0") {
		t.Fatalf("same file again: exit %d\n%s%s", again.code, again.out, again.errOut)
	}
	if r.hook.n != 0 {
		t.Fatalf("a same import sent %d MULTI pipelines, want 0", r.hook.n)
	}
	if got := r.hash(t, key)["at"]; got != clean["at"] {
		t.Fatalf("a same import moved at from %s to %s", clean["at"], got)
	}

	for _, dmg := range []struct {
		name   string
		damage func() error
	}{
		{"index member missing", func() error { return r.client.ZRem(ctx, cost.IndexKey, mem).Err() }},
		{"index score wrong", func() error { return r.client.ZAdd(ctx, cost.IndexKey, redis.Z{Score: 1, Member: mem}).Err() }},
		{"a field missing", func() error { return r.client.HDel(ctx, key, "nova|model:claude-sonnet-5").Err() }},
	} {
		t.Run(dmg.name, func(t *testing.T) {
			if err := dmg.damage(); err != nil {
				t.Fatal(err)
			}
			r.hook.n = 0
			res := r.run(t, "anthropic", fixture("anthropic.csv"), t0.Add(2*time.Hour))
			if res.code != 0 || !strings.Contains(res.out, "day="+day+" ") ||
				!strings.Contains(res.out, "state=repaired") || !strings.Contains(res.out, " repaired=1 ") {
				t.Fatalf("repair: exit %d\n%s%s", res.code, res.out, res.errOut)
			}
			if r.hook.n != 1 {
				t.Fatalf("the repair sent %d MULTI pipelines, want 1", r.hook.n)
			}
			if got := r.hash(t, key); !sameMap(withoutAt(got), withoutAt(clean)) {
				t.Fatalf("the repaired hash\n got %v\nwant %v", got, clean)
			}
			if z := r.score(t, mem); z != 20260921 {
				t.Fatalf("the repaired score is %v, want 20260921", z)
			}
			r.hook.n = 0
			next := r.run(t, "anthropic", fixture("anthropic.csv"), t0.Add(3*time.Hour))
			if next.code != 0 || strings.Count(next.out, "state=same") != 2 || r.hook.n != 0 {
				t.Fatalf("after the repair: exit %d, %d MULTI\n%s%s", next.code, r.hook.n, next.out, next.errOut)
			}
		})
	}

	t.Run("a revised file replaces the day whole", func(t *testing.T) {
		b, err := os.ReadFile(fixture("anthropic.csv"))
		if err != nil {
			t.Fatal(err)
		}
		revised := strings.Replace(string(b), "2026-09-21,nova,claude-sonnet-5", "2026-09-21,nova-lab,claude-sonnet-5", 1)
		res := r.run(t, "anthropic", writeFile(t, "anthropic-revised.csv", revised), t0.Add(4*time.Hour))
		if res.code != 0 || !strings.Contains(res.out, "day="+day+" ") || strings.Count(res.out, "state=replaced") != 2 {
			t.Fatalf("revised: exit %d\n%s%s", res.code, res.out, res.errOut)
		}
		h := r.hash(t, key)
		if _, old := h["nova|model:claude-sonnet-5"]; old {
			t.Fatalf("the old file's field survived the replace: %v", h)
		}
		if h["nova-lab|model:claude-sonnet-5"] != "3.210000" {
			t.Fatalf("the revised field is missing: %v", h)
		}
		members, err := r.client.ZRange(ctx, cost.IndexKey, 0, -1).Result()
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(strings.Join(members, " ")+" ", mem+" "); n != 1 {
			t.Fatalf("cost:idx holds %d members for %s: %v", n, mem, members)
		}
	})

	for _, k := range []string{key, cost.IndexKey} {
		ttl, err := r.client.Do(ctx, "TTL", k).Int()
		if err != nil || ttl != -1 {
			t.Fatalf("TTL %s = %d (%v), want -1", k, ttl, err)
		}
	}
}

func TestCostImportWriteOutcomes(t *testing.T) {
	t.Parallel()

	const key21, key22, mem21 = "cost:anthropic:2026-09-21", "cost:anthropic:2026-09-22", "anthropic:2026-09-21"
	file := fixture("anthropic.csv")

	// The hash a clean import of the file writes at t0, for the idempotence checks.
	ref := newRig(t)
	if res := ref.run(t, "anthropic", file, t0); res.code != 0 {
		t.Fatalf("reference import: %d %s", res.code, res.errOut)
	}
	clean21, clean22 := ref.hash(t, key21), ref.hash(t, key22)

	// seeded is a rig holding an older import of both days (before).
	seeded := func(t *testing.T) *rig {
		t.Helper()
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		r := newRig(t)
		older := writeFile(t, "anthropic-older.csv", strings.Replace(string(b), "12.345678", "11.000000", 1))
		if res := r.run(t, "anthropic", older, t0.Add(-24*time.Hour)); res.code != 0 {
			t.Fatalf("seeding the older import: %d %s", res.code, res.errOut)
		}
		r.hook.n = 0
		return r
	}
	toString := func(r *rig) {
		r.mr.Del(cost.IndexKey)
		if err := r.mr.Set(cost.IndexKey, "not-a-zset"); err != nil {
			panic(err)
		}
	}
	noClaim := func(t *testing.T, res result) {
		t.Helper()
		if strings.Contains(res.out+res.errOut, "nothing written") {
			t.Fatalf("a write that may have applied claims nothing written:\n%s%s", res.out, res.errOut)
		}
	}
	idempotent := func(t *testing.T, r *rig) {
		t.Helper()
		if !sameMap(r.hash(t, key21), clean21) || !sameMap(r.hash(t, key22), clean22) {
			t.Fatalf("the recovered hashes differ from a clean import's:\n%v\n%v", r.hash(t, key21), clean21)
		}
		if z := r.score(t, mem21); z != 20260921 {
			t.Fatalf("ZSCORE %s = %v, want 20260921", mem21, z)
		}
	}

	t.Run("pre-write", func(t *testing.T) {
		r := seeded(t)
		before21, before22 := r.hash(t, key21), r.hash(t, key22)
		toString(r)
		res := r.run(t, "anthropic", file, t0)
		if res.code != cost.ExitPreWrite || !strings.Contains(res.errOut, "nothing written") {
			t.Fatalf("exit %d, want 6 with nothing written\n%s%s", res.code, res.out, res.errOut)
		}
		if r.hook.n != 0 {
			t.Fatalf("%d MULTI pipelines on a pre-write failure, want 0", r.hook.n)
		}
		if !sameMap(r.hash(t, key21), before21) || !sameMap(r.hash(t, key22), before22) {
			t.Fatal("a pre-write failure changed the seeded hashes")
		}
	})

	t.Run("command error, not recovered", func(t *testing.T) {
		r := seeded(t)
		var totalAfterExec string
		r.hook.before = func(n int) {
			if n == 1 {
				toString(r)
			}
		}
		r.hook.after = func(n int, err error) error {
			if n == 1 {
				totalAfterExec = r.mr.HGet(key21, "total")
			}
			return err
		}
		res := r.run(t, "anthropic", file, t0)
		if totalAfterExec != clean21["total"] {
			t.Fatalf("after the failed EXEC total = %q, want %q: EXEC applied the HSET beside the failed ZADD", totalAfterExec, clean21["total"])
		}
		// The literal 7, not cost.ExitPartial: the exit number is the contract, so a
		// change to the constant must fail here.
		if res.code != 7 {
			t.Fatalf("exit %d, want 7 (partial; rev 7: exits are scoped per verb)\n%s%s", res.code, res.out, res.errOut)
		}
		if !strings.Contains(res.out, "state=partial") || strings.Contains(res.out, "state=unknown") {
			t.Fatalf("want state=partial and no state=unknown:\n%s", res.out)
		}
		for _, w := range []string{"ZADD cost:idx", "ZSCORE cost:idx", "WRONGTYPE"} {
			if !strings.Contains(res.errOut, w) {
				t.Errorf("stderr does not name %q:\n%s", w, res.errOut)
			}
		}
		// ZSCORE's WRONGTYPE was a reply received in both read-backs: once per read-back
		// per day, so four lines, and never an unknown.
		if n := strings.Count(res.errOut, "ZSCORE cost:idx"); n != 4 {
			t.Errorf("ZSCORE cost:idx error replies named %d times, want 4 (two read-backs, two days):\n%s", n, res.errOut)
		}
		if r.hook.n != 2 {
			t.Fatalf("%d MULTI pipelines, want 2 (the write and one retry)", r.hook.n)
		}
		noClaim(t, res)
	})

	t.Run("command error, recovered", func(t *testing.T) {
		r := seeded(t)
		r.hook.before = func(n int) {
			switch n {
			case 1:
				toString(r)
			case 2:
				r.mr.Del(cost.IndexKey)
			}
		}
		res := r.run(t, "anthropic", file, t0)
		if res.code != cost.ExitOK || strings.Count(res.out, "recovered=1") != 2 || !strings.Contains(res.out, " recovered=2\n") {
			t.Fatalf("exit %d, want 0 with recovered=1 on each day\n%s%s", res.code, res.out, res.errOut)
		}
		if r.hook.n != 2 {
			t.Fatalf("%d MULTI pipelines, want 2", r.hook.n)
		}
		idempotent(t, r)
		noClaim(t, res)
	})

	t.Run("unknown, recovered", func(t *testing.T) {
		r := seeded(t)
		r.hook.after = func(n int, err error) error {
			if n == 1 {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		res := r.run(t, "anthropic", file, t0)
		if res.code != cost.ExitOK || strings.Count(res.out, "recovered=1") != 2 {
			t.Fatalf("exit %d, want 0 with recovered=1\n%s%s", res.code, res.out, res.errOut)
		}
		if r.hook.n != 1 {
			t.Fatalf("%d MULTI pipelines, want exactly 1: the read-back showed written, so no retry", r.hook.n)
		}
		idempotent(t, r)
		noClaim(t, res)
	})

	t.Run("unknown, read-back fails", func(t *testing.T) {
		r := seeded(t)
		r.hook.after = func(n int, err error) error {
			if n == 1 {
				r.mr.Close()
				return io.ErrUnexpectedEOF
			}
			return err
		}
		res := r.run(t, "anthropic", file, t0)
		// The literal 8, not cost.ExitUnknown: the exit number is the contract, so a
		// change to the constant must fail here.
		if res.code != 8 {
			t.Fatalf("exit %d, want 8 (outcome unknown; rev 7: exits are scoped per verb)\n%s%s", res.code, res.out, res.errOut)
		}
		if strings.Count(res.out, "state=unknown") != 2 {
			t.Fatalf("want state=unknown on both changed days:\n%s", res.out)
		}
		if !strings.Contains(res.errOut, "outcome unknown") || !strings.Contains(res.errOut, "re-run the same import (it is idempotent)") {
			t.Fatalf("stderr does not say the outcome is unknown:\n%s", res.errOut)
		}
		if r.hook.n != 1 {
			t.Fatalf("%d MULTI pipelines, want 1 (no retry after a read-back with no reply)", r.hook.n)
		}
		noClaim(t, res)
	})
}
