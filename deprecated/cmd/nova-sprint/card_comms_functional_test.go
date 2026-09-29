//go:build functional

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func commsRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	return addr, client
}

func TestCardCommsCLI(t *testing.T) {
	t.Parallel()

	addr, c := commsRedis(t)
	ctx := context.Background()
	now := time.Now()

	consumer := "friend:f1"
	c.SAdd(ctx, "friends", "f1")

	// Set up working copy
	copy1 := "card-100~1"
	tLease := strconv.FormatInt(now.Add(-40*time.Second).UnixMilli(), 10)
	tBeat := strconv.FormatInt(now.Add(-8*time.Second).UnixMilli(), 10)
	c.HSet(ctx, "task:"+copy1, map[string]any{
		"primary":   "card-100",
		"where":     "working",
		"state":     "working",
		"consumer":  consumer,
		"model":     "test-model",
		"child":     "proc-99",
		"leased_at": tLease,
		"beat_at":   tBeat,
	})
	c.ZAdd(ctx, ws.ConsumerKeyAt(0, consumer, "working"), redis.Z{Score: 1, Member: copy1})

	// Set up completed copy
	copy2 := "card-101~1"
	tDone := strconv.FormatInt(now.Add(-90*time.Second).UnixMilli(), 10)
	c.HSet(ctx, "task:"+copy2, map[string]any{
		"primary":    "card-101",
		"where":      "ok",
		"state":      "ok",
		"consumer":   consumer,
		"model":      "eval-model",
		"child":      "proc-101",
		"created_at": tDone,
		"repo":       "example-org/repo-y",
		"pr":         "77",
		"head":       "c0ffee01",
		"ci":         "pass",
		"result_md":  "### Outcome\nAll 15 assertions passed cleanly",
	})
	c.ZAdd(ctx, ws.ConsumerKeyAt(0, consumer, "ok"), redis.Z{Score: 2, Member: copy2})

	// 1. card tell --id <copy> --text "..."
	code, out, errOut := runCLI("card", "tell", "--redis", addr, "--id", copy1, "--text", "refactor loop")
	if code != 0 || errOut != "" {
		t.Fatalf("card tell failed: code=%d out=%q errOut=%q", code, out, errOut)
	}
	if !strings.HasPrefix(out, "CARD TELL id=card-100~1 streams=copy:card-100~1:tell,copy:card-100~1:inbox") {
		t.Fatalf("card tell unexpected out: %q", out)
	}

	// Verify tell entry in stream
	entries, err := c.XRange(ctx, "copy:"+copy1+":tell", "-", "+").Result()
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 stream entry, got %v (err: %v)", entries, err)
	}
	if entries[0].Values["text"] != "refactor loop" {
		t.Errorf("stream text = %v, want 'refactor loop'", entries[0].Values["text"])
	}

	// Tell refused on non-working copy
	code, out, _ = runCLI("card", "tell", "--redis", addr, "--id", copy2, "--text", "too late")
	if code != 1 || !strings.Contains(out, "CARD TELL REFUSED id=card-101~1 why=\"copy card-101~1 is ok, not running\"") {
		t.Fatalf("card tell non-working copy: code=%d out=%q", code, out)
	}

	// 2. card report --id <copy>
	code, out, errOut = runCLI("card", "report", "--redis", addr, "--id", copy2)
	if code != 0 || errOut != "" {
		t.Fatalf("card report failed: code=%d out=%q errOut=%q", code, out, errOut)
	}
	for _, expected := range []string{
		"== CARD REPORT: card-101~1",
		"PRIMARY:   card-101",
		"CONSUMER:  friend:f1",
		"STATE:     ok",
		"MODEL:     eval-model",
		"CHILD:     proc-101",
		"#77:",
		"repo-y/pull/77",
		"STATUS: pass",
		"### Outcome",
		"All 15 assertions passed cleanly",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("card report missing %q in output:\n%s", expected, out)
		}
	}

	// 3. card ls --as friend:f1 --live
	code, out, errOut = runCLI("card", "ls", "--redis", addr, "--as", consumer, "--live")
	if code != 0 || errOut != "" {
		t.Fatalf("card ls --live failed: code=%d out=%q errOut=%q", code, out, errOut)
	}
	if !strings.Contains(out, "COPY card-100~1 primary=card-100 state=working model=test-model child=proc-99") {
		t.Errorf("card ls --live missing copy1 line:\n%s", out)
	}
	if strings.Contains(out, "card-101~1") {
		t.Errorf("card ls --live should not list non-working copy2:\n%s", out)
	}
	if !strings.Contains(out, "CARD LS as=friend:f1 n=1 live=true") {
		t.Errorf("card ls --live missing receipt line:\n%s", out)
	}

	// 4. card ls --as friend:f1 (all copies)
	code, out, errOut = runCLI("card", "ls", "--redis", addr, "--as", consumer)
	if code != 0 || errOut != "" {
		t.Fatalf("card ls all failed: code=%d out=%q errOut=%q", code, out, errOut)
	}
	if !strings.Contains(out, "COPY card-100~1") || !strings.Contains(out, "COPY card-101~1") {
		t.Errorf("card ls missing copies in output:\n%s", out)
	}
	if !strings.Contains(out, "CARD LS as=friend:f1 n=2 live=false") {
		t.Errorf("card ls missing receipt line:\n%s", out)
	}
}
