package taskcard_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

const (
	mvStream = "swarm: cards"
	baseSHA  = "0123456789abcdef0123456789abcdef01234567"
)

func head(i int) string { return fmt.Sprintf("%040x", 0xa0000+i) }

// pushPrimaries pushes n primaries into mvStream's waiting set, complete
// enough for a copy's brief (#3911): repo, base, base_sha, paths, done_when.
func pushPrimaries(t *testing.T, c *redis.Client, n int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%02d", i)
		_, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: mvStream, Sprint: sprint,
			Kind: "build", Ref: fmt.Sprintf("nova-tools#%d", 4000+i), Origin: fmt.Sprintf("issue:nova-tools#%d", 4000+i),
			Title: "primary " + id, Repo: "mas-bandwidth/nova-tools", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", baseSHA, "paths", "internal/x.go",
				"done_when", "go test ./internal/x -run TestX passes"}})
		if err != nil {
			t.Fatalf("push %s: %v", id, err)
		}
		ids = append(ids, id)
	}
	return ids
}

// cellsOf is one consumer's row, read the way the table reads it.
func cellsOf(t *testing.T, c *redis.Client, k taskcard.Consumer) taskcard.Cells {
	t.Helper()
	rows, err := taskcard.ReadCells(context.Background(), c, []taskcard.Consumer{k})
	if err != nil {
		t.Fatal(err)
	}
	return rows[0]
}

func wsCount(c *redis.Client, where string) int64 {
	return c.ZCard(context.Background(), taskcard.StreamKey(mvStream, where)).Val()
}

