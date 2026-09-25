package taskcard_test

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// The reading column's machinery (nova-tools #4094, #4097; Glenn 2026-09-25
// 4:50 PM ET: "As soon as something new lands in reading, it should
// generate friend/swarm work cards that back reference to it."): the move
// of a primary into reading cuts its read copies in the same call (one to a
// friend reader with room, else four on the swarm), each naming the
// primary and the head; a SCORE 8+ at the record head ends the copy and
// moves the PRIMARY to merging in the same call; a SCORE under 8 ends the
// copy ok with its finding, leaves the primary in reading and cuts one fix
// copy on the author's queue; the fix's new head (its card end, or pr
// record --head) cuts fresh read copies; a head move retires the open read
// copies and cuts fresh ones.

// enroll makes k a live consumer with slots; a friend gets roles (the
// friend:<f>:roles csv, e.g. reader) and joins friends, a bench joins
// benches.
func enroll(t *testing.T, c *redis.Client, s string, slots int, roles string) taskcard.Consumer {
	t.Helper()
	ctx := context.Background()
	k := mustConsumer(t, s)
	c.HSet(ctx, k.DesiredKey(), "slots", strconv.Itoa(slots))
	if err := taskcard.Enroll(ctx, c, k, true); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, k.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if k.Kind == "friend" {
		c.SAdd(ctx, "friends", k.Name)
		if roles != "" {
			c.HSet(ctx, "friend:"+k.Name+":roles", "roles", roles)
		}
	} else {
		c.SAdd(ctx, "benches", k.Name)
	}
	return k
}

// toReading deals primary id to its author, works it and ends it ok with PR
// pr at h: the move into reading. It returns the end.
func toReading(t *testing.T, c *redis.Client, author taskcard.Consumer, id string, pr int, h string) taskcard.Ended {
	t.Helper()
	ctx := context.Background()
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: author, IDs: []string{id}, By: "rowan"})
	if err != nil {
		t.Fatalf("deal %s: %v", id, err)
	}
	if _, err := taskcard.Work(ctx, c, author, "rowan", 0, false, d[0].Copy); err != nil {
		t.Fatalf("work %s: %v", d[0].Copy, err)
	}
	recordPR(c, pr, h)
	calls := countCalls(c)
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, OK: true, Repo: "nova-tools",
		PR: strconv.Itoa(pr), Head: h, By: author.Name})
	if err != nil || len(e) != 1 {
		t.Fatalf("end ok %s: %v %v", d[0].Copy, e, err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("the move into reading took %d calls, want 1 (the read copies are cut in the same call)", n)
	}
	return e[0]
}

func split(next string) []string {
	if next == "" || next == "-" {
		return nil
	}
	return strings.Split(next, ",")
}

// liveReads is the primary's reads field (its live read copies) as a
// sorted list.
func liveReads(c *redis.Client, id string) []string {
	f := strings.Fields(c.HGet(context.Background(), taskcard.Key(id), "reads").Val())
	sort.Strings(f)
	return f
}

func rec(c *redis.Client, id string) map[string]string {
	return c.HGetAll(context.Background(), taskcard.Key(id)).Val()
}

func post(t *testing.T, c *redis.Client, n int, line string) string {
	t.Helper()
	var out, errOut strings.Builder
	if code := read.Post(context.Background(), c, "nova-tools", strconv.Itoa(n), line, nil, &out, &errOut); code != 0 {
		t.Fatalf("read post %q: exit %d %q", line, code, errOut.String())
	}
	return out.String() + errOut.String()
}

// workCopy moves one copy ready -> working on its own consumer.
func workCopy(t *testing.T, c *redis.Client, id string) {
	t.Helper()
	k := mustConsumer(t, rec(c, id)["consumer"])
	if _, err := taskcard.Work(context.Background(), c, k, k.Name, 0, false, id); err != nil {
		t.Fatalf("work %s: %v", id, err)
	}
}

func ciOK(c *redis.Client, h string) {
	c.HSet(context.Background(), "ci:nova-tools:"+h, "final", "OK", "ci", "green")
}

