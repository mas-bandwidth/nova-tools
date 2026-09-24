package consume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func initTestRedis(t *testing.T) (*store.Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	// A test double of the PR-keyed hold contract this package reads; it is
	// named apart from #3092's real ns_ingest_disposition (unit-keyed, in the
	// nova_sprint library) so the two never collide.
	registerIngestDispositionFallback(t, client)
	return store.New(client), client
}

func registerIngestDispositionFallback(t *testing.T, client *redis.Client) {
	t.Helper()
	ctx := context.Background()
	src := `#!lua name=nova_hold
local function now_ms()
  local tm = redis.call('TIME')
  return tonumber(tm[1]) * 1000 + math.floor(tonumber(tm[2]) / 1000)
end

local function ingest_disposition(keys, args)
  local S = args[1]
  local repo = args[2]
  local pr = args[3]
  local who = args[4]
  local head = args[5]
  local verdict = args[6]
  local score = args[7]
  local url = args[8]
  local cid = args[9] or '1'
  local release_for = args[10] or ''

  local disp_key = 's:' .. S .. ':disp:' .. repo .. ':' .. pr
  redis.call('HSET', disp_key, who .. '@' .. head, verdict .. ' ' .. score .. ' ' .. url .. ' ' .. cid)

  local hold_key = 's:' .. S .. ':hold:' .. repo .. ':' .. pr
  local holds = redis.call('HGETALL', hold_key)
  for i = 1, #holds, 2 do
    local hk = holds[i]
    local hj = holds[i+1]
    local h = cjson.decode(hj)
    if (not h.released_by or h.released_by == '') and h.holder then
      local released = false
      local rel_by = ''
      local rel_kind = ''
      if who == h.holder and head ~= h.head then
        released = true
        rel_by = who
        rel_kind = 'approve'
      end
      local policy_rr = redis.call('HGET', 's:' .. S .. ':policy', 'release_reader') or ''
      if not released and who == policy_rr and release_for == h.holder and head ~= h.head then
        local hdown = redis.call('EXISTS', 'friend:' .. h.holder .. ':down') == 1
        local hstate = redis.call('HGET', 'friend:' .. h.holder .. ':state', 'state') or ''
        if hdown or hstate == 'down' or hstate == 'out-of-credits' or hstate == 'away' then
          released = true
          rel_by = who
          rel_kind = 'release_reader'
        end
      end
      if released then
        h.released_by = rel_by
        h.release_kind = rel_kind
        h.released_at = tostring(now_ms())
        redis.call('HSET', hold_key, hk, cjson.encode(h))
      end
    end
  end
  return { 'OK' }
end

redis.register_function('ns_test_ingest_disposition', ingest_disposition)
`
	_ = client.FunctionLoad(ctx, src).Err()
}

func ingestDisposition(ctx context.Context, client *redis.Client, sprint, repo string, pr int, who, head, verdict string, score int, url, releaseFor string) error {
	prStr := strconv.Itoa(pr)
	scoreStr := strconv.Itoa(score)
	args := []any{sprint, repo, prStr, who, head, verdict, scoreStr, url, "1"}
	if releaseFor != "" {
		args = append(args, releaseFor)
	}
	err := client.FCall(ctx, "ns_test_ingest_disposition", nil, args...).Err()
	if err == nil {
		return nil
	}
	// Fallback in Go if Lua function is missing
	nowMs := time.Now().UnixMilli()
	dispKey := fmt.Sprintf("s:%s:disp:%s:%d", sprint, repo, pr)
	dispVal := fmt.Sprintf("%s %d %s 1", verdict, score, url)
	if err := client.HSet(ctx, dispKey, who+"@"+head, dispVal).Err(); err != nil {
		return err
	}
	holdKey := fmt.Sprintf("s:%s:hold:%s:%d", sprint, repo, pr)
	holds, err := client.HGetAll(ctx, holdKey).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	for hk, hj := range holds {
		var h struct {
			Holder      string `json:"holder"`
			Head        string `json:"head"`
			Kind        string `json:"kind"`
			Reason      string `json:"reason"`
			URL         string `json:"url"`
			At          int64  `json:"at"`
			ReleasedBy  string `json:"released_by"`
			ReleaseKind string `json:"release_kind"`
			ReleasedAt  string `json:"released_at"`
		}
		if err := json.Unmarshal([]byte(hj), &h); err != nil {
			continue
		}
		if h.ReleasedBy == "" && h.Holder != "" {
			released := false
			relBy := ""
			relKind := ""
			if who == h.Holder && head != h.Head {
				released = true
				relBy = who
				relKind = "approve"
			}
			policyRR, _ := client.HGet(ctx, "s:"+sprint+":policy", "release_reader").Result()
			if !released && who == policyRR && releaseFor == h.Holder && head != h.Head {
				hdown, _ := client.Exists(ctx, "friend:"+h.Holder+":down").Result()
				hstate, _ := client.HGet(ctx, "friend:"+h.Holder+":state", "state").Result()
				if hdown > 0 || hstate == "down" || hstate == "out-of-credits" || hstate == "away" {
					released = true
					relBy = who
					relKind = "release_reader"
				}
			}
			if released {
				h.ReleasedBy = relBy
				h.ReleaseKind = relKind
				h.ReleasedAt = strconv.FormatInt(nowMs, 10)
				b, _ := json.Marshal(h)
				_ = client.HSet(ctx, holdKey, hk, string(b)).Err()
			}
		}
	}
	return nil
}

func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "-b", "main", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v (%s)", err, out)
	}
	return dir
}

func setBareRef(t *testing.T, bareDir, ref, msg string) string {
	t.Helper()
	treeCmd := exec.Command("git", "-C", bareDir, "hash-object", "-w", "-t", "tree", "/dev/null")
	treeOut, err := treeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hash-object: %v (%s)", err, treeOut)
	}
	tree := strings.TrimSpace(string(treeOut))

	commitCmd := exec.Command("git", "-C", bareDir, "commit-tree", tree, "-m", msg)
	// The fixture names its own identity: a hosted runner has no global git
	// user and an empty passwd name, so commit-tree refuses "empty ident name"
	// there (dev push run 35950929169, ubuntu-latest shard 3). The last value
	// of a duplicated key wins in exec.Cmd.Env.
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Consume Test", "GIT_AUTHOR_EMAIL=consume-test@example.com",
		"GIT_COMMITTER_NAME=Consume Test", "GIT_COMMITTER_EMAIL=consume-test@example.com")
	commitOut, err := commitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("commit-tree: %v (%s)", err, commitOut)
	}
	commit := strings.TrimSpace(string(commitOut))

	updateCmd := exec.Command("git", "-C", bareDir, "update-ref", ref, commit)
	if out, err := updateCmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v (%s)", err, out)
	}
	return commit
}

func snapshotKeys(t *testing.T, client *redis.Client, sprint string) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := client.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	snap := make(map[string]string)
	for _, k := range keys {
		kt, err := client.Type(ctx, k).Result()
		if err != nil {
			t.Fatal(err)
		}
		switch kt {
		case "string":
			v, _ := client.Get(ctx, k).Result()
			snap[k] = "string:" + v
		case "hash":
			v, _ := client.HGetAll(ctx, k).Result()
			// Exclude fluctuating timestamps
			var parts []string
			for f, val := range v {
				if f == "at" || f == "pass_at" || f == "took_ms" {
					continue
				}
				parts = append(parts, f+"="+val)
			}
			sort.Strings(parts)
			snap[k] = "hash:" + strings.Join(parts, ";")
		case "set":
			v, _ := client.SMembers(ctx, k).Result()
			sort.Strings(v)
			snap[k] = "set:" + strings.Join(v, ";")
		case "zset":
			v, _ := client.ZRangeWithScores(ctx, k, 0, -1).Result()
			var parts []string
			for _, z := range v {
				parts = append(parts, fmt.Sprintf("%v=%f", z.Member, z.Score))
			}
			snap[k] = "zset:" + strings.Join(parts, ";")
		case "stream":
			v, _ := client.XRange(ctx, k, "-", "+").Result()
			snap[k] = fmt.Sprintf("stream:len=%d", len(v))
		}
	}
	return snap
}