// clean asserts both fsck walks find zero drift: the primaries' own (task
// fsck) and the copy links (card fsck).
func cleanMoves(t *testing.T, c *redis.Client, when string) {
	t.Helper()
	clean(t, c, when)
	r, err := taskcard.FsckMoves(context.Background(), c, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Drift != 0 {
		t.Fatalf("%s: card fsck drift %d: %v", when, r.Drift, r.Lines)
	}
}

func wantWS(t *testing.T, c *redis.Client, when string, want map[string]int64) {
	t.Helper()
	for w, n := range want {
		if got := wsCount(c, w); got != n {
			t.Fatalf("%s: ws %s=%d, want %d", when, w, got, n)
		}
	}
}

func wantCells(t *testing.T, got taskcard.Cells, when string, ready, working, ok, fail int64) {
	t.Helper()
	if got.Ready != ready || got.Working != working || got.OK != ok || got.Fail != fail {
		t.Fatalf("%s: %s", when, got.Line())
	}
}

func mustConsumer(t *testing.T, s string) taskcard.Consumer {
	t.Helper()
	k, err := taskcard.ParseConsumer(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestTableMoves is the #3929 DONE-WHEN, run once with a bench consumer and
// once with a friend consumer by the SAME function (Glenn 12:55 PM ET: "this
// code is the same, whether the consumer card is on a friend, or on the
// swarm"); each reader is the other kind, so a read is dealt to a friend
// and to the swarm.
func TestTableMoves(t *testing.T) {
	for _, tc := range []struct{ consumer, reader string }{
		{"bench:b", "friend:reader"},
		{"friend:f", "bench:reader"},
	} {
		t.Run(tc.consumer, func(t *testing.T) { tableMoves(t, tc.consumer, tc.reader) })
	}
}

func tableMoves(t *testing.T, consumer, reader string) {
	c := start(t)
	ctx := context.Background()
	k, rd := mustConsumer(t, consumer), mustConsumer(t, reader)
	c.SAdd(ctx, "benches", "b", "reader")
	c.SAdd(ctx, "friends", "f", "reader")
	c.HSet(ctx, k.DesiredKey(), "slots", "6")
	c.HSet(ctx, rd.DesiredKey(), "slots", "4")
	for _, x := range []taskcard.Consumer{k, rd} {
		if err := taskcard.Enroll(ctx, c, x, true); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, x.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	}
	c.HSet(ctx, "cfg:ci", "max_attempts", "1")

	ids := pushPrimaries(t, c, 20)
	wantWS(t, c, "push", map[string]int64{"waiting": 20, "working": 0})
	cleanMoves(t, c, "push")

	// card deal --to <consumer> --n 10: ONE call.
	calls := countCalls(c)
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 10, By: "rowan"})
	if err != nil || len(dealt) != 10 {
		t.Fatalf("deal %v %v", dealt, err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("deal took %d calls, want 1", n)
	}
	if dealt[0].Primary != ids[0] || dealt[9].Primary != ids[9] || dealt[0].Copy != ids[0]+"~1" {
		t.Fatalf("deal is not oldest first: %v", dealt)
	}
	wantWS(t, c, "deal", map[string]int64{"waiting": 10, "working": 10})
	wantCells(t, cellsOf(t, c, k), "deal", 10, 0, 0, 0)
	p := c.HGetAll(ctx, taskcard.Key(ids[0])).Val()
	cp := c.HGetAll(ctx, taskcard.Key(dealt[0].Copy)).Val()
	if p["copy"] != dealt[0].Copy || cp["primary"] != ids[0] || cp["consumer"] != consumer || cp["where"] != "ready" ||
		cp["leg"] != "work" || cp["base_sha"] != baseSHA || cp["stream"] != mvStream {
		t.Fatalf("links: primary %v copy %v", p, cp)
	}
	cleanMoves(t, c, "deal")

	// A second deal of a primary with a live copy is refused.
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{ids[0]}, By: "rowan"}); !isRefusedWith(err, "LIVECOPY") {
		t.Fatalf("second deal of a live primary: %v", err)
	}

	// card work --as <consumer> --fill with slots=6: ONE call.
	calls = countCalls(c)
	w, err := taskcard.Work(ctx, c, k, "rowan", 0, true)
	if err != nil || len(w.IDs) != 6 || w.Free != 0 {
		t.Fatalf("work %+v %v", w, err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("work took %d calls, want 1", n)
	}
	wantCells(t, cellsOf(t, c, k), "work", 4, 6, 0, 0)
	cleanMoves(t, c, "work")

	// card end --ok --pr on 4 (each its own PR), --fail on 2 (one call).
	for i := 0; i < 4; i++ {
		recordPR(c, 5000+i, head(i))
		e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{w.IDs[i]}, OK: true, Repo: "nova-tools",
			PR: fmt.Sprint(5000 + i), Head: head(i), By: "rowan",
			Fields: []string{"line1", "RESULT: ok", "line2", "DONE", "branch", "rowan/x", "commit", head(i)[:12]}})
		if err != nil || len(e) != 1 || e[0].To != "working" {
			t.Fatalf("end ok %d (CI pending: stays working): %v %v", i, e, err)
		}
	}
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: w.IDs[4:6], Why: "red at head", By: "rowan"})
	if err != nil || len(e) != 2 || e[0].To != "waiting" {
		t.Fatalf("end fail: %v %v", e, err)
	}
	cells := cellsOf(t, c, k)
	wantCells(t, cells, "end", 4, 0, 4, 2)
	if cells.Done() != 6 || cells.OKPct() != 67 || !strings.Contains(cells.Line(), "done=6 ok=4 fail=2 ok%=67") {
		t.Fatalf("derived cells: %s", cells.Line())
	}
	// CI pending: the four stay working with no copy, no read copy is cut
	wantWS(t, c, "end", map[string]int64{"waiting": 12, "working": 8, "reading": 0})
	wantCells(t, cellsOf(t, c, rd), "ci pending", 0, 0, 0, 0)
	// The primary carries the result; the copy is in no ready/working set.
	p0 := c.HGetAll(ctx, taskcard.Key(PrimaryOf(w.IDs[0]))).Val()
	if p0["where"] != "working" || p0["copy"] != "" || p0["pr"] != "5000" || p0["head"] != head(0) || p0["line1"] != "RESULT: ok" ||
		p0["line2"] != "DONE" || p0["author"] != consumer || p0["last_copy"] != w.IDs[0] {
		t.Fatalf("returned primary %v", p0)
	}
	for _, id := range w.IDs[:6] {
		for _, col := range []string{"ready", "working"} {
			if c.ZScore(ctx, k.Key(col), id).Err() != redis.Nil {
				t.Fatalf("copy %s still in %s", id, k.Key(col))
			}
		}
	}
	if f := c.HGetAll(ctx, taskcard.Key(PrimaryOf(w.IDs[4]))).Val(); f["why"] != "red at head" || f["attempts"] != "1" || f["copy"] != "" {
		t.Fatalf("failed primary %v", f)
	}
	cleanMoves(t, c, "end")

	// TestControl52 (#3093): CI at the exact head. Green on three heads:
	// each verdict call moves its primary to reading and cuts ONE read copy
	// for the reader in that same call (never the author). Red on the
	// fourth: no read copy, the primary stays working with the failure as
	// why and exactly one fix copy on the author's consumer.
	var reads []taskcard.Dealt
	for i := 0; i < 4; i++ {
		rc := "0"
		if i == 3 {
			rc = "1"
		}
		calls := countCalls(c)
		ciVerdict(t, c, 5000+i, head(i), rc)
		if n := countCalls(c) - calls; n != 3 {
			t.Fatalf("ci %d took %d calls (request, claim, receipt), want 3", i, n)
		}
		p := c.HGetAll(ctx, taskcard.Key(PrimaryOf(w.IDs[i]))).Val()
		if i < 3 {
			if p["where"] != "reading" || p["copy"] == "" || p["wait_ci"] != "" {
				t.Fatalf("ci OK %d: primary %v", i, p)
			}
			reads = append(reads, taskcard.Dealt{Primary: PrimaryOf(w.IDs[i]), Copy: p["copy"]})
			continue
		}
		fix := c.HGetAll(ctx, taskcard.Key(p["copy"])).Val()
		if p["where"] != "working" || !strings.HasPrefix(p["why"], "ci red: unit: ") || fix["leg"] != "fix" || fix["consumer"] != consumer {
			t.Fatalf("ci FAIL: primary %v fix %v", p, fix)
		}
	}
	wantCells(t, cellsOf(t, c, rd), "ci", 3, 0, 0, 0)
	wantCells(t, cellsOf(t, c, k), "ci fix", 5, 0, 4, 2)
	if got := c.HGetAll(ctx, taskcard.Key(reads[0].Copy)).Val(); got["leg"] != "read" || got["kind"] != "read" || got["head"] != head(0) ||
		got["consumer"] != reader {
		t.Fatalf("read copy %v", got)
	}
	// never the author
	if _, err := taskcard.Assign(ctx, c, k, reads[0].Primary, true, "rowan", "x"); !isRefusedWith(err, "AUTHOR") {
		t.Fatalf("read moved to its author: %v", err)
	}
	wantWS(t, c, "ci", map[string]int64{"reading": 3, "working": 5})
	cleanMoves(t, c, "ci")
	if _, err := taskcard.Work(ctx, c, rd, "rowan", 0, true); err != nil {
		t.Fatal(err)
	}
	cleanMoves(t, c, "work reads")
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{reads[0].Copy}, OK: true, By: "rowan"}); !isRefusedWith(err, "SCORE") {
		t.Fatalf("a read ended without a score: %v", err)
	}
	// score 9 -> merging, with the SCORE line on the PR record
	e, err = taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{reads[0].Copy}, OK: true, Score: 9,
		Gates: "ci:green,base:ok,scope:ok", By: "rowan"})
	if err != nil || e[0].To != "merging" {
		t.Fatalf("score 9: %v %v", e, err)
	}
	pn := c.HGet(ctx, taskcard.Key(reads[0].Primary), "pr").Val()
	line := c.HGet(ctx, "pr:nova-tools:"+pn, "reads").Val()
	if d := disposition.Parse(line); d.Outcome != disposition.Record || d.Line.Score != 9 || d.Line.Who != rd.Name {
		t.Fatalf("SCORE line %q parsed %+v", line, d)
	}
	// score 6 -> working with the finding as why and a fix copy on the author's consumer
	e, err = taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{reads[1].Copy}, OK: true, Score: 6,
		Finding: "the test does not fail without the fix", By: "rowan"})
	if err != nil || e[0].To != "working" || e[0].Next == "" {
		t.Fatalf("score 6: %v %v", e, err)
	}
	fixed := c.HGetAll(ctx, taskcard.Key(reads[1].Primary)).Val()
	fix := c.HGetAll(ctx, taskcard.Key(e[0].Next)).Val()
	if fixed["where"] != "working" || fixed["why"] != "the test does not fail without the fix" || fixed["copy"] != e[0].Next ||
		fix["leg"] != "fix" || fix["kind"] != "fix" || fix["consumer"] != consumer || fix["finding"] == "" {
		t.Fatalf("fix: primary %v copy %v", fixed, fix)
	}
	if c.ZScore(ctx, k.Key("ready"), e[0].Next).Err() != nil {
		t.Fatalf("fix copy %s is not in %s", e[0].Next, k.Key("ready"))
	}
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{reads[2].Copy}, OK: true, Score: 10, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	wantWS(t, c, "reads", map[string]int64{"reading": 0, "merging": 2, "working": 6})
	wantCells(t, cellsOf(t, c, rd), "reads", 0, 0, 3, 0)
	wantCells(t, cellsOf(t, c, k), "reads", 6, 0, 4, 2)
	cleanMoves(t, c, "reads")

	// card land --stream: merging -> landed in one call; land reads only merging.
	calls = countCalls(c)
	l, err := taskcard.LandStream(ctx, c, mvStream, "deadbeef", "lander", "")
	if err != nil || len(l.Landed) != 2 || len(l.Refused) != 0 {
		t.Fatalf("land %+v %v", l, err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("land took %d calls, want 1", n)
	}
	wantWS(t, c, "land", map[string]int64{"merging": 0, "landed": 2})
	cleanMoves(t, c, "land")
}

