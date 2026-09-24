package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

type tripCounter struct {
	trips int
}

func (c *tripCounter) DialHook(next redis.DialHook) redis.DialHook { return next }

func (c *tripCounter) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "hello", "client", "ping":
			// connection establishment / handshake
		default:
			c.trips++
		}
		return next(ctx, cmd)
	}
}

func (c *tripCounter) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		isInternal := true
		for _, cmd := range cmds {
			if cmd.Name() != "client" && cmd.Name() != "hello" {
				isInternal = false
				break
			}
		}
		if !isInternal {
			c.trips++
		}
		return next(ctx, cmds)
	}
}

func startTestRedis(t *testing.T) (*redis.Client, string) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c, addr
}

func setupDealFixture(t *testing.T, c *redis.Client, sprint string) {
	t.Helper()
	ctx := context.Background()
	if err := c.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	nowMs := now.UnixMilli()

	// Live reconciler lease
	if err := c.HSet(ctx, "lease:reconciler", "instance", "ctl-reconciler", "token", "live-token", "host", "ctl-host", "pid", "1", "at", strconv.FormatInt(nowMs, 10), "acquired_at", strconv.FormatInt(nowMs, 10)).Err(); err != nil {
		t.Fatal(err)
	}

	// Sprint
	c.SAdd(ctx, "sprints", sprint)
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: sprint})
	c.HSet(ctx, "s:"+sprint, "status", "open")

	// 3 cards in s:s1:pool
	for i := 0; i < 3; i++ {
		label := fmt.Sprintf("card-%02d", i)
		c.HSet(ctx, "s:"+sprint+":card:"+label,
			"state", "queued",
			"priority", strconv.Itoa(i),
			"attempt", "0",
			"retries", "0",
			"base_sha", "0123456789abcdef",
			"bench", "",
			"leg", "",
			"tier", "")
		c.ZAdd(ctx, "s:"+sprint+":pool", redis.Z{Score: float64(i), Member: label})
		c.SAdd(ctx, "s:"+sprint+":idx:card:queued", label)
	}

	// Registered benches: down, full, ok, paused
	c.SAdd(ctx, "benches", "down", "full", "ok", "paused")

	// down: no beat
	c.HSet(ctx, "bench:down:desired", "slots", "2", "paused", "0")

	// paused: desired.paused=1
	c.HSet(ctx, "bench:paused:beat", "host", "paused.host", "user", "bench", "at", strconv.FormatInt(nowMs, 10))
	c.HSet(ctx, "bench:paused:state", "state", "UP", "at", "1") // #2046: UP is the fleet record
	c.HSet(ctx, "bench:paused:desired", "slots", "2", "paused", "1")

	// full: free 0
	c.HSet(ctx, "bench:full:beat", "host", "full.host", "user", "bench", "at", strconv.FormatInt(nowMs, 10))
	c.HSet(ctx, "bench:full:state", "state", "UP", "at", "1") // #2046: UP is the fleet record
	c.HSet(ctx, "bench:full:desired", "slots", "2", "paused", "0")
	c.ZAdd(ctx, "bench:full:starting", redis.Z{Score: float64(nowMs), Member: sprint + "/card-f1/1"})
	c.ZAdd(ctx, "bench:full:living", redis.Z{Score: float64(nowMs), Member: sprint + "/card-f2/1"})

	// ok: free 2, ssh=refused
	c.HSet(ctx, "bench:ok:beat", "host", "ok.host", "user", "bench", "at", strconv.FormatInt(nowMs, 10))
	c.HSet(ctx, "bench:ok:state", "state", "UP", "at", "1") // #2046: UP is the fleet record
	c.HSet(ctx, "bench:ok:desired", "slots", "2", "paused", "0")
	c.HSet(ctx, "bench:ok:ssh", "state", "refused", "why", "connection refused", "at", strconv.FormatInt(nowMs-5000, 10))

	// ghost: not in benches, but 1 card in queue
	c.ZAdd(ctx, "s:"+sprint+":bench:ghost:queue", redis.Z{Score: 1, Member: "stranded-card"})

	// Retired keys
	c.Set(ctx, "bench:pool", "retired-pool-value", 0)
	c.HSet(ctx, "bench:ok", "dealer_queue", "99")

	// Log stream: one card deal entry, then 1000 of another kind
	c.XAdd(ctx, &redis.XAddArgs{
		Stream: "s:" + sprint + ":log",
		Values: map[string]any{
			"kind":      "card deal",
			"id":        "card-old",
			"from":      "queued",
			"to":        "dealt",
			"attempt":   "1",
			"token_sha": "0123456789ab",
			"actor":     "reconciler",
			"reason":    "",
			"evidence":  "",
			"idem":      "",
			"at":        strconv.FormatInt(nowMs-100000, 10),
		},
	})
	for i := 0; i < 1000; i++ {
		c.XAdd(ctx, &redis.XAddArgs{
			Stream: "s:" + sprint + ":log",
			Values: map[string]any{
				"kind": "card test",
				"id":   fmt.Sprintf("filler-%d", i),
				"at":   strconv.FormatInt(nowMs, 10),
			},
		})
	}
}

