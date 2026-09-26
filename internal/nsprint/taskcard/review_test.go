//go:build functional

package taskcard_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// reviewPost is `review post` at the Lua function (ns_cm_review): the one
// way out of review. The reply is REVIEWED id verdict to copy, or REFUSED why.
func reviewPost(t *testing.T, c *redis.Client, id, verdict, why string) []string {
	t.Helper()
	v, err := c.FCall(context.Background(), "ns_cm_review", nil, "rowan", id, verdict, why).Result()
	if err != nil {
		t.Fatalf("ns_cm_review %s %s: %v", id, verdict, err)
	}
	rows, ok := v.([]any)
	if !ok {
		t.Fatalf("ns_cm_review reply %T %v", v, v)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprint(r)
	}
	return out
}

// benchWithSlots is bench:<name> in the benches SET with slots and a live beat.
func benchWithSlots(t *testing.T, c *redis.Client, name string, slots int) taskcard.Consumer {
	t.Helper()
	ctx := context.Background()
	k := mustConsumer(t, "bench:"+name)
	c.SAdd(ctx, "benches", name)
	c.HSet(ctx, k.DesiredKey(), "slots", strconv.Itoa(slots))
	c.HSet(ctx, k.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	return k
}

// dealWork deals primary id to k and starts its copy; it returns the copy.
func dealWork(t *testing.T, c *redis.Client, k taskcard.Consumer, id string) string {
	t.Helper()
	ctx := context.Background()
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{id}, By: "rowan"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal %s to %s: %v %v", id, k, d, err)
	}
	w, err := taskcard.Work(ctx, c, k, "rowan", 0, false, d[0].Copy)
	if err != nil || len(w.IDs) != 1 {
		t.Fatalf("work %s: %+v %v", d[0].Copy, w, err)
	}
	return d[0].Copy
}

// failExit1 ends copy cp as a bench crash: exit 1, its model, wall and last
// typed line on the end.
func failExit1(t *testing.T, c *redis.Client, cp string) taskcard.Ended {
	t.Helper()
	e, err := taskcard.End(context.Background(), c, taskcard.EndRequest{IDs: []string{cp}, Why: "child exit 1", By: "b",
		Fields: []string{"exit", "1", "model", "kimi-k3", "wall", "412s", "line2", "BLOCKED: go test ./internal/x red"}})
	if err != nil || len(e) != 1 {
		t.Fatalf("end --fail %s: %v %v", cp, e, err)
	}
	return e[0]
}