// recordPR writes the PR record pr:nova-tools:<n> a card end checks (#3488).
func recordPR(c *redis.Client, n int, head string) {
	c.HSet(context.Background(), fmt.Sprintf("pr:nova-tools:%d", n), "repo", "nova-tools", "n", n, "head", head, "base", "dev",
		"state", "open")
}

// ciVerdict runs our own CI for one head through its three calls (request,
// claim, receipt): rc 0 is green (final OK), else red at the attempt cap
// (final FAIL, cfg:ci max_attempts 1).
func ciVerdict(t *testing.T, c *redis.Client, pr int, sha, rc string) {
	t.Helper()
	ctx := context.Background()
	if v, err := c.FCall(ctx, "ns_ci_request", nil, "nova-tools", sha, pr, "", "", "unit", "go test ./...").Result(); err != nil {
		t.Fatalf("ci request %v %v", v, err)
	}
	if v, err := c.FCall(ctx, "ns_ci_claim", nil, "b", "tok-"+sha, 60000).Result(); err != nil || fmt.Sprint(v.([]any)[0]) != "CLAIMED" {
		t.Fatalf("ci claim %v %v", v, err)
	}
	fail := ""
	if rc != "0" {
		fail = "--- FAIL: TestX"
	}
	if v, err := c.FCall(ctx, "ns_ci_receipt", nil, "nova-tools", sha, "unit", "tok-"+sha, rc, 10, "log", "b", 60000, fail).Result(); err != nil {
		t.Fatalf("ci receipt %v %v", v, err)
	}
}

