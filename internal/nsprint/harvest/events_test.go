package harvest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// TestHarvestPROpenedMovesTasksToMerging (nova-tools#3779): the harvest
// that opens a card's PR moves, in the same fenced ns_harvest_pr call, every
// sprint task naming the PR or the card's origin issue to merging; a task
// naming another issue stays; the record carries the issue the body closes;
// a repeat moves nothing; the ws sets agree with every record throughout.
func TestHarvestPROpenedMovesTasksToMerging(t *testing.T) {
	c := startRedis(t)
	ctx := context.Background()
	const (
		S, label, bench = "events-0925", "card-500", "hulk"
		repo, pr        = "mas-bandwidth/nova-tools", "77"
		cards           = "swarm: cards"
	)
	head := strings.Repeat("a", 40)
	// The origin is an issue URL; the ref parse reads /<repo>/issues/<n>
	// whatever the host (a .invalid host keeps the CI path offline).
	branch := "nova/" + S + "/" + label + "-a1"
	c.HSet(ctx, "s:"+S+":card:"+label, "state", "ended", "bench", bench, "attempt", "1", "repo", repo,
		"harvest_step", "pushed", "pushed_sha", head, "base", "dev", "base_sha", strings.Repeat("b", 40),
		"stream", cards, "origin", "https://forge.invalid/mas-bandwidth/nova-tools/issues/500")
	c.HSet(ctx, "lease:harvest:"+bench, "instance", "worker-1", "token", "tok")
	c.SAdd(ctx, "ws:names", cards)
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: cards})
	tasks := []struct{ id, state, field, value string }{
		{"build-500-thing", "working", "ref", "nova-tools#500"},                                    // names the origin issue
		{"fix-77-review", "ready", "pr", "https://forge.invalid/mas-bandwidth/nova-tools/pull/77"}, // names the PR
		{"build-501-other", "working", "ref", "nova-tools#501"},                                    // names another issue
	}
	var ids []string
	for i, tk := range tasks {
		age := float64(1000 + i)
		c.HSet(ctx, "task:"+tk.id, "stream", cards, "state", tk.state, "created_at", fmt.Sprint(age), tk.field, tk.value)
		c.ZAdd(ctx, ws.Key(cards, tk.state), redis.Z{Score: age, Member: tk.id})
		if err := c.FCall(ctx, "ns_task_refs", nil, tk.id).Err(); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.id)
	}
	if err := ws.Check(ctx, c, ids); err != nil {
		t.Fatalf("seed: %v", err)
	}
	call := func() string {
		t.Helper()
		r, err := c.FCall(ctx, harvest.FunctionPR, nil, S, bench, "worker-1", "tok", label, repo, branch, pr, head).Text()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := call(); r != "PR|77" {
		t.Fatalf("ns_harvest_pr = %q", r)
	}
	for id, want := range map[string]string{"build-500-thing": "merging", "fix-77-review": "merging", "build-501-other": "working"} {
		if got := c.HGet(ctx, "task:"+id, "state").Val(); got != want {
			t.Fatalf("%s is %s after the PR opened, want %s", id, got, want)
		}
	}
	if why := c.HGet(ctx, "task:build-500-thing", "why").Val(); why != "PR opened: nova-tools#77 ("+label+")" {
		t.Fatalf("why %q", why)
	}
	if cl := c.HGet(ctx, "pr:nova-tools:77", "closes").Val(); cl != "500" {
		t.Fatalf("record closes %q, want the origin issue 500", cl)
	}
	if err := ws.Check(ctx, c, ids); err != nil {
		t.Fatalf("after harvest: %v", err)
	}
	logLen := c.XLen(ctx, "ws:log").Val()
	// the one move walks the graph (#3778): the working task one step, the
	// ready one two (ready -> working -> merging), one receipt per step
	if logLen != 3 {
		t.Fatalf("ws:log %d, want the three steps", logLen)
	}
	if r := call(); r != "PR|77" || c.XLen(ctx, "ws:log").Val() != logLen {
		t.Fatalf("repeat: %q, ws:log %d -> %d", r, logLen, c.XLen(ctx, "ws:log").Val())
	}
	// A fenced caller moves nothing.
	c.HSet(ctx, "task:build-501-other", "ref", "nova-tools#77")
	c.FCall(ctx, "ns_task_refs", nil, "build-501-other")
	if r, _ := c.FCall(ctx, harvest.FunctionPR, nil, S, bench, "worker-2", "stale", label, repo, branch, pr, head).Text(); r != "FENCED|" {
		t.Fatalf("stale lease: %q", r)
	}
	if got := c.HGet(ctx, "task:build-501-other", "state").Val(); got != "working" {
		t.Fatalf("a fenced harvest moved a task: %s", got)
	}
}

