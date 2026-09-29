//go:build functional

package taskcard_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// benchPlatform registers a bench in Redis with slots, live beat, and a platform.
func benchPlatform(t *testing.T, c *redis.Client, name, platform string, slots int) taskcard.Consumer {
	t.Helper()
	ctx := context.Background()
	k := mustConsumer(t, "bench:"+name)
	c.SAdd(ctx, "benches", name)
	c.HSet(ctx, k.DesiredKey(), "slots", strconv.Itoa(slots), "platform", platform)
	c.HSet(ctx, k.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10), "platform", platform)
	return k
}

// TestCardPlatformsMultiCopy verifies issue #4395:
//  1. A card with PLATFORMS: darwin,linux cuts one work copy per named platform
//     routed to benches matching that platform OS.
//  2. Both copy IDs are tracked on the primary card in platform_copies and copy:<platform>.
//  3. RenderCopy outputs PLATFORM: <platform>.
//  4. Primary reaches review only when every platform copy ends ok.
//  5. If any copy ends fail, the primary transitions to review naming the failing platform.
func TestCardPlatformsMultiCopy(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()

	darwinBench := benchPlatform(t, c, "darwin-worker", "darwin-arm64", 4)
	linuxBench := benchPlatform(t, c, "linux-worker", "linux-amd64", 4)

	// 1. Push a multi-platform card: darwin,linux
	id := "card-plat-1"
	_, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     id,
		Where:  "ready",
		Stream: mvStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "nova-tools#4395",
		Origin: "issue:nova-tools#4395",
		Title:  "multi-platform card",
		Repo:   "mas-bandwidth/nova-tools",
		By:     "emma",
		Fields: []string{
			"base", "dev",
			"base_sha", baseSHA,
			"paths", "internal/sandbox",
			"test", "./internal/sandbox TestWrap",
			"done_when", "go test ./internal/sandbox passes",
			"platforms", "darwin,linux",
		},
	})
	if err != nil {
		t.Fatalf("push card: %v", err)
	}

	// 2. Deal to darwinBench -> cuts darwin copy
	d1, err := taskcard.Deal(ctx, c, taskcard.DealRequest{
		To:  darwinBench,
		IDs: []string{id},
		By:  "emma",
	})
	if err != nil || len(d1) != 1 {
		t.Fatalf("deal to darwinBench: %v (deals=%+v)", err, d1)
	}
	darwinCopy := d1[0].Copy

	// Verify primary record state after 1st copy
	rec := c.HGetAll(ctx, "task:"+id).Val()
	if rec["where"] != "working" {
		t.Fatalf("primary where = %q, want working", rec["where"])
	}
	if rec["copy:darwin"] != darwinCopy {
		t.Fatalf("primary copy:darwin = %q, want %q", rec["copy:darwin"], darwinCopy)
	}
	if !strings.Contains(rec["platform_copies"], darwinCopy) {
		t.Fatalf("primary platform_copies = %q, want containing %q", rec["platform_copies"], darwinCopy)
	}

	// Verify copy record and RenderCopy
	copy1Rec := c.HGetAll(ctx, "task:"+darwinCopy).Val()
	if copy1Rec["platform"] != "darwin" {
		t.Fatalf("copy1 platform = %q, want darwin", copy1Rec["platform"])
	}
	cc1 := card.CopyCardFrom(darwinCopy, copy1Rec)
	rendered1, err := card.RenderCopy(cc1)
	if err != nil {
		t.Fatalf("RenderCopy: %v", err)
	}
	if !strings.Contains(string(rendered1), "PLATFORM: darwin\n") {
		t.Fatalf("RenderCopy missing PLATFORM: darwin:\n%s", string(rendered1))
	}

	// Dealing again to darwinBench should be refused (darwin copy already live)
	dRefused, _ := taskcard.Deal(ctx, c, taskcard.DealRequest{
		To:  darwinBench,
		IDs: []string{id},
		By:  "emma",
	})
	if len(dRefused) != 0 {
		t.Fatalf("expected deal to darwinBench to be refused, got: %+v", dRefused)
	}

	// 3. Deal to linuxBench -> cuts linux copy
	d2, err := taskcard.Deal(ctx, c, taskcard.DealRequest{
		To:  linuxBench,
		IDs: []string{id},
		By:  "emma",
	})
	if err != nil || len(d2) != 1 {
		t.Fatalf("deal to linuxBench: %v (deals=%+v)", err, d2)
	}
	linuxCopy := d2[0].Copy

	// Verify primary record state after 2nd copy
	rec = c.HGetAll(ctx, "task:"+id).Val()
	if rec["where"] != "working" {
		t.Fatalf("primary where = %q, want working", rec["where"])
	}
	if rec["copy:linux"] != linuxCopy {
		t.Fatalf("primary copy:linux = %q, want %q", rec["copy:linux"], linuxCopy)
	}
	if !strings.Contains(rec["platform_copies"], linuxCopy) {
		t.Fatalf("primary platform_copies = %q, want containing %q", rec["platform_copies"], linuxCopy)
	}

	// Verify linux copy record and RenderCopy
	copy2Rec := c.HGetAll(ctx, "task:"+linuxCopy).Val()
	if copy2Rec["platform"] != "linux" {
		t.Fatalf("copy2 platform = %q, want linux", copy2Rec["platform"])
	}
	cc2 := card.CopyCardFrom(linuxCopy, copy2Rec)
	rendered2, err := card.RenderCopy(cc2)
	if err != nil {
		t.Fatalf("RenderCopy: %v", err)
	}
	if !strings.Contains(string(rendered2), "PLATFORM: linux\n") {
		t.Fatalf("RenderCopy missing PLATFORM: linux:\n%s", string(rendered2))
	}

	// 4. Work both copies on their respective benches
	w1, err := taskcard.Work(ctx, c, darwinBench, "emma", 0, false, darwinCopy)
	if err != nil || len(w1.IDs) != 1 {
		t.Fatalf("work darwinCopy: %v %+v", err, w1)
	}
	w2, err := taskcard.Work(ctx, c, linuxBench, "emma", 0, false, linuxCopy)
	if err != nil || len(w2.IDs) != 1 {
		t.Fatalf("work linuxCopy: %v %+v", err, w2)
	}

	// Check table working counts
	darwinWorking := c.ZCard(ctx, darwinBench.KeyAt(0, "working")).Val()
	linuxWorking := c.ZCard(ctx, linuxBench.KeyAt(0, "working")).Val()
	if darwinWorking != 1 || linuxWorking != 1 {
		t.Fatalf("working counts: darwin=%d, linux=%d; want 1 each", darwinWorking, linuxWorking)
	}

	// 5. End darwin copy ok with PR
	fakeHead := head(1)
	// Create PR record in redis for head verification
	c.HSet(ctx, "pr:nova-tools:4395", "head", fakeHead, "base", "dev")
	e1, err := taskcard.End(ctx, c, taskcard.EndRequest{
		IDs:  []string{darwinCopy},
		OK:   true,
		By:   "emma",
		Repo: "mas-bandwidth/nova-tools",
		PR:   "4395",
		Head: fakeHead,
	})
	if err != nil || len(e1) != 1 {
		t.Fatalf("end darwinCopy: %v %+v", err, e1)
	}
	if e1[0].To != "working" {
		t.Fatalf("darwin copy end ok should keep primary in working, got To = %q", e1[0].To)
	}

	// Verify primary STILL in working (only 1 of 2 platforms ok)
	rec = c.HGetAll(ctx, "task:"+id).Val()
	if rec["where"] != "working" {
		t.Fatalf("primary where = %q, want working (only darwin ok)", rec["where"])
	}
	if rec["ok:darwin"] != "1" {
		t.Fatalf("primary ok:darwin = %q, want 1", rec["ok:darwin"])
	}

	// 6. End linux copy ok -> NOW primary reaches review!
	e2, err := taskcard.End(ctx, c, taskcard.EndRequest{
		IDs: []string{linuxCopy},
		OK:  true,
		By:  "emma",
	})
	if err != nil || len(e2) != 1 {
		t.Fatalf("end linuxCopy: %v %+v", err, e2)
	}
	if e2[0].To != "review" {
		t.Fatalf("all platform copies ok should move primary to review, got To = %q", e2[0].To)
	}

	rec = c.HGetAll(ctx, "task:"+id).Val()
	if rec["where"] != "review" {
		t.Fatalf("primary where = %q, want review (all platforms ok)", rec["where"])
	}
	if rec["ok:linux"] != "1" {
		t.Fatalf("primary ok:linux = %q, want 1", rec["ok:linux"])
	}
}

