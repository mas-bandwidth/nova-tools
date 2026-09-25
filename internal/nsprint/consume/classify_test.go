package consume

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const (
	clsBase = "09fbedc9"
	clsTip  = "7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a"
)

func clsRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 200})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return client
}

func clsMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func clsSprint(t *testing.T, c *redis.Client, sprint string, benches []string, policy ...string) {
	t.Helper()
	ctx := context.Background()
	clsMust(t, c.HSet(ctx, "s:"+sprint, "status", "open").Err())
	for i := 0; i+1 < len(policy); i += 2 {
		clsMust(t, c.HSet(ctx, "s:"+sprint+":policy", policy[i], policy[i+1]).Err())
	}
	for _, bench := range benches {
		clsMust(t, c.SAdd(ctx, "benches", bench).Err())
		clsMust(t, c.HSet(ctx, "bench:"+bench+":desired", "slots", "1", "legs", "").Err())
		clsMust(t, c.HSet(ctx, "bench:"+bench+":beat", "host", bench).Err())
	}
}

func clsEnd(t *testing.T, c *redis.Client, sprint, label string, attempt int, bench, outcome, reason string, extra ...string) string {
	t.Helper()
	ctx := context.Background()
	key := "s:" + sprint + ":card:" + label
	old, err := c.HGetAll(ctx, key).Result()
	clsMust(t, err)
	base := old["base_sha"]
	if base == "" {
		base = clsBase
	}
	fields := []any{"label", label, "state", "ended", "attempt", strconv.Itoa(attempt), "bench", bench,
		"outcome", outcome, "reason", reason, "base_sha", base, "kind", "model", "leg", "go", "tier", "bulk",
		"repo", "mas-bandwidth/nova-tools", "base", "dev", "paths", "internal/x", "depends_on", "", "priority", "5"}
	for _, v := range extra {
		fields = append(fields, v)
	}
	clsMust(t, c.HSet(ctx, key, fields...).Err())
	for _, state := range []string{"queued", "dealt", "launched", "running", "superseded"} {
		clsMust(t, c.SRem(ctx, "s:"+sprint+":idx:card:"+state, label).Err())
	}
	clsMust(t, c.ZRem(ctx, "s:"+sprint+":pool", label).Err())
	clsMust(t, c.SAdd(ctx, "s:"+sprint+":idx:card:ended", label).Err())
	id, err := c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + sprint + ":log", Values: []any{
		"kind", "card", "id", label, "from", "running", "to", "ended", "attempt", strconv.Itoa(attempt),
		"actor", "card-end", "reason", reason, "at", "1"}}).Result()
	clsMust(t, err)
	clsMust(t, c.HSet(ctx, key, "end_receipt", id).Err())
	// The hand end wrote the fine state around the card move (#3692): fsck
	// --repair re-points the card from its record, as after any drift.
	clsMust(t, c.FCall(ctx, "ns_card_repair", nil, sprint).Err())
	return id
}

func clsClassifier(c *redis.Client, sprint, consumer string) *Classifier {
	return &Classifier{Store: store.New(c), Sprint: sprint, Consumer: consumer, Actor: "classify", Block: -1,
		Tip: func(context.Context, string, string) (string, error) { return clsTip, nil }}
}

func clsPass(t *testing.T, c *redis.Client, sprint string) []Classification {
	t.Helper()
	rows, err := clsClassifier(c, sprint, "ctl-classify").PassResults(context.Background())
	clsMust(t, err)
	return rows
}

func clsCard(t *testing.T, c *redis.Client, sprint, label string) map[string]string {
	t.Helper()
	h, err := c.HGetAll(context.Background(), "s:"+sprint+":card:"+label).Result()
	clsMust(t, err)
	return h
}

func clsCount(t *testing.T, c *redis.Client, sprint, root, field string) int {
	t.Helper()
	n, err := c.HGet(context.Background(), "s:"+sprint+":classify:"+root, field).Int()
	if err == redis.Nil {
		return 0
	}
	clsMust(t, err)
	return n
}

func clsItems(t *testing.T, c *redis.Client, sprint string) map[string]string {
	t.Helper()
	h, err := c.HGetAll(context.Background(), "s:"+sprint+":unresolved").Result()
	clsMust(t, err)
	return h
}

func clsResult(rows []Classification, label string) string {
	for _, row := range rows {
		if row.Label == label {
			return row.Result
		}
	}
	return ""
}