// PrimaryOf is taskcard.PrimaryOf, named here for the test's lines.
func PrimaryOf(id string) string { return taskcard.PrimaryOf(id) }

func isRefusedWith(err error, word string) bool {
	why, ok := taskcard.IsRefused(err)
	return ok && strings.HasPrefix(why, word)
}

// countCalls is the server's total FCALL count (INFO commandstats), so a
// step can assert it was one call.
func countCalls(c *redis.Client) int {
	info := c.Info(context.Background(), "commandstats").Val()
	n := 0
	for _, l := range strings.Split(info, "\n") {
		if strings.HasPrefix(l, "cmdstat_fcall:") || strings.HasPrefix(l, "cmdstat_fcall_ro:") {
			var calls int
			if _, err := fmt.Sscanf(l[strings.Index(l, "calls=")+len("calls="):], "%d", &calls); err == nil {
				n += calls
			}
		}
	}
	return n
}

// TestCardEndDoneAlreadyLands: an ABSTAIN done-already <sha> moves the
// primary working -> landed at that sha in the copy's end.
func TestCardEndDoneAlreadyLands(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	k := mustConsumer(t, "friend:f")
	c.HSet(ctx, k.DesiredKey(), "slots", "2")
	ids := pushPrimaries(t, c, 1)
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: ids, By: "rowan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Work(ctx, c, k, "f", 1, false); err != nil {
		t.Fatal(err)
	}
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, OK: true, DoneAlready: "c0ffee12", By: "f"})
	if err != nil || e[0].To != "landed" {
		t.Fatalf("done-already %v %v", e, err)
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[0])).Val(); h["merge_sha"] != "c0ffee12" || h["why"] != "done-already c0ffee12" {
		t.Fatalf("primary %v", h)
	}
	cleanMoves(t, c, "done-already")
}

