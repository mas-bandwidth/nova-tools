package stream

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
)

// TestWaitCIReturnsGreenFromRedisWithoutPolling tests that waitCI checks the
// ci-ok verdict from Redis at the proved SHA (webhook.Key) directly without
// polling GitHub or waiting on merge_group (#4386).
func TestWaitCIReturnsGreenFromRedisWithoutPolling(t *testing.T) {
	t.Parallel()

	c := prRedis(t)
	ctx := context.Background()

	head := strings.Repeat("a", 40)
	repoName := "o/r"
	prNum := 77

	// PR record with ci=pending
	if err := c.HSet(ctx, PRKey(repoName, prNum), "repo", repoName, "n", prNum, "head", head, "base", "dev", "stream", "quack", "state", "open", "ci", "pending").Err(); err != nil {
		t.Fatal(err)
	}

	l := Landing{
		Repo: repoName,
		PR:   prNum,
		Head: head,
	}

	// 1. When webhook.Key has gh=green in Redis, waitCI returns "green" immediately.
	if err := c.HSet(ctx, webhook.Key(repoName, head), "gh", "green", "check:ci-ok", "green 1").Err(); err != nil {
		t.Fatal(err)
	}

	awaitCalled := false
	o := RunOptions{
		Options: Options{Repo: repoName},
		Tick:    10 * time.Millisecond,
		Await: func(ctx context.Context, h string, d time.Duration) error {
			awaitCalled = true
			return nil
		},
	}

	ci, err := waitCI(ctx, c, o, l, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("waitCI failed: %v", err)
	}
	if ci != "green" {
		t.Fatalf("waitCI = %q, want green", ci)
	}
	if awaitCalled {
		t.Fatal("waitCI waited when ci-ok was already green in Redis")
	}

	// 2. When webhook.Key has gh=red in Redis, waitCI returns "red" immediately.
	headRed := strings.Repeat("b", 40)
	if err := c.HSet(ctx, PRKey(repoName, 78), "repo", repoName, "n", 78, "head", headRed, "base", "dev", "stream", "quack", "state", "open", "ci", "pending").Err(); err != nil {
		t.Fatal(err)
	}
	lRed := Landing{Repo: repoName, PR: 78, Head: headRed}
	if err := c.HSet(ctx, webhook.Key(repoName, headRed), "gh", "red", "gh_fail", "check:lint").Err(); err != nil {
		t.Fatal(err)
	}

	ciRed, err := waitCI(ctx, c, o, lRed, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("waitCI red failed: %v", err)
	}
	if ciRed != "red" {
		t.Fatalf("waitCI red = %q, want red", ciRed)
	}
}

// TestLandedStepWallsFormat verifies that steps.Line outputs the exact
// LANDED n=<n> total_ms=<ms> line format with step walls (#4386).
func TestLandedStepWallsFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	t0 := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		t0 = t0.Add(100 * time.Millisecond)
		return t0
	}

	steps := NewSteps(&buf, clock)
	steps.Line("BUILT %s head=%s pr=#%d members=%d build=%d", "stream/quack", "aaaa1111", 10, 2, 1)
	steps.Line("CI %s %s pr=#%d request=%s", "aaaa1111", "green", 10, "CREATED")
	steps.Line("MERGED %s pr=#%d already=%t", "mmmm1111", 10, false)
	totalMS := steps.Total().Milliseconds()
	steps.Line("LANDED n=%d stream=%s moved=%d total_ms=%d", 2, "quack", 2, totalMS)

	out := buf.String()
	wantLines := []string{
		"BUILT stream/quack head=aaaa1111 pr=#10 members=2 build=1 ms=100",
		"CI aaaa1111 green pr=#10 request=CREATED ms=100",
		"MERGED mmmm1111 pr=#10 already=false ms=100",
		"LANDED n=2 stream=quack moved=2 total_ms=400 ms=200",
	}
	for _, want := range wantLines {
		if !strings.Contains(out, want) {
			t.Fatalf("missing line %q in output:\n%s", want, out)
		}
	}
}
