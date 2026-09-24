package harvest_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func startRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	return client
}

const sprint = "control-0000c013"

func sha(label string) string {
	s := fmt.Sprintf("%x", label)
	for len(s) < 40 {
		s += "0"
	}
	return s[:40]
}

// seedEnded writes a card in the shape card end (#2928) leaves it: ended,
// indexed under idx:card:ended and s:<S>:bench:<b>:ended.
func seedEnded(t *testing.T, c *redis.Client, bench, label, kind, outcome, pushed string) {
	t.Helper()
	ctx := context.Background()
	identity := sprint + "/" + label + "/09fbedc9/" + bench + "/1"
	if err := c.HSet(ctx, "s:"+sprint+":card:"+label,
		"kind", kind, "repo", "nova-tools", "base", "dev", "base_sha", "09fbedc9",
		"state", "ended", "outcome", outcome, "reason", "done", "bench", bench,
		"attempt", "1", "identity", identity, "token_sha", "abcdef012345",
		"pushed_sha", pushed, "results", identity).Err(); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "s:"+sprint+":idx:card:ended", label)
	c.SAdd(ctx, "s:"+sprint+":bench:"+bench+":ended", label)
}

// fixturePusher: an UP bench's push returns as soon as it is scheduled (no
// wall-clock wait to fake -- the event under test is completion, not a
// duration); a DOWN bench's ssh never answers, so the push blocks until that
// bench's own clock ends.
type fixturePusher struct {
	down   map[string]bool
	mu     sync.Mutex
	pushes map[string]int
}

