package harvest_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

const orphanBase = "09fbedc9"

// seedOrphan writes a card in the shape ns_card_required (#2930) leaves an
// orphan: state orphan-effect, indexed under idx:card:orphan-effect, never in
// the ended index, and the unresolved item <label>:orphan-effect:<attempt>.
func seedOrphan(t *testing.T, c *redis.Client, bench, label string, attempt int, token string) card.Identity {
	t.Helper()
	ctx := context.Background()
	id := card.Identity{Sprint: sprint, Label: label, BaseSHA: orphanBase, Bench: bench, Attempt: attempt}
	branch := fmt.Sprintf("nova/%s/%s-a%d", sprint, label, attempt)
	now, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, card.CardKey(sprint, label),
		"kind", "code", "repo", "nova-tools", "base", "dev", "base_sha", orphanBase,
		"state", "orphan-effect", "reason", "orphan-effect", "bench", bench,
		"attempt", fmt.Sprint(attempt), "identity", id.String(), "token_sha", card.TokenSHA(token),
		"orphan_evidence", "branch="+branch+",pushed="+sha(label), "orphan_at", fmt.Sprint(now.UnixMilli())).Err(); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, card.IdxKey(sprint, "orphan-effect"), label)
	c.HSet(ctx, "s:"+sprint+":unresolved", fmt.Sprintf("%s:orphan-effect:%d", label, attempt),
		id.String()+" branch="+branch+",pushed="+sha(label))
	return id
}

// attemptDir is the attempt's own results directory under root.
func attemptDir(t *testing.T, root string, id card.Identity) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(id.String()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func endRecord(t *testing.T, dir string, id card.Identity, token, outcome, reason, pushed string) {
	t.Helper()
	exit := 0
	if outcome != "DONE" {
		exit = 1
	}
	if err := card.WriteEndRecord(dir, card.EndRecord{Identity: id, Outcome: outcome, Reason: reason, ExitCode: exit,
		TokenSHA: card.TokenSHA(token), PushedSHA: pushed, At: "2026-09-23T13:00:00Z"}); err != nil {
		t.Fatal(err)
	}
}

// orphanForge is GitHub for the supersede step: branches by name, PRs by head
// branch, closes with their comment, renames. Every call is counted.
type orphanForge struct {
	mu       sync.Mutex
	branches map[string]string // name -> sha
	open     map[string]harvest.PR
	closed   map[int]string // PR -> comment
	renames  int
	finds    int
	closes   int
}

func newOrphanForge() *orphanForge {
	return &orphanForge{branches: map[string]string{}, open: map[string]harvest.PR{}, closed: map[int]string{}}
}

func (f *orphanForge) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renames + f.finds + f.closes
}

func (f *orphanForge) FindOpenPR(ctx context.Context, repo, branch string) (harvest.PR, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finds++
	pr, ok := f.open[repo+":"+branch]
	return pr, ok, nil
}

func (f *orphanForge) ClosePR(ctx context.Context, repo string, n int, comment string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	for k, pr := range f.open {
		if pr.Number == n {
			delete(f.open, k)
			f.closed[n] = comment
			return nil
		}
	}
	return fmt.Errorf("422 pull %d is not open", n)
}

func (f *orphanForge) RenameBranch(ctx context.Context, repo, from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renames++
	sha, ok := f.branches[from]
	if !ok {
		if _, done := f.branches[to]; done {
			return nil
		}
		return fmt.Errorf("404 branch %s", from)
	}
	if _, taken := f.branches[to]; taken {
		return fmt.Errorf("422 branch %s exists", to)
	}
	delete(f.branches, from)
	f.branches[to] = sha
	return nil
}

func sweepOrphans(t *testing.T, st *store.Store, root string, forge harvest.OrphanForge, bench string) harvest.OrphanResult {
	t.Helper()
	res := harvest.RunOrphans(context.Background(), st, harvest.OrphanOptions{
		Sprint: sprint, Benches: []string{bench}, ResultsRoot: root, Instance: "orphans-test", Forge: forge,
	})
	if len(res) != 1 {
		t.Fatalf("results = %d", len(res))
	}
	if res[0].Err != nil || len(res[0].Failed) > 0 {
		t.Fatalf("sweep %s: err=%v failed=%v", bench, res[0].Err, res[0].Failed)
	}
	return res[0]
}

