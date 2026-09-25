package main

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// consumePusher is the fixture bench's push (CI-NET: no ssh): every push
// succeeds and is counted.
type consumePusher struct {
	mu     sync.Mutex
	pushes map[string]int
}

func (p *consumePusher) Push(_ context.Context, _ harvest.BenchInfo, c harvest.Card) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pushes[c.Branch]++
	return nil
}

// consumeForge is GitHub for the fixture (CI-NET: no REST): no PR exists
// until the harvest opens one, and its head reads back as pushed.
type consumeForge struct {
	mu   sync.Mutex
	head string
	prs  map[string]harvest.PR
}

func (f *consumeForge) FindOpenPR(_ context.Context, repo, branch string) (harvest.PR, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := f.prs[repo+":"+branch]
	return pr, ok, nil
}

func (f *consumeForge) OpenPR(_ context.Context, repo, branch, _, _, _ string) (harvest.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 101 + len(f.prs)
	pr := harvest.PR{Number: n, Head: f.head, URL: fmt.Sprintf("https://github.com/mas-bandwidth/%s/pull/%d", repo, n)}
	f.prs[repo+":"+branch] = pr
	return pr, nil
}

func (f *consumeForge) ReadPR(_ context.Context, repo string, n int) (harvest.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pr := range f.prs {
		if pr.Number == n {
			return pr, nil
		}
	}
	return harvest.PR{}, fmt.Errorf("fixture forge: no pull %d in %s", n, repo)
}