// TestControl4072FailedCopyGoesToReview is the #4072 DONE-WHEN fixture: a
// bench copy that fails with exit 1 lands its PRIMARY in ws:<stream>:review
// (the copy in bench:<b>:cards:fail) with the evidence on the record
// (consumer, model, exit, the last typed line, the wall) and the mechanical
// first pass (REVIEW-JEV: shape, same-shape counts, a suggested verdict,
// never a verdict); nothing but a typed `review post` verdict moves it out
// (a deal, a hand task move and a cancel are refused); redeal returns it to
// ready; and a second fail on the same consumer records same_shape=2.
func TestControl4072FailedCopyGoesToReview(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	k := benchWithSlots(t, c, "b", 2)
	ids := pushPrimaries(t, c, 1)
	id := ids[0]

	cp := dealWork(t, c, k, id)
	if e := failExit1(t, c, cp); e.To != "review" || e.Primary != id {
		t.Fatalf("a failed copy's primary went to %q, want review: %+v", e.To, e)
	}
	wantWS(t, c, "fail", map[string]int64{"review": 1, "waiting": 0, "working": 0, "ready": 0})
	wantCells(t, cellsOf(t, c, k), "fail", 0, 0, 0, 1)
	p := c.HGetAll(ctx, taskcard.Key(id)).Val()
	for f, want := range map[string]string{
		"where": "review", "state": "review", "copy": "", "review_copy": cp, "review_consumer": "bench:b",
		"review_model": "kimi-k3", "review_exit": "1", "review_line": "BLOCKED: go test ./internal/x red",
		"review_wall": "412s", "review_why": "child exit 1", "review_leg": "work", "review_shape": "exit-1",
		"same_shape": "1", "same_shape_consumer": "1",
	} {
		if p[f] != want {
			t.Errorf("fail: primary %s=%q, want %q", f, p[f], want)
		}
	}
	if !strings.HasPrefix(p["review_jev"], "REVIEW-JEV id="+id+" consumer=bench:b shape=exit-1 same_card=1 same_consumer=1 suggest=") {
		t.Errorf("fail: review_jev %q", p["review_jev"])
	}
	if h := c.HGetAll(ctx, taskcard.Key(cp)).Val(); h["where"] != "fail" || h["outcome"] != "fail" {
		t.Errorf("fail: copy %v", h)
	}
	if t.Failed() {
		t.FailNow()
	}
	cleanMoves(t, c, "fail")

	// Review is left only by a typed verdict: nothing else moves the card.
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{id}, By: "rowan"}); err == nil {
		t.Fatal("a primary in review was dealt")
	}
	if _, err := taskcard.Move(ctx, c, id, "ready", taskcard.Opts{By: "rowan", Why: "by hand"}); !isRefusedWith(err, "REVIEW") {
		t.Fatalf("a hand task move out of review: %v", err)
	}
	if _, err := taskcard.CancelCards(ctx, c, "rowan", "tidy", id); !isRefusedWith(err, "REVIEW") {
		t.Fatalf("a cancel out of review: %v", err)
	}
	if got := reviewPost(t, c, id, "maybe", "unsure"); got[0] != "REFUSED" || !strings.HasPrefix(got[1], "VERDICT") {
		t.Fatalf("an untyped verdict: %v", got)
	}
	if got := reviewPost(t, c, id, "redeal", ""); got[0] != "REFUSED" || !strings.HasPrefix(got[1], "WHY") {
		t.Fatalf("a verdict with no why: %v", got)
	}
	wantWS(t, c, "refused", map[string]int64{"review": 1, "ready": 0})

	// redeal: back to ready for the same route, with the REVIEW line.
	if got := reviewPost(t, c, id, "redeal", "transient: the bench was swapping"); strings.Join(got, " ") != "REVIEWED "+id+" redeal ready " {
		t.Fatalf("redeal: %v", got)
	}
	wantWS(t, c, "redeal", map[string]int64{"review": 0, "ready": 1})
	if h := c.HGetAll(ctx, taskcard.Key(id)).Val(); h["review_verdict"] != "redeal" ||
		h["review"] != "REVIEW verdict=redeal by=rowan: transient: the bench was swapping" {
		t.Fatalf("redeal record %v", h)
	}
	cleanMoves(t, c, "redeal")

	// The same failure again on the same consumer: same_shape=2, and the
	// next copy carried the REVIEW line to its consumer.
	cp2 := dealWork(t, c, k, id)
	if got := c.HGet(ctx, taskcard.Key(cp2), "review").Val(); !strings.HasPrefix(got, "REVIEW verdict=redeal") {
		t.Fatalf("the next copy does not carry the REVIEW line: %q", got)
	}
	if e := failExit1(t, c, cp2); e.To != "review" {
		t.Fatalf("second fail: %+v", e)
	}
	p = c.HGetAll(ctx, taskcard.Key(id)).Val()
	if p["same_shape"] != "2" || p["same_shape_consumer"] != "2" || p["review_copy"] != cp2 ||
		!strings.HasSuffix(p["review_jev"], " same_card=2 same_consumer=2 suggest=reassign") {
		t.Fatalf("second fail: same_shape=%q same_shape_consumer=%q jev=%q", p["same_shape"], p["same_shape_consumer"], p["review_jev"])
	}
	wantCells(t, cellsOf(t, c, k), "second fail", 0, 0, 0, 2)
	cleanMoves(t, c, "second fail")
}