// TestCardMovesRefuseOffGraph: the primary moves only through its copy.
func TestCardMovesRefuseOffGraph(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	k := mustConsumer(t, "bench:b")
	c.HSet(ctx, k.DesiredKey(), "slots", "3")
	ids := pushPrimaries(t, c, 3)
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: ids[:1], By: "rowan"})
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"task done on a primary with a live copy": func() error {
			_, err := taskcard.Done(ctx, c, ids[0], "rowan", "x", "1")
			return err
		}(),
		"task move of a copy": func() error {
			_, err := taskcard.Move(ctx, c, d[0].Copy, "done", taskcard.Opts{By: "rowan", Why: "x", OK: "fail"})
			return err
		}(),
		"waiting -> working without a copy": func() error {
			_, err := taskcard.Move(ctx, c, ids[1], "working", taskcard.Opts{By: "rowan"})
			return err
		}(),
		"end of a ready copy with ok": func() error {
			_, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, OK: true, By: "rowan"})
			return err
		}(),
		"work of more than the free slots": func() error {
			_, err := taskcard.Work(ctx, c, mustConsumer(t, "bench:none"), "rowan", 0, true)
			return err
		}(),
		"a named deal with one refusal writes nothing": func() error {
			_, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{ids[1], ids[0]}, By: "rowan"})
			return err
		}(),
	} {
		if _, ok := taskcard.IsRefused(err); !ok {
			t.Errorf("%s: %v, want a refusal", name, err)
		}
	}
	if c.HGet(ctx, taskcard.Key(ids[1]), "copy").Val() != "" || wsCount(c, "waiting") != 2 {
		t.Fatal("a refused batch wrote")
	}
	cleanMoves(t, c, "refusals")
}

