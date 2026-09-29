package read_test

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func registerTestFCALL(mr *miniredis.Miniredis) {
	handler := func(c *server.Peer, cmd string, args []string) {
		if len(args) < 2 {
			c.WriteError("ERR wrong number of arguments for 'fcall' command")
			return
		}
		fn := strings.ToLower(args[0])
		fnArgs := args[2:]

		switch fn {
		case "ns_tcard_push":
			if len(fnArgs) < 4 {
				c.WriteError("ERR wrong number of arguments for ns_tcard_push")
				return
			}
			id, where, by, why := fnArgs[0], fnArgs[1], fnArgs[2], fnArgs[3]
			key := "task:" + id
			if mr.Exists(key) {
				c.WriteBulk("REFUSED EXISTS task:" + id)
				return
			}

			mr.HSet(key, "id", id)
			mr.HSet(key, "where", where)
			mr.HSet(key, "where_ok", "-")
			mr.HSet(key, "by", by)
			mr.HSet(key, "why", why)

			var stream, friend string
			var score float64
			for i := 4; i+1 < len(fnArgs); i += 2 {
				k, v := fnArgs[i], fnArgs[i+1]
				mr.HSet(key, k, v)
				switch k {
				case "stream":
					stream = v
				case "friend":
					friend = v
				case "consumer":
					if friend == "" {
						friend = strings.TrimPrefix(v, "friend:")
					}
				case "created_at":
					if n, err := strconv.ParseFloat(v, 64); err == nil {
						score = n
					}
				}
			}
			if score <= 0 {
				score = float64(time.Now().UnixMilli())
				mr.HSet(key, "created_at", fmt.Sprint(int64(score)))
			}

			if stream != "" {
				_, _ = mr.ZAdd(ws.KeyAt(0, stream, where), score, id)
			}
			if friend != "" {
				_, _ = mr.ZAdd(ws.ConsumerKeyAt(0, "friend:"+friend, where), score, id)
			}
			c.WriteBulk("PUSHED " + where + " -")

		case "ns_tcard_move":
			if len(fnArgs) < 4 {
				c.WriteError("ERR wrong number of arguments for ns_tcard_move")
				return
			}
			id, to, by, why := fnArgs[0], fnArgs[1], fnArgs[2], fnArgs[3]
			key := "task:" + id
			from := mr.HGet(key, "where")
			stream := mr.HGet(key, "stream")
			friend := mr.HGet(key, "friend")

			mr.HSet(key, "where", to)
			mr.HSet(key, "by", by)
			mr.HSet(key, "why", why)

			for i := 4; i+1 < len(fnArgs); i += 2 {
				k, v := fnArgs[i], fnArgs[i+1]
				mr.HSet(key, k, v)
				if k == "as" && friend == "" {
					friend = v
					mr.HSet(key, "friend", friend)
				}
			}

			score := float64(time.Now().UnixMilli())
			if ca := mr.HGet(key, "created_at"); ca != "" {
				if n, err := strconv.ParseFloat(ca, 64); err == nil && n > 0 {
					score = n
				}
			}

			if from != to {
				if stream != "" && from != "" {
					_, _ = mr.ZRem(ws.KeyAt(0, stream, from), id)
				}
				if stream != "" {
					_, _ = mr.ZAdd(ws.KeyAt(0, stream, to), score, id)
				}
				if friend != "" && from != "" {
					_, _ = mr.ZRem(ws.ConsumerKeyAt(0, "friend:"+friend, from), id)
				}
				if friend != "" {
					_, _ = mr.ZAdd(ws.ConsumerKeyAt(0, "friend:"+friend, to), score, id)
				}
				c.WriteBulk("MOVED " + from + " " + to)
			} else {
				c.WriteBulk("SAME " + to)
			}

		case "ns_tcard_beat":
			if len(fnArgs) < 1 {
				c.WriteError("ERR wrong number of arguments for ns_tcard_beat")
				return
			}
			id := fnArgs[0]
			nowMs := strconv.FormatInt(time.Now().UnixMilli(), 10)
			mr.HSet("task:"+id, "beat_at", nowMs)
			c.WriteBulk("BEAT " + strconv.FormatInt(time.Now().UnixMilli()+60000, 10))

		case "ns_tcard_done":
			if len(fnArgs) < 2 {
				c.WriteError("ERR wrong number of arguments for ns_tcard_done")
				return
			}
			id, by := fnArgs[0], fnArgs[1]
			key := "task:" + id
			from := mr.HGet(key, "where")
			stream := mr.HGet(key, "stream")
			friend := mr.HGet(key, "friend")

			mr.HSet(key, "where", ws.Done)
			mr.HSet(key, "where_ok", "ok")
			mr.HSet(key, "by", by)

			score := float64(time.Now().UnixMilli())
			if ca := mr.HGet(key, "created_at"); ca != "" {
				if n, err := strconv.ParseFloat(ca, 64); err == nil && n > 0 {
					score = n
				}
			}

			if from != "" && stream != "" {
				_, _ = mr.ZRem(ws.KeyAt(0, stream, from), id)
				_, _ = mr.ZAdd(ws.KeyAt(0, stream, ws.Done), score, id)
			}
			if from != "" && friend != "" {
				_, _ = mr.ZRem(ws.ConsumerKeyAt(0, "friend:"+friend, from), id)
			}
			c.WriteBulk("MOVED " + from + " done")

		default:
			c.WriteError("ERR unsupported fcall: " + fn)
		}
	}

	_ = mr.Server().Register("FCALL", handler)
	_ = mr.Server().Register("fcall", handler)
}

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	registerTestFCALL(mr)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return mr, c
}