// TestControl06CancelledCINeverLandReady:
// CANCELLED, SKIPPED, empty, absent, and OK at an older head each leave the
// card review-ready with reason ci MISSING. OK at the exact head, plus reads at
// the bar and no open hold, makes it land-ready.
func TestControl06CancelledCINeverLandReady(t *testing.T) {
	st, client := initTestRedis(t)
	ctx := context.Background()
	const S = "control-c06"
	const repo = "nova-tools"
	const prNum = 101
	const label = "card-1"
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)

	pipe := client.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2")
	pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")
	pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
	pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
		"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
	pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label),
		"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
		"author", "rowan", "state", "review-ready", "attempt", "1")
	pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label)
	pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum),
		"stella@"+headB, "APPROVE 9 https://example.com/1 1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	pr := &PRRead{
		Store:    st,
		Sprint:   S,
		Consumer: "test-c06",
		Instance: "test-c06",
		Actor:    "pr-to-read",
	}

	pass := func() string {
		t.Helper()
		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}
		return pr.Reason(label)
	}

	assertReviewReady := func(caseName string) {
		t.Helper()
		r := pass()
		if r != "ci MISSING" {
			t.Fatalf("%s: reason = %q, want 'ci MISSING'", caseName, r)
		}
		cardState, err := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
		if err != nil || cardState != "review-ready" {
			t.Fatalf("%s: card state = %q (%v), want 'review-ready'", caseName, cardState, err)
		}
		landable, _ := client.ZScore(ctx, "s:"+S+":landable", fmt.Sprintf("%s#%d", repo, prNum)).Result()
		if landable != 0 {
			t.Fatalf("%s: card is landable with score %f, want absent", caseName, landable)
		}
	}

	// 1. CANCELLED
	_ = client.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "CANCELLED").Err()
	assertReviewReady("verdict CANCELLED")

	// 2. SKIPPED
	_ = client.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "SKIPPED").Err()
	assertReviewReady("verdict SKIPPED")

	// 3. empty verdict
	_ = client.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "").Err()
	assertReviewReady("verdict empty")

	// 4. absent
	_ = client.Del(ctx, "ci:"+repo+":"+headB).Err()
	assertReviewReady("ci record absent")

	// 5. OK at older head A
	_ = client.HSet(ctx, "ci:"+repo+":"+headA, "verdict", "OK").Err()
	assertReviewReady("ci OK at older head A")

	// 6. OK at exact head B
	_ = client.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK").Err()
	r := pass()
	if r != "reads 1/1" {
		t.Fatalf("verdict OK at head B: reason = %q, want 'reads 1/1'", r)
	}
	cardState, err := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
	if err != nil || cardState != "land-ready" {
		t.Fatalf("card state = %q (%v), want 'land-ready'", cardState, err)
	}
	if isMember, err := client.SIsMember(ctx, "s:"+S+":idx:card:land-ready", label).Result(); err != nil || !isMember {
		t.Fatalf("card %s not in s:%s:idx:card:land-ready (%v)", label, S, err)
	}
	// Verify exactly one land-ready event on s:<S>:log
	logEntries, err := client.XRange(ctx, "s:"+S+":log", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	landReadyEvents := 0
	for _, entry := range logEntries {
		if entry.Values["from"] == "review-ready" && entry.Values["to"] == "land-ready" {
			landReadyEvents++
		}
	}
	if landReadyEvents != 1 {
		t.Fatalf("land-ready events count = %d, want 1", landReadyEvents)
	}
}