// TestReadingCutsAFriendReaderCopy (#4094 DONE-WHEN 1, 2): a friend with
// the reader role and open slots gets the one read copy, cut in the same
// call as the move into reading; the copy names its primary, kind read,
// the PR and the head; the primary names it.
func TestReadingCutsAFriendReaderCopy(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	author := enroll(t, c, "bench:b", 2, "")
	emma := enroll(t, c, "friend:emma", 2, "reader")
	enroll(t, c, "bench:s1", 4, "")
	ids := pushPrimaries(t, c, 1)
	e := toReading(t, c, author, ids[0], 8100, head(0))
	cp := split(e.Next)
	if e.To != "reading" || len(cp) != 1 {
		t.Fatalf("end ok: to=%s next=%q, want reading and one read copy (a friend reader has room)", e.To, e.Next)
	}
	r := rec(c, cp[0])
	if r["primary"] != ids[0] || r["kind"] != "read" || r["leg"] != "read" || r["consumer"] != emma.String() ||
		r["pr"] != "8100" || r["head"] != head(0) || r["where"] != "ready" {
		t.Fatalf("read copy %v", r)
	}
	if got := liveReads(c, ids[0]); len(got) != 1 || got[0] != cp[0] {
		t.Fatalf("primary reads=%v, want [%s]", got, cp[0])
	}
	if c.ZScore(ctx, emma.Key("ready"), cp[0]).Err() != nil {
		t.Fatalf("%s is not in %s", cp[0], emma.Key("ready"))
	}
	cleanMoves(t, c, "friend read")
}

// TestReadingCutsFourSwarmReadCopies (#4094 DONE-WHEN 2): with no friend
// reader with room, the move into reading cuts four read copies on the
// swarm (never on the author), each naming the primary and the head.
func TestReadingCutsFourSwarmReadCopies(t *testing.T) {
	t.Parallel()
	c := start(t)
	author := enroll(t, c, "bench:b", 2, "")
	enroll(t, c, "friend:emma", 0, "reader") // a reader with no open slot
	enroll(t, c, "bench:s1", 4, "")
	enroll(t, c, "bench:s2", 4, "")
	ids := pushPrimaries(t, c, 1)
	e := toReading(t, c, author, ids[0], 8200, head(0))
	cp := split(e.Next)
	if e.To != "reading" || len(cp) != 4 {
		t.Fatalf("end ok: to=%s next=%q, want reading and four swarm read copies", e.To, e.Next)
	}
	on := map[string]int{}
	for _, id := range cp {
		r := rec(c, id)
		if r["primary"] != ids[0] || r["kind"] != "read" || r["head"] != head(0) || r["pr"] != "8200" {
			t.Fatalf("read copy %s %v", id, r)
		}
		on[r["consumer"]]++
	}
	if on["bench:b"] != 0 || on["bench:s1"] != 2 || on["bench:s2"] != 2 {
		t.Fatalf("read copies by consumer %v, want two each on s1 and s2, none on the author", on)
	}
	if got := liveReads(c, ids[0]); len(got) != 4 {
		t.Fatalf("primary reads=%v, want the four copies", got)
	}
	cleanMoves(t, c, "swarm reads")

	// the first 8+ advances the primary; the other three copies retire
	ciOK(c, head(0))
	workCopy(t, c, cp[2])
	calls := countCalls(c)
	x, err := taskcard.End(context.Background(), c, taskcard.EndRequest{IDs: []string{cp[2]}, OK: true, Score: 9, By: "s2"})
	if err != nil || x[0].To != "merging" {
		t.Fatalf("score 9: %v %v", x, err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("score took %d calls, want 1", n)
	}
	if p := rec(c, ids[0]); p["where"] != "merging" || p["reads"] != "" || p["copy"] != "" {
		t.Fatalf("primary after a 9: %v", p)
	}
	for i, id := range cp {
		want := "fail"
		if i == 2 {
			want = "ok"
		}
		if w := rec(c, id)["where"]; w != want {
			t.Fatalf("copy %s is %s after the 9, want %s", id, w, want)
		}
	}
	cleanMoves(t, c, "swarm score")
}

// TestReadPostScoreAdvancesThePrimary (#4094 DONE-WHEN 3): read post with
// a SCORE 9 at the record head ends the reader's copy ok and moves the
// PRIMARY reading -> merging in the same call.
func TestReadPostScoreAdvancesThePrimary(t *testing.T) {
	t.Parallel()
	c := start(t)
	author := enroll(t, c, "bench:b", 2, "")
	enroll(t, c, "friend:emma", 2, "reader")
	ids := pushPrimaries(t, c, 1)
	e := toReading(t, c, author, ids[0], 8300, head(0))
	cp := split(e.Next)
	if len(cp) != 1 {
		t.Fatalf("next=%q", e.Next)
	}
	ciOK(c, head(0))
	calls := countCalls(c)
	out := post(t, c, 8300, "SCORE who=emma head="+head(0)+" score=9/10 gates=ci:green,base:ok,scope:ok")
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("read post took %d calls, want 1", n)
	}
	if !strings.Contains(out, "tasks_moved=1") {
		t.Fatalf("read post: %q", out)
	}
	if p := rec(c, ids[0]); p["where"] != "merging" || p["score"] != "9" || p["reads"] != "" {
		t.Fatalf("primary after SCORE 9: %v", p)
	}
	if r := rec(c, cp[0]); r["where"] != "ok" || r["score"] != "9" {
		t.Fatalf("read copy after SCORE 9: %v", r)
	}
	if reads := c.HGet(context.Background(), "pr:nova-tools:8300", "reads").Val(); !strings.Contains(reads, "score=9/10") {
		t.Fatalf("pr record reads %q", reads)
	}
	cleanMoves(t, c, "read post 9")
}

