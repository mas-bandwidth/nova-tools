//go:build functional

package consume

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// rtWait is the generous bound for waiting on an event (NOVA_TEST_WAIT,
// default 60 s): the tests assert the event, never the clock.
func rtWait() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}

// rtUntil polls cond until it holds or the generous bound passes.
func rtUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(rtWait())
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for %s", rtWait(), what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// rtFake stands in for a rule that is not built yet (pr-to-read and
// hold-to-fix, #2941): it counts its starts and passes.
type rtFake struct {
	started atomic.Int64
	passes  atomic.Int64
}

func (f *rtFake) Start(context.Context) error {
	f.started.Add(1)
	return nil
}

func (f *rtFake) Pass(ctx context.Context) (int, error) {
	f.passes.Add(1)
	// A real rule blocks on its source for up to its block; the fake paces
	// itself the same way so the router loop does not spin.
	time.Sleep(5 * time.Millisecond)
	return 0, ctx.Err()
}

// rtRouter builds the router the way `nova-sprint route` does, with the
// #2941 rules as fakes.
func rtRouter(st *store.Store, sprint, instance string, pr PRToRead, hold HoldToFix) *Router {
	ok := &OkFriend{Store: st, Sprint: sprint, Consumer: instance, Actor: "route", Block: 50 * time.Millisecond}
	report := &Report{Store: st, Sprint: sprint, Consumer: instance, Actor: "route", Block: 50 * time.Millisecond}
	return &Router{
		Store: st, Sprint: sprint, Instance: instance, Host: "ctl-host",
		Rules: Rules(ok, report, nil, pr, hold),
		TTL:   6 * time.Second, Renew: 20 * time.Millisecond, Backoff: 20 * time.Millisecond,
	}
}

// endNoCommit ends a card DONE that committed nothing: the end record has
// no pushed sha, and there is no branch and no PR (#2756 3.2 row
// ended(DONE) -> harvested: a card with no commit skips harvest).
func endNoCommit(t *testing.T, client *redis.Client, sprint, label, author string) {
	t.Helper()
	endCard(t, client, sprint, label, "DONE", "done", "docs/report.md", author)
	must(t, client.HSet(context.Background(), "s:"+sprint+":card:"+label,
		"pushed_sha", "", "results", "results/"+sprint+"/"+label+"/a1").Err())
}

func rtTaskIDs(t *testing.T, client *redis.Client, sprint string) map[string]map[string]string {
	t.Helper()
	ctx := context.Background()
	ids, err := client.SMembers(ctx, "s:"+sprint+":idx:task:open").Result()
	must(t, err)
	out := map[string]map[string]string{}
	for _, id := range ids {
		h, err := client.HGetAll(ctx, "task:"+id).Result()
		must(t, err)
		out[id] = h
	}
	return out
}

func rtEndedEvent(t *testing.T, client *redis.Client, sprint, label string) redis.XMessage {
	t.Helper()
	entries, err := client.XRange(context.Background(), "s:"+sprint+":log", "-", "+").Result()
	must(t, err)
	for _, e := range entries {
		if e.Values["kind"] == "card" && e.Values["id"] == label && e.Values["to"] == "ended" {
			return e
		}
	}
	t.Fatalf("no ended event for %s", label)
	return redis.XMessage{}
}

