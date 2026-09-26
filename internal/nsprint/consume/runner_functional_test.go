//go:build functional

package consume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

const (
	c33Sprint = "c33"
	c33Repo   = "mas-bandwidth/nova-tools" // ev:github names the full repo
	c33Short  = "nova-tools"               // sprint records and ci keys name the short repo
	c33Head   = "3333aaaa3333aaaa3333aaaa3333aaaa3333aaaa"
	c33Rows   = "windows, s390x nova-tools:platform-selftest other-repo:linux"
)

type c33Fix struct {
	t      *testing.T
	ctx    context.Context
	client *redis.Client
	rule   *PRToReadRule
	out    *bytes.Buffer
	cuts   atomic.Int64
}

func newC33(t *testing.T) *c33Fix {
	t.Helper()
	st, client := controlRedis(t)
	seedSprint(t, client, c33Sprint)
	ctx := context.Background()
	must(t, client.HSet(ctx, "s:"+c33Sprint+":policy", "runner_rows", c33Rows).Err())
	must(t, client.HSet(ctx, civerdict.PolicyKey(c33Short, "dev"), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1").Err())
	must(t, client.HSet(ctx, civerdict.TipKey(c33Short, "dev"), "sha", c33Head).Err())
	// The check_runs name PR 7; its sprint record names the base the runner
	// rows resolve their gid from (it is not in s:<S>:prs, so nothing adopts it).
	must(t, client.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:7", c33Sprint, c33Short), "head", c33Head, "base", "dev").Err())
	f := &c33Fix{t: t, ctx: ctx, client: client, out: &bytes.Buffer{}}
	f.rule = &PRToReadRule{Store: st, Sprint: c33Sprint, Consumer: "c33-route", Out: f.out,
		Block: -1, Actor: "route",
		CICut: func(context.Context, CICut) error { f.cuts.Add(1); return nil }}
	must(t, f.rule.Start(ctx))
	return f
}

// check publishes one check_run delivery the way the nova-post hook does.
func (f *c33Fix) check(id, name, action, status, conclusion, at string) {
	f.t.Helper()
	_, err := ghevent.Publish(f.ctx, f.client, ghevent.Entry{
		Repo: c33Repo, Kind: "check_run", Number: "7", Head: c33Head, Action: action, At: at,
		Sender: "github-actions", Check: name, CheckRunID: id, Status: status, Conclusion: conclusion,
	})
	must(f.t, err)
}

// pass runs one Pass and returns what it printed.
func (f *c33Fix) pass() string {
	f.t.Helper()
	f.out.Reset()
	if _, err := f.rule.Pass(f.ctx); err != nil {
		f.t.Fatalf("pass: %v", err)
	}
	return f.out.String()
}

// apply publishes one check_run, passes, and returns the RUNNER line.
func (f *c33Fix) apply(id, name, action, status, conclusion, at string) string {
	f.t.Helper()
	f.check(id, name, action, status, conclusion, at)
	for _, line := range strings.Split(f.pass(), "\n") {
		if strings.HasPrefix(line, "RUNNER ") {
			return line
		}
	}
	f.t.Fatalf("no RUNNER line for %s %s %s/%s", name, id, status, conclusion)
	return ""
}

// ci is the runner rows at the gid the lander expects for base dev at its tip.
func (f *c33Fix) ci() map[string]string {
	f.t.Helper()
	gid, err := civerdict.Expected(f.ctx, f.client, c33Short, "dev", c33Head)
	must(f.t, err)
	m, err := f.client.HGetAll(f.ctx, civerdict.RunnersKey(c33Short, c33Head, gid)).Result()
	must(f.t, err)
	return m
}

func (f *c33Fix) attempt(row string) RunnerAttempt {
	f.t.Helper()
	a, ok := ReadRunnerAttempt(f.ci(), row)
	if !ok {
		f.t.Fatalf("ci for %s@%s has no runner:%s", c33Short, c33Head, row)
	}
	return a
}

// want asserts the RUNNER line's verdict, the stored attempt and readiness.
func (f *c33Fix) want(line, verdict string, gen int, id, status, row, state string) {
	f.t.Helper()
	if !strings.HasSuffix(line, " "+verdict) {
		f.t.Fatalf("line %q, want %s", line, verdict)
	}
	a := f.attempt(row)
	if a.Gen != gen || a.CheckRunID != id || a.Status != status {
		f.t.Fatalf("runner:%s = %+v, want gen %d id %s status %s", row, a, gen, id, status)
	}
	if got := RunnerRow(f.ci(), row); got != state {
		f.t.Fatalf("runner:%s is %s, want %s (%+v)", row, got, state, a)
	}
	ok, missing := RunnerReady(f.ci(), []string{row})
	if ok != (state == RunnerStateReady) {
		f.t.Fatalf("RunnerReady = %v %v with %s %s", ok, missing, row, state)
	}
	if state == RunnerStateMissing && !reflect.DeepEqual(missing, []string{row}) {
		f.t.Fatalf("missing = %v, want [%s]", missing, row)
	}
}

func (f *c33Fix) pending() int64 {
	f.t.Helper()
	p, err := f.client.XPending(f.ctx, ghevent.Stream, f.rule.Group()).Result()
	must(f.t, err)
	return p.Count
}

// seedPR writes one sprint PR record the way the lander's records hold it.
func (f *c33Fix) seedPR(n int, author, draft, state string) string {
	f.t.Helper()
	head := fmt.Sprintf("%040d", n)
	must(f.t, f.client.HSet(f.ctx, fmt.Sprintf("s:%s:pr:%s:%d", c33Sprint, c33Short, n),
		"head", head, "state", state, "draft", draft, "author", author, "base", "dev").Err())
	must(f.t, f.client.SAdd(f.ctx, "s:"+c33Sprint+":prs", fmt.Sprintf("%s#%d", c33Short, n)).Err())
	return head
}

func (f *c33Fix) tasks() []string {
	f.t.Helper()
	keys, err := f.client.Keys(f.ctx, "task:*").Result()
	must(f.t, err)
	return keys
}

// roundTrips records every command and pipeline the client sends.
type roundTrips struct {
	mu    sync.Mutex
	trips [][]redis.Cmder
}

func (r *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		r.mu.Lock()
		r.trips = append(r.trips, []redis.Cmder{cmd})
		r.mu.Unlock()
		return err
	}
}

func (r *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		err := next(ctx, cmds)
		r.mu.Lock()
		r.trips = append(r.trips, cmds)
		r.mu.Unlock()
		return err
	}
}

func TestControl33PrToRead(t *testing.T) {
	var httpCalls atomic.Int64
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		httpCalls.Add(1)
		return nil, errors.New("control 33: no HTTP client may run")
	})
	t.Cleanup(func() { http.DefaultTransport = orig })

	const (
		t1 = "2026-09-23T20:00:01Z"
		t2 = "2026-09-23T20:00:02Z"
		t3 = "2026-09-23T20:00:03Z"
		t4 = "2026-09-23T20:00:04Z"
		t5 = "2026-09-23T20:00:05Z"
	)

	t.Run("windows_green_one_key", func(t *testing.T) {
		f := newC33(t)
		line := f.apply("100", "windows", "completed", "completed", "success", t1)
		if want := "RUNNER " + c33Short + "@3333aaaa windows g0/100 completed/success REPLACED"; line != want {
			t.Fatalf("line %q, want %q", line, want)
		}
		f.want(line, "REPLACED", 0, "100", "completed", "windows", RunnerStateReady)
		if a := f.attempt("windows"); a.Source != "runner:windows" || a.At != t1 {
			t.Fatalf("runner:windows = %+v, want source runner:windows at %s", a, t1)
		}
		keys, err := f.client.Keys(f.ctx, "ci:*").Result()
		must(t, err)
		sort.Strings(keys)
		expGID := civerdict.GID("single", "dev", c33Head, "req1", "pol1", "run1")
		// Only the runners key: never the write-once receipt or its gids set.
		wantKeys := []string{"ci:" + c33Short + ":" + c33Head + ":" + expGID + ":runners"}
		if !reflect.DeepEqual(keys, wantKeys) {
			t.Fatalf("ci keys %v, want %v", keys, wantKeys)
		}
		// RunnerReady reads only the ci record it is handed.
		if ok, missing := RunnerReady(map[string]string{}, []string{"windows"}); ok || !reflect.DeepEqual(missing, []string{"windows"}) {
			t.Fatalf("an empty record is ready=%v missing=%v", ok, missing)
		}
	})

	t.Run("cancelled_row_missing", func(t *testing.T) {
		f := newC33(t)
		f.apply("100", "windows", "completed", "completed", "success", t1)
		line := f.apply("200", "s390x", "completed", "completed", "cancelled", t1)
		f.want(line, "REPLACED", 0, "200", "completed", "s390x", RunnerStateMissing)
		ok, missing := RunnerReady(f.ci(), RunnerRows(c33Rows, c33Short))
		if ok || !reflect.DeepEqual(missing, []string{"s390x", "platform-selftest"}) {
			t.Fatalf("head ready=%v missing=%v, want not ready, missing [s390x platform-selftest]", ok, missing)
		}
	})

	t.Run("unnamed_row_not_copied", func(t *testing.T) {
		f := newC33(t)
		f.check("300", "linux", "completed", "completed", "success", t1)
		out := f.pass()
		if strings.Contains(out, "RUNNER") {
			t.Fatalf("a check not in runner_rows printed %q", out)
		}
		if keys, _ := f.client.Keys(f.ctx, "ci:*").Result(); len(keys) != 0 {
			t.Fatalf("a check not in runner_rows wrote ci keys: %v", keys)
		}
		if p := f.pending(); p != 0 {
			t.Fatalf("pending %d after the pass, want 0", p)
		}
	})

	for _, c := range []string{"cancelled", "skipped"} {
		t.Run("success_then_"+c+"_replaces", func(t *testing.T) {
			f := newC33(t)
			f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "REPLACED", 0, "100", "completed", "windows", RunnerStateReady)
			line := f.apply("101", "windows", "completed", "completed", c, t2)
			f.want(line, "REPLACED", 0, "101", "completed", "windows", RunnerStateMissing)
			if a := f.attempt("windows"); a.Conclusion != c {
				t.Fatalf("runner:windows = %+v, want conclusion %s", a, c)
			}
		})
	}

	t.Run("rerun_queued_replaces", func(t *testing.T) {
		f := newC33(t)
		f.apply("100", "windows", "completed", "completed", "success", t1)
		f.want(f.apply("101", "windows", "created", "queued", "", t2), "REPLACED", 0, "101", "queued", "windows", RunnerStateMissing)
		f.want(f.apply("101", "windows", "completed", "completed", "success", t3), "REPLACED", 0, "101", "completed", "windows", RunnerStateReady)
	})

	t.Run("older_attempt_kept", func(t *testing.T) {
		f := newC33(t)
		f.want(f.apply("101", "windows", "completed", "completed", "cancelled", t2), "REPLACED", 0, "101", "completed", "windows", RunnerStateMissing)
		f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "KEPT", 0, "101", "completed", "windows", RunnerStateMissing)
	})

	t.Run("rerequested_same_id_old_payload", func(t *testing.T) {
		// Steps 1-4 are the same for every variant: the green, the rerequest
		// carrying the old green, its redelivery, a late copy of the old green.
		rerun := func(t *testing.T) *c33Fix {
			f := newC33(t)
			f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "REPLACED", 0, "100", "completed", "windows", RunnerStateReady)
			line := f.apply("100", "windows", "rerequested", "completed", "success", t1)
			if !strings.Contains(line, " g1/100 rerequested/ ") {
				t.Fatalf("rerequest line %q, want g1/100 rerequested/", line)
			}
			f.want(line, "RERUN", 1, "100", "rerequested", "windows", RunnerStateMissing)
			f.want(f.apply("100", "windows", "rerequested", "completed", "success", t1), "KEPT", 1, "100", "rerequested", "windows", RunnerStateMissing)
			f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "KEPT", 1, "100", "rerequested", "windows", RunnerStateMissing)
			return f
		}
		t.Run("same_id_completes_later", func(t *testing.T) {
			f := rerun(t)
			f.want(f.apply("100", "windows", "completed", "completed", "success", t2), "REPLACED", 1, "100", "completed", "windows", RunnerStateReady)
		})
		t.Run("new_id_queued_then_green", func(t *testing.T) {
			f := rerun(t)
			f.want(f.apply("101", "windows", "created", "queued", "", t2), "REPLACED", 1, "101", "queued", "windows", RunnerStateMissing)
			f.want(f.apply("101", "windows", "completed", "completed", "success", t3), "REPLACED", 1, "101", "completed", "windows", RunnerStateReady)
		})
		t.Run("new_generation_fails", func(t *testing.T) {
			f := rerun(t)
			f.want(f.apply("100", "windows", "completed", "completed", "failure", t2), "REPLACED", 1, "100", "completed", "windows", RunnerStateFail)
			f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "KEPT", 1, "100", "completed", "windows", RunnerStateFail)
		})
	})

	t.Run("rerequested_then_queued_then_completed_same_id", func(t *testing.T) {
		f := newC33(t)
		f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "REPLACED", 0, "100", "completed", "windows", RunnerStateReady)
		f.want(f.apply("100", "windows", "rerequested", "completed", "success", t1), "RERUN", 1, "100", "rerequested", "windows", RunnerStateMissing)
		// rank 0 (queued) beats the sentinel's -1 at an equal id and a later at.
		f.want(f.apply("100", "windows", "created", "queued", "", t2), "REPLACED", 1, "100", "queued", "windows", RunnerStateMissing)
		f.want(f.apply("100", "windows", "completed", "completed", "success", t3), "REPLACED", 1, "100", "completed", "windows", RunnerStateReady)
		f.want(f.apply("101", "windows", "completed", "completed", "success", t4), "REPLACED", 1, "101", "completed", "windows", RunnerStateReady)
		// A rerequest of an attempt a newer id superseded bumps nothing.
		f.want(f.apply("100", "windows", "rerequested", "completed", "success", t5), "KEPT", 1, "101", "completed", "windows", RunnerStateReady)
	})

	t.Run("redelivery_noop", func(t *testing.T) {
		f := newC33(t)
		f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "REPLACED", 0, "100", "completed", "windows", RunnerStateReady)
		before := f.ci()
		f.want(f.apply("100", "windows", "completed", "completed", "success", t1), "KEPT", 0, "100", "completed", "windows", RunnerStateReady)
		if after := f.ci(); !reflect.DeepEqual(before, after) {
			t.Fatalf("a redelivered entry wrote: %v -> %v", before, after)
		}
	})

	t.Run("drain_one_pipeline", func(t *testing.T) {
		f := newC33(t)
		f.check("100", "windows", "created", "queued", "", t1)
		f.check("100", "windows", "completed", "completed", "success", t2)
		f.check("300", "linux", "completed", "completed", "success", t2)
		_, err := ghevent.Publish(f.ctx, f.client, ghevent.Entry{Repo: c33Repo, Kind: "pull_request",
			Number: "7", Head: c33Head, Action: "synchronize", At: t2, Sender: "rowan"})
		must(t, err)
		f.check("101", "windows", "created", "queued", "", t3)
		rt := &roundTrips{}
		f.client.AddHook(rt)
		out := f.pass()
		// The XREADGROUP that returned the five entries, then exactly four
		// round trips (the PR's base, the base's tip and policy, the FCALLs,
		// the XACK), then the next XREADGROUP.
		at := -1
		for i, trip := range rt.trips {
			if len(trip) != 1 || trip[0].Name() != "xreadgroup" {
				continue
			}
			if xs, ok := trip[0].(*redis.XStreamSliceCmd); ok && len(xs.Val()) == 1 && len(xs.Val()[0].Messages) == 5 {
				at = i
				break
			}
		}
		if at < 0 || at+5 >= len(rt.trips) {
			t.Fatalf("no XREADGROUP returned the 5 entries followed by 5 round trips: %d trips", len(rt.trips))
		}
		bases, pols, pipe, ack, next := rt.trips[at+1], rt.trips[at+2], rt.trips[at+3], rt.trips[at+4], rt.trips[at+5]
		if len(bases) != 1 || bases[0].Name() != "hget" || fmt.Sprint(bases[0].Args()[1]) != "s:"+c33Sprint+":pr:"+c33Short+":7" {
			t.Fatalf("first round trip after the read is %v, want one HGET of PR 7's base (three rows, one PR)", bases)
		}
		if len(pols) != 2 || pols[0].Name() != "hget" || pols[1].Name() != "hmget" {
			t.Fatalf("second round trip after the read is %v, want the base's tip and policy in one pipeline", pols)
		}
		if len(pipe) != 3 {
			t.Fatalf("round trip after the read has %d commands, want one pipeline of 3 FCALLs", len(pipe))
		}
		for i, want := range []string{"100", "100", "101"} {
			args := pipe[i].Args()
			if strings.ToLower(fmt.Sprint(args[0])) != "fcall" || fmt.Sprint(args[1]) != FunctionPRToReadRunner {
				t.Fatalf("pipeline command %d is %v, want FCALL %s", i, args, FunctionPRToReadRunner)
			}
			if a := fmt.Sprint(args[len(args)-1]); !strings.Contains(a, `"check_run_id":"`+want+`"`) {
				t.Fatalf("pipeline command %d carries %s, want check_run_id %s (stream order)", i, a, want)
			}
		}
		if len(ack) != 1 || ack[0].Name() != "xack" || len(ack[0].Args()) != 3+5 ||
			fmt.Sprint(ack[0].Args()[1]) != ghevent.Stream || fmt.Sprint(ack[0].Args()[2]) != f.rule.Group() {
			t.Fatalf("second round trip is %v, want one XACK %s %s with the 5 ids", ack[0].Args(), ghevent.Stream, f.rule.Group())
		}
		if len(next) != 1 || next[0].Name() != "xreadgroup" {
			t.Fatalf("third round trip is %v, want the next XREADGROUP", next[0].Args())
		}
		if p := f.pending(); p != 0 {
			t.Fatalf("pending %d, want 0", p)
		}
		if got := strings.Count(out, "RUNNER "); got != 3 {
			t.Fatalf("%d RUNNER lines, want 3:\n%s", got, out)
		}
		f.want("x REPLACED", "REPLACED", 0, "101", "queued", "windows", RunnerStateMissing)
	})

	t.Run("adopt_once", func(t *testing.T) {
		f := newC33(t)
		must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":policy", "readers", "2").Err())
		head := f.seedPR(7, "ctl-a", "false", "opened")
		out := f.pass()
		want := fmt.Sprintf("ADOPT %s#7@%s cut=1 reads=ctl-b,ctl-c", c33Short, head[:12])
		if !strings.Contains(out, want) {
			t.Fatalf("first pass printed %q, want %q", out, want)
		}
		if f.cuts.Load() != 1 {
			t.Fatalf("ci cut called %d times, want 1", f.cuts.Load())
		}
		for _, friend := range []string{"ctl-b", "ctl-c"} {
			id := task.ReviewID(c33Short, 7, head, friend)
			got, err := f.client.HGetAll(f.ctx, "task:"+id).Result()
			must(t, err)
			if got["kind"] != "review" || got["head"] != head || got["state"] != "open" ||
				!strings.HasSuffix(got["title"], ReadTitleSuffix) {
				t.Fatalf("task %s = %v, want an open review at %s ending %q", id, got, head, ReadTitleSuffix)
			}
			if s, err := f.client.ZScore(f.ctx, "s:"+c33Sprint+":open:"+friend, id).Result(); err != nil || s >= 0 {
				t.Fatalf("%s not at the front of open:%s (%v %v)", id, friend, s, err)
			}
		}
		if n := len(f.tasks()); n != 2 {
			t.Fatalf("%d tasks, want 2", n)
		}
		adopt, err := f.client.HGetAll(f.ctx, "s:"+c33Sprint+":adopt:"+c33Short+":7").Result()
		must(t, err)
		if adopt["head"] != head || adopt["readers"] != "ctl-b,ctl-c" || adopt["cut_at"] == "" || adopt["at"] == "" {
			t.Fatalf("adopt record %v", adopt)
		}
		out = f.pass()
		if !strings.Contains(out, fmt.Sprintf("SKIP %s#7 adopted", c33Short)) || strings.Contains(out, "ADOPT") {
			t.Fatalf("second pass printed %q, want SKIP adopted and no ADOPT", out)
		}
		if f.cuts.Load() != 1 || len(f.tasks()) != 2 {
			t.Fatalf("second pass: cuts %d tasks %d, want 1 and 2", f.cuts.Load(), len(f.tasks()))
		}
		// A new head is a new adoption.
		newHead := strings.Repeat("9", 40)
		must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":pr:"+c33Short+":7", "head", newHead).Err())
		if out = f.pass(); !strings.Contains(out, fmt.Sprintf("ADOPT %s#7@%s", c33Short, newHead[:12])) {
			t.Fatalf("new head printed %q, want ADOPT at %s", out, newHead[:12])
		}
		if f.cuts.Load() != 2 || len(f.tasks()) != 4 {
			t.Fatalf("new head: cuts %d tasks %d, want 2 and 4", f.cuts.Load(), len(f.tasks()))
		}
	})

	t.Run("author_and_jev_excluded", func(t *testing.T) {
		f := newC33(t)
		for _, friend := range []string{"ctl-b", "ctl-c"} {
			must(t, f.client.Del(f.ctx, "friend:"+friend+":beat").Err())
		}
		must(t, f.client.SAdd(f.ctx, "friends", "jev").Err())
		must(t, f.client.HSet(f.ctx, "friend:jev:desired", "slots", "8", "paused", "0").Err())
		must(t, f.client.HSet(f.ctx, "friend:jev:beat", "harness", "ctl", "at", strconv.FormatInt(time.Now().UnixMilli(), 10)).Err())
		f.seedPR(7, "ctl-a", "false", "opened")
		out := f.pass()
		if !strings.Contains(out, fmt.Sprintf("WAIT %s#7 no readers", c33Short)) || strings.Contains(out, "ADOPT") {
			t.Fatalf("pass printed %q, want WAIT no readers", out)
		}
		if len(f.tasks()) != 0 || f.cuts.Load() != 0 {
			t.Fatalf("tasks %v cuts %d, want none", f.tasks(), f.cuts.Load())
		}
		if n, _ := f.client.Exists(f.ctx, "s:"+c33Sprint+":adopt:"+c33Short+":7").Result(); n != 0 {
			t.Fatal("an adoption with no readers wrote its adopt record")
		}
		// No author: WAIT, never a guess.
		f.seedPR(8, "", "false", "opened")
		if out = f.pass(); !strings.Contains(out, fmt.Sprintf("WAIT %s#8 author MISSING", c33Short)) {
			t.Fatalf("pass printed %q, want WAIT author MISSING", out)
		}
	})

	t.Run("card_and_hold_skipped", func(t *testing.T) {
		f := newC33(t)
		// PR 8 was produced by a card: s:<S>:prcard names it.
		const label, bench = "c33-card", ctlBench
		cardHead := f.seedPR(8, "ctl-a", "false", "opened")
		must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":card:"+label, "state", "ended", "outcome", "DONE",
			"attempt", "1", "bench", bench, "repo", c33Short, "pushed_sha", cardHead, "identity", "c33/id").Err())
		must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":prcard", c33Short+"#8", label).Err())
		// PR 9 has an open hold; PR 10's hold is released; PR 11 is a draft.
		f.seedPR(9, "ctl-a", "false", "opened")
		must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":hold:"+c33Short+":9", "ctl-b",
			`{"holder":"ctl-b","head":"x","released_by":""}`).Err())
		head10 := f.seedPR(10, "ctl-a", "false", "opened")
		must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":hold:"+c33Short+":10", "ctl-b",
			`{"holder":"ctl-b","head":"x","released_by":"ctl-b"}`).Err())
		f.seedPR(11, "ctl-a", "true", "opened")
		f.seedPR(12, "ctl-a", "false", "landed")
		out := f.pass()
		for _, want := range []string{
			fmt.Sprintf("SKIP %s#8 card %s", c33Short, label),
			fmt.Sprintf("SKIP %s#9 hold", c33Short),
			fmt.Sprintf("ADOPT %s#10@%s", c33Short, head10[:12]),
			fmt.Sprintf("SKIP %s#11 draft", c33Short),
			fmt.Sprintf("SKIP %s#12 state landed", c33Short),
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("pass printed %q, want %q", out, want)
			}
		}
		if strings.Count(out, "ADOPT ") != 1 || f.cuts.Load() != 1 {
			t.Fatalf("adoptions %d cuts %d, want only PR 10:\n%s", strings.Count(out, "ADOPT "), f.cuts.Load(), out)
		}
	})

	t.Run("no_http", func(t *testing.T) {
		f := newC33(t)
		f.seedPR(7, "ctl-a", "false", "opened")
		f.check("100", "windows", "completed", "completed", "success", t1)
		out := f.pass()
		if !strings.Contains(out, "RUNNER ") || !strings.Contains(out, "ADOPT ") {
			t.Fatalf("pass printed %q, want a RUNNER and an ADOPT line", out)
		}
		if _, err := http.DefaultTransport.RoundTrip(&http.Request{}); err == nil {
			t.Fatal("http.DefaultTransport is not the failing guard")
		}
		if n := httpCalls.Load(); n != 1 {
			t.Fatalf("%d HTTP requests, want only this subtest's own probe of the guard", n)
		}
	})
}

