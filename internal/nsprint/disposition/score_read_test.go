package disposition_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// readFixture (#3612) is one unit authored by johnny at head, CI OK for the
// expected identity, and a policy that needs one read at land_bar 8, so the
// evaluator reaches the reads condition and `why` prints its reads line.
type readFixture struct {
	ctx  context.Context
	c    *redis.Client
	head string
}

const (
	readS    = "s3612"
	readUnit = "u-3612"
)

func newReadFixture(t *testing.T) readFixture {
	t.Helper()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("b", 40)
	c.HSet(ctx, "machine:ctl:ceiling", "slots", 64)
	for _, f := range []string{"stella", "johnny", "rowan"} {
		if r, err := c.FCall(ctx, "ns_capacity_desired", nil, "friend", f, 0, "ctl", "config", "").StringSlice(); err != nil || len(r) == 0 || r[0] != "SET" {
			t.Fatalf("capacity friend %s: %v %v", f, r, err)
		}
		if _, err := life.Hello(ctx, store.New(c), life.HelloRequest{As: f, Actor: f, Slots: -1, Host: "ctl", Session: "ctl-" + f}); err != nil {
			t.Fatal(err)
		}
	}
	c.HSet(ctx, "s:"+readS+":policy", "fix_to", "rowan", "release_reader", "stella")
	if _, err := land.CallUnitHead(ctx, c, land.UnitHeadParams{Sprint: readS, Unit: readUnit, Repo: "nova-tools", Base: "dev",
		Branch: "b-3612", Head: head, BaseSHA: head, PR: "3612", Author: "johnny"}); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, land.PolicyKey("nova-tools", "dev"), "readers", "1", "land_bar", "8",
		"policy_id", "p1", "required_set_id", "r1", "runner_id", "x1")
	gid := land.GID("single", "dev", head, "r1", "p1", "x1")
	c.HSet(ctx, land.CIKey("nova-tools", head, gid), "verdict", "OK")
	return readFixture{ctx: ctx, c: c, head: head}
}

func (f readFixture) ingest(t *testing.T, body string) disposition.IngestResult {
	t.Helper()
	r, err := disposition.Ingest(f.ctx, f.c, disposition.IngestRequest{Sprint: readS, Repo: "mas-bandwidth/nova-tools", PR: 3612,
		URL: "mas-bandwidth/nova-tools/pull/3612#issuecomment-1", Body: body, Actor: "rowan"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// readsLine is `why`'s reads line and the evaluator's count at head.
func (f readFixture) readsLine(t *testing.T) (string, int) {
	t.Helper()
	u, err := land.LoadUnit(f.ctx, f.c, readS, readUnit)
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range land.Why(u, time.Now()) {
		if strings.HasPrefix(l, "reads ") {
			line = l
		}
	}
	res, err := land.EvaluateUnit(f.ctx, f.c, readS, readUnit, &land.BasePolicy{Readers: 1, LandBar: 8})
	if err != nil {
		t.Fatal(err)
	}
	return line, res.ReadsAtHead
}

// TestScoreLineCountsAsRead: the read rubric's SCORE line from a non-author
// at land_bar or above is a read for `why` and for the evaluator; the
// author's SCORE and one under the bar are named and not counted; a SCORE
// line with a verdict= key or no score is refused, never guessed.
func TestScoreLineCountsAsRead(t *testing.T) {
	t.Parallel()

	f := newReadFixture(t)
	h := f.head
	if got := disposition.Parse("SCORE who=stella head=" + h + " verdict=APPROVE score=9").String(); got != "REFUSED unknown-key verdict" {
		t.Fatalf("SCORE with verdict= parsed %q", got)
	}
	if got := disposition.Parse("SCORE who=stella head=" + h + " gates=ci:ok: fine").String(); got != "REFUSED missing score" {
		t.Fatalf("SCORE without score parsed %q", got)
	}
	if line, n := f.readsLine(t); !strings.HasPrefix(line, "reads 0/1") || n != 0 {
		t.Fatalf("before any line: %q count %d", line, n)
	}
	for _, body := range []string{
		"SCORE who=johnny head=" + h + " score=10/10 gates=ci:ok,base:ok,scope:ok: my own PR\n",
		"SCORE who=rowan head=" + h + " score=6/10 gates=ci:ok,base:ok,scope:ok: thin tests\n",
		"SCORE who=stella head=" + h + " score=10/10 gates=ci:ok,base:ok,scope:ok: the DONE-WHEN test fails on dev\n\nbody",
	} {
		if r := f.ingest(t, body); r.Outcome != disposition.Record || r.Exit() != 0 {
			t.Fatalf("ingest %q: %s", body, r)
		}
	}
	p := disposition.Parse("SCORE who=stella head=" + h + " score=10/10 gates=ci:ok,base:ok,scope:ok: tail")
	if p.Line.Type != disposition.TypeScore || p.Line.Verdict != "APPROVE" || p.Line.Score != 10 || p.Line.Gates != "ci:ok,base:ok,scope:ok" || p.Line.Tail != "tail" {
		t.Fatalf("parse %+v", p.Line)
	}
	line, n := f.readsLine(t)
	want := "reads 1/1 (stella 10 @" + h[:8] + ")"
	if !strings.HasPrefix(line, want) || !strings.Contains(line, "johnny 10 (author)") || !strings.Contains(line, "rowan 6 (<8)") {
		t.Fatalf("why reads line %q, want prefix %q naming johnny (author) and rowan (<8)", line, want)
	}
	if n != 1 {
		t.Fatalf("evaluator reads at head %d, want 1 (stella; the author's SCORE never counts)", n)
	}
}

// TestHoldLineNeverCounts: the read rubric's HOLD line is a HOLD record
// (a hold opens), whatever score it carries, and never counts as a read.
func TestHoldLineNeverCounts(t *testing.T) {
	t.Parallel()

	f := newReadFixture(t)
	h := f.head
	if p := disposition.Parse("HOLD who=stella head=" + h + ": no score"); p.Outcome != disposition.Record || p.Line.Type != disposition.TypeHold || p.Line.Verdict != "HOLD" || p.Line.Score != 0 {
		t.Fatalf("HOLD without score: %s %+v", p, p.Line)
	}
	r := f.ingest(t, "HOLD who=stella head="+h+" score=9/10 gates=ci:ok,base:ok,scope:ok: the verb is never called at cmd/x/y.go:4\n")
	if r.Outcome != disposition.Record {
		t.Fatalf("ingest HOLD: %s", r)
	}
	if rec := f.c.HGetAll(f.ctx, land.ReadKey(readS, readUnit, "stella")).Val(); rec["verdict"] != "HOLD" || rec["score"] != "9" || rec["head"] != h {
		t.Fatalf("read record %v", rec)
	}
	if f.c.Exists(f.ctx, "s:"+readS+":hold:"+readUnit+":stella").Val() != 1 {
		t.Fatal("a HOLD line opened no hold")
	}
	line, n := f.readsLine(t)
	if !strings.HasPrefix(line, "reads 0/1") || !strings.Contains(line, "stella HOLD") || n != 0 {
		t.Fatalf("why reads line %q count %d; want reads 0/1 naming stella HOLD, count 0", line, n)
	}
}