func TestExtractFindings(t *testing.T) {
	t.Parallel()

	// 1. Single inline finding
	line1 := "SCORE who=emma head=abcdef123456 score=6/10 gates=ci:ok,base:ok,scope:ok: the test does not fail without the fix"
	score, findings, who, head := read.ExtractFindings(line1)
	if score != 6 {
		t.Errorf("score = %d, want 6", score)
	}
	if who != "emma" {
		t.Errorf("who = %q, want emma", who)
	}
	if head != "abcdef123456" {
		t.Errorf("head = %q, want abcdef123456", head)
	}
	if len(findings) != 1 || findings[0] != "the test does not fail without the fix" {
		t.Errorf("findings = %v, want ['the test does not fail without the fix']", findings)
	}

	// 2. Multiline numbered findings
	line2 := "SCORE who=stella head=12345678 score=5/10 gates=ci:ok\n1. unit test fails without the change\n2. missing error handling in writer"
	score, findings, who, head = read.ExtractFindings(line2)
	if score != 5 {
		t.Errorf("score = %d, want 5", score)
	}
	if who != "stella" || head != "12345678" {
		t.Errorf("who=%q head=%q", who, head)
	}
	if len(findings) != 2 || findings[0] != "unit test fails without the change" || findings[1] != "missing error handling in writer" {
		t.Errorf("findings = %v", findings)
	}

	// 3. Bullet points in HOLD
	line3 := "HOLD who=johnny head=87654321 score=4/10 gates=ci:red\n- leaked goroutine\n* outside paths touched"
	score, findings, who, head = read.ExtractFindings(line3)
	if score != 4 {
		t.Errorf("score = %d, want 4", score)
	}
	if len(findings) != 2 || findings[0] != "leaked goroutine" || findings[1] != "outside paths touched" {
		t.Errorf("findings = %v", findings)
	}

	// 4. Score 10/10
	line4 := "SCORE who=emma head=fedcba98 score=10/10 gates=ci:ok,base:ok,scope:ok"
	score, findings, who, head = read.ExtractFindings(line4)
	if score != 10 {
		t.Errorf("score = %d, want 10", score)
	}
	if len(findings) != 0 {
		t.Errorf("findings for 10/10 = %v, want empty", findings)
	}

	// 5. Sub-8 without finding text generates fallback finding
	line5 := "SCORE who=emma head=abcdef123456 score=7/10 gates=ci:ok"
	score, findings, _, _ = read.ExtractFindings(line5)
	if score != 7 {
		t.Errorf("score = %d, want 7", score)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "sub-8 score (7/10)") {
		t.Errorf("findings = %v", findings)
	}

	// 6. Non-score lines
	line6 := "CLOSE who=jev head=12345678 landed: in dev"
	score, _, _, _ = read.ExtractFindings(line6)
	if score != -1 {
		t.Errorf("expected score -1 for non-score line, got %d", score)
	}
}