// TestCardCancelAndExpire: a copy given back returns its primary without an
// attempt; a primary cancelled retires its live copy; a lapsed lease
// returns the copy as a fail and counts an attempt.
func TestCardCancelAndExpire(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	k := mustConsumer(t, "friend:f")
	c.SAdd(ctx, "friends", "f")
	c.HSet(ctx, k.DesiredKey(), "slots", "3")
	ids := pushPrimaries(t, c, 3)
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 3, By: "rowan"})
	if err != nil || len(d) != 3 {
		t.Fatal(d, err)
	}
	w, err := taskcard.Work(ctx, c, k, "f", 0, true)
	if err != nil || len(w.IDs) != 3 {
		t.Fatal(w, err)
	}
	e, err := taskcard.CancelCards(ctx, c, "rowan", "moved to another stream", d[0].Copy, ids[1])
	if err != nil || len(e) != 2 || e[0].To != "waiting" || e[1].To != "done" {
		t.Fatalf("cancel %v %v", e, err)
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[0])).Val(); h["where"] != "waiting" || h["attempts"] != "0" {
		t.Fatalf("given back %v", h)
	}
	if h := c.HGetAll(ctx, taskcard.Key(d[1].Copy)).Val(); h["where"] != "fail" {
		t.Fatalf("cancelled primary's copy %v", h)
	}
	cleanMoves(t, c, "cancel")
	if _, err := taskcard.BeatCopies(ctx, c, k, d[2].Copy); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, taskcard.Key(d[2].Copy), "lease_until", "1")
	x, err := taskcard.ExpireCopies(ctx, c, "reconciler")
	if err != nil || len(x) != 1 || x[0].Copy != d[2].Copy || x[0].To != "waiting" {
		t.Fatalf("expire %v %v", x, err)
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[2])).Val(); h["why"] != "lease lapsed" || h["attempts"] != "1" {
		t.Fatalf("expired primary %v", h)
	}
	wantCells(t, cellsOf(t, c, k), "cancel+expire", 0, 0, 0, 3)
	cleanMoves(t, c, "expire")
}

// TestCardFsckFindsBrokenLinksBothWays injects drift by hand in both
// directions and proves fsck names it and --repair clears it.
func TestCardFsckFindsBrokenLinksBothWays(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	k := mustConsumer(t, "bench:b")
	c.SAdd(ctx, "benches", "b")
	c.HSet(ctx, k.DesiredKey(), "slots", "3")
	ids := pushPrimaries(t, c, 3)
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 3, By: "rowan"})
	if err != nil {
		t.Fatal(err)
	}
	// set -> record: a copy removed from its set (the primary names a lost copy)
	c.ZRem(ctx, k.Key("ready"), d[0].Copy)
	// record -> set: a ghost in a consumer set whose record names another place
	c.ZAdd(ctx, k.Key("working"), redis.Z{Score: 1, Member: d[1].Copy})
	// a copy whose primary no longer names it (two findings: the orphan
	// copy, and the primary working with no copy)
	c.HSet(ctx, taskcard.Key(ids[2]), "copy", "")
	r, err := taskcard.FsckMoves(ctx, c, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Drift != 4 {
		t.Fatalf("drift %d, want 4: %v", r.Drift, r.Lines)
	}
	if _, err := taskcard.FsckMoves(ctx, c, true); err != nil {
		t.Fatal(err)
	}
	r, err = taskcard.FsckMoves(ctx, c, false)
	if err != nil || r.Drift != 0 {
		t.Fatalf("after repair: %+v %v", r, err)
	}
}