func TestClassificationTable33(t *testing.T) {
	rows := []struct {
		outcome, reason, action, counter, policy string
		budget                                   int
	}{
		{"FAILED", "crash", ActionRequeue, "fail", "retry_fail", 2},
		{"FAILED", "timeout", ActionRequeue, "fail", "retry_fail", 2},
		{"FAILED", "idle-killed", ActionRequeue, "fail", "retry_fail", 2},
		{"BLOCKED", "env", ActionRequeueEnv, "env", "retry_env", 1},
		{"BLOCKED", "base-moved", ActionRecut, "recut", "retry_recut", 1},
		{"BLOCKED", "deps", ActionWaiting, "deps", "retry_deps", 1},
		{"FAILED", "tests-red", ActionFix, "fix", "retry_fix", 1},
		{"BLOCKED", "spec", ActionUnresolved, "", "", 0},
	}
	for _, row := range rows {
		got := ClassifyOutcome(row.outcome, row.reason)
		if got.Action != row.action || got.Class != row.counter || got.Policy != row.policy || got.Budget(nil) != row.budget {
			t.Errorf("%s/%s = %+v", row.outcome, row.reason, got)
		}
	}

	c := clsRedis(t)
	const S = "control-table33"
	clsSprint(t, c, S, []string{"X", "Y"})
	clsEnd(t, c, S, "crash", 1, "X", "FAILED", "crash", "leg", "")
	clsEnd(t, c, S, "env", 1, "X", "BLOCKED", "env", "leg", "")
	clsEnd(t, c, S, "recut", 1, "X", "BLOCKED", "base-moved", "root", "recut-root")
	clsEnd(t, c, S, "red", 1, "X", "FAILED", "tests-red", "root", "red-root")
	clsEnd(t, c, S, "spec", 1, "X", "BLOCKED", "spec")
	clsMust(t, c.HSet(context.Background(), "s:"+S+":card:live", "state", "queued").Err())
	clsEnd(t, c, S, "wait", 1, "X", "BLOCKED", "deps", "depends_on", "live", "leg", "")
	clsMust(t, c.HSet(context.Background(), "s:"+S+":pr:mas-bandwidth/nova-tools:41", "state", "closed").Err())
	clsEnd(t, c, S, "closed", 1, "X", "BLOCKED", "deps", "depends_on", "mas-bandwidth/nova-tools#41")
	clsEnd(t, c, S, "unknown", 1, "X", "BLOCKED", "deps", "depends_on", "mas-bandwidth/nova-tools#60")
	clsEnd(t, c, S, "ci-red", 1, "X", "FAILED", "tests-red", "kind", "ci")
	got := clsPass(t, c, S)

	if clsResult(got, "crash") != "REQUEUE" || clsCard(t, c, S, "crash")["avoid"] != "X" || clsCard(t, c, S, "crash")["bench"] != "" {
		t.Errorf("crash row = %v %v", clsResult(got, "crash"), clsCard(t, c, S, "crash"))
	}
	if clsResult(got, "env") != "REQUEUE" || clsCard(t, c, S, "env")["why"] != "env " {
		t.Errorf("env row = %v %v", clsResult(got, "env"), clsCard(t, c, S, "env"))
	}
	if clsResult(got, "recut") != "RECUT" || clsCard(t, c, S, "recut-root.recut1")["state"] != "queued" || clsCard(t, c, S, "recut")["state"] != "superseded" {
		t.Errorf("recut row old=%v new=%v", clsCard(t, c, S, "recut"), clsCard(t, c, S, "recut-root.recut1"))
	}
	if clsResult(got, "red") != "FIX" || clsCard(t, c, S, "red-root.fix1")["kind"] != "fix" || clsCard(t, c, S, "red")["state"] != "ended" {
		t.Errorf("fix row old=%v new=%v", clsCard(t, c, S, "red"), clsCard(t, c, S, "red-root.fix1"))
	}
	if clsResult(got, "wait") != "WAIT" || clsCard(t, c, S, "wait")["state"] != "queued" {
		t.Errorf("wait row = %v %v", clsResult(got, "wait"), clsCard(t, c, S, "wait"))
	}
	waiting, _ := c.SIsMember(context.Background(), "s:"+S+":waiting", "wait").Result()
	if !waiting {
		t.Error("wait card is not parked")
	}
	for _, label := range []string{"spec", "closed", "unknown"} {
		if clsResult(got, label) != "UNRESOLVED" || clsCard(t, c, S, label)["state"] != "ended" {
			t.Errorf("%s = %s %v", label, clsResult(got, label), clsCard(t, c, S, label))
		}
	}
	if why := clsCard(t, c, S, "closed")["why"]; !strings.Contains(why, "closed without merge") {
		t.Errorf("closed why = %q", why)
	}
	if why := clsCard(t, c, S, "unknown")["why"]; !strings.Contains(why, "unknown: no PR record") {
		t.Errorf("unknown why = %q", why)
	}
	if clsResult(got, "ci-red") != "SKIP ci" || clsCard(t, c, S, "ci-red")["state"] != "ended" {
		t.Errorf("ci = %s %v", clsResult(got, "ci-red"), clsCard(t, c, S, "ci-red"))
	}
}

