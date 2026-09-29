//go:build functional

package taskcard_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestPathsWithinStreamOverlapAtCut verifies Item 1 of #4318:
// Cards in one stream whose PATHS overlap (exact file, or Go package)
// gain DEPENDS-ON from the newer to the older with why=overlap:<path>,
// and start in waiting even if ready was requested.
func TestPathsWithinStreamOverlapAtCut(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	testStream := "stream:paths-cut-test"

	// 1. Exact file overlap within stream
	res1, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "c1-file",
		Where:  "ready",
		Stream: testStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#1",
		Origin: "issue#1",
		Title:  "card 1",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "docs/guide.md", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push c1: %v", err)
	}
	if res1.Where != "ready" {
		t.Fatalf("c1 where = %s, want ready", res1.Where)
	}

	res2, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "c2-file",
		Where:  "ready",
		Stream: testStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#2",
		Origin: "issue#2",
		Title:  "card 2",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "docs/guide.md", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push c2: %v", err)
	}
	if res2.Where != "waiting" {
		t.Fatalf("c2 where = %s, want waiting due to overlap", res2.Where)
	}

	rec2, err := taskcard.Record(ctx, c, "c2-file")
	if err != nil {
		t.Fatalf("record c2: %v", err)
	}
	if rec2["where"] != "waiting" {
		t.Errorf("c2 where = %s, want waiting", rec2["where"])
	}
	if !strings.Contains(rec2["blocked_on"], "c1-file") {
		t.Errorf("c2 blocked_on = %q, want c1-file", rec2["blocked_on"])
	}
	if !strings.Contains(rec2["depends_on"], "c1-file") {
		t.Errorf("c2 depends_on = %q, want c1-file", rec2["depends_on"])
	}
	if rec2["why"] != "overlap:docs/guide.md" {
		t.Errorf("c2 why = %q, want overlap:docs/guide.md", rec2["why"])
	}

	// 2. Go package overlap within stream
	res3, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "c3-gopkg",
		Where:  "ready",
		Stream: testStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#3",
		Origin: "issue#3",
		Title:  "card 3",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "pkg/net/client.go", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push c3: %v", err)
	}
	if res3.Where != "ready" {
		t.Fatalf("c3 where = %s, want ready", res3.Where)
	}

	res4, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "c4-gopkg",
		Where:  "ready",
		Stream: testStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#4",
		Origin: "issue#4",
		Title:  "card 4",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "pkg/net/server.go", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push c4: %v", err)
	}
	if res4.Where != "waiting" {
		t.Fatalf("c4 where = %s, want waiting due to Go package overlap", res4.Where)
	}

	rec4, err := taskcard.Record(ctx, c, "c4-gopkg")
	if err != nil {
		t.Fatalf("record c4: %v", err)
	}
	if !strings.Contains(rec4["blocked_on"], "c3-gopkg") {
		t.Errorf("c4 blocked_on = %q, want c3-gopkg", rec4["blocked_on"])
	}
	if rec4["why"] != "overlap:pkg/net" {
		t.Errorf("c4 why = %q, want overlap:pkg/net", rec4["why"])
	}

	// 3. Plan and stitch cards do not participate in automatic path overlap
	resPlan, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "p1-plan",
		Where:  "waiting",
		Stream: testStream,
		Sprint: sprint,
		Kind:   "plan",
		Ref:    "ref#plan",
		Origin: "issue#plan",
		Title:  "plan card",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "pkg/net/server.go", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push plan: %v", err)
	}
	if resPlan.Where != "waiting" {
		t.Fatalf("plan where = %s, want waiting", resPlan.Where)
	}
	recPlan, err := taskcard.Record(ctx, c, "p1-plan")
	if err != nil {
		t.Fatalf("record plan: %v", err)
	}
	// Should not have gained automatic path overlap dependencies on c3 or c4
	if strings.Contains(recPlan["why"], "overlap:") {
		t.Errorf("plan card why = %q, should not have overlap why", recPlan["why"])
	}

	clean(t, c, "after TestPathsWithinStreamOverlapAtCut")
}

// TestPathsCrossStreamOverlapRefusedAtPush verifies that overlapping PATHS
// across different streams is refused at push time (width-is-independent-work).
func TestPathsCrossStreamOverlapRefusedAtPush(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()

	streamA := "stream:paths-cross-alpha"
	streamB := "stream:paths-cross-beta"

	// Card in stream A
	_, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "cross-1",
		Where:  "ready",
		Stream: streamA,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#c1",
		Origin: "issue#c1",
		Title:  "card 1",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "internal/core/store.go", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push cross-1: %v", err)
	}

	// Pushing card with same Go package into stream B must be refused
	_, err = taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "cross-2",
		Where:  "ready",
		Stream: streamB,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#c2",
		Origin: "issue#c2",
		Title:  "card 2",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "internal/core/util.go", "done_when", "test"},
	})
	if err == nil {
		t.Fatal("push cross-2 succeeded, want REFUSED PATHS overlap")
	}
	why, refused := taskcard.IsRefused(err)
	if !refused || !strings.Contains(why, "PATHS") || !strings.Contains(why, "overlap") {
		t.Fatalf("push cross-2 err = %v, why = %q, want REFUSED PATHS overlap", err, why)
	}

	clean(t, c, "after TestPathsCrossStreamOverlapRefusedAtPush")
}