// TestReviewVerdicts: recut returns the card to waiting with the REVIEW
// line; reassign:<consumer> cuts its copy on the named consumer; drop moves
// it to landed with outcome=dropped. A primary not in review refuses every
// verdict.
func TestReviewVerdicts(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	k := benchWithSlots(t, c, "b", 4)
	other := benchWithSlots(t, c, "other", 4)
	ids := pushPrimaries(t, c, 3)
	if got := reviewPost(t, c, ids[0], "redeal", "not failed"); got[0] != "REFUSED" || !strings.HasPrefix(got[1], "NOTREVIEW") {
		t.Fatalf("a verdict on a waiting card: %v", got)
	}
	for _, id := range ids {
		failExit1(t, c, dealWork(t, c, k, id))
	}
	wantWS(t, c, "three fails", map[string]int64{"review": 3})

	if got := reviewPost(t, c, ids[0], "recut", "PATHS too narrow: add internal/y"); strings.Join(got, " ") != "REVIEWED "+ids[0]+" recut waiting " {
		t.Fatalf("recut: %v", got)
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[0])).Val(); h["where"] != "waiting" || h["review"] != "REVIEW verdict=recut by=rowan: PATHS too narrow: add internal/y" {
		t.Fatalf("recut record %v", h)
	}

	if got := reviewPost(t, c, ids[1], "reassign:bench:nope", "x"); got[0] != "REFUSED" {
		t.Fatalf("reassign to a consumer with no slots: %v", got)
	}
	got := reviewPost(t, c, ids[1], "reassign:"+other.String(), "kimi fails this shape; try another bench")
	if len(got) != 5 || got[0] != "REVIEWED" || got[2] != "reassign" || got[3] != "working" || got[4] != ids[1]+"~2" {
		t.Fatalf("reassign: %v", got)
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[1]+"~2")).Val(); h["consumer"] != other.String() || h["where"] != "ready" {
		t.Fatalf("reassigned copy %v", h)
	}

	if got := reviewPost(t, c, ids[2], "drop", "superseded by #4100"); strings.Join(got, " ") != "REVIEWED "+ids[2]+" drop landed " {
		t.Fatalf("drop: %v", got)
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[2])).Val(); h["where"] != "landed" || h["outcome"] != "dropped" || h["review_why"] == "" {
		t.Fatalf("dropped record %v", h)
	}
	wantWS(t, c, "verdicts", map[string]int64{"review": 0, "waiting": 1, "working": 1, "landed": 1})
	cleanMoves(t, c, "verdicts")
}

// TestLeaseAbandonedGoesToReview: a friend's copy whose lease lapses is a
// fail (lease abandoned): its primary goes to review, shape lease-lapsed.
// A copy given back (a cancel) is not a fail: its primary returns.
func TestLeaseAbandonedGoesToReview(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	k := mustConsumer(t, "friend:f")
	c.SAdd(ctx, "friends", "f")
	c.HSet(ctx, k.DesiredKey(), "slots", "2")
	ids := pushPrimaries(t, c, 2)
	cp := dealWork(t, c, k, ids[0])
	given := dealWork(t, c, k, ids[1])
	c.HSet(ctx, taskcard.Key(cp), "lease_until", "1")
	x, err := taskcard.ExpireCopies(ctx, c, "reconciler", k)
	if err != nil || len(x) != 1 || x[0].To != "review" {
		t.Fatalf("expire %v %v", x, err)
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[0])).Val(); h["review_shape"] != "lease-lapsed" || h["review_consumer"] != "friend:f" {
		t.Fatalf("expired primary %v", h)
	}
	e, err := taskcard.CancelCards(ctx, c, "rowan", "moved", given)
	if err != nil || e[0].To != "waiting" {
		t.Fatalf("give back %v %v", e, err)
	}
	wantCells(t, cellsOf(t, c, k), "expire", 0, 0, 0, 2)
	cleanMoves(t, c, "expire")
}