func TestControl16Classification(t *testing.T) {
	c := clsRedis(t)
	ctx := context.Background()

	t.Run("one unresolved marker", func(t *testing.T) {
		const S = "control-16-spec"
		clsSprint(t, c, S, []string{"X"})
		for i := 0; i < 3; i++ {
			clsEnd(t, c, S, "s1", 1, "X", "BLOCKED", "spec")
		}
		clsPass(t, c, S)
		clsEnd(t, c, S, "s1", 2, "X", "BLOCKED", "spec")
		clsPass(t, c, S)
		if items := clsItems(t, c, S); len(items) != 1 || items["s1:spec:"+clsBase] == "" {
			t.Fatalf("items = %v", items)
		}
	})

	t.Run("one fix then unresolved", func(t *testing.T) {
		const S = "control-16-fix"
		clsSprint(t, c, S, []string{"X"})
		clsEnd(t, c, S, "r1", 1, "X", "FAILED", "tests-red")
		clsPass(t, c, S)
		if clsCard(t, c, S, "r1.fix1")["state"] != "queued" {
			t.Fatal("fix1 was not cut")
		}
		clsEnd(t, c, S, "r1.fix1", 1, "X", "FAILED", "tests-red")
		clsPass(t, c, S)
		if clsCard(t, c, S, "r1.fix2")["state"] != "" || clsCard(t, c, S, "r1.fix1")["classified"] != "UNRESOLVED" {
			t.Fatalf("second red cut another fix: %v", clsCard(t, c, S, "r1.fix2"))
		}
	})

	t.Run("crash budget and avoids", func(t *testing.T) {
		const S = "control-16-crash"
		clsSprint(t, c, S, []string{"X", "Y", "Z"})
		for i, bench := range []string{"X", "Y", "Z"} {
			clsEnd(t, c, S, "k1", i+1, bench, "FAILED", "crash", "leg", "")
			clsPass(t, c, S)
		}
		card := clsCard(t, c, S, "k1")
		if card["state"] != "ended" || card["classified"] != "UNRESOLVED" || clsCount(t, c, S, "k1", "fail") != 3 {
			t.Fatalf("crash card=%v counter=%d", card, clsCount(t, c, S, "k1", "fail"))
		}
	})

	t.Run("env why and bench item", func(t *testing.T) {
		const S = "control-16-env"
		clsSprint(t, c, S, []string{"X", "Y"})
		clsEnd(t, c, S, "e1", 1, "X", "BLOCKED", "env", "leg", "go")
		clsPass(t, c, S)
		if clsCard(t, c, S, "e1")["why"] != "env go" {
			t.Errorf("why=%q", clsCard(t, c, S, "e1")["why"])
		}
		if ok, _ := c.HExists(ctx, "s:"+S+":unresolved", "bench:X:env:go").Result(); !ok {
			t.Error("missing bench env item")
		}
	})
}