// TestUnderEightCutsAFixCopy (#4097 DONE-WHEN): a SCORE 6 at the record
// head ends the read copy ok with its finding, leaves the primary in
// reading and cuts one fix copy (kind fix) on the author's queue carrying
// the PR, the head, the SCORE line as its finding and the DONE-WHEN; a pr
// record --head after it (the fix's new head) ends the fix copy ok and cuts
// one fresh read copy at the new head.
func TestUnderEightCutsAFixCopy(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	author := enroll(t, c, "friend:f", 2, "builder")
	emma := enroll(t, c, "friend:emma", 2, "reader")
	ids := pushPrimaries(t, c, 1)
	e := toReading(t, c, author, ids[0], 8400, head(0))
	cp := split(e.Next)
	if len(cp) != 1 {
		t.Fatalf("next=%q", e.Next)
	}
	line := "SCORE who=emma head=" + head(0) + " score=6/10 gates=ci:green,base:ok,scope:ok: the test does not fail without the fix"
	out := post(t, c, 8400, line)
	if !strings.Contains(out, "tasks_moved=0") || !strings.Contains(out, "copies_cut=1") {
		t.Fatalf("read post 6: %q", out)
	}
	p := rec(c, ids[0])
	if p["where"] != "reading" || p["copy"] == "" || p["reads"] != "" {
		t.Fatalf("primary after SCORE 6: %v", p)
	}
	fix := rec(c, p["copy"])
	if fix["kind"] != "fix" || fix["leg"] != "fix" || fix["primary"] != ids[0] || fix["consumer"] != author.String() ||
		fix["pr"] != "8400" || fix["head"] != head(0) || fix["finding"] != line ||
		fix["done_when"] != "go test ./internal/x -run TestX passes" || fix["where"] != "ready" {
		t.Fatalf("fix copy %v", fix)
	}
	if c.ZScore(ctx, author.Key("ready"), p["copy"]).Err() != nil {
		t.Fatalf("fix copy %s is not on the author's queue %s", p["copy"], author.Key("ready"))
	}
	if r := rec(c, cp[0]); r["where"] != "ok" || r["score"] != "6" {
		t.Fatalf("read copy after SCORE 6 (ok with its finding): %v", r)
	}
	cleanMoves(t, c, "score 6")

	// the fix pushes a new head: pr record --head is the one event
	recordPR(c, 8400, head(1))
	calls := countCalls(c)
	got, err := c.FCall(ctx, "ns_cm_head", nil, "nova-tools", "8400", "pr record").StringSlice()
	if err != nil {
		t.Fatalf("ns_cm_head: %v", err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("head move took %d calls, want 1", n)
	}
	p2 := rec(c, ids[0])
	fresh := liveReads(c, ids[0])
	if p2["where"] != "reading" || p2["copy"] != "" || p2["head"] != head(1) || len(fresh) != 1 {
		t.Fatalf("after pr record --head: %v primary %v", got, p2)
	}
	if r := rec(c, fresh[0]); r["head"] != head(1) || r["consumer"] != emma.String() || r["kind"] != "read" {
		t.Fatalf("fresh read copy %v", r)
	}
	if f := rec(c, p["copy"]); f["where"] != "ok" {
		t.Fatalf("fix copy after its new head: %v", f)
	}
	cleanMoves(t, c, "fix new head")
}

// TestFixCopyEndCutsAFreshRead: the fix copy's own card end --ok --pr
// --head (a new head) cuts the fresh read copy in the same call; a swarm
// author's fix goes to the swarm route.
func TestFixCopyEndCutsAFreshRead(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	author := enroll(t, c, "bench:b", 2, "")
	enroll(t, c, "friend:emma", 2, "reader")
	ids := pushPrimaries(t, c, 1)
	e := toReading(t, c, author, ids[0], 8500, head(0))
	cp := split(e.Next)
	workCopy(t, c, cp[0])
	x, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: cp, OK: true, Score: 5, Finding: "scope over", By: "emma"})
	if err != nil || x[0].To != "reading" || len(split(x[0].Next)) != 1 {
		t.Fatalf("score 5: %v %v", x, err)
	}
	fixID := split(x[0].Next)[0]
	fix := rec(c, fixID)
	if fix["kind"] != "fix" || fix["consumer"] != author.String() || !strings.Contains(fix["finding"], "scope over") {
		t.Fatalf("fix copy %v", fix)
	}
	if _, err := taskcard.Work(ctx, c, author, "b", 0, false, fixID); err != nil {
		t.Fatal(err)
	}
	recordPR(c, 8500, head(1))
	y, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{fixID}, OK: true, Repo: "nova-tools", PR: "8500",
		Head: head(1), By: "b"})
	if err != nil || y[0].To != "reading" || len(split(y[0].Next)) != 1 {
		t.Fatalf("fix ok with a new head: %v %v", y, err)
	}
	if r := rec(c, split(y[0].Next)[0]); r["head"] != head(1) || r["kind"] != "read" || r["primary"] != ids[0] {
		t.Fatalf("fresh read copy %v", r)
	}
	if p := rec(c, ids[0]); p["where"] != "reading" || p["head"] != head(1) || p["copy"] != "" {
		t.Fatalf("primary %v", p)
	}
	cleanMoves(t, c, "fix end")
}