// TestHarvestRefusesAPRNoCardNames is #3915's comment (no card, no
// landing): harvest records a PR only from the branch its card names; a
// branch no card names, or a label with no card, is refused NOCOPY and
// nothing is written (no reservation, no record, no task moved); the same
// PR from the card's branch is accepted.
func TestHarvestRefusesAPRNoCardNames(t *testing.T) {
	c := startRedis(t)
	ctx := context.Background()
	const (
		S, label, bench = "nocopy-0925", "card-600", "hulk"
		repo, pr        = "mas-bandwidth/nova-tools", "88"
		cards           = "swarm: cards"
	)
	head := strings.Repeat("c", 40)
	c.HSet(ctx, "s:"+S+":card:"+label, "state", "ended", "bench", bench, "attempt", "1", "repo", repo,
		"harvest_step", "pushed", "pushed_sha", head, "base", "dev", "base_sha", strings.Repeat("d", 40),
		"stream", cards, "origin", "https://forge.invalid/mas-bandwidth/nova-tools/issues/600")
	c.HSet(ctx, "lease:harvest:"+bench, "instance", "worker-1", "token", "tok")
	c.SAdd(ctx, "ws:names", cards)
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: cards})
	c.HSet(ctx, "task:build-600-thing", "stream", cards, "state", "working", "created_at", "1000", "ref", "nova-tools#600")
	c.ZAdd(ctx, ws.Key(cards, "working"), redis.Z{Score: 1000, Member: "build-600-thing"})
	if err := c.FCall(ctx, "ns_task_refs", nil, "build-600-thing").Err(); err != nil {
		t.Fatal(err)
	}
	call := func(label, branch string) string {
		t.Helper()
		r, err := c.FCall(ctx, harvest.FunctionPR, nil, S, bench, "worker-1", "tok", label, repo, branch, pr, head).Text()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	nothing := func(when string) {
		t.Helper()
		if n := c.Exists(ctx, "pr:nova-tools:"+pr).Val(); n != 0 {
			t.Fatalf("%s: the PR record was written", when)
		}
		if v := c.HGet(ctx, "s:"+S+":idem", "pr:"+repo+":nova/"+S+"/"+label+"-a1").Val(); v != "" {
			t.Fatalf("%s: reservation written %q", when, v)
		}
		if st := c.HGet(ctx, "task:build-600-thing", "state").Val(); st != "working" {
			t.Fatalf("%s: a task moved to %s", when, st)
		}
	}
	if r := call(label, "emma/600-by-hand"); !strings.HasPrefix(r, "NOCOPY|emma/600-by-hand is not s:"+S+":card:"+label) {
		t.Fatalf("a branch no card names: %q, want NOCOPY", r)
	}
	nothing("foreign branch")
	if r := call("card-601", "nova/"+S+"/card-601-a1"); !strings.HasPrefix(r, "NOCOPY|no card s:"+S+":card:card-601") {
		t.Fatalf("a label with no card: %q, want NOCOPY", r)
	}
	nothing("no card")
	if r := call(label, "nova/"+S+"/"+label+"-a1"); r != "PR|"+pr {
		t.Fatalf("the card's own branch: %q", r)
	}
	if st := c.HGet(ctx, "task:build-600-thing", "state").Val(); st != "merging" {
		t.Fatalf("the card's PR left its task %s", st)
	}
}