// TestDealPassHoldsLiveOverlapInSameStream verifies Item 2 of #4318:
// The deal pass holds a ready card whose paths a working card in the same
// stream touches (emits HELD overlap=<card> with=<card>), and skips it in deal.
func TestDealPassHoldsLiveOverlapInSameStream(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	testStream := "stream:deal-hold-overlap"
	now := time.Now()
	at := fmt.Sprintf("%d", now.UnixMilli())

	consumerA := mustConsumer(t, "bench:worker-a")
	consumerB := mustConsumer(t, "bench:worker-b")
	for _, k := range []taskcard.Consumer{consumerA, consumerB} {
		if err := taskcard.Enroll(ctx, c, k, true); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, k.BeatKey(), "at", at)
		c.HSet(ctx, k.DesiredKey(), "slots", "5")
	}

	// Push first card with path pkg/shared/a.go
	_, err := taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "card-working",
		Where:  "ready",
		Stream: testStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#w1",
		Origin: "issue#w1",
		Title:  "card working",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "pkg/shared/a.go", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push card-working: %v", err)
	}

	// Deal card-working to consumerA
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{
		To:  consumerA,
		IDs: []string{"card-working"},
		By:  "reconciler",
	})
	if err != nil {
		t.Fatalf("deal card-working: %v", err)
	}
	if len(dealt) != 1 {
		t.Fatalf("dealt %d cards, want 1", len(dealt))
	}
	if _, err := taskcard.Work(ctx, c, consumerA, "worker-a", 1, false); err != nil {
		t.Fatalf("work card-working: %v", err)
	}

	// Verify card-working is in working set
	recW, err := taskcard.Record(ctx, c, "card-working")
	if err != nil {
		t.Fatalf("record card-working: %v", err)
	}
	if recW["where"] != "working" {
		t.Fatalf("card-working where = %s, want working", recW["where"])
	}

	// Push card-ready with non-overlapping dummy path first so it lands ready
	_, err = taskcard.Push(ctx, c, taskcard.PushRequest{
		ID:     "card-ready",
		Where:  "ready",
		Stream: testStream,
		Sprint: sprint,
		Kind:   "build",
		Ref:    "ref#w2",
		Origin: "issue#w2",
		Title:  "card ready",
		By:     "author",
		Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "pkg/other/standalone.go", "done_when", "test"},
	})
	if err != nil {
		t.Fatalf("push card-ready: %v", err)
	}
	// Now set card-ready's paths to overlap with card-working (same Go package: pkg/shared)
	c.HSet(ctx, taskcard.Key("card-ready"), "paths", "pkg/shared/b.go", "stream_paths", "pkg/shared/b.go")

	// 1. Named deal of card-ready must be refused with HELD overlap
	_, err = taskcard.Deal(ctx, c, taskcard.DealRequest{
		To:  consumerB,
		IDs: []string{"card-ready"},
		By:  "reconciler",
	})
	if err == nil {
		t.Fatal("named deal succeeded, want HELD overlap refusal")
	}
	why, refused := taskcard.IsRefused(err)
	if !refused || !strings.Contains(why, "HELD overlap=card-ready with=card-working") {
		t.Fatalf("deal refusal = %q (refused=%v), want HELD overlap=card-ready with=card-working", why, refused)
	}

	// 2. DealPass must emit HELD line and must NOT deal card-ready
	passRes, err := taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil {
		t.Fatalf("DealPass: %v", err)
	}
	heldLineFound := false
	for _, line := range passRes.Lines {
		if line == "HELD overlap=card-ready with=card-working" {
			heldLineFound = true
			break
		}
	}
	if !heldLineFound {
		t.Errorf("DealPass lines = %v, want HELD overlap=card-ready with=card-working", passRes.Lines)
	}
	if passRes.Dealt != 0 {
		t.Errorf("DealPass dealt = %d, want 0 (card-ready should be held)", passRes.Dealt)
	}

	// 3. Move card-working out of working (to landed via End with DoneAlready)
	copyID := dealt[0].Copy
	c.HSet(ctx, taskcard.Key("card-working"), "result_paths", "pkg/shared/a.go")
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{
		IDs:         []string{copyID},
		OK:          true,
		DoneAlready: "c0ffee12",
		By:          "worker-a",
	}); err != nil {
		t.Fatalf("end card-working copy: %v", err)
	}

	// Now run DealPass again; card-ready is no longer held and is dealt to consumerB
	passRes2, err := taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil {
		t.Fatalf("DealPass after land: %v", err)
	}
	if passRes2.Dealt != 1 {
		t.Fatalf("DealPass after land dealt = %d, want 1", passRes2.Dealt)
	}

	clean(t, c, "after TestDealPassHoldsLiveOverlapInSameStream")
}
