//go:build functional

package ws_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

const (
	snStream   = "swarm: cards"
	snSentinel = "swarm-cards:sentinel"
	snSHA      = "0123456789abcdef0123456789abcdef01234567"
)

// snPush pushes one task into stream (waiting) through the one create.
func snPush(t *testing.T, c *redis.Client, id, stream, dependsOn string) {
	t.Helper()
	r := taskcard.PushRequest{ID: id, Where: "waiting", Stream: stream, Kind: "build", Title: "STREAM: " + stream + " | " + id,
		By: "test", DependsOn: dependsOn}
	if _, err := taskcard.Push(context.Background(), c, r); err != nil {
		t.Fatalf("push %s: %v", id, err)
	}
}

// TestSentinelIsCreatedAtFirstPushAndLandsLast is #4318's DONE-WHEN for the
// sentinel: the first push into a stream creates <slug>:sentinel in its
// waiting set (once: a second push adds nothing), task land on it is refused
// by name while any other card of the stream is live, it is never dealt and
// moves nowhere but parked or done, and it lands, waiting -> landed at the
// sha, once every other card is landed or done.
func TestSentinelIsCreatedAtFirstPushAndLandsLast(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()

	snPush(t, c, "A", snStream, "")
	snPush(t, c, "B", snStream, "A")
	waiting, err := c.ZRange(ctx, ws.Key(snStream, "waiting"), 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(waiting)
	if strings.Join(waiting, " ") != "A B "+snSentinel {
		t.Fatalf("waiting %v, want A, B and the sentinel", waiting)
	}
	if kind, _ := c.HGet(ctx, "task:"+snSentinel, "kind").Result(); kind != "sentinel" {
		t.Fatalf("sentinel kind %q", kind)
	}
	if err := ws.Check(ctx, c, []string{"A", "B", snSentinel}); err != nil {
		t.Fatalf("invariant: %v", err)
	}

	// Refused by name while A and B are live; nothing moves.
	_, err = taskcard.Land(ctx, c, snSentinel, "test", snSHA, "")
	if err == nil || !strings.Contains(err.Error(), "SENTINEL task:"+snSentinel+" lands after the 2 live card(s) of stream "+snStream+" (first A waiting)") {
		t.Fatalf("land with live siblings: %v", err)
	}
	// Never dealt.
	k, _ := taskcard.ParseConsumer("friend:f1")
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, N: 1, IDs: []string{snSentinel}, By: "test"}); err == nil || !strings.Contains(err.Error(), "SENTINEL task:"+snSentinel+" is the stream stop, never dealt") {
		t.Fatalf("deal: %v", err)
	}
	// Nowhere but parked or done.
	if _, err := ws.Move(ctx, c, snSentinel, "ready", "test", "by hand"); err == nil || !strings.Contains(err.Error(), "SENTINEL task:"+snSentinel+" is the stream stop") {
		t.Fatalf("move to ready: %v", err)
	}
	if _, err := ws.Move(ctx, c, snSentinel, "parked", "test", "scope"); err != nil {
		t.Fatalf("park: %v", err)
	}
	if _, err := ws.Move(ctx, c, snSentinel, "waiting", "test", "unpark"); err != nil {
		t.Fatalf("unpark: %v", err)
	}

	// A lands (a working card at its merge sha), B is cancelled: nothing
	// live is left, and the sentinel lands from waiting.
	if _, err := taskcard.Move(ctx, c, "A", "ready", taskcard.Opts{By: "test", Why: "deps met"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "A", "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Land(ctx, c, "A", "test", snSHA, "merged"); err != nil {
		t.Fatal(err)
	}
	_, err = taskcard.Land(ctx, c, snSentinel, "test", snSHA, "")
	if err == nil || !strings.Contains(err.Error(), "1 live card(s) of stream "+snStream+" (first B waiting)") {
		t.Fatalf("land with B live: %v", err)
	}
	if _, err := taskcard.Cancel(ctx, c, "B", "test", "not needed"); err != nil {
		t.Fatal(err)
	}
	r, err := taskcard.Land(ctx, c, snSentinel, "test", snSHA, "")
	if err != nil || r.From != "waiting" || r.To != "landed" {
		t.Fatalf("land: %+v %v", r, err)
	}
	if where, _ := c.HGet(ctx, "task:"+snSentinel, "where").Result(); where != "landed" {
		t.Fatalf("sentinel where %q", where)
	}
	if err := ws.Check(ctx, c, []string{"A", "B", snSentinel}); err != nil {
		t.Fatalf("invariant after landing: %v", err)
	}
	// A push after the stop landed starts the stream again: no second
	// sentinel, the one record returns to waiting behind the new card.
	snPush(t, c, "C", snStream, "")
	waiting, _ = c.ZRange(ctx, ws.Key(snStream, "waiting"), 0, -1).Result()
	sort.Strings(waiting)
	if strings.Join(waiting, " ") != "C "+snSentinel {
		t.Fatalf("waiting after C: %v, want C and the sentinel back in waiting", waiting)
	}
	if n, _ := c.ZCard(ctx, ws.Key(snStream, "landed")).Result(); n != 1 {
		t.Fatalf("landed after C: %d, want A alone", n)
	}
	if err := ws.Check(ctx, c, []string{"A", "B", "C", snSentinel}); err != nil {
		t.Fatalf("invariant after the restart: %v", err)
	}
}

