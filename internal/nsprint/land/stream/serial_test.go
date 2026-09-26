package stream

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestLoadConfigPartial: cfg:land partial is the duty's switch.
func TestLoadConfigPartial(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if cfg, err := LoadConfig(ctx, c, "o/r"); err != nil || cfg.Partial || cfg.MinScore != 8 {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	c.HSet(ctx, "cfg:land", "partial", "1")
	if cfg, err := LoadConfig(ctx, c, "o/r"); err != nil || !cfg.Partial {
		t.Fatalf("partial=1: %+v %v", cfg, err)
	}
	c.HSet(ctx, "cfg:land", "partial", "off")
	if cfg, err := LoadConfig(ctx, c, "o/r"); err != nil || cfg.Partial {
		t.Fatalf("partial=off: %+v %v", cfg, err)
	}
}

// TestStepsPrintTheWall: every step line carries the ms since the last step
// on the injected clock; Total is the whole.
func TestStepsPrintTheWall(t *testing.T) {
	t.Parallel()
	now := time.UnixMilli(1700000000000)
	clock := func() time.Time { now = now.Add(250 * time.Millisecond); return now }
	var out bytes.Buffer
	st := NewSteps(&out, clock)
	st.Line("REBASED #%d at %s onto %s", 7, "abcdef12", "stream/x")
	st.Line("PR #%d opened base=%s members=%d", 900, "dev", 2)
	want := "REBASED #7 at abcdef12 onto stream/x ms=250\nPR #900 opened base=dev members=2 ms=250\n"
	if out.String() != want {
		t.Fatalf("lines:\n%s", out.String())
	}
	if st.Total() != 750*time.Millisecond {
		t.Fatalf("total %s", st.Total())
	}
	var nilSteps *Steps
	nilSteps.Line("nothing") // a nil clock prints nothing and never panics
	if got := SerialLine([]string{"a b"}, 1, []Skip{{Task: "t9", Why: "no-pr"}, {Task: "t4", N: 4, Why: "hold:emma"}}); got != `LAND-SERIAL stream=a\x20b carrying=1 merging=3 left_out=t9:no-pr,#4:hold:emma` {
		t.Fatalf("serial line %q", got)
	}
}

// TestLandingFieldsCarryTheSerialReceipt: the LAND-SERIAL line a --partial
// landing was allowed with, who allowed it and when, survive the land hash
// round trip; a landing with none writes no such field.
func TestLandingFieldsCarryTheSerialReceipt(t *testing.T) {
	t.Parallel()
	l := Landing{Repo: "o/r", Slug: "s", Streams: "s", Base: "dev", State: "open", PR: 9,
		Serial: "LAND-SERIAL stream=s carrying=1 merging=2 left_out=#2:red:x", PartialBy: "rowan", PartialAt: "1700000000000"}
	got := landingFrom("o/r", l.fields())
	if got.Serial != l.Serial || got.PartialBy != "rowan" || got.PartialAt != "1700000000000" || got.PR != 9 {
		t.Fatalf("round trip: %+v", got)
	}
	if f := (Landing{Repo: "o/r", Slug: "s"}).fields(); f["serial"] != "" || f["partial_by"] != "" {
		t.Fatalf("empty serial written: %v", f)
	}
}
