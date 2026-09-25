package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
)

// routeFixture is a throwaway Redis with the library loaded, the host seams
// swapped for fixtures (CI-NET: no host in a test) and one open sprint.
func routeFixture(t *testing.T, S string) (context.Context, *redis.Client, string, *verbSSH) {
	t.Helper()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	ssh := &verbSSH{}
	seams := reconcileSeams
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return ssh, verbForge{} }
	hseams := consumeHarvestSeams
	consumeHarvestSeams = func() (harvest.Forge, harvest.Pusher) {
		return &consumeForge{prs: map[string]harvest.PR{}}, &consumePusher{pushes: map[string]int{}}
	}
	readers := reconcileReaders
	t.Cleanup(func() { reconcileSeams, consumeHarvestSeams, reconcileReaders = seams, hseams, readers })
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	pipe.HSet(ctx, "s:"+S, "status", "open", "stream", "nova-sprint")
	pipe.HSet(ctx, "s:"+S+":policy", "share", "1", "backpressure_missing", "open")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, c, addr, ssh
}

// reconcileOnce runs `nova-sprint reconcile --once` and returns its output.
func reconcileOnce(t *testing.T, addr string, extra ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	args := append([]string{"--redis", addr, "--host", "ctl-host", "--once"}, extra...)
	if code := runReconcile(context.Background(), args, &out, &errOut); code != 0 {
		t.Fatalf("reconcile --once: exit %d\nout: %s\nerr: %s", code, out.String(), errOut.String())
	}
	return out.String()
}

func dutyLine(t *testing.T, out, name string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "DUTY "+name+" ") {
			return l
		}
	}
	t.Fatalf("no DUTY %s line in:\n%s", name, out)
	return ""
}

// TestReconcileOnceDealsPooledCard (nova-tools #3199): a pooled card and one
// UP bench with a free slot; one `reconcile --once` deals it (dealt, an
// attempt and a token, one `card launch --stdin` batch at the ssh seam) and
// the pass prints one DUTY line per duty, the refill's saying dealt=1.
func TestReconcileOnceDealsPooledCard(t *testing.T) {
	const S, bench, label = "control-3199d3a1", "ctl-deal", "card-redis-auth"
	ctx, c, addr, ssh := routeFixture(t, S)
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":desired", "slots", "2")
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", bench, "at", "1")
	pipe.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")
	pipe.HSet(ctx, "s:"+S+":card:"+label, "state", "queued", "priority", "1",
		"attempt", "0", "retries", "0", "base_sha", "0123456789abcdef", "bench", "", "leg", "", "tier", "")
	pipe.ZAdd(ctx, "s:"+S+":pool", redis.Z{Score: 1, Member: label})
	pipe.SAdd(ctx, "s:"+S+":idx:card:queued", label)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	out := reconcileOnce(t, addr)
	card, err := c.HGetAll(ctx, "s:"+S+":card:"+label).Result()
	if err != nil {
		t.Fatal(err)
	}
	if card["state"] != "dealt" || card["attempt"] != "1" || card["token"] == "" || card["bench"] != bench {
		t.Fatalf("card after reconcile --once: state=%q attempt=%q token=%q bench=%q; want dealt/1/token/%s\n%s",
			card["state"], card["attempt"], card["token"], card["bench"], bench, out)
	}
	if n := ssh.launched(); n != 1 {
		t.Fatalf("ssh seam saw %d launch lines, want 1\n%s", n, out)
	}
	if l := dutyLine(t, out, "refill"); !strings.Contains(l, "dealt=1") || !strings.HasSuffix(l, "err=") {
		t.Fatalf("refill receipt: %q", l)
	}
	for _, d := range []string{"ok-to-friend", "harvest", "expire", "route"} {
		if l := dutyLine(t, out, d); !strings.HasSuffix(l, "err=") {
			t.Fatalf("%s receipt: %q", d, l)
		}
	}
}