// TestCardPlatformsFailureMovesToReview verifies that if any platform copy fails,
// the primary moves to review naming the failing platform, and other live copies are dropped.
func TestCardPlatformsFailureMovesToReview(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()

	darwinBench := benchPlatform(t, c, "darwin-worker-fail", "darwin-arm64", 4)
	linuxBench := benchPlatform(t, c, "linux-worker-fail", "linux-amd64", 4)

	id := "card-plat-fail"
	_, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     id,
		Where:  "ready",
		Stream: mvStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "nova-tools#4396",
		Origin: "issue:nova-tools#4396",
		Title:  "multi-platform card fail",
		Repo:   "mas-bandwidth/nova-tools",
		By:     "emma",
		Fields: []string{
			"base", "dev",
			"base_sha", baseSHA,
			"paths", "internal/sandbox",
			"test", "./internal/sandbox TestWrap",
			"done_when", "go test passes",
			"platforms", "darwin,linux",
		},
	})
	if err != nil {
		t.Fatalf("push card: %v", err)
	}

	// Deal to both
	d1, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: darwinBench, IDs: []string{id}, By: "emma"})
	if err != nil || len(d1) != 1 {
		t.Fatalf("deal darwin: %v", err)
	}
	darwinCopy := d1[0].Copy

	d2, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: linuxBench, IDs: []string{id}, By: "emma"})
	if err != nil || len(d2) != 1 {
		t.Fatalf("deal linux: %v", err)
	}
	linuxCopy := d2[0].Copy

	// Work both
	if _, err := taskcard.Work(ctx, c, darwinBench, "emma", 0, false, darwinCopy); err != nil {
		t.Fatalf("work darwin: %v", err)
	}
	if _, err := taskcard.Work(ctx, c, linuxBench, "emma", 0, false, linuxCopy); err != nil {
		t.Fatalf("work linux: %v", err)
	}

	// Linux copy fails!
	res, err := taskcard.End(ctx, c, taskcard.EndRequest{
		IDs: []string{linuxCopy},
		OK:  false,
		Why: "test failed: nil pointer dereference",
		By:  "emma",
	})
	if err != nil || len(res) != 1 {
		t.Fatalf("end linux copy fail: %v %+v", err, res)
	}
	if res[0].To != "review" {
		t.Fatalf("failing copy should move primary to review, got To = %q", res[0].To)
	}

	rec := c.HGetAll(ctx, "task:"+id).Val()
	if rec["where"] != "review" {
		t.Fatalf("primary where = %q, want review", rec["where"])
	}
	if !strings.Contains(rec["review_why"], "platform linux failed") {
		t.Fatalf("primary review_why = %q, want containing 'platform linux failed'", rec["review_why"])
	}
	if rec["review_platform"] != "linux" {
		t.Fatalf("primary review_platform = %q, want linux", rec["review_platform"])
	}

	// The darwin copy should have been dropped (in fail set)
	darwinRec := c.HGetAll(ctx, "task:"+darwinCopy).Val()
	if darwinRec["where"] != "fail" || darwinRec["outcome"] != "fail" {
		t.Fatalf("darwin copy should be dropped (where=fail, outcome=fail), got where=%q outcome=%q", darwinRec["where"], darwinRec["outcome"])
	}
}