// TestStoreCICut: the cut route wires (Stella's hold 5 on #3532, Rowan's
// hold 4 item 2). A branch base resolves through land:<repo>:<base>:tip; with
// no tip the adoption WAITs (no tasks, no adopt record), so the same head
// adopts with cut=1 once the tip is recorded; the cut writes only the card, so
// the runner rows at ci:<repo>:<head>:<gid>:runners are untouched.
func TestStoreCICut(t *testing.T) {
	t.Parallel()

	f := newC33(t)
	f.rule.CICut = StoreCICut(f.rule.Store, "route")
	must(t, f.client.HSet(f.ctx, "bench:"+ctlBench+":desired", "slots", "4", "paused", "0", "legs", "go").Err())
	must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":policy", "readers", "1").Err())

	must(t, f.client.Del(f.ctx, civerdict.TipKey(c33Short, "dev")).Err())
	head := f.seedPR(7, "ctl-a", "false", "opened")
	akey := "s:" + c33Sprint + ":adopt:" + c33Short + ":7"
	for i := 0; i < 2; i++ {
		out := f.pass()
		if !strings.Contains(out, fmt.Sprintf("WAIT %s#7 no base tip", c33Short)) || strings.Contains(out, "ADOPT") {
			t.Fatalf("no tip, pass %d printed %q, want WAIT no base tip and no ADOPT", i, out)
		}
		if n := len(f.tasks()); n != 0 {
			t.Fatalf("no tip, pass %d: %d tasks, want 0", i, n)
		}
		if ex, err := f.client.Exists(f.ctx, akey).Result(); err != nil || ex != 0 {
			t.Fatalf("no tip, pass %d: %s exists=%d err=%v, want no adopt record", i, akey, ex, err)
		}
	}

	tip := strings.Repeat("b", 40)
	must(t, f.client.HSet(f.ctx, "land:"+c33Short+":dev:tip", "sha", tip).Err())
	gid, err := civerdict.Expected(f.ctx, f.client, c33Short, "dev", tip)
	must(t, err)
	runners := civerdict.RunnersKey(c33Short, head, gid)
	must(t, f.client.HSet(f.ctx, runners, "runner:windows", `{"status":"completed"}`).Err())
	out := f.pass()
	if !strings.Contains(out, fmt.Sprintf("ADOPT %s#7@%s cut=1", c33Short, head[:12])) || strings.Contains(out, "WAIT") {
		t.Fatalf("tip set: pass printed %q, want ADOPT cut=1 at the same head", out)
	}
	card, err := f.client.HMGet(f.ctx, "s:"+c33Sprint+":card:"+ci.Label(7, head, tip), "state", "verdict", "base", "base_sha").Result()
	must(t, err)
	if card[0] != "queued" || card[1] != "PENDING" || card[2] != "dev" || card[3] != tip {
		t.Fatalf("ci card state/verdict/base/base_sha = %v, want queued PENDING dev %s", card, tip)
	}
	// The cut writes only the card: the runner rows stay, no receipt appears.
	if got, _ := f.client.HGet(f.ctx, runners, "runner:windows").Result(); got != `{"status":"completed"}` {
		t.Fatalf("runner:windows after the cut = %q, want kept", got)
	}
	if n, _ := f.client.Exists(f.ctx, civerdict.Key(c33Short, head, gid), civerdict.GIDsKey(c33Short, head)).Result(); n != 0 {
		t.Fatalf("the cut wrote %d receipt keys for %s, want 0", n, head[:8])
	}
	if cutAt, err := f.client.HGet(f.ctx, akey, "cut_at").Result(); err != nil || cutAt == "" {
		t.Fatalf("adopt record cut_at %q err %v, want set", cutAt, err)
	}
}