// TestReconcileOnceRoutesReadFixMerging (nova-tools #3323): the route duty
// over records alone. An OK (harvested) card with a PR record and a JEV line
// at head yields exactly one read task, read-<n>-<sha8>, on the least-loaded
// live reader's queue, never the author's; a second pass pushes nothing; a
// HOLD at head yields one fix task on the author's queue; with the hold
// released, an APPROVE at the bar and CI green at head, the PR's task moves
// to ws:<stream>:merging; and a further pass moves nothing more.
func TestReconcileOnceRoutesReadFixMerging(t *testing.T) {
	const (
		S, label, repo, pr, unit = "control-3323r0e1", "card-77", "nova-tools", "77", "u77"
		stream                   = "nova-sprint"
	)
	head := strings.Repeat("7", 40)
	ctx, c, addr, _ := routeFixture(t, S)
	pipe := c.TxPipeline()
	for _, f := range []string{"rowan", "emma", "stella"} {
		pipe.SAdd(ctx, "friends", f)
		pipe.HSet(ctx, "friend:"+f+":desired", "slots", "8", "paused", "0")
		pipe.HSet(ctx, "friend:"+f+":beat", "harness", "ctl", "at", "1")
	}
	pipe.SAdd(ctx, "friends", "ghost") // registered, no beat: never dealt a read
	pipe.SAdd(ctx, "readers", "rowan", "emma", "ghost")
	// rowan already holds two ready tasks; emma none: emma is least loaded.
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "q:rowan", Values: []any{"id", "other-1"}})
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "q:rowan:front", Values: []any{"id", "other-2"}})
	// The OK card, its PR record (unit) and the JEV line at head.
	pipe.HSet(ctx, "s:"+S+":card:"+label, "state", "harvested", "repo", repo, "pr", pr, "head", head,
		"attempt", "1", "kind", "code")
	pipe.SAdd(ctx, "s:"+S+":idx:card:harvested", label)
	pipe.Set(ctx, "s:"+S+":prunit:"+repo+":"+pr, unit, 0)
	pipe.SAdd(ctx, "s:"+S+":units", unit)
	pipe.HSet(ctx, "s:"+S+":u:"+unit, "head", head, "author", "stella", "repo", repo, "pr", pr,
		"state", "reading", "holds_open", "0")
	pipe.HSet(ctx, "s:"+S+":read:"+unit+":jev", "head", head, "verdict", "APPROVE", "score", "9", "kind", "")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	readID := "read-" + pr + "-" + head[:8]
	out := reconcileOnce(t, addr)
	if l := dutyLine(t, out, "route"); !strings.Contains(l, "reads=1 fixes=0 merging=0") || !strings.HasSuffix(l, "err=") {
		t.Fatalf("route receipt after the read pass: %q", l)
	}
	task, err := c.HGetAll(ctx, "task:"+readID).Result()
	if err != nil || task["owner"] != "emma" || task["kind"] != "read" || task["state"] != "open" || task["head"] != head {
		t.Fatalf("task:%s = %v (%v); want a read task owned by emma at head", readID, task, err)
	}
	if !strings.HasPrefix(task["title"], "STREAM: "+stream+" | read "+repo+"#"+pr) {
		t.Fatalf("read task title %q: want STREAM and the PR", task["title"])
	}
	if n := c.XLen(ctx, "q:emma").Val(); n != 1 {
		t.Fatalf("q:emma has %d entries, want 1", n)
	}
	if n := c.XLen(ctx, "q:rowan").Val(); n != 1 {
		t.Fatalf("q:rowan has %d entries, want the 1 it had", n)
	}
	if !c.SIsMember(ctx, "sprint:"+S+":idx:emma:open", readID).Val() {
		t.Fatalf("%s is not in emma's open index", readID)
	}
	if n := c.XLen(ctx, "q:stella").Val(); n != 0 {
		t.Fatalf("the author's queue has %d entries, want 0", n)
	}
	// The second pass pushes nothing more.
	out = reconcileOnce(t, addr)
	if l := dutyLine(t, out, "route"); !strings.Contains(l, "reads=0 fixes=0 merging=0") {
		t.Fatalf("route receipt on the second pass: %q", l)
	}
	if n := c.XLen(ctx, "q:emma").Val(); n != 1 {
		t.Fatalf("q:emma has %d entries after the second pass, want still 1", n)
	}

	// A HOLD by rowan at head: one fix task on the author's (stella's) queue.
	pipe = c.TxPipeline()
	pipe.HSet(ctx, "s:"+S+":hold:"+unit+":rowan", "seq", "5", "head", head, "kind", "substance",
		"reason", "the control is missing", "origin", "typed", "post_land", "0", "at", "1")
	pipe.HSet(ctx, "s:"+S+":u:"+unit, "holds_open", "1")
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + S + ":hold:events", Values: []any{
		"type", "hold", "unit", unit, "repo", repo, "pr", pr, "who", "rowan", "head", head, "kind", "substance", "seq", "5", "at", "1"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	fixID := "fix-" + pr + "-" + head[:8]
	out = reconcileOnce(t, addr)
	if l := dutyLine(t, out, "route"); !strings.Contains(l, "reads=0 fixes=1 merging=0") {
		t.Fatalf("route receipt after the hold: %q", l)
	}
	fix, err := c.HGetAll(ctx, "task:"+fixID).Result()
	if err != nil || fix["owner"] != "stella" || fix["kind"] != "fix" || !strings.Contains(fix["title"], "HOLD substance by rowan") {
		t.Fatalf("task:%s = %v (%v); want a fix task owned by stella", fixID, fix, err)
	}
	if n := c.XLen(ctx, "q:stella").Val(); n != 1 {
		t.Fatalf("q:stella has %d entries, want 1", n)
	}
	// While the hold is open the PR never moves to merging, whatever its reads and CI say.
	pipe = c.TxPipeline()
	pipe.SAdd(ctx, "ws:names", stream)
	pipe.HSet(ctx, "task:build-77", "stream", stream, "state", "working", "owner", "stella", "pr", pr, "kind", "build")
	pipe.ZAdd(ctx, "ws:"+stream+":working", redis.Z{Score: 1, Member: "build-77"})
	pipe.HSet(ctx, "task:pr", repo+"#"+pr, "build-77")
	pipe.SAdd(ctx, "s:"+S+":readers:"+unit, "emma")
	pipe.HSet(ctx, "s:"+S+":read:"+unit+":emma", "head", head, "verdict", "APPROVE", "score", "8", "kind", "substance")
	pipe.SAdd(ctx, "ci:"+repo+":"+head+":gids", "g1")
	pipe.HSet(ctx, "ci:"+repo+":"+head+":g1", "verdict", "OK", "head", head)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	out = reconcileOnce(t, addr)
	if l := dutyLine(t, out, "route"); !strings.Contains(l, "merging=0") {
		t.Fatalf("route receipt with the hold open: %q", l)
	}
	// The hold released: the score-8 green PR's task moves to merging.
	pipe = c.TxPipeline()
	pipe.HSet(ctx, "s:"+S+":hold:"+unit+":rowan", "released_by", "rowan")
	pipe.HSet(ctx, "s:"+S+":u:"+unit, "holds_open", "0")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	out = reconcileOnce(t, addr)
	if l := dutyLine(t, out, "route"); !strings.Contains(l, "reads=0 fixes=0 merging=1") {
		t.Fatalf("route receipt after the release: %q", l)
	}
	if st := c.HGet(ctx, "task:build-77", "state").Val(); st != "merging" {
		t.Fatalf("task:build-77 state %q, want merging", st)
	}
	if c.ZScore(ctx, "ws:"+stream+":merging", "build-77").Err() != nil || c.ZScore(ctx, "ws:"+stream+":working", "build-77").Err() == nil {
		t.Fatalf("build-77 is not in ws:%s:merging alone", stream)
	}
	if n := c.XLen(ctx, "ws:log").Val(); n < 1 {
		t.Fatalf("ws:log has no move receipt")
	}
	// Nothing more moves: the receipts in s:<S>:log are exactly the three.
	out = reconcileOnce(t, addr)
	if l := dutyLine(t, out, "route"); !strings.Contains(l, "reads=0 fixes=0 merging=0") {
		t.Fatalf("route receipt on the final pass: %q", l)
	}
	moves := 0
	for _, m := range c.XRange(ctx, "s:"+S+":log", "-", "+").Val() {
		if r, _ := m.Values["reason"].(string); strings.HasPrefix(r, "route ") {
			moves++
		}
	}
	if moves != 3 {
		t.Fatalf("%d route receipts in s:%s:log, want 3", moves, S)
	}
}