// TestControl32HeadChange:
// Head change A->B cancels A's open reviews and pushes one review per prior
// reader at B, never to the author or jev. A redelivered event adds nothing.
// holder_skipped: a prior reader with an open hold gets no push from pr-to-read,
// and the other prior readers still get theirs.
func TestControl32HeadChange(t *testing.T) {
	st, client := initTestRedis(t)
	ctx := context.Background()
	const S = "control-c32"
	const repo = "nova-tools"
	const prNum = 101
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)

	pipe := client.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2")
	pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")
	pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
	pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
		"head", headA, "author", "rowan", "land_bar", "8", "state", "reading")

	// Prior readers at head A: stella, johnny, emma, rowan, jev
	for _, f := range []string{"stella", "johnny", "emma", "rowan", "jev"} {
		tid := task.ReviewID(repo, prNum, headA, f)
		pipe.HSet(ctx, "s:"+S+":task:"+tid,
			"state", "open", "kind", "review", "repo", repo, "pr", strconv.Itoa(prNum),
			"head", headA, "owner", "", "priority", "5")
		pipe.ZAdd(ctx, "s:"+S+":open:"+f, redis.Z{Score: 5, Member: tid})
		pipe.SAdd(ctx, "s:"+S+":idx:task:open", tid)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	pr := &PRRead{
		Store:    st,
		Sprint:   S,
		Consumer: "test-c32",
		Instance: "test-c32",
		Actor:    "pr-to-read",
	}

	// Emit pr head event A -> B
	eventID, err := client.XAdd(ctx, &redis.XAddArgs{
		Stream: "s:" + S + ":log",
		Values: []any{
			"kind", "pr head", "repo", repo, "pr", strconv.Itoa(prNum),
			"head", headB, "prev", headA, "source", "ls-remote", "at", "1",
		},
	}).Result()
	if err != nil {
		t.Fatal(err)
	}

	if err := pr.Once(ctx); err != nil {
		t.Fatalf("pr.Once: %v", err)
	}

	// Tasks at head A cancelled
	for _, f := range []string{"stella", "johnny", "emma"} {
		tid := task.ReviewID(repo, prNum, headA, f)
		state, err := client.HGet(ctx, "s:"+S+":task:"+tid, "state").Result()
		if err != nil || state != "cancelled" {
			t.Fatalf("task %s state = %q (%v), want 'cancelled'", tid, state, err)
		}
		if score, _ := client.ZScore(ctx, "s:"+S+":open:"+f, tid).Result(); score != 0 {
			t.Fatalf("task %s still in s:%s:open:%s", tid, S, f)
		}
	}

	// New tasks at head B pushed to prior readers (stella, johnny, emma)
	for _, f := range []string{"stella", "johnny", "emma"} {
		tid := task.ReviewID(repo, prNum, headB, f)
		state, err := client.HGet(ctx, "s:"+S+":task:"+tid, "state").Result()
		if err != nil || state != "open" {
			t.Fatalf("new task %s state = %q (%v), want 'open'", tid, state, err)
		}
		if _, err := client.ZScore(ctx, "s:"+S+":open:"+f, tid).Result(); err != nil {
			t.Fatalf("new task %s not in s:%s:open:%s", tid, S, f)
		}
	}

	// Never pushed to author (rowan) or jev
	for _, f := range []string{"rowan", "jev"} {
		tid := task.ReviewID(repo, prNum, headB, f)
		exists, err := client.Exists(ctx, "s:"+S+":task:"+tid).Result()
		if err != nil || exists != 0 {
			t.Fatalf("unexpected task %s for %s at head B exists=%d (%v)", tid, f, exists, err)
		}
	}

	// Redelivered event adds nothing
	logLenBefore, _ := client.XLen(ctx, "s:"+S+":log").Result()
	openStellaBefore, _ := client.ZCard(ctx, "s:"+S+":open:stella").Result()
	// Re-add same event id (or re-run)
	_ = eventID
	if err := pr.Once(ctx); err != nil {
		t.Fatalf("pr.Once redelivered: %v", err)
	}
	openStellaAfter, _ := client.ZCard(ctx, "s:"+S+":open:stella").Result()
	if openStellaBefore != openStellaAfter {
		t.Fatalf("redelivered event changed open:stella count from %d to %d", openStellaBefore, openStellaAfter)
	}
	logLenAfter, _ := client.XLen(ctx, "s:"+S+":log").Result()
	if logLenAfter != logLenBefore {
		t.Fatalf("redelivered event added to log: before %d, after %d", logLenBefore, logLenAfter)
	}

	t.Run("holder_skipped", func(t *testing.T) {
		const pr2 = 102
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, pr2))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr2),
			"head", headA, "author", "rowan", "land_bar", "8", "state", "reading")

		// Prior readers at head A: stella, johnny, emma
		for _, f := range []string{"stella", "johnny", "emma"} {
			tid := task.ReviewID(repo, pr2, headA, f)
			pipe.HSet(ctx, "s:"+S+":task:"+tid,
				"state", "open", "kind", "review", "repo", repo, "pr", strconv.Itoa(pr2),
				"head", headA, "owner", "", "priority", "5")
			pipe.ZAdd(ctx, "s:"+S+":open:"+f, redis.Z{Score: 5, Member: tid})
			pipe.SAdd(ctx, "s:"+S+":idx:task:open", tid)
		}
		// Open hold by johnny
		holdJSON := fmt.Sprintf(`{"holder":"johnny","head":%q,"kind":"scope","reason":"wait","released_by":""}`, headA)
		pipe.HSet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, pr2), "h1", holdJSON)
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}

		// Emit pr head event A -> B for pr2
		_, err := client.XAdd(ctx, &redis.XAddArgs{
			Stream: "s:" + S + ":log",
			Values: []any{
				"kind", "pr head", "repo", repo, "pr", strconv.Itoa(pr2),
				"head", headB, "prev", headA, "source", "ls-remote", "at", "2",
			},
		}).Result()
		if err != nil {
			t.Fatal(err)
		}

		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}

		// stella and emma get their review task pushes at head B
		for _, f := range []string{"stella", "emma"} {
			tid := task.ReviewID(repo, pr2, headB, f)
			state, err := client.HGet(ctx, "s:"+S+":task:"+tid, "state").Result()
			if err != nil || state != "open" {
				t.Fatalf("new task %s state = %q (%v), want 'open'", tid, state, err)
			}
		}

		// johnny holds an open hold: skipped by pr-to-read!
		johnnyTID := task.ReviewID(repo, pr2, headB, "johnny")
		exists, err := client.Exists(ctx, "s:"+S+":task:"+johnnyTID).Result()
		if err != nil || exists != 0 {
			t.Fatalf("johnny review task at head B exists=%d (%v); want skipped", exists, err)
		}
	})
}