func TestClassifyConcurrentDelivery(t *testing.T) {
	c := clsRedis(t)
	const S = "control-concurrent"
	clsSprint(t, c, S, []string{"X", "Y"})
	event := clsEnd(t, c, S, "red", 1, "X", "FAILED", "tests-red")
	row := Classification{EventID: event, Label: "red"}
	classifiers := []*Classifier{clsClassifier(c, S, "one"), clsClassifier(c, S, "two")}
	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)
	for i := range classifiers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := classifiers[i].handle(context.Background(), row, "1")
			results[i], errs[i] = got.Result, err
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !((results[0] == "FIX" && results[1] == "DUP") || (results[1] == "FIX" && results[0] == "DUP")) {
		t.Fatalf("results=%v, want FIX and DUP", results)
	}
	if clsCount(t, c, S, "red", "fix") != 1 || clsCard(t, c, S, "red.fix1")["state"] != "queued" {
		t.Fatalf("counter=%d fix=%v", clsCount(t, c, S, "red", "fix"), clsCard(t, c, S, "red.fix1"))
	}
	entries, err := c.XRange(context.Background(), "s:"+S+":log", "-", "+").Result()
	clsMust(t, err)
	n := 0
	for _, entry := range entries {
		if entry.Values["kind"] == "card-classify" && entry.Values["id"] == "red" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("card-classify receipts=%d", n)
	}
	stale, err := c.XAdd(context.Background(), &redis.XAddArgs{Stream: "s:" + S + ":log", Values: map[string]any{
		"kind": "card", "id": "red", "to": "ended", "attempt": "1"}}).Result()
	clsMust(t, err)
	got, err := classifiers[0].handle(context.Background(), Classification{EventID: stale, Label: "red"}, "1")
	clsMust(t, err)
	if got.Result != "SKIP stale" || clsCount(t, c, S, "red", "fix") != 1 {
		t.Fatalf("stale=%q counter=%d", got.Result, clsCount(t, c, S, "red", "fix"))
	}
}

func TestClassifyPolicyBudgets(t *testing.T) {
	t.Run("policy and no bench left", func(t *testing.T) {
		c := clsRedis(t)
		const S = "control-policy"
		clsSprint(t, c, S, []string{"X", "Y", "Z", "W"}, "retry_fail", "3")
		for i, bench := range []string{"X", "Y", "Z", "W"} {
			clsEnd(t, c, S, "k", i+1, bench, "FAILED", "crash", "leg", "")
			clsPass(t, c, S)
		}
		if clsCount(t, c, S, "k", "fail") != 4 || clsCard(t, c, S, "k")["classified"] != "UNRESOLVED" {
			t.Fatalf("counter=%d card=%v", clsCount(t, c, S, "k", "fail"), clsCard(t, c, S, "k"))
		}
	})

	t.Run("avoid exhaustion and leg", func(t *testing.T) {
		c := clsRedis(t)
		const S = "control-no-bench"
		clsSprint(t, c, S, []string{"X", "Y"}, "retry_fail", "3")
		clsEnd(t, c, S, "k", 1, "X", "FAILED", "crash", "leg", "")
		clsPass(t, c, S)
		clsEnd(t, c, S, "k", 2, "Y", "FAILED", "crash", "leg", "")
		clsPass(t, c, S)
		card := clsCard(t, c, S, "k")
		inPool, _ := c.ZScore(context.Background(), "s:"+S+":pool", "k").Result()
		if card["state"] != "ended" || card["classified"] != "UNRESOLVED" || inPool != 0 || clsCount(t, c, S, "k", "fail") != 2 {
			t.Fatalf("card=%v pool=%v counter=%d", card, inPool, clsCount(t, c, S, "k", "fail"))
		}
		clsMust(t, c.HSet(context.Background(), "bench:X:desired", "legs", "go").Err())
		clsMust(t, c.HSet(context.Background(), "bench:Y:desired", "legs", "lisp").Err())
		clsEnd(t, c, S, "leg", 1, "X", "FAILED", "crash", "leg", "go")
		clsPass(t, c, S)
		if clsCard(t, c, S, "leg")["classified"] != "UNRESOLVED" {
			t.Fatalf("leg card=%v", clsCard(t, c, S, "leg"))
		}
	})

	t.Run("pinned behavior", func(t *testing.T) {
		c := clsRedis(t)
		const S = "control-pin"
		clsSprint(t, c, S, []string{"X"})
		clsEnd(t, c, S, "crash", 1, "X", "FAILED", "crash", "pin", "X", "leg", "")
		clsPass(t, c, S)
		card := clsCard(t, c, S, "crash")
		if card["state"] != "queued" || card["bench"] != "X" || card["avoid"] != "" {
			t.Fatalf("pinned crash=%v", card)
		}
		clsEnd(t, c, S, "env", 1, "X", "BLOCKED", "env", "pin", "X", "leg", "go")
		clsPass(t, c, S)
		if clsCard(t, c, S, "env")["state"] != "ended" || clsCard(t, c, S, "env")["classified"] != "UNRESOLVED" || clsCount(t, c, S, "env", "env") != 0 {
			t.Fatalf("pinned env=%v count=%d", clsCard(t, c, S, "env"), clsCount(t, c, S, "env", "env"))
		}
	})
}

func ExampleClassification() {
	fmt.Println("label FAILED/crash -> REQUEUE")
	// Output: label FAILED/crash -> REQUEUE
}