// TestPRToReadAdoptRechecksLive: Stella's hold 5 item 2 on #3532. The rule
// reads the PR record, card and holds, then calls ns_prtoread_adopt; the
// Function rechecks them atomically with its writes, so a head that moved or
// a card, hold, draft or close that landed in between writes no review task
// and no adopt record.
func TestPRToReadAdoptRechecksLive(t *testing.T) {
	t.Parallel()

	f := newC33(t)
	head := f.seedPR(8, "ctl-a", "false", "opened")
	pkey := "s:" + c33Sprint + ":pr:" + c33Short + ":8"
	hkey := "s:" + c33Sprint + ":hold:" + c33Short + ":8"
	adopt := func() []string {
		t.Helper()
		id := task.ReviewID(c33Short, 8, head, "ctl-b")
		reply, err := f.client.FCall(f.ctx, FunctionPRToReadAdopt, nil, c33Sprint, c33Short, "8", head, "route", "1", "1",
			id, "ctl-b", "read", "0", "sha-"+id, c33Short+"#8").StringSlice()
		must(t, err)
		return reply
	}
	for _, c := range []struct {
		name  string
		set   func()
		reset func()
		want  string
	}{
		{"head moved", func() { must(t, f.client.HSet(f.ctx, pkey, "head", strings.Repeat("d", 40)).Err()) },
			func() { must(t, f.client.HSet(f.ctx, pkey, "head", head).Err()) }, "WAIT head moved"},
		{"closed", func() { must(t, f.client.HSet(f.ctx, pkey, "state", "closed").Err()) },
			func() { must(t, f.client.HSet(f.ctx, pkey, "state", "opened").Err()) }, "WAIT state closed"},
		{"draft", func() { must(t, f.client.HSet(f.ctx, pkey, "draft", "true").Err()) },
			func() { must(t, f.client.HSet(f.ctx, pkey, "draft", "false").Err()) }, "WAIT draft"},
		{"card", func() { must(t, f.client.HSet(f.ctx, "s:"+c33Sprint+":prcard", c33Short+"#8", "c-8").Err()) },
			func() { must(t, f.client.HDel(f.ctx, "s:"+c33Sprint+":prcard", c33Short+"#8").Err()) }, "WAIT card c-8"},
		{"open hold", func() { must(t, f.client.HSet(f.ctx, hkey, "ctl-c", `{"holder":"ctl-c","head":"x"}`).Err()) },
			func() { must(t, f.client.Del(f.ctx, hkey).Err()) }, "WAIT hold"},
		{"unreadable hold", func() { must(t, f.client.HSet(f.ctx, hkey, "ctl-c", `not json`).Err()) },
			func() { must(t, f.client.Del(f.ctx, hkey).Err()) }, "WAIT hold"},
	} {
		c.set()
		if got := strings.Join(adopt(), " "); got != c.want {
			t.Fatalf("%s: reply %q, want %q", c.name, got, c.want)
		}
		if n := len(f.tasks()); n != 0 {
			t.Fatalf("%s: %d tasks written, want 0", c.name, n)
		}
		c.reset()
	}
	must(t, f.client.HSet(f.ctx, hkey, "ctl-c", `{"holder":"ctl-c","head":"x","released_by":"ctl-c"}`).Err())
	if got := adopt(); len(got) == 0 || got[0] != "OK" {
		t.Fatalf("live record unchanged, released hold: reply %v, want OK", got)
	}
	if n := len(f.tasks()); n != 1 {
		t.Fatalf("after OK: %d tasks, want 1", n)
	}
}