// TestCardEndIsIdempotentFencedAndChecksThePR carries #3488 into card end:
// the same end again is already (no second move); other evidence on an
// ended copy is a CONFLICT; a token that is not the copy's, or a lapsed
// lease, is FENCED; an ok with a PR needs the PR record at --head on the
// card's base.
func TestCardEndIsIdempotentFencedAndChecksThePR(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	k := mustConsumer(t, "bench:b")
	c.HSet(ctx, k.DesiredKey(), "slots", "4")
	pushPrimaries(t, c, 4)
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 4, By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	w, err := taskcard.Work(ctx, c, k, "b", 0, true)
	if err != nil || len(w.Tokens) != 4 || w.Tokens[0] == "" {
		t.Fatalf("work %+v %v", w, err)
	}
	ok := func(id, tok string, pr int, h string) error {
		_, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{id}, OK: true, Repo: "nova-tools", PR: fmt.Sprint(pr),
			Head: h, Token: tok, By: "b"})
		return err
	}
	// the PR record: missing, another head, another base
	if err := ok(w.IDs[0], w.Tokens[0], 6000, head(0)); !isRefusedWith(err, "NOPR") {
		t.Fatalf("no PR record: %v", err)
	}
	recordPR(c, 6000, head(9))
	if err := ok(w.IDs[0], w.Tokens[0], 6000, head(0)); !isRefusedWith(err, "PRHEAD") {
		t.Fatalf("PR record at another head: %v", err)
	}
	c.HSet(ctx, "pr:nova-tools:6000", "head", head(0), "base", "main")
	if err := ok(w.IDs[0], w.Tokens[0], 6000, head(0)); !isRefusedWith(err, "PRBASE") {
		t.Fatalf("PR record on another base: %v", err)
	}
	c.HSet(ctx, "pr:nova-tools:6000", "base", "dev")
	// fencing: another token, then a lapsed lease
	if err := ok(w.IDs[0], "stale", 6000, head(0)); !isRefusedWith(err, "FENCED") {
		t.Fatalf("stale token: %v", err)
	}
	c.HSet(ctx, taskcard.Key(w.IDs[1]), "lease_until", "1")
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{w.IDs[1]}, Why: "x", Token: w.Tokens[1], By: "b"}); !isRefusedWith(err, "FENCED") {
		t.Fatalf("lapsed lease: %v", err)
	}
	if err := ok(w.IDs[0], w.Tokens[0], 6000, head(0)); err != nil {
		t.Fatal(err)
	}
	moves := c.XLen(ctx, "ws:log").Val()
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{w.IDs[0]}, OK: true, Repo: "nova-tools", PR: "6000", Head: head(0), By: "b"})
	if err != nil || e[0].To != "already" || c.XLen(ctx, "ws:log").Val() != moves {
		t.Fatalf("repeat end: %v %v (log %d -> %d)", e, err, moves, c.XLen(ctx, "ws:log").Val())
	}
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{w.IDs[0]}, Why: "red", By: "b"}); !isRefusedWith(err, "CONFLICT") {
		t.Fatalf("other evidence on an ended copy: %v", err)
	}
	cleanMoves(t, c, "idempotent end")
}

// TestControl17AssignLiveRefusesWithoutRevoke (#2940): a live card cannot be
// dealt again, nor assigned to another consumer without --revoke; with it,
// ONE call gives the live copy back (fenced: its holder's end is refused,
// its slot is free) and cuts the new copy on the other consumer.
func TestControl17AssignLiveRefusesWithoutRevoke(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	a, b := mustConsumer(t, "friend:f"), mustConsumer(t, "bench:b")
	c.HSet(ctx, a.DesiredKey(), "slots", "1")
	c.HSet(ctx, b.DesiredKey(), "slots", "1")
	ids := pushPrimaries(t, c, 1)
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: a, IDs: ids, By: "rowan"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := taskcard.Work(ctx, c, a, "f", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, IDs: ids, By: "rowan"}); !isRefusedWith(err, "LIVECOPY") {
		t.Fatalf("second deal: %v", err)
	}
	if _, err := taskcard.Assign(ctx, c, b, ids[0], false, "rowan", "rebalance"); !isRefusedWith(err, "LIVECOPY") {
		t.Fatalf("assign without --revoke: %v", err)
	}
	calls := countCalls(c)
	got, err := taskcard.Assign(ctx, c, b, ids[0], true, "rowan", "rebalance")
	if err != nil || got.Revoked != d[0].Copy || got.Copy != ids[0]+"~2" {
		t.Fatalf("assign --revoke %+v %v", got, err)
	}
	if n := countCalls(c) - calls; n != 1 {
		t.Fatalf("assign --revoke took %d calls, want 1", n)
	}
	wantCells(t, cellsOf(t, c, a), "revoked", 0, 0, 0, 1)
	wantCells(t, cellsOf(t, c, b), "assigned", 1, 0, 0, 0)
	if _, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: w.IDs, OK: true, Token: w.Tokens[0], By: "f"}); err == nil {
		t.Fatal("the revoked holder's end was accepted")
	}
	if h := c.HGetAll(ctx, taskcard.Key(ids[0])).Val(); h["copy"] != got.Copy || h["where"] != "working" || h["attempts"] != "0" {
		t.Fatalf("primary %v", h)
	}
	cleanMoves(t, c, "assign")
}
