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
		"waiting B <- A(waiting), task:ghost(no record)",
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