// TestControlOpenHoldBlocksLandReady:
//   - blocks: open hold h1 blocks land-ready even with ci OK and reads at the bar.
//   - release: feeding dispositions through ns_test_ingest_disposition leaves h1 open
//     until authorized release, after which the card goes land-ready.
func TestControlOpenHoldBlocksLandReady(t *testing.T) {
	st, client := initTestRedis(t)
	ctx := context.Background()
	const S = "control-hold"
	const repo = "nova-tools"
	const prNum = 101
	const label = "card-1"
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)

	seedBase := func() {
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "sprints", S)
		pipe.HSet(ctx, "s:"+S, "status", "open")
		pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2", "release_reader", "stella")
		pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label),
			"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label)
		pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum),
			"stella@"+headB, "APPROVE 9 https://example.com/1 1")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	pr := &PRRead{
		Store:    st,
		Sprint:   S,
		Consumer: "test-hold",
		Instance: "test-hold",
		Actor:    "pr-to-read",
	}

	t.Run("blocks", func(t *testing.T) {
		seedBase()
		// Open hold h1 by johnny at head A
		holdJSONA := fmt.Sprintf(`{"holder":"johnny","head":%q,"kind":"scope","reason":"wait for me","url":"https://example.com/h1","at":123,"released_by":""}`, headA)
		if err := client.HSet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, prNum), "h1", holdJSONA).Err(); err != nil {
			t.Fatal(err)
		}

		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}

		wantReasonA := fmt.Sprintf("hold h1 open (johnny @%s)", headA[:12])
		if r := pr.Reason(label); r != wantReasonA {
			t.Fatalf("reason = %q, want %q", r, wantReasonA)
		}
		cardState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
		if cardState != "review-ready" {
			t.Fatalf("card state = %q, want 'review-ready'", cardState)
		}
		landable, _ := client.ZScore(ctx, "s:"+S+":landable", fmt.Sprintf("%s#%d", repo, prNum)).Result()
		if landable != 0 {
			t.Fatalf("s:%s:landable contains PR", S)
		}

		// The same holds with the hold at head B
		holdJSONB := fmt.Sprintf(`{"holder":"johnny","head":%q,"kind":"scope","reason":"wait for me","url":"https://example.com/h1","at":123,"released_by":""}`, headB)
		if err := client.HSet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, prNum), "h1", holdJSONB).Err(); err != nil {
			t.Fatal(err)
		}
		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}
		wantReasonB := fmt.Sprintf("hold h1 open (johnny @%s)", headB[:12])
		if r := pr.Reason(label); r != wantReasonB {
			t.Fatalf("reason = %q, want %q", r, wantReasonB)
		}
		cardState, _ = client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
		if cardState != "review-ready" {
			t.Fatalf("card state = %q, want 'review-ready'", cardState)
		}
	})

	t.Run("release", func(t *testing.T) {
		seedBase()
		// Open hold h1 by johnny at head A
		holdJSONA := fmt.Sprintf(`{"holder":"johnny","head":%q,"kind":"scope","reason":"wait for me","url":"https://example.com/h1","at":123,"released_by":""}`, headA)
		if err := client.HSet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, prNum), "h1", holdJSONA).Err(); err != nil {
			t.Fatal(err)
		}
		// johnny is up
		_ = client.HSet(ctx, "friend:johnny:state", "state", "up").Err()
		_ = client.Del(ctx, "friend:johnny:down").Err()

		assertStillBlocked := func(desc string) {
			t.Helper()
			if err := pr.Once(ctx); err != nil {
				t.Fatalf("%s: pr.Once: %v", desc, err)
			}
			h1Val, err := client.HGet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, prNum), "h1").Result()
			if err != nil {
				t.Fatalf("%s: HGet h1: %v", desc, err)
			}
			var h struct {
				ReleasedBy string `json:"released_by"`
			}
			_ = json.Unmarshal([]byte(h1Val), &h)
			if h.ReleasedBy != "" {
				t.Fatalf("%s: h1 released_by = %q; want empty", desc, h.ReleasedBy)
			}
			cardState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
			if cardState != "review-ready" {
				t.Fatalf("%s: card state = %q, want 'review-ready'", desc, cardState)
			}
		}

		// 1. johnny's APPROVE at A (the hold's head)
		if err := ingestDisposition(ctx, client, S, repo, prNum, "johnny", headA, "APPROVE", 9, "url", ""); err != nil {
			t.Fatal(err)
		}
		assertStillBlocked("johnny APPROVE at A")

		// 2. author's (rowan) APPROVE 10 at B
		if err := ingestDisposition(ctx, client, S, repo, prNum, "rowan", headB, "APPROVE", 10, "url", ""); err != nil {
			t.Fatal(err)
		}
		assertStillBlocked("author APPROVE 10 at B")

		// 3. jev's APPROVE 10 at B
		if err := ingestDisposition(ctx, client, S, repo, prNum, "jev", headB, "APPROVE", 10, "url", ""); err != nil {
			t.Fatal(err)
		}
		assertStillBlocked("jev APPROVE 10 at B")

		// 4. stella's APPROVE 9 at B with johnny up
		if err := ingestDisposition(ctx, client, S, repo, prNum, "stella", headB, "APPROVE", 9, "url", ""); err != nil {
			t.Fatal(err)
		}
		assertStillBlocked("stella APPROVE 9 at B with johnny up")

		// 5. release_reader's (stella) line at B with johnny up
		if err := ingestDisposition(ctx, client, S, repo, prNum, "stella", headB, "APPROVE", 9, "url", "johnny"); err != nil {
			t.Fatal(err)
		}
		assertStillBlocked("release_reader line at B with johnny up")

		// Then johnny's APPROVE 9 at B sets released_by=johnny
		if err := ingestDisposition(ctx, client, S, repo, prNum, "johnny", headB, "APPROVE", 9, "url", ""); err != nil {
			t.Fatal(err)
		}
		h1Val, err := client.HGet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, prNum), "h1").Result()
		if err != nil {
			t.Fatal(err)
		}
		var h struct {
			ReleasedBy string `json:"released_by"`
		}
		_ = json.Unmarshal([]byte(h1Val), &h)
		if h.ReleasedBy != "johnny" {
			t.Fatalf("h1 released_by = %q, want 'johnny'", h.ReleasedBy)
		}

		// Next pass makes card land-ready with exactly one land-ready event
		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}
		cardState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
		if cardState != "land-ready" {
			t.Fatalf("card state = %q, want 'land-ready'", cardState)
		}
		logEntries, _ := client.XRange(ctx, "s:"+S+":log", "-", "+").Result()
		landReadyCount := 0
		for _, entry := range logEntries {
			if entry.Values["from"] == "review-ready" && entry.Values["to"] == "land-ready" {
				landReadyCount++
			}
		}
		if landReadyCount != 1 {
			t.Fatalf("land-ready events count = %d, want 1", landReadyCount)
		}

		// Contrast: with friend:johnny:state down past absent_after,
		// release_reader's line at B naming release_for=johnny releases h1 and card goes land-ready
		const pr3 = 103
		const label3 = "card-3"
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, pr3))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr3),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label3),
			"repo", repo, "pr", strconv.Itoa(pr3), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label3)
		pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, pr3),
			"stella@"+headB, "APPROVE 9 https://example.com/1 1")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, pr3), "h1", holdJSONA)
		pipe.HSet(ctx, "friend:johnny:state", "state", "down", "since", strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10))
		pipe.Set(ctx, "friend:johnny:down", "1", 0)
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}

		if err := ingestDisposition(ctx, client, S, repo, pr3, "stella", headB, "APPROVE", 9, "url", "johnny"); err != nil {
			t.Fatal(err)
		}
		h1Pr3Val, _ := client.HGet(ctx, fmt.Sprintf("s:%s:hold:%s:%d", S, repo, pr3), "h1").Result()
		var hPr3 struct {
			ReleasedBy string `json:"released_by"`
		}
		_ = json.Unmarshal([]byte(h1Pr3Val), &hPr3)
		if hPr3.ReleasedBy != "stella" {
			t.Fatalf("h1 on pr3 released_by = %q, want 'stella'", hPr3.ReleasedBy)
		}
		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once on pr3: %v", err)
		}
		c3State, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label3), "state").Result()
		if c3State != "land-ready" {
			t.Fatalf("card-3 state = %q, want 'land-ready'", c3State)
		}
	})
}

// TestPrToReadMakesNoRestCall:
// http.DefaultTransport and a REST stub both fail every request and count calls.
// A full pass with a head change, a CI flip and a land-ready move completes,
// and the count is 0.
func TestPrToReadMakesNoRestCall(t *testing.T) {
	st, client := initTestRedis(t)
	ctx := context.Background()
	const S = "control-norest"
	const label = "card-1"
	const prNum = 101
	headA := strings.Repeat("a", 40)

	bareDir := initBareRepo(t)
	headB := setBareRef(t, bareDir, "refs/pull/101/head", "commit head B")

	var restCalls atomic.Int64
	origTransport := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		restCalls.Add(1)
		return nil, errors.New("unexpected HTTP call: " + req.URL.String())
	})
	t.Cleanup(func() { http.DefaultTransport = origTransport })

	repo := bareDir // bare repo path passed straight through without network
	pipe := client.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.HSet(ctx, "s:"+S+":policy", "repos", repo, "readers", "1", "readers_security", "2")
	pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")
	pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
	pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
		"head", headA, "author", "rowan", "land_bar", "8", "state", "reading")
	pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label),
		"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
		"author", "rowan", "state", "review-ready", "attempt", "1")
	pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label)
	// Prior review task at headA for stella
	tA := task.ReviewID(repo, prNum, headA, "stella")
	pipe.HSet(ctx, "s:"+S+":task:"+tA, "state", "open", "kind", "review", "repo", repo, "pr", strconv.Itoa(prNum), "head", headA)
	pipe.ZAdd(ctx, "s:"+S+":open:stella", redis.Z{Score: 5, Member: tA})
	pipe.SAdd(ctx, "s:"+S+":idx:task:open", tA)
	// CI verdict OK at headB
	pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")
	// Disp at headB: stella APPROVE 9
	pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum),
		"stella@"+headB, "APPROVE 9 https://example.com/1 1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	pr := &PRRead{
		Store:    st,
		Sprint:   S,
		Consumer: "test-norest",
		Instance: "test-norest",
		Actor:    "pr-to-read",
		Remote:   GitRemote,
	}

	if err := pr.Once(ctx); err != nil {
		t.Fatalf("pr.Once: %v", err)
	}

	if n := restCalls.Load(); n != 0 {
		t.Fatalf("REST calls made = %d, want 0", n)
	}

	// Verify head change happened, task at headA was cancelled, and card went land-ready
	tAState, _ := client.HGet(ctx, "s:"+S+":task:"+tA, "state").Result()
	if tAState != "cancelled" {
		t.Fatalf("task %s state = %q, want 'cancelled'", tA, tAState)
	}
	cardState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
	if cardState != "land-ready" {
		t.Fatalf("card state = %q, want 'land-ready'", cardState)
	}
}