// TestHeadMoveRetiresAndRecutsReads (#4094 DONE-WHEN 4): a head move retires
// the open read copies and cuts fresh ones at the new head; a lapsed read
// copy is re-cut on the next tick of the deal pass; the reading column
// never holds a primary with zero live copies while a reader exists.
func TestHeadMoveRetiresAndRecutsReads(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	author := enroll(t, c, "bench:b", 2, "")
	enroll(t, c, "bench:s1", 8, "")
	ids := pushPrimaries(t, c, 1)
	e := toReading(t, c, author, ids[0], 8600, head(0))
	old := split(e.Next)
	if len(old) != 4 {
		t.Fatalf("next=%q, want four swarm copies", e.Next)
	}
	// a rebase pushes a new head; the deal pass's next tick re-cuts
	recordPR(c, 8600, head(1))
	if _, err := taskcard.DealPass(ctx, c, "reconciler", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, id := range old {
		if r := rec(c, id); r["where"] != "fail" || !strings.HasPrefix(r["why"], "head moved") {
			t.Fatalf("old read copy %s after the head move: %v", id, r)
		}
	}
	fresh := liveReads(c, ids[0])
	if len(fresh) != 4 {
		t.Fatalf("fresh reads %v, want four", fresh)
	}
	for _, id := range fresh {
		if r := rec(c, id); r["head"] != head(1) {
			t.Fatalf("fresh copy %s %v", id, r)
		}
	}
	cleanMoves(t, c, "head move")

	// a second primary whose only friend read copy lapses: the next tick
	// re-cuts it
	enroll(t, c, "friend:emma", 1, "reader")
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "q00", Where: "waiting", Stream: mvStream, Sprint: sprint,
		Kind: "build", Title: "primary q00", Repo: "mas-bandwidth/nova-tools", By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	ids = append(ids, "q00")
	e2 := toReading(t, c, author, ids[1], 8601, head(2))
	one := split(e2.Next)
	if len(one) != 1 || rec(c, one[0])["consumer"] != "friend:emma" {
		t.Fatalf("next=%q, want one friend copy", e2.Next)
	}
	if _, err := taskcard.Work(ctx, c, mustConsumer(t, "friend:emma"), "emma", 0, false, one[0]); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, taskcard.Key(one[0]), "lease_until", "1")
	if _, err := taskcard.DealPass(ctx, c, "reconciler", time.Now()); err != nil {
		t.Fatal(err)
	}
	if r := rec(c, one[0]); r["where"] != "fail" {
		t.Fatalf("lapsed copy %v", r)
	}
	again := liveReads(c, ids[1])
	if len(again) == 0 {
		t.Fatalf("the reading primary holds zero live copies after the tick: %v", rec(c, ids[1]))
	}
	cleanMoves(t, c, "lapsed")
}