func TestDutyCardLifecycle(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	stream := "friends"
	actor := "coordinator"
	now := time.UnixMilli(1700000000000)

	// Ensure duty card
	id, err := read.EnsureDutyCard(ctx, c, stream, actor, 0, now)
	if err != nil {
		t.Fatalf("EnsureDutyCard failed: %v", err)
	}
	if id != "duty:review-loop:friends" {
		t.Errorf("id = %q, want duty:review-loop:friends", id)
	}

	// Verify hash fields
	h := c.HGetAll(ctx, "task:"+id).Val()
	if h["kind"] != "duty" || h["where"] != ws.Working || h["stream"] != stream || h["actor"] != actor {
		t.Errorf("duty card hash = %v", h)
	}

	// Verify presence in ws:friends:working
	score, err := c.ZScore(ctx, ws.KeyAt(0, stream, ws.Working), id).Result()
	if err != nil || score != float64(now.UnixMilli()) {
		t.Errorf("zscore = %v, %v, want %v", score, err, float64(now.UnixMilli()))
	}

	// Touch duty card
	now2 := now.Add(5 * time.Second)
	if err := read.TouchDutyCard(ctx, c, stream, now2); err != nil {
		t.Fatalf("TouchDutyCard failed: %v", err)
	}
	if val := c.HGet(ctx, "task:"+id, "beat_at").Val(); val != fmt.Sprint(now2.UnixMilli()) {
		t.Errorf("beat_at = %q, want %d", val, now2.UnixMilli())
	}

	// Clear duty card
	if err := read.ClearDutyCard(ctx, c, stream, 0); err != nil {
		t.Fatalf("ClearDutyCard failed: %v", err)
	}
	if _, err := c.ZScore(ctx, ws.KeyAt(0, stream, ws.Working), id).Result(); err != redis.Nil {
		t.Errorf("duty card should be removed from working, got err: %v", err)
	}
	if val := c.HGet(ctx, "task:"+id, "where").Val(); val != ws.Done {
		t.Errorf("duty card where = %q, want done", val)
	}
}