func logLen(t *testing.T, c *redis.Client) int64 {
	t.Helper()
	n, err := c.XLen(context.Background(), "s:"+sprint+":log").Result()
	if err != nil && err != redis.Nil {
		t.Fatal(err)
	}
	return n
}

func cardHash(t *testing.T, c *redis.Client, label string) map[string]string {
	t.Helper()
	h, err := c.HGetAll(context.Background(), card.CardKey(sprint, label)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Control 25 at the harvest worker (#2756 3.2, 4.3; #3042): an orphan-effect
// card ends only on the end record of its own fixed identity.
func TestHarvestOrphansEndsOnlyOnItsRecord(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	const bench = "orphan-bench"
	const label, red = "c25-orphan", "c25-red"
	root := t.TempDir()
	token, rtoken := "attempt-1-token-orphan", "attempt-1-token-red"
	id := seedOrphan(t, c, bench, label, 1, token)
	rid := seedOrphan(t, c, bench, red, 1, rtoken)
	c.HSet(ctx, "bench:"+bench+":beat", "host", bench, "user", "nova")
	forge := newOrphanForge()
	harvestForge := newForge()
	harvestForge.heads["nova/"+sprint+"/"+label+"-a1"] = sha(label)
	pusher := &fixturePusher{pushes: map[string]int{}}
	harvestPass := func() []harvest.CardResult {
		r := harvest.Run(ctx, st, harvest.Options{Sprint: sprint, Benches: []string{bench}, Instance: "harvest-test",
			Forge: harvestForge, Pusher: pusher})
		if r[0].Err != nil || len(r[0].Failed) > 0 {
			t.Fatalf("harvest: err=%v failed=%v", r[0].Err, r[0].Failed)
		}
		return r[0].Cards
	}

	// NEGATIVE: a pushed branch and no end record stays orphan-effect across
	// five sweeps; no harvest, no review task, no receipt, no GitHub call.
	before := logLen(t, c)
	for i := 0; i < 5; i++ {
		res := sweepOrphans(t, st, root, forge, bench)
		if len(res.Ended) != 0 || len(res.Superseded) != 0 || len(res.Waiting) != 2 {
			t.Fatalf("sweep %d with no record: %+v", i, res)
		}
		if h := cardHash(t, c, label); h["state"] != "orphan-effect" || h["outcome"] != "" {
			t.Fatalf("sweep %d: orphan hash %+v", i, h)
		}
		if got := harvestPass(); len(got) != 0 {
			t.Fatalf("sweep %d: an orphan was harvested: %+v", i, got)
		}
	}
	if logLen(t, c) != before {
		t.Fatalf("five sweeps over an orphan wrote %d receipts", logLen(t, c)-before)
	}
	if keys, _ := c.Keys(ctx, "s:"+sprint+":task:*").Result(); len(keys) != 0 {
		t.Fatalf("tasks exist for an orphan: %v", keys)
	}
	if forge.calls() != 0 || harvestForge.finds+harvestForge.opens != 0 || len(pusher.pushes) != 0 {
		t.Fatalf("an orphan reached GitHub: orphan forge %d, harvest finds %d opens %d, pushes %v",
			forge.calls(), harvestForge.finds, harvestForge.opens, pusher.pushes)
	}
	if ok, _ := c.SIsMember(ctx, card.IdxKey(sprint, "ended"), label).Result(); ok {
		t.Fatal("orphan is in the ended index")
	}

	// A record for another attempt, or for another cut sha, resolves nothing:
	// in the orphan's own directory, and in the other attempt's directory.
	own := attemptDir(t, root, id)
	other := id
	other.Attempt = 2
	endRecord(t, attemptDir(t, root, other), other, token, "DONE", "done", sha(label))
	endRecord(t, own, other, token, "DONE", "done", sha(label))
	if res := sweepOrphans(t, st, root, forge, bench); len(res.Ended) != 0 {
		t.Fatalf("a record for attempt 2 ended attempt 1: %+v", res)
	}
	recut := id
	recut.BaseSHA = "7a7a7a7a"
	endRecord(t, own, recut, token, "DONE", "done", sha(label))
	if res := sweepOrphans(t, st, root, forge, bench); len(res.Ended) != 0 {
		t.Fatalf("a record for another cut sha ended the orphan: %+v", res)
	}
	if h := cardHash(t, c, label); h["state"] != "orphan-effect" || logLen(t, c) != before {
		t.Fatalf("a foreign record wrote: state %s, receipts %d", h["state"], logLen(t, c)-before)
	}

	// POSITIVE: the record of the same identity, written later, ends it with
	// the record's outcome: DONE goes on to harvest with the branch it names;
	// FAILED tests-red ends FAILED and is never harvested.
	endRecord(t, own, id, token, "DONE", "done", sha(label))
	endRecord(t, attemptDir(t, root, rid), rid, rtoken, "FAILED", "tests-red", sha(red))
	res := sweepOrphans(t, st, root, forge, bench)
	if len(res.Ended) != 2 || len(res.Waiting) != 0 {
		t.Fatalf("records of the same identity: %+v", res)
	}
	outcomes := map[string]string{}
	for _, e := range res.Ended {
		outcomes[e.Label] = e.Outcome + "/" + e.Reason
		if e.Receipt == "" || e.Settled == "" {
			t.Fatalf("ended without receipts: %+v", e)
		}
	}
	if outcomes[label] != "DONE/done" || outcomes[red] != "FAILED/tests-red" {
		t.Fatalf("outcomes = %v", outcomes)
	}
	if h := cardHash(t, c, label); h["state"] != "ended" || h["outcome"] != "DONE" || h["pushed_sha"] != sha(label) {
		t.Fatalf("ended hash: %+v", h)
	}
	if h := cardHash(t, c, red); h["state"] != "ended" || h["outcome"] != "FAILED" || h["reason"] != "tests-red" {
		t.Fatalf("red hash: %+v", h)
	}
	if n, _ := c.HLen(ctx, "s:"+sprint+":unresolved").Result(); n != 0 {
		t.Fatalf("unresolved items left after both ended: %d", n)
	}
	got := harvestPass()
	if len(got) != 1 || got[0].Label != label || got[0].Branch != "nova/"+sprint+"/"+label+"-a1" {
		t.Fatalf("harvest after the DONE record: %+v", got)
	}
	// Idempotent: another sweep finds nothing and writes nothing.
	n := logLen(t, c)
	if res := sweepOrphans(t, st, root, forge, bench); len(res.Ended)+len(res.Waiting)+len(res.Superseded) != 0 || logLen(t, c) != n {
		t.Fatalf("sweep after the end: %+v (+%d receipts)", res, logLen(t, c)-n)
	}
	if forge.calls() != 0 {
		t.Fatalf("ending an orphan by its record called GitHub %d times", forge.calls())
	}
}

// Control 26 (#2756 4.3; #3042): when a later attempt of the label ends DONE,
// the orphan is superseded: its branch is renamed orphan/<S>/<label>-a<n> and
// its PR is closed with the identity in the comment, once.
func TestSupersededOrphanRenamed(t *testing.T) {
	c := startRedis(t)
	st := store.New(c)
	ctx := context.Background()
	const bench = "orphan-bench"
	const label, failing = "c26-orphan", "c26-red"
	root := t.TempDir()
	forge := newOrphanForge()

	id1 := seedOrphan(t, c, bench, label, 1, "a1-token")
	fid1 := seedOrphan(t, c, bench, failing, 1, "f1-token")
	for _, l := range []string{label, failing} {
		b := "nova/" + sprint + "/" + l + "-a1"
		forge.branches[b] = sha(l)
		forge.open["nova-tools:"+b] = harvest.PR{Number: 700 + len(forge.open), Head: sha(l)}
	}
	prNum := forge.open["nova-tools:nova/"+sprint+"/"+label+"-a1"].Number

	// The recut: attempt 2 of each label runs (on another bench; the recut
	// is not this task's) while attempt 1 stays an unresolved orphan.
	recut := func(l, token string) card.Identity {
		id := card.Identity{Sprint: sprint, Label: l, BaseSHA: "5e5e5e5e", Bench: "other-bench", Attempt: 2}
		c.SRem(ctx, card.IdxKey(sprint, "orphan-effect"), l)
		c.HSet(ctx, card.CardKey(sprint, l), "state", "running", "attempt", "2", "identity", id.String(),
			"bench", id.Bench, "base_sha", id.BaseSHA, "token_sha", card.TokenSHA(token), "reason", "")
		c.SAdd(ctx, card.IdxKey(sprint, "running"), l)
		return id
	}
	id2 := recut(label, "a2-token")
	fid2 := recut(failing, "f2-token")

	// NEGATIVE: attempt 2 still running supersedes nothing.
	if res := sweepOrphans(t, st, root, forge, bench); len(res.Superseded) != 0 || forge.renames+forge.closes != 0 {
		t.Fatalf("attempt 2 running superseded attempt 1: %+v", res)
	}
	// attempt 2 ends: DONE for one label, FAILED for the other.
	endRecord(t, attemptDir(t, root, id2), id2, "a2-token", "DONE", "done", sha(label+"2"))
	endRecord(t, attemptDir(t, root, fid2), fid2, "f2-token", "FAILED", "tests-red", sha(failing+"2"))
	for _, x := range []struct {
		l  string
		id card.Identity
	}{{label, id2}, {failing, fid2}} {
		r, err := card.Resolve(ctx, st, card.ResolveRequest{Sprint: sprint, Label: x.l, ResultsDir: attemptDir(t, root, x.id)})
		if err != nil || !r.Resolved {
			t.Fatalf("attempt 2 of %s did not end: %+v %v", x.l, r, err)
		}
	}

	before := logLen(t, c)
	res := sweepOrphans(t, st, root, forge, bench)
	if len(res.Superseded) != 1 {
		t.Fatalf("superseded = %+v", res.Superseded)
	}
	s := res.Superseded[0]
	wantTo := "orphan/" + sprint + "/" + label + "-a1"
	if s.Label != label || s.Identity != id1.String() || s.From != "nova/"+sprint+"/"+label+"-a1" || s.To != wantTo {
		t.Fatalf("superseded row: %+v", s)
	}
	if _, ok := forge.branches[wantTo]; !ok {
		t.Fatalf("branch not renamed to %s: %v", wantTo, forge.branches)
	}
	if _, ok := forge.branches["nova/"+sprint+"/"+label+"-a1"]; ok {
		t.Fatal("the attempt-1 branch is still under nova/")
	}
	comment, closed := forge.closed[prNum]
	if !closed || !strings.Contains(comment, id1.String()) {
		t.Fatalf("PR %d closed=%v comment=%q, want the identity %s", prNum, closed, comment, id1)
	}
	if len(s.Closed) != 1 || s.Closed[0] != prNum {
		t.Fatalf("closed PRs reported: %v", s.Closed)
	}
	if logLen(t, c) != before+1 {
		t.Fatalf("supersede wrote %d receipts, want 1", logLen(t, c)-before)
	}
	if v, _ := c.HGet(ctx, "s:"+sprint+":superseded", id1.String()).Result(); !strings.Contains(v, wantTo) {
		t.Fatalf("superseded record = %q", v)
	}
	if ok, _ := c.HExists(ctx, "s:"+sprint+":unresolved", label+":orphan-effect:1").Result(); ok {
		t.Fatal("the superseded orphan's item is still unresolved")
	}
	// Attempt 2 is untouched: still ended DONE, its own identity.
	if h := cardHash(t, c, label); h["state"] != "ended" || h["attempt"] != "2" || h["identity"] != id2.String() {
		t.Fatalf("attempt 2 hash after supersede: %+v", h)
	}

	// The label whose attempt 2 ended FAILED keeps its orphan: no rename, no close.
	if _, ok := forge.branches["nova/"+sprint+"/"+failing+"-a1"]; !ok {
		t.Fatal("a FAILED attempt 2 superseded attempt 1")
	}
	if ok, _ := c.HExists(ctx, "s:"+sprint+":unresolved", failing+":orphan-effect:1").Result(); !ok {
		t.Fatalf("the FAILED label's orphan item is gone (%s)", fid1)
	}

	// Once: a second sweep renames nothing, closes nothing, writes nothing.
	calls, n := forge.calls(), logLen(t, c)
	if res := sweepOrphans(t, st, root, forge, bench); len(res.Superseded) != 0 || forge.calls() != calls || logLen(t, c) != n {
		t.Fatalf("second sweep: %+v (calls %d -> %d, receipts +%d)", res, calls, forge.calls(), logLen(t, c)-n)
	}
}