// TestRunnerRowThenCIEndWritesVerdict: Rowan's hold 5 item 1 on #3495. A
// runner row arrives when the PR is pushed, before the ci card ends; it must
// not occupy the write-once receipt ci:<repo>:<head>:<gid>, or ns_ci_end
// answers ALREADY and the head never gets a verdict.
func TestRunnerRowThenCIEndWritesVerdict(t *testing.T) {
	t.Parallel()

	f := newC33(t)
	must(t, f.client.HSet(f.ctx, "bench:"+ctlBench+":desired", "slots", "4", "paused", "0", "legs", "go").Err())
	must(t, f.client.HSet(f.ctx, "bench:"+ctlBench+":beat", "host", ctlBench, "at", "1").Err())
	f.apply("901", "windows", "completed", "completed", "success", "2026-09-24T20:00:01Z")

	st := f.rule.Store
	res, err := ci.Cut(f.ctx, st, ci.CutRequest{Sprint: c33Sprint, Repo: c33Short, PR: 7, Head: c33Head,
		Base: c33Head, BaseRef: "dev", Actor: "ctl"})
	if err != nil || res.Status != "CREATED" {
		t.Fatalf("ci cut: %v %v", res, err)
	}
	label := ci.Label(7, c33Head, c33Head)
	card := "s:" + c33Sprint + ":card:" + label
	vals, err := f.client.HMGet(f.ctx, card, "attempt", "base_sha").Result()
	must(t, err)
	attempt, _ := vals[0].(string)
	baseSHA, _ := vals[1].(string)
	identity := fmt.Sprintf("%s/%s/%s/%s/%s", c33Sprint, label, baseSHA, ctlBench, attempt)
	token := attempt + ".0123456789abcdef0123456789abcdef"
	must(t, f.client.HSet(f.ctx, card, "state", "dealt", "bench", ctlBench, "identity", identity, "token", token, "token_sha", "abcdefabcdef").Err())
	must(t, f.client.SMove(f.ctx, "s:"+c33Sprint+":idx:card:queued", "s:"+c33Sprint+":idx:card:dealt", label).Err())
	// The hand deal wrote the fine state around the card move (#3692).
	must(t, f.client.FCall(f.ctx, "ns_card_repair", nil, c33Sprint).Err())

	end, err := ci.End(f.ctx, st, ci.EndRecord{Sprint: c33Sprint, Label: label, Token: token, Identity: identity,
		Outcome: "DONE", Reason: "done", Verdict: "OK", Actor: "wrapper"})
	if err != nil || end.Status != "ENDED" || end.Detail != "OK" {
		t.Fatalf("ci end after a runner row = %v %v, want ENDED OK (not ALREADY)", end, err)
	}
	rec, err := civerdict.ReadHead(f.ctx, f.client, c33Short, c33Head, "dev")
	must(t, err)
	if civerdict.Of(rec) != civerdict.OK {
		t.Fatalf("ReadHead after runner row and ci end = %v, want verdict OK", rec)
	}
	if got := RunnerRow(f.ci(), "windows"); got != RunnerStateReady {
		t.Fatalf("runner:windows after ci end is %s, want READY", got)
	}
}