// TestReadUnderEightTwiceGoesToReview: a read under 8 cuts the author's fix
// copy; a second read under 8 moves the primary to review (the author's
// shape read-under-8-twice) and the author's fix copy counts as a fail.
func TestReadUnderEightTwiceGoesToReview(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	k := benchWithSlots(t, c, "b", 4)
	rd := mustConsumer(t, "friend:reader")
	c.SAdd(ctx, "friends", "reader")
	c.HSet(ctx, "friend:reader:roles", "roles", "reader") // a read copy goes to a friend with the reader role (#4094)
	c.HSet(ctx, rd.DesiredKey(), "slots", "4")
	c.HSet(ctx, rd.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	for _, x := range []taskcard.Consumer{k, rd} {
		if err := taskcard.Enroll(ctx, c, x, true); err != nil {
			t.Fatal(err)
		}
	}
	c.HSet(ctx, "cfg:ci", "max_attempts", "1")
	ids := pushPrimaries(t, c, 1)
	cp := dealWork(t, c, k, ids[0])
	recordPR(c, 6000, head(10))
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{cp}, OK: true, Repo: "nova-tools", PR: "6000", Head: head(10), By: "b"})
	if err != nil || e[0].To != "review" || e[0].Next == "" {
		t.Fatalf("end ok %v %v", e, err)
	}
	read1 := e[0].Next
	ciVerdict(t, c, 6000, head(10), "0")
	if _, err := taskcard.Work(ctx, c, rd, "reader", 0, true); err != nil {
		t.Fatal(err)
	}
	e, err = taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{read1}, OK: true, Score: 5, Finding: "no test", By: "reader"})
	// the first read under 8: the primary stays in review with one fix copy
	// cut to the author (#4097)
	if err != nil || e[0].To != "review" || e[0].Next == "" {
		t.Fatalf("first read under 8: %v %v", e, err)
	}
	fix := e[0].Next
	if _, err := taskcard.Work(ctx, c, k, "b", 0, false, fix); err != nil {
		t.Fatal(err)
	}
	recordPR(c, 6000, head(11))
	e, err = taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{fix}, OK: true, Repo: "nova-tools", PR: "6000", Head: head(11), By: "b"})
	if err != nil || e[0].To != "review" || e[0].Next == "" {
		t.Fatalf("fix ok %v %v", e, err)
	}
	read2 := e[0].Next
	ciVerdict(t, c, 6000, head(11), "0")
	if _, err := taskcard.Work(ctx, c, rd, "reader", 0, true); err != nil {
		t.Fatal(err)
	}
	e, err = taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{read2}, OK: true, Score: 4, Finding: "still no test", By: "reader"})
	if err != nil || e[0].To != "review" || e[0].Next != "" {
		t.Fatalf("second read under 8: %v %v", e, err)
	}
	p := c.HGetAll(ctx, taskcard.Key(ids[0])).Val()
	if p["where"] != "review" || p["review_shape"] != "read-under-8-twice" || p["review_consumer"] != "bench:b" ||
		p["review_copy"] != fix || p["review_pr"] != "nova-tools#6000" || p["review_read"] != "4" {
		t.Fatalf("primary %v", p)
	}
	// the author: the work copy ok, the fix copy a fail; the reader: two ok reads
	wantCells(t, cellsOf(t, c, k), "author", 0, 0, 1, 1)
	wantCells(t, cellsOf(t, c, rd), "reader", 0, 0, 2, 0)
	if h := c.HGetAll(ctx, taskcard.Key(fix)).Val(); h["where"] != "fail" || h["outcome"] != "fail" {
		t.Fatalf("fix copy %v", h)
	}
	cleanMoves(t, c, "read under 8 twice")
}
