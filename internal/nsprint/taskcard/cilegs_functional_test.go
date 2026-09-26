//go:build functional

package taskcard_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestDealAndFillTakeCILegsOffABenchsSlots (nova-tools#4293, in Redis): a
// bench with 8 slots whose beat counts 4 CI legs is dealt 4 and filled to
// 4, not 8; when the legs end (ci 0) the next pass fills the other 4; a
// friend's slots shrink by its own beat's ci the same way (the Studio
// hosts friends and CI both).
func TestDealAndFillTakeCILegsOffABenchsSlots(t *testing.T) {
	t.Parallel()

	c := start(t)
	ctx := context.Background()
	now := time.Now()
	at := strconv.FormatInt(now.UnixMilli(), 10)
	b, f := mustConsumer(t, "bench:b"), mustConsumer(t, "friend:f")
	for _, k := range []taskcard.Consumer{b, f} {
		if err := taskcard.Enroll(ctx, c, k, true); err != nil {
			t.Fatal(err)
		}
	}
	c.HSet(ctx, b.BeatKey(), "at", at, "ci", "4")
	c.HSet(ctx, f.BeatKey(), "at", at)
	c.HSet(ctx, f.MachineBeatKey(), "ci", "1") // one leg beside the friend: one of its two slots
	c.HSet(ctx, b.DesiredKey(), "slots", "8")
	c.HSet(ctx, f.DesiredKey(), "slots", "2")
	pushPrimaries(t, c, 20)

	r, err := taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Dealt != 5 {
		t.Fatalf("dealt %d, want 4 to the bench (8 slots - 4 legs) + 1 to the friend (2 - 1): %v", r.Dealt, r.Lines)
	}
	if !hasLine(r.Lines, "DEAL bench:b dealt=4 free=4 ci=4") || !hasLine(r.Lines, "DEAL friend:f dealt=1 free=1 ci=1") {
		t.Fatalf("lines %v", r.Lines)
	}
	// The bench's own fill (card work --fill) computes the same in Redis.
	w, err := taskcard.Work(ctx, c, b, "b", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.IDs) != 4 || w.Free != 0 {
		t.Fatalf("fill took %d copies with %d free, want 4 and 0", len(w.IDs), w.Free)
	}
	// A named take past the CI-shrunk slots is FULL, and says so.
	c.HSet(ctx, b.BeatKey(), "ci", "5")
	more, err := taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil || more.Dealt != 0 {
		t.Fatalf("a bench past its slots was dealt %d: %v %v", more.Dealt, more.Lines, err)
	}
	// The legs end: the next pass deals and the fill takes the other four.
	c.HSet(ctx, b.BeatKey(), "ci", "0")
	r, err = taskcard.DealPass(ctx, c, "reconciler", now)
	if err != nil || r.Dealt != 4 {
		t.Fatalf("after the legs ended dealt %d, want 4: %v %v", r.Dealt, r.Lines, err)
	}
	if w, err = taskcard.Work(ctx, c, b, "b", 0, true); err != nil || len(w.IDs) != 4 || w.Free != 0 {
		t.Fatalf("refill %+v %v", w, err)
	}
	if n := c.ZCard(ctx, b.Key("working")).Val(); n != 8 {
		t.Fatalf("working %d, want 8", n)
	}
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}