func TestReviewLoopPassSubEightCutsFixCardsBehindOriginal(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	stream := "friends"
	now := time.UnixMilli(1700000000000)
	cardID := "card-test-100"
	head := "abc12345678"
	repo := "nova-tools"
	pr := "42"
	author := "friend:rowan"

	// Place original card in ws:friends:review with score 1000
	c.ZAdd(ctx, ws.KeyAt(0, stream, ws.Review), redis.Z{Score: 1000, Member: cardID})
	c.HSet(ctx, "task:"+cardID, map[string]any{
		"id":         cardID,
		"stream":     stream,
		"where":      ws.Review,
		"repo":       repo,
		"pr":         pr,
		"head":       head,
		"consumer":   author,
		"paths":      "internal/x",
		"done_when":  "make test",
		"created_at": "1000",
	})

	// Post a sub-8 score line on PR #42
	line := "SCORE who=emma head=" + head + " score=6/10 gates=ci:ok,base:ok,scope:ok: missing unit test for timeout"
	c.RPush(ctx, "pr:nova-tools:42:lines", line)
	c.HSet(ctx, "pr:nova-tools:42", "head", head, "stream", stream)

	// Run Pass
	res, err := read.Pass(ctx, c, read.LoopOptions{
		Stream: stream,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Pass failed: %v", err)
	}

	if res.PRs != 1 {
		t.Errorf("res.PRs = %d, want 1", res.PRs)
	}
	if res.Scores != 1 {
		t.Errorf("res.Scores = %d, want 1", res.Scores)
	}
	if len(res.Fixes) != 1 {
		t.Fatalf("len(res.Fixes) = %d, want 1", len(res.Fixes))
	}

	fix := res.Fixes[0]
	if fix.Primary != cardID || fix.PR != pr || fix.Repo != repo || fix.Score != 6 {
		t.Errorf("fix card summary = %+v", fix)
	}
	if fix.Finding != "missing unit test for timeout" {
		t.Errorf("fix finding = %q", fix.Finding)
	}

	// Verify fix card in Redis
	fixRec := c.HGetAll(ctx, "task:"+fix.ID).Val()
	if fixRec["kind"] != "fix" || fixRec["primary"] != cardID || fixRec["consumer"] != author || fixRec["where"] != ws.Ready {
		t.Errorf("fix card record in Redis = %v", fixRec)
	}

	// Verify fix card dealt BEHIND original card:
	// original card score was 1000, fix card score must be > 1000
	readyScore, err := c.ZScore(ctx, ws.KeyAt(0, stream, ws.Ready), fix.ID).Result()
	if err != nil {
		t.Fatalf("fix card not in ws:%s:ready: %v", stream, err)
	}
	if readyScore <= 1000 {
		t.Errorf("readyScore = %f, want > 1000 (behind original)", readyScore)
	}

	// Verify fix card also in consumer's ready queue
	consumerScore, err := c.ZScore(ctx, ws.ConsumerKeyAt(0, author, ws.Ready), fix.ID).Result()
	if err != nil || consumerScore != readyScore {
		t.Errorf("consumer ready score = %f, %v, want %f", consumerScore, err, readyScore)
	}

	// Verify primary card has copy pointing to fix card
	if copyField := c.HGet(ctx, "task:"+cardID, "copy").Val(); copyField != fix.ID {
		t.Errorf("task:%s copy = %q, want %q", cardID, copyField, fix.ID)
	}

	// Second pass: idempotent, does not duplicate fix card
	res2, err := read.Pass(ctx, c, read.LoopOptions{
		Stream: stream,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("second Pass failed: %v", err)
	}
	if len(res2.Fixes) != 0 {
		t.Errorf("expected 0 new fixes on repeat, got %d", len(res2.Fixes))
	}
}

func TestReviewLoopPassMultipleFindings(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	stream := "build"
	now := time.UnixMilli(1700000000000)
	cardID := "card-build-200"
	head := "9876543210ab"
	repo := "nova-tools"
	pr := "50"

	c.ZAdd(ctx, ws.KeyAt(0, stream, ws.Review), redis.Z{Score: 2000, Member: cardID})
	c.HSet(ctx, "task:"+cardID, map[string]any{
		"id":        cardID,
		"stream":    stream,
		"where":     ws.Review,
		"repo":      repo,
		"pr":        pr,
		"head":      head,
		"paths":     "cmd/",
		"done_when": "go test ./...",
	})

	// Multiline findings
	line := "SCORE who=stella head=" + head + " score=5/10 gates=ci:ok\n1. add cli flag validation\n2. check exit code on failure"
	c.RPush(ctx, "pr:nova-tools:50:lines", line)
	c.HSet(ctx, "pr:nova-tools:50", "head", head, "stream", stream)

	res, err := read.Pass(ctx, c, read.LoopOptions{
		Stream: stream,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Pass failed: %v", err)
	}

	if len(res.Fixes) != 2 {
		t.Fatalf("len(res.Fixes) = %d, want 2 (one per finding)", len(res.Fixes))
	}
	if res.Fixes[0].Finding != "add cli flag validation" {
		t.Errorf("fix[0] finding = %q", res.Fixes[0].Finding)
	}
	if res.Fixes[1].Finding != "check exit code on failure" {
		t.Errorf("fix[1] finding = %q", res.Fixes[1].Finding)
	}

	// Verify both in ready queue with increasing scores (dealt behind original)
	score1, _ := c.ZScore(ctx, ws.KeyAt(0, stream, ws.Ready), res.Fixes[0].ID).Result()
	score2, _ := c.ZScore(ctx, ws.KeyAt(0, stream, ws.Ready), res.Fixes[1].ID).Result()
	if score1 <= 2000 || score2 <= score1 {
		t.Errorf("scores = (%f, %f), want 2000 < score1 < score2", score1, score2)
	}
}

func TestReviewLoopStopsWhenScoreReachesTen(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	stream := "friends"
	cardID := "card-ten-1"
	c.ZAdd(ctx, ws.KeyAt(0, stream, ws.Review), redis.Z{Score: 1000, Member: cardID})
	c.HSet(ctx, "task:"+cardID, map[string]any{
		"id":     cardID,
		"stream": stream,
		"where":  ws.Review,
		"repo":   "nova-tools",
		"pr":     "77",
		"head":   "headsha10",
	})

	// Perfect 10/10 score
	line := "SCORE who=emma head=headsha10 score=10/10 gates=ci:ok,base:ok,scope:ok"
	c.RPush(ctx, "pr:nova-tools:77:lines", line)
	c.HSet(ctx, "pr:nova-tools:77", "head", "headsha10")

	res, err := read.Pass(ctx, c, read.LoopOptions{Stream: stream})
	if err != nil {
		t.Fatalf("Pass failed: %v", err)
	}
	if len(res.Fixes) != 0 {
		t.Errorf("expected 0 fix cards for score 10/10, got %d", len(res.Fixes))
	}
}

func TestReviewLoopStopsWhenCoordinatorPostsVerdict(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	stream := "friends"
	cardID := "card-verdict-1"
	c.ZAdd(ctx, ws.KeyAt(0, stream, ws.Review), redis.Z{Score: 1000, Member: cardID})
	c.HSet(ctx, "task:"+cardID, map[string]any{
		"id":      cardID,
		"stream":  stream,
		"where":   ws.Review,
		"repo":    "nova-tools",
		"pr":      "88",
		"head":    "headsha88",
		"verdict": "drop", // coordinator posted verdict
		"review":  "REVIEW verdict=drop why=re-architected",
	})

	// Sub-8 score
	line := "SCORE who=emma head=headsha88 score=5/10 gates=ci:ok: architecture rejected"
	c.RPush(ctx, "pr:nova-tools:88:lines", line)
	c.HSet(ctx, "pr:nova-tools:88", "head", "headsha88")

	res, err := read.Pass(ctx, c, read.LoopOptions{Stream: stream})
	if err != nil {
		t.Fatalf("Pass failed: %v", err)
	}
	if len(res.Fixes) != 0 {
		t.Errorf("expected 0 fix cards when coordinator verdict is posted, got %d", len(res.Fixes))
	}
}

func TestRunLoopOnePassAndReceipt(t *testing.T) {
	t.Parallel()
	_, c := newTestRedis(t)
	ctx := context.Background()

	stream := "friends"
	cardID := "card-run-1"
	head := "head1122"
	c.ZAdd(ctx, ws.KeyAt(0, stream, ws.Review), redis.Z{Score: 1000, Member: cardID})
	c.HSet(ctx, "task:"+cardID, map[string]any{
		"id":     cardID,
		"stream": stream,
		"where":  ws.Review,
		"repo":   "nova-tools",
		"pr":     "99",
		"head":   head,
	})
	c.RPush(ctx, "pr:nova-tools:99:lines", "SCORE who=emma head="+head+" score=6/10 gates=ci:ok: fix test timeout")

	var out, errOut bytes.Buffer
	code, err := read.RunLoop(ctx, c, read.LoopOptions{
		Stream: stream,
		Daemon: false,
	}, &out, &errOut)
	if err != nil || code != 0 {
		t.Fatalf("RunLoop failed: code=%d err=%v errOut=%s", code, err, errOut.String())
	}

	stdout := out.String()
	if !strings.Contains(stdout, "FIX CARD id=") || !strings.Contains(stdout, "finding=\"fix test timeout\"") {
		t.Errorf("stdout missing FIX CARD line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "REVIEW LOOP stream=friends duty=duty:review-loop:friends prs=1 scores=1 fixes=1") {
		t.Errorf("stdout missing REVIEW LOOP receipt:\n%s", stdout)
	}
}