func testTokenSHA(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

func callDealStatus(addr string, extraArgs ...string) (stdout, stderr string, code int, trips int) {
	var counter tripCounter
	oldOpen := openDealStore
	openDealStore = func(ctx context.Context, a string) (*store.Store, error) {
		client := redis.NewClient(&redis.Options{Addr: a})
		client.AddHook(&counter)
		return store.New(client), nil
	}
	defer func() { openDealStore = oldOpen }()

	var out, errOut bytes.Buffer
	args := append([]string{"deal", "status", "--redis", addr}, extraArgs...)
	code = run(args, &out, &errOut)
	return out.String(), errOut.String(), code, counter.trips
}

func parseField(line, key string) string {
	for _, part := range strings.Fields(line) {
		if strings.HasPrefix(part, key+"=") {
			return strings.TrimPrefix(part, key+"=")
		}
	}
	return ""
}

func parseBenchName(line string) string {
	fields := strings.Fields(line)
	if len(fields) >= 2 && fields[0] == "bench" {
		return fields[1]
	}
	return ""
}

func verifyAgreesWithDealGuard(t *testing.T, c *redis.Client, sprint, bench, expectedVerdict string) {
	t.Helper()
	ctx := context.Background()

	poolMembers := c.ZRange(ctx, "s:"+sprint+":pool", 0, 0).Val()
	cardLabel := "card-00"
	if len(poolMembers) > 0 {
		cardLabel = poolMembers[0]
	}
	attemptStr, _ := c.HGet(ctx, "s:"+sprint+":card:"+cardLabel, "attempt").Result()
	attempt, _ := strconv.Atoi(attemptStr)
	nextAttempt := attempt + 1
	token := fmt.Sprintf("%d.0123456789abcdef0123456789abcdef", nextAttempt)
	tokenSha := testTokenSHA(token)

	reply, err := c.FCall(ctx, "ns_card_deal", nil, bench, "live-token", "test", "idem",
		sprint, cardLabel, strconv.Itoa(nextAttempt), token, tokenSha).StringSlice()
	if err != nil {
		t.Fatalf("ns_card_deal bench=%s: %v", bench, err)
	}

	if expectedVerdict == "ok" {
		if len(reply) == 0 || reply[0] != "DEALT" {
			t.Fatalf("ns_card_deal for bench %s returned %v, want DEALT", bench, reply)
		}
	} else {
		if len(reply) < 2 || reply[0] != "NONE" || reply[1] != expectedVerdict {
			t.Fatalf("ns_card_deal for bench %s returned %v, want NONE %s", bench, reply, expectedVerdict)
		}
	}
}

func TestDealStatusAgreesWithDealGuard(t *testing.T) {
	c, addr := startTestRedis(t)
	const sprint = "s1"
	ctx := context.Background()

	t.Run("list", func(t *testing.T) {
		setupDealFixture(t, c, sprint)

		stdout, stderr, code, trips := callDealStatus(addr, "--sprint", sprint)
		if code != 0 {
			t.Fatalf("deal status failed code %d: %s", code, stderr)
		}
		if trips != 1 {
			t.Fatalf("wrapped client counted %d round trips, want exactly 1", trips)
		}
		if strings.Contains(stdout, "bench:pool") || strings.Contains(stdout, "dealer_queue") {
			t.Fatalf("output contains retired keys: %s", stdout)
		}
		if strings.Contains(stdout, "unregistered") {
			t.Fatalf("list mode must never contain unregistered: %s", stdout)
		}

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) != 5 {
			t.Fatalf("expected 5 lines (4 benches + 1 sprint), got %d:\n%s", len(lines), stdout)
		}

		expectedBenches := []struct {
			name    string
			verdict string
		}{
			{"down", "down"},
			{"full", "full"},
			{"ok", "ok"},
			{"paused", "paused"},
		}

		sprintLine := lines[4]
		if !strings.HasPrefix(sprintLine, "sprint "+sprint) {
			t.Fatalf("expected sprint line, got: %s", sprintLine)
		}
		poolN, _ := strconv.Atoi(parseField(sprintLine, "pool"))
		waitingN, _ := strconv.Atoi(parseField(sprintLine, "waiting"))
		actualPool, _ := c.ZCard(ctx, "s:"+sprint+":pool").Result()
		actualWait, _ := c.SCard(ctx, "s:"+sprint+":waiting").Result()
		if int64(poolN) != actualPool {
			t.Errorf("pool=%d does not match s:s1:pool ZCARD %d", poolN, actualPool)
		}
		if int64(waitingN) != actualWait {
			t.Errorf("waiting=%d does not match s:s1:waiting SCARD %d", waitingN, actualWait)
		}
		if parseField(sprintLine, "last_deal_age") != "-" {
			t.Errorf("last_deal_age=%s, want - (1000 entries of other kind ahead)", parseField(sprintLine, "last_deal_age"))
		}

		for i, eb := range expectedBenches {
			line := lines[i]
			bName := parseBenchName(line)
			if bName != eb.name {
				t.Errorf("line %d: bench name %s, want %s", i, bName, eb.name)
			}
			verdict := parseField(line, "verdict")
			if verdict != eb.verdict {
				t.Errorf("line %d: bench %s verdict=%s, want %s", i, bName, verdict, eb.verdict)
			}
			verifyAgreesWithDealGuard(t, c, sprint, bName, eb.verdict)
		}
	})

	t.Run("bench_unregistered", func(t *testing.T) {
		setupDealFixture(t, c, sprint)

		stdout, stderr, code, trips := callDealStatus(addr, "--sprint", sprint, "--bench", "ghost")
		if code != 0 {
			t.Fatalf("deal status --bench ghost failed code %d: %s", code, stderr)
		}
		if trips != 1 {
			t.Fatalf("wrapped client counted %d round trips, want exactly 1", trips)
		}
		if strings.Contains(stdout, "bench:pool") || strings.Contains(stdout, "dealer_queue") {
			t.Fatalf("output contains retired keys: %s", stdout)
		}

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 lines (1 bench + 1 sprint), got %d:\n%s", len(lines), stdout)
		}

		benchLine := lines[0]
		if parseBenchName(benchLine) != "ghost" {
			t.Errorf("expected bench ghost, got: %s", benchLine)
		}
		if parseField(benchLine, "verdict") != "unregistered" {
			t.Errorf("expected verdict=unregistered, got: %s", benchLine)
		}
		if parseField(benchLine, "queue") != "1" {
			t.Errorf("expected queue=1 for ghost, got: %s", benchLine)
		}

		verifyAgreesWithDealGuard(t, c, sprint, "ghost", "unregistered")
	})

	t.Run("bench_registered", func(t *testing.T) {
		setupDealFixture(t, c, sprint)

		// Add one more card deal to log within the 1000 window
		nowMs := time.Now().UnixMilli()
		c.XAdd(ctx, &redis.XAddArgs{
			Stream: "s:" + sprint + ":log",
			Values: map[string]any{
				"kind":      "card deal",
				"id":        "card-recent",
				"from":      "queued",
				"to":        "dealt",
				"attempt":   "1",
				"token_sha": "0123456789ab",
				"actor":     "reconciler",
				"reason":    "",
				"evidence":  "",
				"idem":      "",
				"at":        strconv.FormatInt(nowMs, 10),
			},
		})

		stdout, stderr, code, trips := callDealStatus(addr, "--sprint", sprint, "--bench", "ok")
		if code != 0 {
			t.Fatalf("deal status --bench ok failed code %d: %s", code, stderr)
		}
		if trips != 1 {
			t.Fatalf("wrapped client counted %d round trips, want exactly 1", trips)
		}
		if strings.Contains(stdout, "bench:pool") || strings.Contains(stdout, "dealer_queue") {
			t.Fatalf("output contains retired keys: %s", stdout)
		}

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 lines (1 bench + 1 sprint), got %d:\n%s", len(lines), stdout)
		}

		benchLine := lines[0]
		if parseBenchName(benchLine) != "ok" {
			t.Errorf("expected bench ok, got: %s", benchLine)
		}
		if parseField(benchLine, "verdict") != "ok" {
			t.Errorf("expected verdict=ok, got: %s", benchLine)
		}
		if parseField(benchLine, "free") != "2" {
			t.Errorf("expected free=2, got: %s", benchLine)
		}
		if parseField(benchLine, "ssh") != "refused" {
			t.Errorf("expected ssh=refused, got: %s", benchLine)
		}

		sprintLine := lines[1]
		lastDealAge := parseField(sprintLine, "last_deal_age")
		if lastDealAge == "-" {
			t.Errorf("expected last_deal_age to be a number, got -")
		}
		if _, err := strconv.Atoi(lastDealAge); err != nil {
			t.Errorf("expected numeric last_deal_age, got %s", lastDealAge)
		}

		verifyAgreesWithDealGuard(t, c, sprint, "ok", "ok")
	})

	t.Run("usage_refusals", func(t *testing.T) {
		var out, errOut bytes.Buffer
		// Missing --sprint
		code := run([]string{"deal", "status", "--redis", addr}, &out, &errOut)
		if code != 2 {
			t.Errorf("missing --sprint exit code = %d, want 2", code)
		}

		errOut.Reset()
		// Unknown flag
		code = run([]string{"deal", "status", "--redis", addr, "--sprint", sprint, "--bogus"}, &out, &errOut)
		if code != 2 {
			t.Errorf("unknown flag exit code = %d, want 2", code)
		}

		errOut.Reset()
		// Stray positional argument
		code = run([]string{"deal", "status", "--redis", addr, "--sprint", sprint, "stray"}, &out, &errOut)
		if code != 2 {
			t.Errorf("stray argument exit code = %d, want 2", code)
		}
	})
}
