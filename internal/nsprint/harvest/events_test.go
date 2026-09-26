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
// friend-held task naming the PR or the card's origin issue to merging; a
// task naming another issue stays; a primary (no friend, no copy) naming the
// origin issue is skipped with nothing written, since only a copy's end
// advances a primary (rowan-new specs/table-moves.md, 2026-09-25); the record
// carries the issue the body closes; a repeat moves nothing; the ws sets
// agree with every record throughout.
func TestHarvestPROpenedMovesTasksToMerging(t *testing.T) {
	t.Parallel()

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
	tasks := []struct{ id, state, friend, field, value string }{
		{"build-500-thing", "working", "emma", "ref", "nova-tools#500"},                                    // names the origin issue
		{"fix-77-review", "ready", "emma", "pr", "https://forge.invalid/mas-bandwidth/nova-tools/pull/77"}, // names the PR
		{"build-501-other", "working", "emma", "ref", "nova-tools#501"},                                    // names another issue
		{"build-500-primary", "waiting", "", "ref", "nova-tools#500"},                                      // a primary: no copy
		{"build-500-held", "working", "", "ref", "nova-tools#500"},                                         // a primary in working
	}
	var ids []string
	for i, tk := range tasks {
		age := float64(1000 + i)
		c.HSet(ctx, "task:"+tk.id, "stream", cards, "state", tk.state, "created_at", fmt.Sprint(age), tk.field, tk.value)
		if tk.friend != "" {
			c.HSet(ctx, "task:"+tk.id, "friend", tk.friend, "owner", tk.friend)
		}
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
	for id, want := range map[string]string{"build-500-thing": "merging", "fix-77-review": "merging", "build-501-other": "working",
		"build-500-primary": "waiting", "build-500-held": "working"} {
		if got := c.HGet(ctx, "task:"+id, "state").Val(); got != want {
			t.Fatalf("%s is %s after the PR opened, want %s", id, got, want)
		}
	}
	if why := c.HGet(ctx, "task:build-500-thing", "why").Val(); why != "PR opened: nova-tools#77 ("+label+")" {
		t.Fatalf("why %q", why)
	}
	// the primaries are skipped before any step writes: no where, no why
	for _, id := range []string{"build-500-primary", "build-500-held"} {
		if w := c.HGet(ctx, "task:"+id, "where").Val(); w != "" {
			t.Fatalf("%s: a skipped primary was written (where=%q)", id, w)
		}
		if why := c.HGet(ctx, "task:"+id, "why").Val(); why != "" {
			t.Fatalf("%s: a skipped primary took a why %q", id, why)
		}
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
