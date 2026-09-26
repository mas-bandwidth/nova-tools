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

// frontierWorker enrols consumer s, live with slots, advertising tiers ("":
// nothing advertised, the default).
func frontierWorker(t *testing.T, c *redis.Client, s, tiers string) taskcard.Consumer {
	t.Helper()
	ctx := context.Background()
	k := mustConsumer(t, s)
	c.SAdd(ctx, k.Kind+"s", k.Name)
	c.HSet(ctx, k.DesiredKey(), "slots", "4")
	if tiers != "" {
		c.HSet(ctx, k.DesiredKey(), "tiers", tiers)
	}
	if err := taskcard.Enroll(ctx, c, k, true); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, k.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	return k
}

// pushRouted pushes one primary carrying ROUTE route (a cut card carries its
// route on the record, #3911).
func pushRouted(t *testing.T, c *redis.Client, id, route string) {
	t.Helper()
	_, err := taskcard.Push(context.Background(), c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: mvStream,
		Sprint: sprint, Kind: "build", Ref: "nova-tools#4300", Origin: "issue:nova-tools#4300", Title: "primary " + id,
		Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"route", route, "base", "dev", "base_sha", baseSHA, "paths", "internal/x.go",
			"done_when", "go test ./internal/x -run TestX passes"}})
	if err != nil {
		t.Fatalf("push %s: %v", id, err)
	}
}

// TestFrontierCardGoesOnlyToAWorkerAdvertisingIt (Glenn 2026-09-26 10:03 AM
// ET: three model types, frontier, pro, flash; "each friend and each bench
// can advertize certain models they can run"): a frontier card is refused
// for a worker with no tiers advertised (the default, flash,pro) and for one
// advertising pro,flash, and dealt to one advertising frontier, bench or
// friend alike; the unnamed deal skips it for a worker that cannot run it;
// a pro card still goes to the default worker; a read is pro, so a worker
// advertising frontier alone is refused the read and the default worker
// takes it. Nothing here names a friend.
func TestFrontierCardGoesOnlyToAWorkerAdvertisingIt(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	plain := frontierWorker(t, c, "bench:plain", "")
	swarmy := frontierWorker(t, c, "friend:swarmy", "pro,flash")
	fbench := frontierWorker(t, c, "bench:fbench", "frontier")
	ffriend := frontierWorker(t, c, "friend:ffriend", "frontier,pro")
	fonly := frontierWorker(t, c, "bench:fonly", "frontier") // never an author: the read leg's tier check alone
	for _, id := range []string{"fr1", "fr2", "fr3", "fr4"} {
		pushRouted(t, c, id, taskcard.RouteFrontier)
	}
	pushRouted(t, c, "pro1", taskcard.RoutePro)
	pushRouted(t, c, "fl1", taskcard.RouteFlash)

	refused := func(to taskcard.Consumer, id, want string) {
		t.Helper()
		_, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: to, IDs: []string{id}, By: "rowan"})
		if err == nil || !strings.Contains(err.Error(), "TIER "+to.String()+" advertises "+want) {
			t.Fatalf("deal %s to %s: %v; want TIER %s advertises %s", id, to, err, to, want)
		}
	}
	dealt := func(to taskcard.Consumer, id, leg string) {
		t.Helper()
		d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: to, IDs: []string{id}, By: "rowan"})
		if err != nil || len(d) != 1 || d[0].Primary != id {
			t.Fatalf("deal %s to %s: %v %v", id, to, d, err)
		}
		if got := c.HGet(ctx, taskcard.Key(d[0].Copy), "leg").Val(); got != leg {
			t.Fatalf("deal %s to %s: leg %q, want %s", id, to, got, leg)
		}
	}
	refused(plain, "fr1", "flash,pro, not frontier")
	refused(swarmy, "fr1", "flash,pro, not frontier")
	dealt(fbench, "fr1", "work")
	dealt(ffriend, "fr2", "work")
	dealt(plain, "pro1", "work")
	refused(fbench, "fl1", "frontier, not flash")

	// the unnamed deal never hands a frontier card to a worker that did not
	// advertise it: the two left in waiting stay there for plain and go to
	// the frontier friend
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: plain, N: 10, By: "rowan"}); err != nil || len(d) != 1 || d[0].Primary != "fl1" {
		t.Fatalf("unnamed deal to the default worker: %v %v; want fl1 alone", d, err)
	}
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: ffriend, N: 10, By: "rowan"}); err != nil || len(d) != 2 ||
		d[0].Primary != "fr3" || d[1].Primary != "fr4" {
		t.Fatalf("unnamed deal to the frontier friend: %v %v; want fr3, fr4", d, err)
	}
	wantWS(t, c, "deals", map[string]int64{"waiting": 0, "working": 6})

	// a read is pro: fr1's work copy ends ok with a PR, its primary is in
	// review and its read copies are cut at once (#4094) onto the swarm's
	// benches with room that may read it: the default worker, never the
	// bench advertising frontier alone though it has room and wrote nothing
	w, err := taskcard.Work(ctx, c, fbench, "rowan", 0, true)
	if err != nil || len(w.IDs) != 1 {
		t.Fatalf("work %+v %v", w, err)
	}
	recordPR(c, 4300, head(0))
	e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: w.IDs, OK: true, Repo: "nova-tools", PR: "4300",
		Head: head(0), By: "rowan", Fields: []string{"line1", "RESULT: ok", "line2", "DONE", "branch", "x/y", "commit", head(0)[:12]}})
	if err != nil || len(e) != 1 || e[0].To != "review" || e[0].Next == "" {
		t.Fatalf("end ok: %v %v; want review with read copies", e, err)
	}
	for _, cid := range strings.Split(e[0].Next, ",") {
		r := c.HMGet(ctx, taskcard.Key(cid), "leg", "tier", "route", "consumer").Val()
		if fmt.Sprint(r) != "[read pro read "+plain.String()+"]" {
			t.Fatalf("read copy %s: leg tier route consumer = %v; want read pro read %s", cid, r, plain)
		}
	}
	if n := c.ZCard(ctx, fonly.Key("ready")).Val(); n != 0 {
		t.Fatalf("the frontier-only bench holds %d read copies; a read is pro", n)
	}
	cleanMoves(t, c, "frontier")
}