func (p *fixturePusher) Push(ctx context.Context, b harvest.BenchInfo, c harvest.Card) error {
	if p.down[b.Name] {
		<-ctx.Done()
		return fmt.Errorf("ssh %s: %w", b.Name, ctx.Err())
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	p.mu.Lock()
	p.pushes[c.Branch]++
	p.mu.Unlock()
	return nil
}

// fixtureForge is GitHub: one PR per head branch, heads read back as pushed.
type fixtureForge struct {
	mu       sync.Mutex
	next     int
	byBranch map[string]harvest.PR
	byNumber map[int]harvest.PR
	opens    int
	finds    int
	failRead map[int]int // PR number -> reads left that fail
	failOnce map[string]bool
	heads    map[string]string // branch -> sha pushed there
}

func newForge() *fixtureForge {
	return &fixtureForge{next: 100, byBranch: map[string]harvest.PR{}, byNumber: map[int]harvest.PR{},
		failRead: map[int]int{}, failOnce: map[string]bool{}, heads: map[string]string{}}
}

func (f *fixtureForge) add(repo, branch, head string) harvest.PR {
	f.next++
	pr := harvest.PR{Number: f.next, Head: head, URL: fmt.Sprintf("https://github.com/mas-bandwidth/%s/pull/%d", repo, f.next)}
	f.byBranch[repo+":"+branch] = pr
	f.byNumber[pr.Number] = pr
	return pr
}

func (f *fixtureForge) FindOpenPR(ctx context.Context, repo, branch string) (harvest.PR, bool, error) {
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finds++
	pr, ok := f.byBranch[repo+":"+branch]
	return pr, ok, nil
}

func (f *fixtureForge) OpenPR(ctx context.Context, repo, branch, base, title, body string) (harvest.PR, error) {
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byBranch[repo+":"+branch]; ok {
		return harvest.PR{}, fmt.Errorf("422 a pull request already exists for %s", branch)
	}
	f.opens++
	pr := f.add(repo, branch, f.heads[branch])
	if f.failOnce[branch] {
		f.failRead[pr.Number] = 1
	}
	return pr, nil
}

func (f *fixtureForge) ReadPR(ctx context.Context, repo string, n int) (harvest.PR, error) {
	time.Sleep(20 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRead[n] > 0 {
		f.failRead[n]--
		return harvest.PR{}, errors.New("502 bad gateway")
	}
	pr, ok := f.byNumber[n]
	if !ok {
		return harvest.PR{}, fmt.Errorf("404 pull %d", n)
	}
	return pr, nil
}

func byBench(results []harvest.BenchResult) map[string]harvest.BenchResult {
	m := map[string]harvest.BenchResult{}
	for _, r := range results {
		m[r.Bench] = r
	}
	return m
}

// Control 13 (#2756 section 8): harvest with one bench down; the other
// benches' harvests finish within their own clock. And 5.4: a re-run finds
// the PR by its idem key and opens no second PR.
func TestControl13OneBenchDownOthersFinish(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()

	up := []string{"ctl-a", "ctl-b", "ctl-c"}
	benches := append([]string{"ctl-down"}, up...)
	forge := newForge()
	for _, b := range benches {
		c.HSet(ctx, "bench:"+b+":beat", "host", b+".tailnet", "user", "nova")
		for i := 1; i <= 3; i++ {
			label := fmt.Sprintf("%s-card%d", b, i)
			seedEnded(t, c, b, label, "model", "DONE", sha(label))
			forge.heads["nova/"+sprint+"/"+label+"-a1"] = sha(label)
		}
	}
	// Nothing to harvest: a ci card and a FAILED card on an UP bench.
	seedEnded(t, c, "ctl-a", "ci-3011-deadbeef", "script", "DONE", sha("ci"))
	seedEnded(t, c, "ctl-a", "ctl-a-failed", "model", "FAILED", sha("failed"))
	// A worker opened this PR and died before recording it: found by REST.
	forge.add("nova-tools", "nova/"+sprint+"/ctl-c-card3-a1", sha("ctl-c-card3"))
	// This PR opens, then its head read-back fails once: the card stays ended
	// with the idem key recorded, for the re-run.
	forge.failOnce["nova/"+sprint+"/ctl-b-card2-a1"] = true

	pusher := &fixturePusher{down: map[string]bool{"ctl-down": true}, pushes: map[string]int{}}
	clock := 2 * time.Second
	opt := harvest.Options{Sprint: sprint, Benches: benches, Clock: clock, Instance: "ctl-run-1",
		Forge: forge, Pusher: pusher}

	start := time.Now()
	res := byBench(harvest.Run(ctx, st, opt))

	// The down bench spends its whole clock and harvests nothing: assert the
	// event (its own context deadline fired), not how long the run took by
	// the wall clock.
	down := res["ctl-down"]
	if down.Err == nil || len(down.Cards) != 0 {
		t.Fatalf("down bench: err=%v cards=%v; want its clock to end it with nothing harvested", down.Err, down.Cards)
	}
	if !errors.Is(down.Err, context.DeadlineExceeded) {
		t.Fatalf("down bench err=%v; want its own clock's deadline to have fired", down.Err)
	}
	// Every UP bench finishes inside its own clock, in parallel: each is done
	// long before the down bench's clock ends (serial would be 3 x ~0.5 s,
	// then the down bench).
	for _, b := range up {
		r := res[b]
		if r.Err != nil {
			t.Fatalf("%s: %v", b, r.Err)
		}
		if r.Took >= clock/2 || r.Done.Sub(start) >= clock/2 {
			t.Fatalf("%s took %v (done at +%v); want inside its own clock and not waiting on ctl-down", b, r.Took, r.Done.Sub(start))
		}
	}
	if n := len(res["ctl-a"].Cards) + len(res["ctl-b"].Cards) + len(res["ctl-c"].Cards); n != 8 {
		t.Fatalf("harvested %d cards on the UP benches, want 8 (ctl-b-card2's head read failed once)", n)
	}
	if len(res["ctl-b"].Failed) != 1 || res["ctl-b"].Failed[0].Label != "ctl-b-card2" {
		t.Fatalf("ctl-b failures = %+v, want ctl-b-card2 only", res["ctl-b"].Failed)
	}
	via := map[string]string{}
	for _, b := range up {
		for _, cr := range res[b].Cards {
			via[cr.Label] = cr.Via
		}
	}
	if via["ctl-c-card3"] != "rest" || via["ctl-a-card1"] != "opened" {
		t.Fatalf("via = %v; want ctl-c-card3 found by REST and ctl-a-card1 opened", via)
	}
	if forge.opens != 8 {
		t.Fatalf("opened %d PRs, want 8 (9 UP cards, one already open on GitHub)", forge.opens)
	}

	// Redis state: harvested with pr and head, ended ones untouched, the
	// lease released and the pass line written for each bench.
	for _, label := range []string{"ctl-a-card1", "ctl-c-card3"} {
		h := c.HGetAll(ctx, "s:"+sprint+":card:"+label).Val()
		if h["state"] != "harvested" || h["head"] != sha(label) || h["pr"] == "" {
			t.Fatalf("%s = %v; want harvested at its pushed sha", label, h)
		}
	}
	for _, label := range []string{"ctl-b-card2", "ctl-down-card1", "ci-3011-deadbeef", "ctl-a-failed"} {
		if s := c.HGet(ctx, "s:"+sprint+":card:"+label, "state").Val(); s != "ended" {
			t.Fatalf("%s state = %s, want ended", label, s)
		}
	}
	if n := c.SCard(ctx, "s:"+sprint+":idx:card:harvested").Val(); n != 8 {
		t.Fatalf("idx:card:harvested = %d, want 8", n)
	}
	if n := c.XLen(ctx, "s:"+sprint+":log").Val(); n != 8 {
		t.Fatalf("receipts = %d, want one per harvested card (8)", n)
	}
	for _, b := range benches {
		if c.Exists(ctx, "lease:harvest:"+b).Val() != 0 {
			t.Fatalf("lease:harvest:%s still held after the pass", b)
		}
	}
	if p := c.HGetAll(ctx, "proc:harvest:ctl-down").Val(); p["err"] == "" {
		t.Fatalf("proc:harvest:ctl-down = %v, want the clock error named", p)
	}
	idemKey := "pr:nova-tools:nova/" + sprint + "/ctl-b-card2-a1"
	stuckPR := c.HGet(ctx, "s:"+sprint+":idem", idemKey).Val()
	if stuckPR == "" {
		t.Fatalf("idem %s not recorded after the PR opened", idemKey)
	}

	// The re-run: the PR is found by its idem key, never searched or opened.
	findsBefore, opensBefore := forge.finds, forge.opens
	opt.Instance = "ctl-run-2"
	opt.Clock = time.Second
	res = byBench(harvest.Run(ctx, st, opt))
	b := res["ctl-b"]
	if b.Err != nil || len(b.Cards) != 1 || b.Cards[0].Label != "ctl-b-card2" || b.Cards[0].Via != "idem" ||
		fmt.Sprint(b.Cards[0].PR) != stuckPR {
		t.Fatalf("re-run ctl-b = %+v; want ctl-b-card2 harvested via idem as PR %s", b, stuckPR)
	}
	if forge.opens != opensBefore || forge.finds != findsBefore {
		t.Fatalf("re-run opened %d and searched %d; want no second PR and no REST search",
			forge.opens-opensBefore, forge.finds-findsBefore)
	}
	if len(res["ctl-a"].Cards)+len(res["ctl-c"].Cards) != 0 {
		t.Fatalf("re-run touched harvested cards: %+v %+v", res["ctl-a"].Cards, res["ctl-c"].Cards)
	}
	if n := c.SCard(ctx, "s:"+sprint+":idx:card:harvested").Val(); n != 9 {
		t.Fatalf("idx:card:harvested = %d after the re-run, want 9", n)
	}
	if pusher.pushes["nova/"+sprint+"/ctl-b-card2-a1"] != 2 {
		t.Fatalf("ctl-b-card2 pushed %d times; the idempotent re-push is expected once per pass",
			pusher.pushes["nova/"+sprint+"/ctl-b-card2-a1"])
	}
}

// A second worker for a bench whose lease is held harvests nothing.
func TestHarvestLeaseHeldElsewhere(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	seedEnded(t, c, "ctl-a", "ctl-a-card1", "model", "DONE", sha("x"))
	c.HSet(ctx, "lease:harvest:ctl-a", "instance", "other", "token", "t", "at", "1")
	c.PExpire(ctx, "lease:harvest:ctl-a", 5*time.Second)
	forge := newForge()
	pusher := &fixturePusher{pushes: map[string]int{}}
	res := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{"ctl-a"},
		Clock: time.Second, Instance: "mine", Forge: forge, Pusher: pusher})
	if !errors.Is(res[0].Err, harvest.ErrLeaseHeld) || len(pusher.pushes) != 0 || forge.opens != 0 {
		t.Fatalf("held lease: %+v pushes=%v opens=%d; want ErrLeaseHeld and nothing done", res[0], pusher.pushes, forge.opens)
	}
	if s := c.HGet(ctx, "s:"+sprint+":card:ctl-a-card1", "state").Val(); s != "ended" {
		t.Fatalf("state %s, want ended", s)
	}
}