// TestRouteReportReadForNoCommitCard is #3036 (#2756 section 11 row 5): a
// card that ends DONE and commits nothing gets exactly one report read task
// for a non-author friend at its results ref, and never a harvest or review
// task; a redelivered end event adds nothing. A card that committed still
// gets its harvest task and no report read.
func TestRouteReportReadForNoCommitCard(t *testing.T) {
	t.Parallel()

	st, client := controlRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sprint := "control-3036a0b1"
	seedSprint(t, client, sprint)
	pr, hold := &rtFake{}, &rtFake{}
	router := rtRouter(st, sprint, "route-a", pr, hold)
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx) }()

	endNoCommit(t, client, sprint, "card-r", "ctl-a")
	endCard(t, client, sprint, "card-c", "DONE", "done", "internal/x/c.go", "ctl-a")

	reportID := ReportReadID("card-r", "1")
	rtUntil(t, "the report read of card-r and the harvest task of card-c", func() bool {
		tasks := rtTaskIDs(t, client, sprint)
		return tasks[reportID] != nil && tasks["harvest-card-c"] != nil
	})
	rtUntil(t, "both ended events acked by both groups", func() bool {
		for _, g := range []string{GroupOkFriend, GroupReport} {
			p, err := client.XPending(ctx, "s:"+sprint+":log", g).Result()
			if err != nil || p.Count != 0 {
				return false
			}
		}
		return true
	})

	check := func(when string) {
		t.Helper()
		tasks := rtTaskIDs(t, client, sprint)
		var mine []string
		for id, h := range tasks {
			if strings.Contains(h["ref"], "/card-r/") || strings.Contains(id, "card-r") {
				mine = append(mine, id)
			}
		}
		if len(mine) != 1 || mine[0] != reportID {
			t.Fatalf("%s: card-r has tasks %v; want exactly the one report read %s", when, mine, reportID)
		}
		h := tasks[reportID]
		if h["kind"] != "read" || h["state"] != "open" || h["effects"] != "none" {
			t.Fatalf("%s: %s = %v; want an open read task with effects none", when, reportID, h)
		}
		if h["ref"] != "results/"+sprint+"/card-r/a1" {
			t.Fatalf("%s: %s ref %q; want the card's results ref", when, reportID, h["ref"])
		}
		var owners []string
		for _, f := range []string{"ctl-a", "ctl-b", "ctl-c", "ctl-down"} {
			if _, err := client.ZScore(context.Background(), "s:"+sprint+":open:"+f, reportID).Result(); err == nil {
				owners = append(owners, f)
			}
		}
		if len(owners) != 1 || owners[0] == "ctl-a" || owners[0] == "ctl-down" {
			t.Fatalf("%s: %s is queued for %v; want exactly one UP non-author friend", when, reportID, owners)
		}
		if n, _ := client.Exists(context.Background(), "task:harvest-card-r").Result(); n != 0 {
			t.Fatalf("%s: a card that committed nothing got a harvest task", when)
		}
		for id, h := range tasks {
			if h["kind"] == "review" {
				t.Fatalf("%s: review task %s exists with no PR", when, id)
			}
		}
		if _, ok := tasks[ReportReadID("card-c", "1")]; ok {
			t.Fatalf("%s: a card that committed got a report read", when)
		}
		pushes, _ := logCounts(t, client, sprint)
		if pushes[reportID] != 1 || pushes["harvest-card-c"] != 1 {
			t.Fatalf("%s: task push receipts %v; want one for %s and one for harvest-card-c", when, pushes, reportID)
		}
	}
	check("after the end")

	// Stop the router, then redeliver: the same event id (an ack lost after
	// the function ran) and a second end event for the same attempt (a
	// reconciler ingesting the end record after `card end`).
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router run: %v", err)
	}
	ended := rtEndedEvent(t, client, sprint, "card-r")
	report := &Report{Store: st, Sprint: sprint, Consumer: "route-b", Actor: "route"}
	ok := &OkFriend{Store: st, Sprint: sprint, Consumer: "route-b", Actor: "route"}
	bg := context.Background()
	if _, err := report.handleBatch(bg, []redis.XMessage{ended}); err != nil {
		t.Fatalf("report redelivery: %v", err)
	}
	if _, err := ok.handleBatch(bg, []redis.XMessage{ended}); err != nil {
		t.Fatalf("ok-to-friend redelivery: %v", err)
	}
	reply, err := client.FCall(bg, FunctionOkFriendSkip, nil, sprint, GroupReport, ended.ID, "redelivered", "").Slice()
	if err != nil || len(reply) != 2 || reply[0] != "DUP" || reply[1] != "CREATED "+reportID {
		t.Fatalf("redelivered report event = %v, %v; want DUP CREATED %s", reply, err, reportID)
	}
	values := make([]any, 0, 2*len(ended.Values))
	for k, v := range ended.Values {
		values = append(values, k, v)
	}
	again, err := client.XAdd(bg, &redis.XAddArgs{Stream: "s:" + sprint + ":log", Values: values}).Result()
	must(t, err)
	second := &Report{Store: st, Sprint: sprint, Consumer: "route-b", Actor: "route", Block: 50 * time.Millisecond}
	must(t, second.Start(bg))
	if _, err := second.Pass(bg); err != nil {
		t.Fatalf("report pass over the second end event: %v", err)
	}
	if got, err := client.HGet(bg, "s:"+sprint+":idem", GroupReport+":"+again).Result(); err != nil || got != "EXISTS "+reportID {
		t.Fatalf("second end event recorded %q, %v; want EXISTS %s", got, err, reportID)
	}
	check("after the redeliveries")
}
