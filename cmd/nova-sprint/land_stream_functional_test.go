//go:build functional

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"

	"fmt"
	"github.com/alicebob/miniredis/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The stream the land-stream tests land, and its slug.
const (
	lsStream = "landing: streams + lander"
	lsSlug   = "landing-streams-lander"
)

// lsFixture is a bare repo with dev and refs/pull/<n>/head: 1 and 3 add a
// file each (green; 3's commit message closes #7), 2 adds red.txt (red
// under the fixture test).
func lsFixture(t *testing.T) (url string, bare string, heads map[int]string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	bare = filepath.Join(root, "remote.git")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	lsGit(t, src, "init", "-q", "-b", "dev")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lsGit(t, src, "add", ".")
	lsGit(t, src, "commit", "-q", "-m", "base")
	heads = map[int]string{}
	for n, file := range map[int]string{1: "one.txt", 2: "red.txt", 3: "three.txt"} {
		lsGit(t, src, "checkout", "-q", "-b", fmt.Sprintf("m%d", n), "dev")
		if err := os.WriteFile(filepath.Join(src, file), []byte(file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lsGit(t, src, "add", ".")
		msg := file
		if n == 3 {
			msg += "\n\nCloses #7" // a commit message closes an issue too
		}
		lsGit(t, src, "commit", "-q", "-m", msg)
		heads[n] = lsGit(t, src, "rev-parse", "HEAD")
		lsGit(t, src, "checkout", "-q", "dev")
	}
	lsGit(t, root, "clone", "-q", "--bare", src, bare)
	for n, h := range heads {
		lsGit(t, bare, "update-ref", fmt.Sprintf("refs/pull/%d/head", n), h)
	}
	return "file://" + bare, bare, heads
}

// TestLandStreamEndToEnd, and the DONE-WHEN of nova-tools#3779 for the
// lander: the merge of a two-member stream (fake forge) lands both member
// tasks and the tasks naming the issues they close, writes the CLOSE line on
// both member records, and the table prints landed 2 for the stream; the ws
// sets agree with every task record after each step.
func TestLandStreamEndToEnd(t *testing.T) {
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	url, bare, heads := lsFixture(t)
	gh := newFakeGitHub(t)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })

	// Three members in merging, aged 3 < 1 < 2: order is #3, #1, #2. The
	// issues #101 and #103 (closed by #1's body and #3's record) have their
	// own tasks in another stream.
	const swarm = "swarm: cards"
	for i, s := range []string{lsStream, swarm} {
		c.SAdd(ctx, "ws:names", s)
		c.ZAdd(ctx, "ws:order", redis.Z{Score: float64(i + 1), Member: s})
	}
	ids := []string{"t1", "t2", "t3", "build-101-one", "build-103-three", "build-7-seven"}
	for id, at := range map[string]float64{"build-101-one": 50, "build-103-three": 60, "build-7-seven": 70} {
		st := map[string]string{"build-101-one": "working", "build-103-three": "waiting", "build-7-seven": "ready"}[id]
		c.ZAdd(ctx, "ws:"+swarm+":"+st, redis.Z{Score: at, Member: id})
		c.HSet(ctx, "task:"+id, "stream", swarm, "state", st, "created_at", fmt.Sprint(at), "ref", "nova-tools#"+strings.Split(id, "-")[1])
		c.FCall(ctx, "ns_task_refs", nil, id)
	}
	for n, at := range map[int]float64{1: 200, 2: 300, 3: 100} {
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: at, Member: id})
		c.HSet(ctx, "task:"+id, "stream", lsStream, "state", "merging", "pr", fmt.Sprint(n), "created_at", fmt.Sprint(at))
		if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n),
			"--head", heads[n], "--base", "dev", "--stream", lsStream, "--task", id); code != 0 || !strings.Contains(out, "created=true") {
			t.Fatalf("pr record: %d %s %s", code, out, errOut)
		}
		line := fmt.Sprintf("SCORE who=rowan head=%s score=10/10 gates=ci:ok,base:ok,scope:ok", heads[n])
		if code, out, errOut := runSprint("pr", "lines", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--add", line); code != 0 || !strings.Contains(out, "lines=1 added=SCORE") {
			t.Fatalf("pr lines: %d %s %s", code, out, errOut)
		}
	}
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", "3", "--closes", "103"); code != 0 {
		t.Fatalf("pr record --closes: %d %s %s", code, out, errOut)
	}
	fsck := func(step string) {
		t.Helper()
		if err := ws.Check(ctx, c, ids); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	fsck("seed")
	c.Set(ctx, "cfg:land:test:"+lsRepo, "test ! -e red.txt", 0)

	// Dry run: the order from Redis alone, no clone, no GitHub.
	code, out, errOut := runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: %d %s %s", code, out, errOut)
	}
	var order []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(l, "ORDER ") {
			order = append(order, strings.Fields(l)[2])
		}
	}
	if strings.Join(order, " ") != lsRepo+"#3 "+lsRepo+"#1 "+lsRepo+"#2" || !strings.Contains(out, "LAND STREAM DRY repo="+lsRepo+" stream="+lsSlug+" branch=stream/"+lsSlug+" members=3") {
		t.Fatalf("dry run order %v:\n%s", order, out)
	}
	if len(gh.Calls()) != 0 {
		t.Fatalf("dry run called GitHub: %v", gh.Calls())
	}

	// Real run: #2 is red and parked by bisect; one PR is opened.
	work := filepath.Join(t.TempDir(), "clone")
	// Without --partial the red member's park is a LAND-SERIAL refusal after
	// the build (#4324): the park happens (t2 leaves merging with its
	// receipt), nothing is pushed, no PR opens.
	code, out, errOut = runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", work, "--api", gh.srv.URL)
	if code != 2 || !strings.Contains(errOut, "REFUSED LAND-SERIAL stream=") || !strings.Contains(errOut, "left_out=#2:red:batch-test (after the build)") {
		t.Fatalf("land stream without --partial: %d\n%s\n%s", code, out, errOut)
	}
	if len(gh.Calls()) != 0 {
		t.Fatalf("a refused landing called GitHub: %v", gh.Calls())
	}
	if st, _ := c.HGet(ctx, "task:t2", "state").Result(); st != "working" {
		t.Fatalf("parked member task state %q after the refusal", st)
	}
	if code, out, _ := runSprint("land", "status", "--redis", addr, "--repo", lsRepo); code != 0 || !strings.Contains(out, "state=serial") || !strings.Contains(out, " serial=LAND-SERIAL") {
		t.Fatalf("status after the refusal: %d\n%s", code, out)
	}
	// The serial record holds the parked member: while t2 is in working a
	// run is refused before any build (#4324), never landing the rest one
	// at a time with no line.
	code, out, errOut = runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", filepath.Join(t.TempDir(), "clone-nb"), "--api", gh.srv.URL)
	if code != 2 || !strings.Contains(errOut, "left_out=#2:not-back:working") || strings.Contains(errOut, "after the build") || len(gh.Calls()) != 0 {
		t.Fatalf("land stream with t2 not back: %d\n%s\n%s calls=%v", code, out, errOut, gh.Calls())
	}
	// One writer per stream: a live merge card holds the stream; a run
	// without its --card is refused naming it, and no claim is left.
	c.HSet(ctx, "task:merge-ls-1", "state", "open", "kind", "merge")
	c.HSet(ctx, "land:merge:"+lsStream, "task", "merge-ls-1", "owner", "card:merge-ls-1")
	code, out, errOut = runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", filepath.Join(t.TempDir(), "clone-own"), "--api", gh.srv.URL, "--partial")
	if code != 2 || !strings.Contains(errOut, "REFUSED LAND-OWNER stream=") || !strings.Contains(errOut, "owner=card:merge-ls-1") || len(gh.Calls()) != 0 {
		t.Fatalf("land stream with a live merge card: %d\n%s\n%s", code, out, errOut)
	}
	// An unrelated open task is not the stream's card: its --card is
	// refused before any build and the merge card's claim stands.
	c.HSet(ctx, "task:t-other", "state", "open")
	code, out, errOut = runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", filepath.Join(t.TempDir(), "clone-other"), "--api", gh.srv.URL, "--partial", "--card", "t-other")
	if code != 2 || !strings.Contains(errOut, "REFUSED --card t-other is not stream ") || !strings.Contains(errOut, "task=merge-ls-1 escalation=-") ||
		c.HGet(ctx, "land:merge:"+lsStream, "owner").Val() != "card:merge-ls-1" || len(gh.Calls()) != 0 {
		t.Fatalf("land stream --card of an unrelated task: %d\n%s\n%s", code, out, errOut)
	}
	c.Del(ctx, "task:t-other")
	// t2 is back in merging for the --partial run, as the card's child
	// (--card): the same build, the line printed as allowed and kept on
	// the landing record.
	// (Seeded the way the fixture seeds: the one move refuses a primary
	// into merging outside a read copy's end, NOCOPY.)
	c.ZRem(ctx, "ws:"+lsStream+":working", "t2")
	c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: 300, Member: "t2"})
	c.HSet(ctx, "task:t2", "state", "merging")
	c.HSet(ctx, "pr:nova-tools:2", "state", "open")
	fsck("t2 back in merging")
	work = filepath.Join(t.TempDir(), "clone2")
	code, out, errOut = runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", url, "--mirror", "none", "--workdir", work, "--api", gh.srv.URL, "--partial", "--card", "merge-ls-1")
	if code != 0 {
		t.Fatalf("land stream: %d\n%s\n%s", code, out, errOut)
	}
	if o := c.HGet(ctx, "land:merge:"+lsStream, "owner").Val(); o != "card:merge-ls-1" {
		t.Fatalf("the card's claim after its child's run: %q", o)
	}
	if !strings.Contains(out, "members=#3,#1 parked=#2:red:batch-test moved=1 tests=5 pr=#900 reused=false state=open") {
		t.Fatalf("receipt:\n%s", out)
	}
	if !strings.Contains(out, "left_out=#2:red:batch-test allowed=partial\n") {
		t.Fatalf("no LAND-SERIAL allowed line:\n%s", out)
	}
	calls := gh.Calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "POST /repos/"+lsRepo+"/pulls stream/"+lsSlug) || !strings.Contains(calls[0], "STREAM: "+lsStream) {
		t.Fatalf("REST calls %v", calls)
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("workdir kept after a clean run: %v", err)
	}
	if lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug) == "" {
		t.Fatal("stream branch not pushed")
	}
	if st, _ := c.HGet(ctx, "task:t2", "state").Result(); st != "working" {
		t.Fatalf("parked member task state %q", st)
	}
	if ok, _ := c.ZScore(ctx, "ws:"+lsStream+":working", "t2").Result(); ok == 0 {
		t.Fatal("t2 not in working")
	}
	fsck("land stream")

	// Status reads the landing and the stream PR record: ci pending.
	code, out, _ = runSprint("land", "status", "--redis", addr, "--repo", lsRepo)
	if code != 0 || !strings.Contains(out, "STREAM "+lsSlug+" streams=") || !strings.Contains(out, "pr=#900 ci=pending mergeable=-") ||
		!strings.Contains(out, " serial=LAND-SERIAL") || !strings.Contains(out, " partial_by=rowan partial_at=") {
		t.Fatalf("status: %d\n%s", code, out)
	}

	// Merge refuses while ci is pending, and calls nothing.
	code, _, errOut = runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--api", gh.srv.URL, "--card", "merge-ls-1")
	if code != 2 || !strings.Contains(errOut, "REFUSED ci=pending") {
		t.Fatalf("merge on pending: %d %s", code, errOut)
	}
	if len(gh.Calls()) != 1 {
		t.Fatalf("a refused merge called GitHub: %v", gh.Calls())
	}

	// CI green and mergeable: merge; #3 and #1 land with their CLOSE lines,
	// and so do the tasks of the issues they close; then they are closed.
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", "900", "--ci", "green", "--mergeable", "true"); code != 0 {
		t.Fatalf("pr record ci: %d %s %s", code, out, errOut)
	}
	code, out, errOut = runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--api", gh.srv.URL, "--card", "merge-ls-1")
	if code != 0 || !strings.Contains(out, "LAND MERGE repo="+lsRepo+" stream="+lsSlug+" pr=#900") ||
		!strings.Contains(out, "members=2 moved=5 missing=0 already=false closed=#3,#1 unclosed=- rest_calls=12 close_lines=2 skipped=0 closes_unread=- issues_closed=#103,#7,#101 issues_unclosed=-") {
		t.Fatalf("merge: %d\n%s\n%s", code, out, errOut)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":landed").Result(); n != 2 {
		t.Fatalf("landed %d", n)
	}
	landedRelease(t, ctx, c, addr, out)
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":merging").Result(); n != 0 {
		t.Fatalf("merging %d", n)
	}
	// the three tasks and the stream's stop, landed by structure with the last (#4318)
	if n, _ := c.ZCard(ctx, "ws:"+swarm+":landed").Result(); n != 4 {
		t.Fatalf("the closed issues' tasks: %s landed %d, want 3 and the stop", swarm, n)
	}
	if h := c.HGetAll(ctx, "task:"+ws.SentinelID(swarm)).Val(); h["where"] != "landed" || !strings.HasPrefix(h["merge_sha"], "dddddddd") {
		t.Fatalf("%s's stop after its last landing: %v", swarm, h)
	}
	for _, id := range []string{"t1", "t3", "build-101-one", "build-103-three", "build-7-seven"} {
		if why := c.HGet(ctx, "task:"+id, "why").Val(); why != "landed with nova-tools#900 (dddddddd)" {
			t.Fatalf("%s why %q", id, why)
		}
	}
	fsck("land merge")
	calls = gh.Calls()
	head := lsGit(t, bare, "rev-parse", "refs/heads/stream/"+lsSlug)
	closeLine := func(n int) string {
		return fmt.Sprintf("CLOSE who=lander head=%s landed: in stream/%s at %s with nova-tools#900 (dddddddd)", heads[n][:8], lsSlug, head[:8])
	}
	// GitHub closes no issue on a merge into dev: the lander closes the
	// issues the members close (#3's record: 103, its commit: 7; #1's body:
	// 101), one line naming the merge sha each, then the members.
	want := []string{
		"POST /repos/" + lsRepo + "/pulls stream/" + lsSlug,
		"PUT /repos/" + lsRepo + "/pulls/900/merge " + head,
		"GET /repos/" + lsRepo + "/pulls/1 ",
		"POST /repos/" + lsRepo + "/issues/103/comments " + closeLine(3) + "; closes this via nova-tools#3",
		"PATCH /repos/" + lsRepo + "/issues/103 closed",
		"POST /repos/" + lsRepo + "/issues/7/comments " + closeLine(3) + "; closes this via nova-tools#3",
		"PATCH /repos/" + lsRepo + "/issues/7 closed",
		"POST /repos/" + lsRepo + "/issues/101/comments " + closeLine(1) + "; closes this via nova-tools#1",
		"PATCH /repos/" + lsRepo + "/issues/101 closed",
		"POST /repos/" + lsRepo + "/issues/3/comments " + closeLine(3),
		"PATCH /repos/" + lsRepo + "/pulls/3 closed",
		"POST /repos/" + lsRepo + "/issues/1/comments " + closeLine(1),
		"PATCH /repos/" + lsRepo + "/pulls/1 closed",
	}
	if len(calls) != len(want) {
		t.Fatalf("REST calls:\n%s", strings.Join(calls, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(calls[i], w) || (i > 1 && calls[i] != w) {
			t.Fatalf("REST call %d = %q, want %q; all:\n%s", i, calls[i], w, strings.Join(calls, "\n"))
		}
	}
	if ic := c.HGet(ctx, "pr:nova-tools:1", "issues_closed").Val(); ic != "101" {
		t.Fatalf("#1 issues_closed %q", ic)
	}
	for _, n := range []int{1, 3} {
		reads := strings.Split(c.HGet(ctx, "pr:nova-tools:"+fmt.Sprint(n), "reads").Val(), "\n")
		if reads[len(reads)-1] != closeLine(n) {
			t.Fatalf("#%d reads %q, want the CLOSE line last", n, reads)
		}
	}
	if cl := c.HGet(ctx, "pr:nova-tools:1", "closes").Val(); cl != "101" {
		t.Fatalf("#1 closes %q, want 101 from its body", cl)
	}
	// the refused run's park, the --partial run's park, the landing, five
	// lands walked step by step through the one move (#3778: a ready member
	// goes ready -> working -> landed, one receipt each) (and the two
	// streams' sentinels created at registration, and the swarm's landed
	// with its last card, #4318)
	if log, _ := c.XLen(ctx, "ws:log").Result(); log != 14 {
		t.Fatalf("ws:log %d", log)
	}
	// The table reads the sets: the stream's landed cell is 2.
	snap, err := table.NewSprintReader(c, table.SprintConfig{Friends: []string{"rowan"}}).Read(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Render(time.Now()); !strings.Contains(got, fmt.Sprintf("%-25s | %7d | %5d | %7d | %6d | %7d | %6d\n", lsStream, 0, 0, 1, 0, 0, 2)) {
		t.Fatalf("table:\n%s", got)
	}
	// A re-run is ALREADY and closes nothing twice.
	code, out, _ = runSprint("land", "merge", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--api", gh.srv.URL, "--card", "merge-ls-1")
	if code != 0 || !strings.Contains(out, "moved=0 missing=0 already=true closed=- unclosed=- rest_calls=0 close_lines=0 skipped=0 closes_unread=- issues_closed=- issues_unclosed=- release=-") {
		t.Fatalf("re-run: %d\n%s", code, out)
	}
	fsck("re-run")
}

// landedRelease is the #4050 DONE-WHEN, run on the landing above: the merge
// into dev left fleet:release version v0.16.0-dev.<merge sha8> and commit
// <merge sha> (its receipt says so), and the reconciler's deploy duty, one
// dry pass through `fleet build duty`, reports WOULD INSTALL for every bench
// whose beat version differs and for no other.
func landedRelease(t *testing.T, ctx context.Context, c *redis.Client, addr, receipt string) {
	t.Helper()
	merge := strings.Repeat("d", 40)
	version := "v0.16.0-dev.dddddddd"
	if !strings.Contains(receipt, " release="+version+"\n") {
		t.Fatalf("the LAND MERGE receipt does not name the release:\n%s", receipt)
	}
	rel := c.HGetAll(ctx, "fleet:release").Val()
	if rel["version"] != version || rel["commit"] != merge || rel["landed"] != "nova-tools#900" {
		t.Fatalf("fleet:release after the landing = %v", rel)
	}
	c.SAdd(ctx, "benches", "hulk", "space", "batman", "vision")
	for b, line := range map[string]string{
		"hulk":   "nova-sprint v0.16.0-dev.7658e89c linux/amd64 go1.26.1",
		"space":  "nova-sprint " + version + " linux/amd64 go1.26.1",
		"batman": "nova-sprint 20260924090000-bbbbbbbbbbbb darwin/amd64 go1.26.1",
	} {
		c.HSet(ctx, "bench:"+b+":beat", "at", "1", "build", line)
		c.Expire(ctx, "bench:"+b+":beat", time.Minute)
	}
	code, out, errOut := runSprint("fleet", "build", "duty", "--dry-run", "--redis", addr)
	want := "FLEET DEPLOY WOULD INSTALL batman beat=20260924090000-bbbbbbbbbbbb want=" + version + "\n" +
		"FLEET DEPLOY WOULD INSTALL hulk beat=v0.16.0-dev.7658e89c want=" + version + "\n" +
		"FLEET DEPLOY DRY-RUN version=" + version + " commit=dddddddddddd drift=2 current=1 quiet=1\n"
	if code != 0 || out != want {
		t.Fatalf("fleet build duty --dry-run: %d\n%s%s\nwant:\n%s", code, out, errOut, want)
	}
	c.Del(ctx, "benches", "bench:hulk:beat", "bench:space:beat", "bench:batman:beat")
}

// TestLandStreamConflictStops runs a whole land stream against a git remote it
// builds with a dozen git processes; exec of whole programs is the functional
// tier's (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s).
func TestLandStreamConflictStops(t *testing.T) {
	mr := miniredis.RunT(t)
	addr := mr.Addr()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	root := t.TempDir()
	src, bare := filepath.Join(root, "src"), filepath.Join(root, "remote.git")
	_ = os.MkdirAll(src, 0o755)
	lsGit(t, src, "init", "-q", "-b", "dev")
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("base\n"), 0o644)
	lsGit(t, src, "add", ".")
	lsGit(t, src, "commit", "-q", "-m", "base")
	heads := map[int]string{}
	for n, body := range map[int]string{1: "one\n", 2: "two\n"} {
		lsGit(t, src, "checkout", "-q", "-b", fmt.Sprintf("m%d", n), "dev")
		_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte(body), 0o644)
		lsGit(t, src, "commit", "-q", "-am", body)
		heads[n] = lsGit(t, src, "rev-parse", "HEAD")
		lsGit(t, src, "checkout", "-q", "dev")
	}
	lsGit(t, root, "clone", "-q", "--bare", src, bare)
	for n, h := range heads {
		lsGit(t, bare, "update-ref", fmt.Sprintf("refs/pull/%d/head", n), h)
		id := fmt.Sprintf("t%d", n)
		c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: float64(n), Member: id})
		c.HSet(ctx, "task:"+id, "pr", fmt.Sprint(n))
		runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--head", h, "--base", "dev", "--stream", lsStream)
		runSprint("pr", "lines", "--redis", addr, "--repo", lsRepo, "--n", fmt.Sprint(n), "--add", "SCORE who=emma head="+h+" score=9/10")
	}
	gh := newFakeGitHub(t)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })
	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut := runSprint("land", "stream", "--redis", addr, "--repo", lsRepo, "--stream", lsStream,
		"--remote", "file://"+bare, "--mirror", "none", "--workdir", work, "--api", gh.srv.URL, "--test", "true")
	if code != 1 || !strings.Contains(out, "LAND STREAM CONFLICT repo="+lsRepo+" stream="+lsSlug+" member=#2 files=a.txt merged_before=1 workdir="+work) {
		t.Fatalf("conflict: %d\n%s\n%s", code, out, errOut)
	}
	if len(gh.Calls()) != 0 {
		t.Fatalf("a conflict called GitHub: %v", gh.Calls())
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the workdir is Rowan's to resolve in; it was removed: %v", err)
	}
	if st, _ := c.HGet(ctx, "land:"+lsRepo+":"+lsSlug, "state").Result(); st != "conflict" {
		t.Fatalf("land state %q", st)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":merging").Result(); n != 2 {
		t.Fatalf("a conflict moved members: merging=%d", n)
	}
}

func lsGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fakeGitHub records every REST call and answers the five the lander makes
// (a member's body closes issue 10<n>).
type fakeGitHub struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.calls = append(g.calls, r.Method+" "+r.URL.Path+" "+body["head"]+body["sha"]+body["body"]+body["state"])
		g.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/"+lsRepo+"/pulls":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":900}`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/"+lsRepo+"/pulls/900/merge":
			_, _ = w.Write([]byte(`{"merged":true,"sha":"` + strings.Repeat("d", 40) + `"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPatch:
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/"+lsRepo+"/pulls/"):
			n := strings.TrimPrefix(r.URL.Path, "/repos/"+lsRepo+"/pulls/")
			_, _ = w.Write([]byte(`{"number":` + n + `,"body":"STREAM: x\n\nCloses #10` + n + `"}`))
		default:
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) Calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}
