//go:build functional

package ws_test

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// Cold read of #4412 at 660ab1736 (who=rowan-opus): the sentinel's
// acceptance probed as a SEQUENCE on one throwaway store.
func TestRowanOpusRead4412SentinelSequence(t *testing.T) {
	t.Parallel()
	_, c := wstest.Start(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.HSet(ctx, "friend:rowan:roles", "roles", "builder,coordinator,may-hold").Err())
	must(c.HSet(ctx, "friend:bob:roles", "roles", "builder,may-hold").Err())
	where := func(id string) (string, string) {
		h := c.HGetAll(ctx, "task:"+id).Val()
		return h["where"], h["merge_sha"]
	}
	logLen := func() int64 { n, _ := c.XLen(ctx, "ws:log").Result(); return n }
	toMerging := func(id string) {
		t.Helper()
		_, err := taskcard.Move(ctx, c, id, "ready", taskcard.Opts{By: "rowan", Why: "deps met"})
		must(err)
		_, err = taskcard.Move(ctx, c, id, "working", taskcard.Opts{By: "rowan", As: "f1", Friend: "f1", SetFriend: true})
		must(err)
	}
	refusedAccept := func(by string) {
		t.Helper()
		_, err := taskcard.Land(ctx, c, ws.SentinelID("p"), by, snSHA, "accept")
		if err == nil || !strings.Contains(err.Error(), "lands by the coordinator's acceptance alone") {
			t.Fatalf("task land by %q: %v, want the acceptance refusal", by, err)
		}
	}
	sid := ws.SentinelID("p")

	// (a) every member lands, BY THE COORDINATOR SEAT: still nothing lands the sentinel
	snPush(t, c, "A", "p", "")
	snPush(t, c, "B", "p", "")
	toMerging("A")
	toMerging("B")
	_, err := taskcard.Land(ctx, c, "A", "rowan", snSHA, "merged")
	must(err)
	_, err = taskcard.Land(ctx, c, "B", "rowan", snSHA, "merged")
	must(err)
	if w, s := where(sid); w != "waiting" || s != "" {
		t.Fatalf("(a) after the last member landed by rowan: %s %s", w, s)
	}
	n := logLen()
	for _, by := range []string{"bob", "stella", ""} {
		refusedAccept(by)
	}
	if logLen() != n {
		t.Fatal("(a) a refused acceptance wrote ws:log")
	}
	if w, _ := where(sid); w != "waiting" {
		t.Fatalf("(a) after refusals: %s", w)
	}

	// (c) ready-for-acceptance, then a member comes back live (done/ok -> merging,
	// and a new push): acceptance refused while live; sentinel stays waiting
	snPush(t, c, "C", "p", "")
	toMerging("C")
	_, err = taskcard.Done(ctx, c, "C", "rowan", "no pr", "")
	must(err)
	if w, _ := where("C"); w != "done" {
		t.Fatalf("C %s", w)
	}
	if w, _ := where(sid); w != "waiting" {
		t.Fatalf("(c) C done: sentinel %s", w)
	}
	_, err = taskcard.Move(ctx, c, "C", "merging", taskcard.Opts{By: "rowan", Why: "reopened: the PR of a closed task"})
	must(err)
	if _, err := taskcard.Land(ctx, c, sid, "rowan", snSHA, "accept"); err == nil || !strings.Contains(err.Error(), "live card") {
		t.Fatalf("(c) the coordinator's accept with C reopened: %v", err)
	}
	_, err = taskcard.Land(ctx, c, "C", "rowan", snSHA, "merged")
	must(err)
	if w, _ := where(sid); w != "waiting" {
		t.Fatalf("(c) C relanded: sentinel %s", w)
	}

	// the coordinator's acceptance
	r, err := taskcard.Land(ctx, c, sid, "rowan", snSHA, "accepted")
	if err != nil || r.From != "waiting" || r.To != "landed" {
		t.Fatalf("accept %+v %v", r, err)
	}
	// (c') after acceptance a new card restarts the stream: the stop returns to waiting
	snPush(t, c, "D", "p", "")
	if w, s := where(sid); w != "waiting" {
		t.Fatalf("(c') push after acceptance: sentinel %s %s, want waiting", w, s)
	}
	toMerging("D")
	_, err = taskcard.Land(ctx, c, "D", "rowan", snSHA, "merged")
	must(err)
	if w, _ := where(sid); w != "waiting" {
		t.Fatalf("(c') D landed: sentinel %s", w)
	}

	// (b1) rename of a stream whose stop is only READY (not accepted), by bob,
	// to another slug: the new stop must NOT land (rename carries no acceptance it never had)
	_, err = ws.Rename(ctx, c, "p", "q", "bob")
	must(err)
	qsid := ws.SentinelID("q")
	if w, _ := where(qsid); w == "landed" {
		t.Fatalf("(b1) rename of an unaccepted stop landed %s", qsid)
	}
	if w, _ := where(sid); w != "done" {
		t.Fatalf("(b1) old stop %s", w)
	}
	// accept q, then (b2) rename it by bob: the new stop lands, the receipt says rename
	_, err = taskcard.Land(ctx, c, qsid, "rowan", snSHA, "accepted q")
	must(err)
	_, err = ws.Rename(ctx, c, "q", "r", "bob")
	must(err)
	rsid := ws.SentinelID("r")
	if w, s := where(rsid); w != "landed" || s != snSHA {
		t.Fatalf("(b2) rename of an accepted stop: %s %s", w, s)
	}
	log, _ := c.XRange(ctx, "ws:log", "-", "+").Result()
	var got string
	for _, e := range log {
		if e.Values["id"] == rsid && e.Values["to"] == "landed" {
			got = e.Values["by"].(string) + " " + e.Values["why"].(string)
		}
	}
	if got != "bob rename: landed as q" {
		t.Fatalf("(b2) receipt %q", got)
	}
	if err := ws.Check(ctx, c, []string{"A", "B", "C", "D", sid, qsid, rsid}); err != nil {
		t.Fatalf("invariant: %v", err)
	}
	_ = redis.Nil

	// (d) no sentinel copy can exist for jev to classify: the deal refuses the stop
	snPush(t, c, "E", "r", "")
	k := taskcard.Consumer{Kind: "bench", Name: "b"}
	must(c.SAdd(ctx, "benches", "b").Err())
	must(c.HSet(ctx, k.DesiredKey(), "slots", "2").Err())
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{rsid}, By: "rowan"})
	if err == nil && len(d) > 0 && d[0].Copy != "" {
		t.Fatalf("(d) a sentinel was dealt: %+v", d)
	}
	t.Logf("(d) deal of %s: %+v %v", rsid, d, err)
}