// TestConsumeTwoSeatsOneWriter:
// - route_then_once: router holds lease; seat-b's Once returns ErrLeaseHeld; snapshot identical.
// - once_then_route: seat-b holds lease paused in Remote; router returns ErrLeaseHeld before rules.
// - stale_instance: review.lua write functions called under old instance return LEASE <holder>.
func TestConsumeTwoSeatsOneWriter(t *testing.T) {
	st, client := initTestRedis(t)
	ctx := context.Background()
	const S = "control-twoseats"
	const label = "card-1"
	const prNum = 101
	headA := strings.Repeat("a", 40)
	bareDir := initBareRepo(t)
	headB := setBareRef(t, bareDir, "refs/pull/101/head", "commit head B")
	repo := bareDir

	seedState := func() {
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "sprints", S)
		pipe.HSet(ctx, "s:"+S, "status", "open")
		pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2", "repos", repo)
		pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
			"head", headA, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label),
			"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label)
		pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum),
			"stella@"+headB, "APPROVE 9 https://example.com/1 1")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("route_then_once", func(t *testing.T) {
		seedState()

		prRoute := &PRRead{
			Store:    st,
			Sprint:   S,
			Consumer: "route-a",
			Instance: "route-a",
			Actor:    "route",
			Remote:   GitRemote,
		}
		router := &Router{
			Store:    st,
			Sprint:   S,
			Instance: "route-a",
			Host:     "ctl-host",
			Rules:    Rules(nil, nil, nil, prRoute, nil),
			TTL:      6 * time.Second,
			Renew:    2 * time.Second,
		}

		routerCtx, routerCancel := context.WithCancel(ctx)
		defer routerCancel()
		routerErrCh := make(chan error, 1)
		go func() {
			routerErrCh <- router.Run(routerCtx)
		}()

		// Wait on card-1 reaching land-ready and stream drained on route-a
		rtUntil(t, "proc:pr-to-read has instance=route-a and settled", func() bool {
			inst, _ := client.HGet(ctx, "proc:pr-to-read", "instance").Result()
			n, _ := client.HGet(ctx, "proc:pr-to-read", "n").Result()
			landReady := client.SIsMember(ctx, "s:"+S+":idx:card:land-ready", "card-1").Val()
			return inst == "route-a" && landReady && n == "0"
		})

		seatB := &PRRead{
			Store:    st,
			Sprint:   S,
			Consumer: "seat-b",
			Instance: "seat-b",
			Actor:    "pr-to-read",
			Remote:   GitRemote,
		}

		snapBefore := snapshotKeys(t, client, S)
		err := seatB.Once(ctx)
		if !errors.Is(err, ErrLeaseHeld) || !strings.Contains(err.Error(), "route-a") {
			t.Fatalf("seatB.Once err = %v; want ErrLeaseHeld naming route-a", err)
		}
		snapAfter := snapshotKeys(t, client, S)
		if !reflect.DeepEqual(snapBefore, snapAfter) {
			t.Fatalf("snapshot keys differed after seatB.Once call:\nbefore: %v\nafter: %v", snapBefore, snapAfter)
		}

		routerCancel()
		_ = <-routerErrCh

		// Check s:<S>:log holds exactly one pr head event and one land-ready event
		logEntries, err := client.XRange(ctx, "s:"+S+":log", "-", "+").Result()
		if err != nil {
			t.Fatal(err)
		}
		headCount := 0
		landCount := 0
		for _, e := range logEntries {
			if e.Values["kind"] == "pr head" {
				headCount++
			}
			if e.Values["from"] == "review-ready" && e.Values["to"] == "land-ready" {
				landCount++
			}
		}
		if headCount != 1 || landCount != 1 {
			t.Fatalf("log counts: headCount=%d, landCount=%d; want 1 and 1", headCount, landCount)
		}
		procInst, _ := client.HGet(ctx, "proc:pr-to-read", "instance").Result()
		if procInst != "route-a" {
			t.Fatalf("proc:pr-to-read instance = %q, want 'route-a'", procInst)
		}
	})

	t.Run("once_then_route", func(t *testing.T) {
		seedState()
		// Clean lease
		_ = client.Del(ctx, "lease:route:"+S).Err()

		var onceEntered sync.Once
		enteredRemote := make(chan struct{})
		releaseRemote := make(chan struct{})

		seatB := &PRRead{
			Store:    st,
			Sprint:   S,
			Consumer: "seat-b",
			Instance: "seat-b",
			Actor:    "pr-to-read",
			Remote: func(_ context.Context, _ string) (map[int]string, error) {
				onceEntered.Do(func() { close(enteredRemote) })
				<-releaseRemote
				return nil, nil
			},
		}

		onceErrCh := make(chan error, 1)
		go func() {
			onceErrCh <- seatB.Once(ctx)
		}()

		<-enteredRemote // seat-b holds the lease now

		router := &Router{
			Store:    st,
			Sprint:   S,
			Instance: "route-a",
			Host:     "ctl-host",
			Rules:    Rules(nil, nil, nil, &rtFake{}, nil),
			TTL:      6 * time.Second,
		}

		rErr := router.Run(ctx)
		if !errors.Is(rErr, ErrLeaseHeld) || !strings.Contains(rErr.Error(), "seat-b") {
			t.Fatalf("router.Run err = %v, want ErrLeaseHeld naming seat-b", rErr)
		}

		close(releaseRemote)
		if err := <-onceErrCh; err != nil {
			t.Fatalf("seatB.Once: %v", err)
		}

		exists, err := client.Exists(ctx, "lease:route:"+S).Result()
		if err != nil || exists != 0 {
			t.Fatalf("lease:route:%s exists = %d (%v), want 0", S, exists, err)
		}
	})

	t.Run("stale_instance", func(t *testing.T) {
		seedState()
		// lease:route:<S> held by seat-c
		_ = client.HSet(ctx, "lease:route:"+S, "instance", "seat-c", "token", "tok-c", "host", "host", "at", "1").Err()

		snapBefore := snapshotKeys(t, client, S)

		assertLeaseRefused := func(name string, fcall func() ([]any, error)) {
			t.Helper()
			res, err := fcall()
			if err != nil {
				t.Fatalf("%s err = %v", name, err)
			}
			if len(res) < 2 || res[0] != "LEASE" || res[1] != "seat-c" {
				t.Fatalf("%s returned %v, want ['LEASE', 'seat-c']", name, res)
			}
		}

		// 1. ns_pr_head
		assertLeaseRefused("ns_pr_head", func() ([]any, error) {
			return client.FCall(ctx, FunctionPRHead, nil, S, "route-a", repo, "101", headB, "ls-remote").Slice()
		})

		// 2. ns_pr_evaluate
		assertLeaseRefused("ns_pr_evaluate", func() ([]any, error) {
			return client.FCall(ctx, FunctionPREvaluate, nil, S, "route-a", label, repo, "101", headB, "short", "pass-1", "pr-to-read", "reason", "1").Slice()
		})

		// 3. ns_reads_short_delete_ended
		assertLeaseRefused("ns_reads_short_delete_ended", func() ([]any, error) {
			return client.FCall(ctx, FunctionReadsShortDeleteEnded, nil, S, "route-a", repo+"#101:reads-short:"+headB[:12]).Slice()
		})

		// 4. ns_reads_short_delete_left
		assertLeaseRefused("ns_reads_short_delete_left", func() ([]any, error) {
			return client.FCall(ctx, FunctionReadsShortDeleteLeft, nil, S, "route-a", repo+"#101:reads-short:"+headB[:12]).Slice()
		})

		// 5. ns_pr_pass
		assertLeaseRefused("ns_pr_pass", func() ([]any, error) {
			return client.FCall(ctx, FunctionPRPass, nil, S, "route-a", "pr-to-read", "10", "1", "").Slice()
		})

		snapAfter := snapshotKeys(t, client, S)
		if !reflect.DeepEqual(snapBefore, snapAfter) {
			t.Fatalf("snapshot keys differed after stale instance calls:\nbefore: %v\nafter: %v", snapBefore, snapAfter)
		}
	})
}