// TestRunnerRowsResolveTheirBase: Rowan's hold 5 item 2 on #3495. A runner row
// takes its base from the PR's sprint record and its gid from that base's tip
// and policy; a stream-branch PR lands under its own base, and a missing PR
// record, policy or tip refuses (NOBASE, NOPOLICY) and writes nothing, never
// a row under a made-up identity (base dev, pol1/req1/run1, base_sha=head).
func TestRunnerRowsResolveTheirBase(t *testing.T) {
	t.Parallel()

	const stream, head8x = "stream-x", "8888aaaa8888aaaa8888aaaa8888aaaa8888aaaa"
	publish := func(f *c33Fix, number string) string {
		t.Helper()
		_, err := ghevent.Publish(f.ctx, f.client, ghevent.Entry{Repo: c33Repo, Kind: "check_run", Number: number,
			Head: head8x, Action: "completed", At: "2026-09-24T21:00:00Z", Sender: "github-actions",
			Check: "windows", CheckRunID: "800", Status: "completed", Conclusion: "success"})
		must(t, err)
		out := f.pass()
		if p := f.pending(); p != 0 {
			t.Fatalf("pending %d after the pass, want 0", p)
		}
		return out
	}
	ciKeys := func(f *c33Fix) []string {
		t.Helper()
		keys, err := f.client.Keys(f.ctx, "ci:*:"+head8x+"*").Result()
		must(t, err)
		return keys
	}
	prKey := "s:" + c33Sprint + ":pr:" + c33Short + ":8"
	streamTip := strings.Repeat("c", 40)

	t.Run("stream_base", func(t *testing.T) {
		f := newC33(t)
		must(t, f.client.HSet(f.ctx, prKey, "head", head8x, "base", stream).Err())
		must(t, f.client.HSet(f.ctx, civerdict.TipKey(c33Short, stream), "sha", streamTip).Err())
		must(t, f.client.HSet(f.ctx, civerdict.PolicyKey(c33Short, stream), "policy_id", "polS", "required_set_id", "reqS", "runner_id", "runS").Err())
		if out := publish(f, "8"); !strings.Contains(out, "RUNNER "+c33Short+"@8888aaaa windows g0/800 completed/success REPLACED") {
			t.Fatalf("pass printed %q, want the RUNNER line", out)
		}
		want := civerdict.RunnersKey(c33Short, head8x, civerdict.GID("single", stream, streamTip, "reqS", "polS", "runS"))
		if keys := ciKeys(f); !reflect.DeepEqual(keys, []string{want}) {
			t.Fatalf("ci keys %v, want only %s (the stream base's identity)", keys, want)
		}
	})
	t.Run("no_pr_record", func(t *testing.T) {
		f := newC33(t)
		if out := publish(f, "8"); !strings.Contains(out, "NOBASE "+c33Short+"@8888aaaa windows") || strings.Contains(out, "RUNNER") {
			t.Fatalf("pass printed %q, want NOBASE and no RUNNER", out)
		}
		if keys := ciKeys(f); len(keys) != 0 {
			t.Fatalf("no PR record wrote %v", keys)
		}
	})
	t.Run("no_policy", func(t *testing.T) {
		f := newC33(t)
		must(t, f.client.HSet(f.ctx, prKey, "head", head8x, "base", stream).Err())
		must(t, f.client.HSet(f.ctx, civerdict.TipKey(c33Short, stream), "sha", streamTip).Err())
		if out := publish(f, "8"); !strings.Contains(out, "NOPOLICY "+c33Short+"@8888aaaa windows "+civerdict.PolicyKey(c33Short, stream)) {
			t.Fatalf("pass printed %q, want NOPOLICY naming %s", out, civerdict.PolicyKey(c33Short, stream))
		}
		if keys := ciKeys(f); len(keys) != 0 {
			t.Fatalf("no policy wrote %v", keys)
		}
	})
	t.Run("no_tip", func(t *testing.T) {
		f := newC33(t)
		must(t, f.client.HSet(f.ctx, prKey, "head", head8x, "base", stream).Err())
		must(t, f.client.HSet(f.ctx, civerdict.PolicyKey(c33Short, stream), "policy_id", "polS", "required_set_id", "reqS", "runner_id", "runS").Err())
		if out := publish(f, "8"); !strings.Contains(out, "NOPOLICY "+c33Short+"@8888aaaa windows no tip in "+civerdict.TipKey(c33Short, stream)) {
			t.Fatalf("pass printed %q, want NOPOLICY no tip", out)
		}
		if keys := ciKeys(f); len(keys) != 0 {
			t.Fatalf("no tip wrote %v", keys)
		}
	})
	t.Run("function_refuses_a_receipt_key", func(t *testing.T) {
		f := newC33(t)
		gid := civerdict.GID("single", "dev", c33Head, "req1", "pol1", "run1")
		err := f.client.FCall(f.ctx, FunctionPRToReadRunner, []string{civerdict.Key(c33Short, head8x, gid)}, "windows",
			`{"check_run_id":"1","action":"completed","status":"completed","conclusion":"success","at":"x"}`).Err()
		if err == nil || !strings.Contains(err.Error(), ":runners") {
			t.Fatalf("FCALL on the receipt key = %v, want a refusal naming :runners", err)
		}
		if keys := ciKeys(f); len(keys) != 0 {
			t.Fatalf("a refused call wrote %v", keys)
		}
	})
}