// TestRenameEndsTheOldSentinel: a renamed stream's old sentinel (its id
// carries the old slug) ends done/fail and the new name gets its own.
func TestRenameEndsTheOldSentinel(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	snPush(t, c, "A", "old: name", "")
	if _, err := ws.Rename(ctx, c, "old: name", "new: name", "test"); err != nil {
		t.Fatal(err)
	}
	waiting, _ := c.ZRange(ctx, ws.Key("new: name", "waiting"), 0, -1).Result()
	sort.Strings(waiting)
	if strings.Join(waiting, " ") != "A new-name:sentinel" {
		t.Fatalf("waiting %v", waiting)
	}
	done, _ := c.ZRange(ctx, ws.Key("new: name", "done"), 0, -1).Result()
	if strings.Join(done, " ") != "old-name:sentinel" {
		t.Fatalf("done %v", done)
	}
	if err := ws.Check(ctx, c, []string{"A", "old-name:sentinel", "new-name:sentinel"}); err != nil {
		t.Fatalf("invariant: %v", err)
	}
}

// TestShowOrderListsCardsWithEdges: ws.Show lists every stream's cards in
// the sets' order with the sentinel last, each DEPENDS-ON entry an edge
// annotated with what its record says, and a stream's edge on another
// stream's sentinel like any other.
func TestShowOrderListsCardsWithEdges(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	snPush(t, c, "A", snStream, "")
	snPush(t, c, "B", snStream, "A, task:ghost")
	snPush(t, c, "C", "ci", "task:"+snSentinel+", mas-bandwidth/nova-tools#77")
	rows, err := ws.Show(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Stream != snStream || rows[1].Stream != "ci" || rows[0].Rank != 1 || rows[1].Rank != 2 {
		t.Fatalf("streams %+v", rows)
	}
	var lines []string
	for _, r := range rows {
		for _, card := range r.Cards {
			lines = append(lines, card.Line(r.Live))
		}
	}
	want := []string{
		"waiting A",
		"waiting B <- A(waiting), task:ghost(unknown)",
		"waiting " + snSentinel + " <- every other card of the stream (live 2)",
		"waiting C <- task:" + snSentinel + "(waiting), mas-bandwidth/nova-tools#77",
		"waiting ci:sentinel <- every other card of the stream (live 1)",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if rows[0].Sentinel != "waiting" || rows[0].Live != 2 || rows[0].Landed != 0 || rows[1].Live != 1 {
		t.Fatalf("counts %+v", rows)
	}
}

// TestSentinelIsStructure (#4318, the cold read's verdict): the stop's graph
// is in the one move, not in the verbs. task done, cancel, a move to ready,
// working, review or merging, a done/ok, a friend, a create of a sentinel
// id, and a second stream name with the same slug are refused by name and
// write nothing; sprint clear leaves it; it lands by structure with the
// last card at that sha; stream order registers a stream that was written
// straight into the keys.
func TestSentinelIsStructure(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	snPush(t, c, "A", snStream, "")
	dump := func() string { return c.Dump(ctx, "task:"+snSentinel).Val() }
	before := dump()
	for name, err := range map[string]error{
		"task done":   func() error { _, err := taskcard.Done(ctx, c, snSentinel, "test", "x", ""); return err }(),
		"task cancel": func() error { _, err := taskcard.Cancel(ctx, c, snSentinel, "test", "not needed"); return err }(),
		"move to done ok": func() error {
			_, err := taskcard.Move(ctx, c, snSentinel, "done", taskcard.Opts{By: "test", Why: "x", OK: "ok"})
			return err
		}(),
		"move to ready": func() error { _, err := ws.Move(ctx, c, snSentinel, "ready", "test", "x"); return err }(),
		"move to working": func() error {
			_, err := taskcard.Move(ctx, c, snSentinel, "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true})
			return err
		}(),
		"move to review":  func() error { _, err := ws.Move(ctx, c, snSentinel, "review", "test", "x"); return err }(),
		"move to merging": func() error { _, err := ws.Move(ctx, c, snSentinel, "merging", "test", "x"); return err }(),
		"a friend": func() error {
			_, err := taskcard.Move(ctx, c, snSentinel, "waiting", taskcard.Opts{By: "test", Friend: "f1", SetFriend: true})
			return err
		}(),
		"land with a live card": func() error { _, err := taskcard.Land(ctx, c, snSentinel, "test", snSHA, ""); return err }(),
	} {
		if err == nil || !strings.Contains(err.Error(), "SENTINEL task:"+snSentinel) {
			t.Errorf("%s: %v, want a SENTINEL refusal", name, err)
		}
	}
	if dump() != before {
		t.Fatal("a refused move wrote the stop's record")
	}
	// a sentinel id is registration's alone; a second name with the slug is refused
	_, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "x:sentinel", Where: "waiting", Stream: snStream, By: "test", Title: "t"})
	if err == nil || !strings.Contains(err.Error(), "SENTINEL x:sentinel is a stream sentinel id") {
		t.Fatalf("create of a sentinel id: %v", err)
	}
	_, err = taskcard.Push(ctx, c, taskcard.PushRequest{ID: "Z", Where: "waiting", Stream: "swarm cards", By: "test", Title: "t"})
	if err == nil || !strings.Contains(err.Error(), `SLUG stream "swarm cards" has the slug swarm-cards of stream "swarm: cards"`) {
		t.Fatalf("slug clash: %v", err)
	}
	if n, _ := c.Exists(ctx, "task:Z", "task:x:sentinel").Result(); n != 0 {
		t.Fatal("a refused push wrote a record")
	}
	// landing by structure: A lands, the stop lands with it at the sha
	if _, err := taskcard.Move(ctx, c, "A", "ready", taskcard.Opts{By: "test", Why: "deps met"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "A", "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Land(ctx, c, "A", "test", snSHA, "merged"); err != nil {
		t.Fatal(err)
	}
	if h := c.HGetAll(ctx, "task:"+snSentinel).Val(); h["where"] != "landed" || h["merge_sha"] != snSHA || h["why"] != "last card A landed" {
		t.Fatalf("the stop after the last landing: %v", h)
	}
	if err := ws.Check(ctx, c, []string{"A", snSentinel}); err != nil {
		t.Fatalf("invariant: %v", err)
	}
	// sprint clear zeros the cards and leaves the stop with its stream
	if _, err := c.FCall(ctx, "ns_sprint_clear", nil, "test", "fresh run", "1").Result(); err != nil {
		t.Fatalf("sprint clear: %v", err)
	}
	if w, _ := c.HGet(ctx, "task:A", "where").Result(); w != "done" {
		t.Fatalf("A after the clear: %q", w)
	}
	if w, _ := c.HGet(ctx, "task:"+snSentinel, "where").Result(); w != "landed" {
		t.Fatalf("the stop after the clear: %q, want landed (left with its stream)", w)
	}
	// a stream written straight into the keys has no stop until it is
	// registered: stream order registers it
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.SAdd(ctx, "ws:names", "fleet").Err())
	must(c.ZAdd(ctx, "ws:order", redis.Z{Score: 2, Member: "fleet"}).Err())
	if n, _ := c.Exists(ctx, "task:fleet:sentinel").Result(); n != 0 {
		t.Fatal("a fixture stream has a stop before registration")
	}
	if _, err := ws.Order(ctx, c, []string{"fleet"}); err != nil {
		t.Fatal(err)
	}
	if w, _ := c.HGet(ctx, "task:fleet:sentinel", "where").Result(); w != "waiting" {
		t.Fatalf("stream order registered fleet: stop where %q, want waiting", w)
	}
}

// TestSentinelKeepsItsStream (the re-read of #4369): no move takes a stop to
// another stream or to none, a move that stays in place carries no fields,
// task fsck names a stop sitting in another stream's set and the repair
// walk moves it home, scope park and unpark count cards only, a rename that
// keeps the slug keeps the stop, and a rename of a landed stream ends the
// old stop done/ok and lands the new name's at the same sha, leaving no
// empty waiting stop and nothing extra in the landed count.
func TestSentinelKeepsItsStream(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	const alpha, beta = "alpha", "beta"
	snPush(t, c, "A", alpha, "")
	snPush(t, c, "B", beta, "")
	sid := ws.SentinelID(alpha)
	for name, err := range map[string]error{
		"to another stream": func() error {
			_, err := taskcard.Move(ctx, c, sid, "waiting", taskcard.Opts{By: "test", Stream: beta, SetStream: true})
			return err
		}(),
		"to no stream": func() error {
			_, err := taskcard.Move(ctx, c, sid, "waiting", taskcard.Opts{By: "test", Stream: "", SetStream: true})
			return err
		}(),
		"task block": func() error {
			_, err := taskcard.Move(ctx, c, sid, "waiting", taskcard.Opts{By: "test", Why: "on x", Fields: []string{"blocked_on", "x"}})
			return err
		}(),
	} {
		if err == nil || !strings.Contains(err.Error(), "SENTINEL task:"+sid) {
			t.Errorf("%s: %v, want a SENTINEL refusal", name, err)
		}
	}
	if st, _ := c.HGet(ctx, "task:"+sid, "stream").Result(); st != alpha {
		t.Fatalf("stop stream %q after refused moves", st)
	}
	// a stop planted in another stream by hand: fsck names it twice (alpha's
	// missing, beta holding it) and the repair walk moves it home
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	score := c.ZScore(ctx, ws.Key(alpha, "waiting"), sid).Val()
	must(c.ZRem(ctx, ws.Key(alpha, "waiting"), sid).Err())
	must(c.ZAdd(ctx, ws.Key(beta, "waiting"), redis.Z{Score: score, Member: sid}).Err())
	must(c.HSet(ctx, "task:"+sid, "stream", beta).Err())
	r, err := taskcard.Fsck(ctx, c, "s1")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(r.Lines, "\n")
	if r.Drift != 2 || !strings.Contains(lines, "NOSENTINEL stream=alpha id="+sid+" (sits in stream beta; nova-sprint task fsck --repair moves it home)") ||
		!strings.Contains(lines, "SENTINEL "+sid+" in ws:beta:waiting is another stream's stop (alpha); nova-sprint task fsck --repair moves it home") {
		t.Fatalf("fsck over a planted stop: drift=%d\n%s", r.Drift, lines)
	}
	sw, err := taskcard.SentinelsWalk(ctx, c, true)
	if err != nil || len(sw.Missing) != 1 || sw.Missing[0].Stream != alpha || sw.Created != 1 {
		t.Fatalf("repair walk: %+v %v", sw, err)
	}
	if r, err := taskcard.Fsck(ctx, c, "s1"); err != nil || r.Drift != 0 {
		t.Fatalf("fsck after the repair: %+v %v", r, err)
	}
	if st, _ := c.HGet(ctx, "task:"+sid, "stream").Result(); st != alpha {
		t.Fatalf("stop stream %q after the repair, want alpha", st)
	}
	if err := ws.Check(ctx, c, []string{"A", "B", sid, ws.SentinelID(beta)}); err != nil {
		t.Fatalf("invariant: %v", err)
	}

	// scope park and unpark count cards: the stop parks with its stream
	snPush(t, c, "A2", alpha, "")
	if n, err := ws.ParkStream(ctx, c, alpha, "test", "scope"); err != nil || n != 2 {
		t.Fatalf("park: %d %v, want 2 cards", n, err)
	}
	if w, _ := c.HGet(ctx, "task:"+sid, "where").Result(); w != "parked" {
		t.Fatalf("stop where %q after park, want parked", w)
	}
	if n, err := ws.UnparkStream(ctx, c, alpha, "test", "scope"); err != nil || n != 2 {
		t.Fatalf("unpark: %d %v, want 2 cards", n, err)
	}

	// the same slug keeps the stop: no SLUG refusal, no done entry
	if _, err := ws.Rename(ctx, c, alpha, "Alpha", "test"); err != nil {
		t.Fatalf("rename to the same slug: %v", err)
	}
	if h := c.HGetAll(ctx, "task:"+sid).Val(); h["stream"] != "Alpha" || h["where"] != "waiting" {
		t.Fatalf("stop after the same-slug rename: %v", h)
	}
	if n, _ := c.ZCard(ctx, ws.Key("Alpha", "done")).Result(); n != 0 {
		t.Fatal("the same-slug rename ended the stop")
	}
	if owner, _ := c.Get(ctx, "ws:slug:alpha").Result(); owner != "Alpha" {
		t.Fatalf("ws:slug:alpha %q, want Alpha", owner)
	}

	// a landed stream renamed to another slug: the old stop ends done/ok,
	// the new name's stop lands at the same sha, the landed count is the cards
	snPush(t, c, "G", "gamma", "")
	gsid := ws.SentinelID("gamma")
	if _, err := taskcard.Move(ctx, c, "G", "ready", taskcard.Opts{By: "test", Why: "deps met"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Move(ctx, c, "G", "working", taskcard.Opts{By: "test", As: "f1", Friend: "f1", SetFriend: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Land(ctx, c, "G", "test", snSHA, "merged"); err != nil {
		t.Fatal(err)
	}
	if w, _ := c.HGet(ctx, "task:"+gsid, "where").Result(); w != "landed" {
		t.Fatalf("gamma's stop after its last landing: %q", w)
	}
	if _, err := ws.Rename(ctx, c, "gamma", "delta", "test"); err != nil {
		t.Fatalf("rename a landed stream: %v", err)
	}
	dsid := ws.SentinelID("delta")
	if h := c.HGetAll(ctx, "task:"+gsid).Val(); h["where"] != "done" || h["where_ok"] != "ok" || h["stream"] != "delta" {
		t.Fatalf("the old stop after the rename: %v", h)
	}
	if h := c.HGetAll(ctx, "task:"+dsid).Val(); h["where"] != "landed" || h["merge_sha"] != snSHA {
		t.Fatalf("the new stop after the rename: %v", h)
	}
	if n, _ := c.ZCard(ctx, ws.Key("delta", "waiting")).Result(); n != 0 {
		t.Fatal("the rename left a waiting stop")
	}
	if n, err := ws.CardCount(ctx, c, "delta", "landed"); err != nil || n != 1 {
		t.Fatalf("delta landed cards %d %v, want 1 (G)", n, err)
	}
	if err := ws.Check(ctx, c, []string{"G", gsid, dsid}); err != nil {
		t.Fatalf("invariant after the rename: %v", err)
	}
}