// TestControlRequiredReads:
//   - multiple: readers=2, land_bar=8. Stella 9 -> 1/2. Johnny 7 (under bar) -> 1/2.
//     Emma 8 at old head -> 1/2. Emma 8 at exact head -> land-ready. Redelivered -> no 2nd event.
//   - author_excluded: readers=1, land_bar=8. Rowan(author), jev, ghost excluded. Card author excluded.
//   - bar_per_pr: 4 PRs: bar 8 lands, bar 10 reads 0/1, no bar MISSING land_bar, no author MISSING author.
//   - security: readers=1, readers_security=2. Security paths vs regular paths. RequiredReads == required().
//   - short_eligible: full 9-step active state lifecycle on s:<S>:unresolved.
func TestControlRequiredReads(t *testing.T) {
	st, client := initTestRedis(t)
	ctx := context.Background()
	const S = "control-reqreads"
	const repo = "nova-tools"
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)

	pr := &PRRead{
		Store:    st,
		Sprint:   S,
		Consumer: "test-reqreads",
		Instance: "test-reqreads",
		Actor:    "pr-to-read",
	}

	pass := func(label string) string {
		t.Helper()
		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}
		return pr.Reason(label)
	}

	t.Run("multiple", func(t *testing.T) {
		const prNum = 201
		const label = "card-201"
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "sprints", S)
		pipe.HSet(ctx, "s:"+S, "status", "open")
		pipe.HSet(ctx, "s:"+S+":policy", "readers", "2", "readers_security", "2")
		pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label),
			"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label)
		pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}

		// 1. stella APPROVE 9 at B -> reads 1/2
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum), "stella@"+headB, "APPROVE 9 url 1").Err()
		if r := pass(label); r != "reads 1/2" {
			t.Fatalf("after stella 9: reason = %q, want 'reads 1/2'", r)
		}

		// 2. johnny APPROVE 7 at B (under the bar) -> still reads 1/2
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum), "johnny@"+headB, "APPROVE 7 url 2").Err()
		if r := pass(label); r != "reads 1/2" {
			t.Fatalf("after johnny 7: reason = %q, want 'reads 1/2'", r)
		}

		// 3. emma APPROVE 8 at A (an old head) -> still reads 1/2
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum), "emma@"+headA, "APPROVE 8 url 3").Err()
		if r := pass(label); r != "reads 1/2" {
			t.Fatalf("after emma at A: reason = %q, want 'reads 1/2'", r)
		}

		// 4. emma APPROVE 8 at B -> makes the card land-ready with exactly one land-ready event
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum), "emma@"+headB, "APPROVE 8 url 4").Err()
		if r := pass(label); r != "reads 2/2" {
			t.Fatalf("after emma 8 at B: reason = %q, want 'reads 2/2'", r)
		}
		cardState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
		if cardState != "land-ready" {
			t.Fatalf("card state = %q, want 'land-ready'", cardState)
		}
		if isMember, err := client.SIsMember(ctx, "s:"+S+":idx:card:land-ready", label).Result(); err != nil || !isMember {
			t.Fatalf("card %s not in s:%s:idx:card:land-ready (%v)", label, S, err)
		}

		// 5. A later johnny APPROVE 10 at B and a redelivered event add no second event
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum), "johnny@"+headB, "APPROVE 10 url 5").Err()
		_ = pass(label)
		logEntries, _ := client.XRange(ctx, "s:"+S+":log", "-", "+").Result()
		landCount := 0
		for _, e := range logEntries {
			if e.Values["id"] == label && e.Values["to"] == "land-ready" {
				landCount++
			}
		}
		if landCount != 1 {
			t.Fatalf("land-ready event count = %d, want 1", landCount)
		}
	})

	t.Run("author_excluded", func(t *testing.T) {
		const prNum = 202
		const label = "card-202"
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "sprints", S)
		pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2")
		pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label),
			"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label)
		pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")

		// Three reads at B: rowan (author), jev, ghost (not in friends)
		pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum),
			"rowan@"+headB, "APPROVE 10 url 1",
			"jev@"+headB, "APPROVE 10 url 2",
			"ghost@"+headB, "APPROVE 10 url 3")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}

		if r := pass(label); r != "reads 0/1" {
			t.Fatalf("reason = %q, want 'reads 0/1'", r)
		}
		cardState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
		if cardState != "review-ready" {
			t.Fatalf("card state = %q, want 'review-ready'", cardState)
		}

		// With readers=2 and stella APPROVE 9 at B added, gives reads 1/2
		_ = client.HSet(ctx, "s:"+S+":policy", "readers", "2").Err()
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum), "stella@"+headB, "APPROVE 9 url 4").Err()
		if r := pass(label); r != "reads 1/2" {
			t.Fatalf("reason = %q, want 'reads 1/2'", r)
		}

		// On a second card whose card author is stella while PR author is rowan, stella's APPROVE 9 does not count
		const labelB = "card-202b"
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, labelB),
			"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
			"author", "stella", "state", "review-ready", "attempt", "1").Err()
		_ = client.SAdd(ctx, "s:"+S+":idx:card:review-ready", labelB).Err()
		if r := pass(labelB); r != "reads 0/2" {
			t.Fatalf("card with author stella: reason = %q, want 'reads 0/2'", r)
		}
	})

	t.Run("bar_per_pr", func(t *testing.T) {
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "sprints", S)
		pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2")
		pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")

		// 4 PRs: 301 (bar 8), 302 (bar 10), 303 (no bar), 304 (no author)
		for _, num := range []int{301, 302, 303, 304} {
			prID := fmt.Sprintf("%s#%d", repo, num)
			lbl := fmt.Sprintf("card-%d", num)
			pipe.SAdd(ctx, "s:"+S+":prs", prID)
			pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, lbl),
				"repo", repo, "pr", strconv.Itoa(num), "paths", "internal/x.go",
				"author", "rowan", "state", "review-ready", "attempt", "1")
			pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", lbl)
			pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")
			pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, num),
				"stella@"+headB, "APPROVE 9 url 1")
		}
		// PR 301: land_bar 8, author rowan
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:301", S, repo), "head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		// PR 302: land_bar 10, author rowan
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:302", S, repo), "head", headB, "author", "rowan", "land_bar", "10", "state", "reading")
		// PR 303: no land_bar, author rowan
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:303", S, repo), "head", headB, "author", "rowan", "state", "reading")
		// PR 304: land_bar 8, no author
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:304", S, repo), "head", headB, "land_bar", "8", "state", "reading")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}

		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}

		// PR 301 -> land-ready
		st301, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:card-301", S), "state").Result()
		if st301 != "land-ready" {
			t.Fatalf("card-301 state = %q, want 'land-ready'", st301)
		}
		// PR 302 -> reads 0/1
		if r := pr.Reason("card-302"); r != "reads 0/1" {
			t.Fatalf("card-302 reason = %q, want 'reads 0/1'", r)
		}
		// PR 303 -> reads MISSING land_bar
		if r := pr.Reason("card-303"); r != "reads MISSING land_bar" {
			t.Fatalf("card-303 reason = %q, want 'reads MISSING land_bar'", r)
		}
		// PR 304 -> reads MISSING author
		if r := pr.Reason("card-304"); r != "reads MISSING author" {
			t.Fatalf("card-304 reason = %q, want 'reads MISSING author'", r)
		}
	})

	t.Run("security", func(t *testing.T) {
		const prSec = 401
		const prNonSec = 402
		policy := map[string]string{
			"readers":          "1",
			"readers_security": "2",
			"security_paths":   "internal/secrets/,.github/",
		}
		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "sprints", S)
		pipe.HSet(ctx, "s:"+S+":policy", policy)
		pipe.SAdd(ctx, "friends", "stella", "johnny", "emma", "rowan")

		cardSec := map[string]string{
			"repo": repo, "pr": strconv.Itoa(prSec), "paths": "internal/secrets/x.go",
			"author": "rowan", "state": "review-ready", "attempt": "1",
		}
		cardNonSec := map[string]string{
			"repo": repo, "pr": strconv.Itoa(prNonSec), "paths": "internal/table/x.go",
			"author": "rowan", "state": "review-ready", "attempt": "1",
		}

		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prSec), fmt.Sprintf("%s#%d", repo, prNonSec))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prSec), "head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNonSec), "head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:card-%d", S, prSec), cardSec)
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:card-%d", S, prNonSec), cardNonSec)
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", fmt.Sprintf("card-%d", prSec), fmt.Sprintf("card-%d", prNonSec))
		pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")

		// Both get stella APPROVE 9
		pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prSec), "stella@"+headB, "APPROVE 9 url 1")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNonSec), "stella@"+headB, "APPROVE 9 url 2")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}

		// Check RequiredReads vs readerCensus.required
		ok := &OkFriend{Store: st, Sprint: S}
		census, err := ok.census(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n1, n2 := RequiredReads(policy, cardSec), census.required(cardSec); n1 != n2 || n1 != 2 {
			t.Fatalf("cardSec required: RequiredReads=%d census.required=%d, want 2", n1, n2)
		}
		if n1, n2 := RequiredReads(policy, cardNonSec), census.required(cardNonSec); n1 != n2 || n1 != 1 {
			t.Fatalf("cardNonSec required: RequiredReads=%d census.required=%d, want 1", n1, n2)
		}

		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}

		// cardSec gives reads 1/2
		if r := pr.Reason(fmt.Sprintf("card-%d", prSec)); r != "reads 1/2" {
			t.Fatalf("cardSec reason = %q, want 'reads 1/2'", r)
		}
		// cardNonSec lands at reads 1/1
		if r := pr.Reason(fmt.Sprintf("card-%d", prNonSec)); r != "reads 1/1" {
			t.Fatalf("cardNonSec reason = %q, want 'reads 1/1'", r)
		}
		nonSecState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:card-%d", S, prNonSec), "state").Result()
		if nonSecState != "land-ready" {
			t.Fatalf("cardNonSec state = %q, want 'land-ready'", nonSecState)
		}

		// johnny APPROVE 9 on cardSec makes it land-ready
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prSec), "johnny@"+headB, "APPROVE 9 url 3").Err()
		if err := pr.Once(ctx); err != nil {
			t.Fatalf("pr.Once: %v", err)
		}
		secState, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:card-%d", S, prSec), "state").Result()
		if secState != "land-ready" {
			t.Fatalf("cardSec state = %q, want 'land-ready'", secState)
		}
	})

	t.Run("short_eligible", func(t *testing.T) {
		const S = "control-short-el"
		pr := &PRRead{
			Store:    st,
			Sprint:   S,
			Consumer: "test-short",
			Instance: "test-short",
			Actor:    "pr-to-read",
		}
		pass := func(label string) string {
			t.Helper()
			if err := pr.Once(ctx); err != nil {
				t.Fatalf("pr.Once: %v", err)
			}
			return pr.Reason(label)
		}
		const prNum = 501
		const label = "card-501"
		F := fmt.Sprintf("%s#%d:reads-short:%s", repo, prNum, headB[:12])
		headC := strings.Repeat("c", 40)
		headX := strings.Repeat("x", 40)

		pipe := client.TxPipeline()
		pipe.SAdd(ctx, "sprints", S)
		pipe.HSet(ctx, "s:"+S, "status", "open")
		pipe.HSet(ctx, "s:"+S+":policy", "readers", "2", "readers_security", "2")
		pipe.Del(ctx, "friends")
		pipe.SAdd(ctx, "friends", "rowan", "stella") // eligible = {stella} -> 1 < 2
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, prNum))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, prNum),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label),
			"repo", repo, "pr", strconv.Itoa(prNum), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label)
		pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")
		pipe.Del(ctx, "s:"+S+":unresolved")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}

		// Step 1: friends={rowan, stella}, readers=2 give reads 0/2; eligible 1 < 2. F exists, value is event id, no land-ready.
		if r := pass(label); r != "reads 0/2; eligible 1 < 2" {
			t.Fatalf("step 1: reason = %q, want 'reads 0/2; eligible 1 < 2'", r)
		}
		u1, err := client.HGetAll(ctx, "s:"+S+":unresolved").Result()
		if err != nil || u1[F] == "" {
			t.Fatalf("step 1: unresolved = %v (%v); want field %s present", u1, err, F)
		}
		valF := u1[F]

		// Step 2: repeat pass leaves s:<S>:unresolved byte-identical: F keeps the first value, no second field.
		_ = pass(label)
		u2, _ := client.HGetAll(ctx, "s:"+S+":unresolved").Result()
		if !reflect.DeepEqual(u1, u2) || u2[F] != valF {
			t.Fatalf("step 2: unresolved differed from u1:\nu1=%v\nu2=%v", u1, u2)
		}

		// Step 3: SADD emma. Next pass gives reads 0/2 with no eligible suffix, and F is gone (HEXISTS 0).
		// Still no land-ready. One more pass leaves byte-identical.
		_ = client.SAdd(ctx, "friends", "emma").Err()
		if r := pass(label); r != "reads 0/2" {
			t.Fatalf("step 3: reason = %q, want 'reads 0/2'", r)
		}
		hexists, _ := client.HExists(ctx, "s:"+S+":unresolved", F).Result()
		if hexists {
			t.Fatalf("step 3: field %s still exists in unresolved", F)
		}
		u3, _ := client.HGetAll(ctx, "s:"+S+":unresolved").Result()
		_ = pass(label)
		u3Repeat, _ := client.HGetAll(ctx, "s:"+S+":unresolved").Result()
		if !reflect.DeepEqual(u3, u3Repeat) {
			t.Fatalf("step 3 repeat differed:\nu3=%v\nu3Repeat=%v", u3, u3Repeat)
		}

		// Step 4: SREM emma. Next pass writes F again, only reads-short field. SADD emma. Next pass deletes F again.
		_ = client.SRem(ctx, "friends", "emma").Err()
		if r := pass(label); r != "reads 0/2; eligible 1 < 2" {
			t.Fatalf("step 4 SREM: reason = %q, want 'reads 0/2; eligible 1 < 2'", r)
		}
		u4, _ := client.HGetAll(ctx, "s:"+S+":unresolved").Result()
		if u4[F] == "" || len(u4) != 1 {
			t.Fatalf("step 4 SREM: unresolved = %v, want only %s", u4, F)
		}
		_ = client.SAdd(ctx, "friends", "emma").Err()
		_ = pass(label)
		hex4, _ := client.HExists(ctx, "s:"+S+":unresolved", F).Result()
		if hex4 {
			t.Fatalf("step 4 SADD: field %s still exists in unresolved", F)
		}

		// Step 5: Stale record: HSET <repo>#<n>:reads-short:<X12> by hand.
		// stella and emma APPROVE 9 at B. Next pass makes card land-ready (1 event).
		// Reason lines never name X, and F is absent.
		fieldX := fmt.Sprintf("%s#%d:reads-short:%s", repo, prNum, headX[:12])
		_ = client.HSet(ctx, "s:"+S+":unresolved", fieldX, "event-x").Err()
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:disp:%s:%d", S, repo, prNum),
			"stella@"+headB, "APPROVE 9 url 1",
			"emma@"+headB, "APPROVE 9 url 2").Err()
		r5 := pass(label)
		if strings.Contains(r5, headX[:12]) {
			t.Fatalf("step 5: reason %q names X", r5)
		}
		c5State, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label), "state").Result()
		if c5State != "land-ready" {
			t.Fatalf("step 5: card state = %q, want 'land-ready'", c5State)
		}
		if hex, _ := client.HExists(ctx, "s:"+S+":unresolved", F).Result(); hex {
			t.Fatalf("step 5: field %s present in unresolved", F)
		}

		// Step 6: Second card: friends={rowan} and readers=1 give reason eligible 0 < 1, field written at head.
		const pr6 = 502
		const label6 = "card-502"
		F6 := fmt.Sprintf("%s#%d:reads-short:%s", repo, pr6, headB[:12])
		pipe = client.TxPipeline()
		pipe.Del(ctx, "friends")
		pipe.SAdd(ctx, "friends", "rowan") // eligible = 0 < 1
		pipe.HSet(ctx, "s:"+S+":policy", "readers", "1")
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, pr6))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr6),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label6),
			"repo", repo, "pr", strconv.Itoa(pr6), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label6)
		pipe.HSet(ctx, "ci:"+repo+":"+headB, "verdict", "OK")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if r := pass(label6); r != "reads 0/1; eligible 0 < 1" {
			t.Fatalf("step 6: reason = %q, want 'reads 0/1; eligible 0 < 1'", r)
		}
		if hex, _ := client.HExists(ctx, "s:"+S+":unresolved", F6).Result(); !hex {
			t.Fatalf("step 6: field %s not written to unresolved", F6)
		}

		// Step 7: Third card, short at head A with field at A: ns_pr_head moves it to head C.
		// After next pass, field at A is gone and field at C exists, the PR's only reads-short field.
		const pr7 = 503
		const label7 = "card-503"
		FA7 := fmt.Sprintf("%s#%d:reads-short:%s", repo, pr7, headA[:12])
		FC7 := fmt.Sprintf("%s#%d:reads-short:%s", repo, pr7, headC[:12])
		pipe = client.TxPipeline()
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, pr7))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr7),
			"head", headA, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label7),
			"repo", repo, "pr", strconv.Itoa(pr7), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label7)
		pipe.HSet(ctx, "ci:"+repo+":"+headA, "verdict", "OK")
		pipe.HSet(ctx, "ci:"+repo+":"+headC, "verdict", "OK")
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		_ = pass(label7) // writes FA7
		if hex, _ := client.HExists(ctx, "s:"+S+":unresolved", FA7).Result(); !hex {
			t.Fatalf("step 7: FA7 not written")
		}
		// Move head to C via ns_pr_head under held lease
		inst, _ := NewInstance()
		token, _ := randomHex(16)
		_ = client.FCall(ctx, FunctionRouteLeaseTake, nil, S, inst, token, "ctl", "6000").Err()
		_ = client.FCall(ctx, FunctionPRHead, nil, S, inst, repo, strconv.Itoa(pr7), headC, "test").Err()
		_ = client.FCall(ctx, FunctionRouteLeaseRelease, nil, S, inst, token).Err()
		_ = pass(label7)
		if hexA, _ := client.HExists(ctx, "s:"+S+":unresolved", FA7).Result(); hexA {
			t.Fatalf("step 7: FA7 still present in unresolved")
		}
		if hexC, _ := client.HExists(ctx, "s:"+S+":unresolved", FC7).Result(); !hexC {
			t.Fatalf("step 7: FC7 missing from unresolved")
		}

		// Step 8: Fourth card, short with its field at its head: HSET its PR record state to closed.
		// The next pass deletes the field and does not move the card.
		const pr8 = 504
		const label8 = "card-504"
		F8 := fmt.Sprintf("%s#%d:reads-short:%s", repo, pr8, headB[:12])
		pipe = client.TxPipeline()
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, pr8))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr8),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label8),
			"repo", repo, "pr", strconv.Itoa(pr8), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label8)
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		_ = pass(label8) // writes F8
		if hex, _ := client.HExists(ctx, "s:"+S+":unresolved", F8).Result(); !hex {
			t.Fatalf("step 8: F8 not written")
		}
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr8), "state", "closed").Err()
		_ = pass(label8)
		if hex, _ := client.HExists(ctx, "s:"+S+":unresolved", F8).Result(); hex {
			t.Fatalf("step 8: F8 not deleted after PR closed")
		}
		c8State, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label8), "state").Result()
		if c8State != "review-ready" {
			t.Fatalf("step 8: card state = %q, want 'review-ready'", c8State)
		}

		// Step 9: Fifth card, short with field F5 at its head, PR record state reading:
		// HSET the card state to harvested, leaving s:<S>:idx:card:review-ready as it is.
		// Next pass deletes F5 (HEXISTS 0), writes no land-ready event, leaves card state harvested
		// and PR record reading. One more pass leaves s:<S>:unresolved byte-identical.
		const pr9 = 505
		const label9 = "card-505"
		F5 := fmt.Sprintf("%s#%d:reads-short:%s", repo, pr9, headB[:12])
		pipe = client.TxPipeline()
		pipe.SAdd(ctx, "s:"+S+":prs", fmt.Sprintf("%s#%d", repo, pr9))
		pipe.HSet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr9),
			"head", headB, "author", "rowan", "land_bar", "8", "state", "reading")
		pipe.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label9),
			"repo", repo, "pr", strconv.Itoa(pr9), "paths", "internal/x.go",
			"author", "rowan", "state", "review-ready", "attempt", "1")
		pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", label9)
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		_ = pass(label9) // writes F5
		if hex, _ := client.HExists(ctx, "s:"+S+":unresolved", F5).Result(); !hex {
			t.Fatalf("step 9: F5 not written")
		}
		_ = client.HSet(ctx, fmt.Sprintf("s:%s:card:%s", S, label9), "state", "harvested").Err()
		_ = pass(label9)
		if hex, _ := client.HExists(ctx, "s:"+S+":unresolved", F5).Result(); hex {
			t.Fatalf("step 9: F5 not deleted after card moved to harvested")
		}
		c9State, _ := client.HGet(ctx, fmt.Sprintf("s:%s:card:%s", S, label9), "state").Result()
		if c9State != "harvested" {
			t.Fatalf("step 9: card state = %q, want 'harvested'", c9State)
		}
		pr9State, _ := client.HGet(ctx, fmt.Sprintf("s:%s:pr:%s:%d", S, repo, pr9), "state").Result()
		if pr9State != "reading" {
			t.Fatalf("step 9: PR state = %q, want 'reading'", pr9State)
		}
		u9, _ := client.HGetAll(ctx, "s:"+S+":unresolved").Result()
		_ = pass(label9)
		u9Repeat, _ := client.HGetAll(ctx, "s:"+S+":unresolved").Result()
		if !reflect.DeepEqual(u9, u9Repeat) {
			t.Fatalf("step 9 repeat differed:\nu9=%v\nu9Repeat=%v", u9, u9Repeat)
		}
	})
}