// TestConsumerVerbsRunOnce is nova-tools #3323: each consumer runs under its
// own `nova-sprint consume <group> once` verb and as a production duty of
// `nova-sprint reconcile`. One fixture card walks the chain on a local Redis:
// its ended(DONE) event gives ok-to-friend's harvest task; the harvest verb
// pushes, opens the PR and reads the head back (harvested); the harvested
// event gives ok-to-friend's read task for the one UP friend at exactly that
// head. Each consumer writes its proc line. pr-to-read and hold-to-fix are not
// built on dev (#2941): their verbs refuse and name the issue, never pass
// silently, and they are not duties until they exist.
func TestConsumerVerbsRunOnce(t *testing.T) {
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	const S, bench, label, repo, friend = "control-3323c0de", "ctl-b1", "card-1", "nova-tools", "ctl-a"
	pushed := strings.Repeat("a", 40)
	identity := S + "/" + label + "/09fbedc9/" + bench + "/1"

	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2")
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", bench+".fixture", "user", "nova", "at", "1")
	pipe.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1") // #2046: UP is the fleet record
	pipe.SAdd(ctx, "friends", friend)
	pipe.HSet(ctx, "friend:"+friend+":desired", "slots", "4", "paused", "0")
	pipe.HSet(ctx, "friend:"+friend+":beat", "harness", "ctl", "at", "1")
	pipe.HSet(ctx, "s:"+S+":card:"+label,
		"kind", "model", "repo", repo, "base", "dev", "base_sha", "09fbedc9",
		"paths", "internal/x/y.go", "author", "", "priority", "5", "state", "ended",
		"attempt", "1", "identity", identity, "bench", bench, "token_sha", "abcdefabcdef",
		"outcome", "DONE", "reason", "done", "pushed_sha", pushed, "results", identity, "ended_at", "1")
	pipe.SAdd(ctx, "s:"+S+":idx:card:ended", label)
	pipe.SAdd(ctx, "s:"+S+":bench:"+bench+":ended", label)
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + S + ":log", Values: []any{
		"kind", "card", "id", label, "from", "running", "to", "ended", "attempt", "1",
		"token_sha", "abcdefabcdef", "actor", "card-end", "reason", "done",
		"evidence", identity, "idem", "end:" + identity, "at", "1"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	forge := &consumeForge{head: pushed, prs: map[string]harvest.PR{}}
	pusher := &consumePusher{pushes: map[string]int{}}
	seams := consumeHarvestSeams
	consumeHarvestSeams = func() (harvest.Forge, harvest.Pusher) { return forge, pusher }
	t.Cleanup(func() { consumeHarvestSeams = seams })

	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := runConsume(ctx, append(args, "--redis", addr), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	procUp := func(key string) {
		t.Helper()
		h, err := c.HGetAll(ctx, key).Result()
		if err != nil || h["pass_at"] == "" || h["err"] != "" {
			t.Fatalf("%s = %v (%v); want a pass line with no err", key, h, err)
		}
	}

	// ok-to-friend: the ended(DONE) event becomes one open harvest task.
	code, out, errOut := run("ok-to-friend", "once")
	if code != 0 || !strings.Contains(out, "CONSUMED ok-to-friend sprint="+S+" n=1") {
		t.Fatalf("consume ok-to-friend once: exit %d out %q err %q", code, out, errOut)
	}
	h, err := c.HGetAll(ctx, "task:harvest-"+label).Result()
	if err != nil || h["state"] != "open" || h["kind"] != "harvest" {
		t.Fatalf("harvest-%s = %v (%v); want an open harvest task", label, h, err)
	}
	procUp("proc:ok-to-friend")

	// harvest: the ended card is pushed, its PR opened and read back.
	code, out, errOut = run("harvest", "once")
	if code != 0 || !strings.Contains(out, "HARVESTED "+S+" "+label+" pr=101") {
		t.Fatalf("consume harvest once: exit %d out %q err %q", code, out, errOut)
	}
	card, err := c.HGetAll(ctx, "s:"+S+":card:"+label).Result()
	if err != nil || card["state"] != "harvested" || card["pr"] != "101" || card["head"] != pushed {
		t.Fatalf("card after harvest = %v (%v); want harvested pr 101 at %s", card, err, pushed)
	}
	if n := pusher.pushes["nova/"+S+"/"+label+"-a1"]; n != 1 {
		t.Fatalf("pushes of the card branch = %d, want 1", n)
	}
	procUp("proc:harvest:" + bench)

	// ok-to-friend again: the harvested event becomes the friend's read at
	// exactly the verified head.
	code, out, errOut = run("ok-to-friend", "once", "--sprint", S)
	if code != 0 || !strings.Contains(out, "CONSUMED ok-to-friend sprint="+S+" n=1") {
		t.Fatalf("consume ok-to-friend once after harvest: exit %d out %q err %q", code, out, errOut)
	}
	pr, _ := strconv.Atoi(card["pr"])
	readID := task.ReviewID(repo, pr, pushed, friend)
	read, err := c.HGetAll(ctx, "task:"+readID).Result()
	if err != nil || read["state"] != "open" || read["head"] != pushed {
		t.Fatalf("read task %s = %v (%v); want an open read for %s at %s", readID, read, err, friend, pushed)
	}
	if _, err := c.ZScore(ctx, "s:"+S+":open:"+friend, readID).Result(); err != nil {
		t.Fatalf("read %s is not in %s's open queue: %v", readID, friend, err)
	}

	// pr-to-read verb tests: requires --sprint; run is refused; once consumes.
	code, _, errOut = run("pr-to-read", "once")
	if code != 2 || !strings.Contains(errOut, "--sprint") {
		t.Fatalf("consume pr-to-read once without --sprint: exit %d err %q; want 2 naming --sprint", code, errOut)
	}
	code, _, errOut = run("pr-to-read", "run", "--sprint", S)
	if code != 2 || !strings.Contains(errOut, "nova-sprint route") {
		t.Fatalf("consume pr-to-read run: exit %d err %q; want 2 naming nova-sprint route", code, errOut)
	}
	origRemote := consumePRReadRemote
	consumePRReadRemote = func(_ context.Context, repo string) (map[int]string, error) {
		t.Fatalf("consumePRReadRemote called unexpectedly for repo %s", repo)
		return nil, nil
	}
	t.Cleanup(func() { consumePRReadRemote = origRemote })

	code, out, errOut = run("pr-to-read", "once", "--sprint", S)
	if code != 0 || !strings.Contains(out, "CONSUMED pr-to-read sprint="+S) {
		t.Fatalf("consume pr-to-read once: exit %d out %q err %q", code, out, errOut)
	}
	procUp("proc:pr-to-read")
	if exists, err := c.Exists(ctx, "lease:route:"+S).Result(); err != nil || exists != 0 {
		t.Fatalf("lease:route:%s exists = %d (%v); want 0 after consume once", S, exists, err)
	}

	// hold-to-fix remains unbuilt: refused naming #3092, exit 2.
	code, _, errOut = run("hold-to-fix", "once")
	if code != 2 || !strings.Contains(errOut, "#3092") {
		t.Fatalf("consume hold-to-fix once: exit %d err %q; want 2 naming #3092", code, errOut)
	}
	code, out, _ = run("list")
	for _, want := range []string{
		"ok-to-friend duty=reconcile",
		"harvest duty=reconcile",
		"pr-to-read duty=reconcile,route verb=consume-once proc=proc:pr-to-read",
		"hold-to-fix not-built=#3092",
	} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("consume list: exit %d out %q; want %q", code, out, want)
		}
	}

	// The production reconciler runs the built consumers as duties.
	_, names, _, err := productionDuties(store.New(c), metrics.Default)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names, ",")
	if !strings.HasPrefix(got, "refill,ok-to-friend,harvest,pr-to-read") {
		t.Fatalf("productionDuties = %s; want refill,ok-to-friend,harvest,pr-to-read first", got)
	}

	// And one `reconcile --once` pass consumes a second ended card with no
	// consume verb typed: its harvest task exists when the pass returns.
	dealSeams := reconcileSeams
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return &verbSSH{}, verbForge{} }
	t.Cleanup(func() { reconcileSeams = dealSeams })
	const label2 = "card-2"
	pipe = c.TxPipeline()
	pipe.HSet(ctx, "s:"+S+":card:"+label2,
		"kind", "model", "repo", repo, "base", "dev", "base_sha", "09fbedc9", "priority", "5",
		"state", "ended", "attempt", "1", "identity", S+"/"+label2+"/09fbedc9/"+bench+"/1",
		"bench", bench, "outcome", "DONE", "reason", "done", "pushed_sha", strings.Repeat("b", 40), "ended_at", "2")
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + S + ":log", Values: []any{
		"kind", "card", "id", label2, "from", "running", "to", "ended", "attempt", "1", "actor", "card-end", "at", "2"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var rOut, rErr bytes.Buffer
	if code := runReconcile(ctx, []string{"--redis", addr, "--host", "ctl-host", "--once"}, &rOut, &rErr); code != 0 {
		t.Fatalf("reconcile --once: exit %d out %q err %q", code, rOut.String(), rErr.String())
	}
	if !strings.Contains(rOut.String(), "DUTIES refill,ok-to-friend,harvest,pr-to-read") {
		t.Fatalf("reconcile out %q; want the consumer duties on the DUTIES line", rOut.String())
	}
	if state, err := c.HGet(ctx, "task:harvest-"+label2, "state").Result(); err != nil || state != "open" {
		t.Fatalf("harvest-%s after one reconcile pass: state %q (%v); want open", label2, state, err)
	}
}