// The runner-row oracle the control tests assert against. No verb reaches
// these any more (the lander reads runner rows in Lua), so they live here.
// Runner row states, as RunnerRow reads the stored latest attempt.
const (
	RunnerStateReady   = "READY"   // completed/success
	RunnerStateFail    = "FAIL"    // completed/failure or completed/timed_out
	RunnerStateMissing = "MISSING" // anything else, an absent field included
)

// ReadRunnerAttempt decodes field runner:<row> of a runners record; false when the
// field is absent or not an attempt.
func ReadRunnerAttempt(ci map[string]string, row string) (RunnerAttempt, bool) {
	raw, ok := ci["runner:"+row]
	if !ok || raw == "" {
		return RunnerAttempt{}, false
	}
	var a RunnerAttempt
	if err := json.Unmarshal([]byte(raw), &a); err != nil || a.CheckRunID == "" {
		return RunnerAttempt{}, false
	}
	return a, true
}

// RunnerRow is one row's state from the stored latest attempt only: an older
// success never counts once a newer attempt is stored.
func RunnerRow(ci map[string]string, row string) string {
	a, ok := ReadRunnerAttempt(ci, row)
	if !ok || a.Status != "completed" {
		return RunnerStateMissing
	}
	switch a.Conclusion {
	case "success":
		return RunnerStateReady
	case "failure", "timed_out":
		return RunnerStateFail
	}
	return RunnerStateMissing
}

// RunnerReady is the one readiness rule the lander and `land why` call over a
// head's ci:<repo>:<head>:<gid>:runners record: ok only when every named row is READY;
// missing names, in order, every row that is not (MISSING or FAIL; RunnerRow
// says which).
func RunnerReady(ci map[string]string, rows []string) (bool, []string) {
	var missing []string
	for _, row := range rows {
		if RunnerRow(ci, row) != RunnerStateReady {
			missing = append(missing, row)
		}
	}
	return len(missing) == 0, missing
}
