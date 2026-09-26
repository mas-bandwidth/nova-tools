package taskcard_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/brief"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

const (
	cardStream   = "swarm: cards"
	issueBaseSHA = "c8178673f0000000000000000000000000000000"
)

// issueText is an issue body as Rowan files one: header lines scattered
// through prose, DONE-WHEN last, and prose lines that look like keys only
// behind a quote.
func issueText(n int, paths string) string {
	lines := []string{"STREAM: " + cardStream, "WHO: any", "BASE: dev", "base-sha: " + issueBaseSHA}
	if paths != "" {
		lines = append(lines, "PATHS: "+paths)
	}
	lines = append(lines, "",
		fmt.Sprintf("Card %d: make the waiting record carry everything; the harness header is rendered from it.", n),
		"> PATHS: a quoted line is prose, not a header",
		"",
		"DONE-WHEN: `go test ./internal/nsprint/taskcard -run TestWaitingCardIsCompleteForFriendOrSwarm` passes.")
	return strings.Join(lines, "\n")
}

func pushIssue(t *testing.T, c *redis.Client, id, route, paths string) (taskcard.PushResult, error) {
	t.Helper()
	spec := taskcard.ParseIssue(issueText(0, paths))
	spec.Route = route
	return taskcard.Push(context.Background(), c, taskcard.PushRequest{ID: id, Where: "waiting", Sprint: sprint,
		Ref: "mas-bandwidth/nova-tools#3911", Origin: "issue:mas-bandwidth/nova-tools#3911",
		Title: "a waiting card is complete", By: "rowan", Spec: &spec})
}

// TestMain makes card push's repository probe local for the whole package:
// a bare mirror directory for nova-tools under $NOVA_MIRROR_ROOT, so the lint
// never asks the network. It is set once here, not per test with t.Setenv,
// so every test in the package can run with t.Parallel().
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "taskcard-mirror-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Join(root, "nova-tools.git", "objects"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("NOVA_MIRROR_ROOT", root)
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// TestWaitingCardIsCompleteForFriendOrSwarm is the #3911 DONE-WHEN: a card
// pushed from an issue with who=flash renders a harness header card push's
// linter accepts with no generation step; a card with who=friend renders a
// friend brief from the same record; a push with who=pro and no PATHS is
// refused naming PATHS and writes nothing; and dealing 100 waiting cards (50
// to a bench, 50 to a friend) with their cards and briefs rendered takes two
// Redis round trips in all (one pipeline of the one move, one of HGETALLs)
// and no generation step: the under-one-second bound, held by structure
// rather than by a wall clock the CI path refuses (internal/ci waits check).
func TestWaitingCardIsCompleteForFriendOrSwarm(t *testing.T) {
	t.Parallel()
	c := start(t)
	c.SAdd(context.Background(), "friends", "batman")
	ctx := context.Background()

	// who=flash: the record alone renders a card the linter admits.
	if _, err := pushIssue(t, c, "flash-3911", taskcard.RouteFlash, "internal/nsprint/taskcard/complete.go"); err != nil {
		t.Fatalf("flash push: %v", err)
	}
	rec, err := taskcard.Record(ctx, c, "flash-3911")
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"route": "flash", "repo": "mas-bandwidth/nova-tools", "base": "dev",
		"base_sha": issueBaseSHA, "paths": "internal/nsprint/taskcard/complete.go", "stream": cardStream, "where": "waiting",
		"test": "./internal/nsprint/taskcard TestWaitingCardIsCompleteForFriendOrSwarm", "depends_on": "none",
		"who": "any", "est": "30", "priority": "0", "source": "issue", "type": "code"} {
		if rec[field] != want {
			t.Errorf("record %s = %q, want %q", field, rec[field], want)
		}
	}
	if !strings.Contains(rec["body"], "DONE-WHEN:") || rec["done_when"] == "" {
		t.Errorf("record lacks body or done_when: %v", rec)
	}
	hdr, err := taskcard.RenderHeader("flash-3911", rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := card.Lint(ctx, hdr); err != nil {
		t.Fatalf("card lint refused the rendered header: %v\n%s", err, hdr)
	}
	for _, want := range []string{"RESULT: flash-3911 sha=" + issueBaseSHA[:12] + "\n", "\nKIND: fix\n", "\nROUTE: flash\n",
		"\nSTREAM: " + cardStream + "\n", "\nTASK: a waiting card is complete\n", "\n> > PATHS: a quoted line"} {
		if !strings.Contains(string(hdr), want) {
			t.Errorf("header lacks %q:\n%s", want, hdr)
		}
	}

	// who=friend: no PATHS needed; the brief comes from the same record.
	if _, err := pushIssue(t, c, "friend-3911", taskcard.RouteFriend, ""); err != nil {
		t.Fatalf("friend push: %v", err)
	}
	frec, err := taskcard.Record(ctx, c, "friend-3911")
	if err != nil {
		t.Fatal(err)
	}
	fb, err := brief.RenderCard("friend-3911", frec, "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CARD: friend-3911\n", "STREAM: " + cardStream + "\n", "PATHS: -\n",
		"DONE-WHEN: `go test ./internal/nsprint/taskcard", "\n> Card 0: make the waiting record"} {
		if !strings.Contains(string(fb), want) {
			t.Errorf("brief lacks %q:\n%s", want, fb)
		}
	}
	if _, err := taskcard.RenderHeader("friend-3911", frec); err == nil {
		t.Error("a friend-route record rendered a swarm header")
	}

	// who=pro with no PATHS: refused at push, the field named, nothing written.
	_, err = pushIssue(t, c, "pro-3911", taskcard.RoutePro, "")
	why, refused := taskcard.IsRefused(err)
	if !refused || !strings.Contains(why, "PATHS") {
		t.Fatalf("pro push without PATHS: err=%v, want a refusal naming PATHS", err)
	}
	if c.Exists(ctx, taskcard.Key("pro-3911")).Val() != 0 {
		t.Error("a refused push wrote the record")
	}

	// 100 waiting cards: 50 to the bench batman, 50 to the friend stella, on
	// the copy model (#4172): each deal cuts a consumer copy per primary in
	// one call, and the copy's card is RenderHeader (bench) or RenderBrief
	// (friend) of the record, nothing generated.
	batman, stella := taskcard.Consumer{Kind: "bench", Name: "batman"}, taskcard.Consumer{Kind: "friend", Name: "stella"}
	c.SAdd(ctx, "benches", "batman")
	c.SAdd(ctx, "friends", "stella")
	for _, x := range []taskcard.Consumer{batman, stella} {
		c.HSet(ctx, x.DesiredKey(), "slots", "50")
		if err := taskcard.Enroll(ctx, c, x, true); err != nil {
			t.Fatal(err)
		}
		c.HSet(ctx, x.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	}
	var ids, toBatman, toStella []string
	for i := 0; i < 100; i++ {
		id, route := fmt.Sprintf("w-%03d", i), taskcard.RouteFlash
		if i%2 == 1 {
			route = taskcard.RouteFriend
		}
		if _, err := pushIssue(t, c, id, route, "docs/fixtures/w.txt"); err != nil {
			t.Fatalf("push %s: %v", id, err)
		}
		ids = append(ids, id)
		if i%2 == 1 {
			toStella = append(toStella, id)
		} else {
			toBatman = append(toBatman, id)
		}
	}
	if n := c.ZCard(ctx, taskcard.StreamKey(cardStream, "waiting")).Val(); n != 102 {
		t.Fatalf("waiting = %d, want 102", n)
	}
	trips := &roundTrips{}
	c.AddHook(trips)
	var copies []string
	for _, d := range []struct {
		to  taskcard.Consumer
		ids []string
	}{{batman, toBatman}, {stella, toStella}} {
		dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: d.to, IDs: d.ids, By: "rowan"})
		if err != nil || len(dealt) != 50 {
			t.Fatalf("deal to %s: %d dealt, %v", d.to, len(dealt), err)
		}
		for _, x := range dealt {
			copies = append(copies, x.Copy)
		}
	}
	recs, err := taskcard.Records(ctx, c, copies...)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range recs {
		var err error
		if i >= 50 {
			_, err = brief.RenderCard(copies[i], r, "sonnet")
		} else {
			_, err = taskcard.RenderHeader(copies[i], r)
		}
		if err != nil {
			t.Fatalf("render %s: %v", copies[i], err)
		}
	}
	if n := trips.n.Load(); n != 3 {
		t.Errorf("two deals and the render of 100 took %d round trips, want 3", n)
	}
	for _, x := range []taskcard.Consumer{batman, stella} {
		if n := c.ZCard(ctx, x.Key("ready")).Val(); n != 50 {
			t.Errorf("%s ready copies = %d, want 50", x, n)
		}
	}
	if n := c.ZCard(ctx, taskcard.StreamKey(cardStream, "waiting")).Val(); n != 2 {
		t.Errorf("waiting after the deal = %d, want 2", n)
	}
	if n := c.ZCard(ctx, taskcard.StreamKey(cardStream, "working")).Val(); n != 100 {
		t.Errorf("working primaries after the deal = %d, want 100", n)
	}
	clean(t, c, "after the deal")
}

// TestPushFromIssueDependsOnLineLandsWaiting is the #3916 regression: a
// push from --issue whose body carries a DEPENDS-ON line, with neither
// --waiting nor --depends-on given (Where and DependsOn both empty on the
// request), must still land in waiting with blocked_on set from the issue.
// where was computed from r.DependsOn before fillSpec filled it from the
// spec, so such a card landed in ready with blocked_on written anyway.
func TestPushFromIssueDependsOnLineLandsWaiting(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()

	text := strings.Replace(issueText(0, ""), "WHO: any", "WHO: any\nDEPENDS-ON: nova-tools#1", 1)
	spec := taskcard.ParseIssue(text)
	spec.Route = taskcard.RouteFriend
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "depends-3916", Sprint: sprint,
		Ref: "mas-bandwidth/nova-tools#3916", Origin: "issue:mas-bandwidth/nova-tools#3916",
		Title: "a waiting card is complete", By: "rowan", Spec: &spec}); err != nil {
		t.Fatalf("push: %v", err)
	}
	rec, err := taskcard.Record(ctx, c, "depends-3916")
	if err != nil {
		t.Fatal(err)
	}
	if rec["where"] != "waiting" {
		t.Errorf("where = %q, want waiting", rec["where"])
	}
	if rec["blocked_on"] != "nova-tools#1" {
		t.Errorf("blocked_on = %q, want nova-tools#1", rec["blocked_on"])
	}
	clean(t, c, "after the depends-on push")
}

// roundTrips counts the client's round trips: a command or a pipeline is one.
type roundTrips struct{ n atomic.Int64 }

func (r *roundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (r *roundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		r.n.Add(1)
		return next(ctx, cmd)
	}
}

func (r *roundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		r.n.Add(1)
		return next(ctx, cmds)
	}
}

// TestFrontierRouteIsCompleteAndRenders: ROUTE: frontier is the third model
// type (Glenn 2026-09-26): a push with it lands the route on the record and
// renders a harness header the card linter admits, ROUTE: frontier on it; a
// route that is none of frontier, pro, flash or friend is refused naming
// ROUTE and the three.
func TestFrontierRouteIsCompleteAndRenders(t *testing.T) {
	t.Parallel()
	c := start(t)
	ctx := context.Background()
	if _, err := pushIssue(t, c, "frontier-4300", taskcard.RouteFrontier, "internal/nsprint/taskcard/complete.go"); err != nil {
		t.Fatalf("frontier push: %v", err)
	}
	rec, err := taskcard.Record(ctx, c, "frontier-4300")
	if err != nil {
		t.Fatal(err)
	}
	if rec["route"] != "frontier" || rec["where"] != "waiting" {
		t.Fatalf("record route=%q where=%q; want frontier, waiting", rec["route"], rec["where"])
	}
	hdr, err := taskcard.RenderHeader("frontier-4300", rec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := card.Lint(ctx, hdr); err != nil {
		t.Fatalf("card lint refused the rendered header: %v\n%s", err, hdr)
	}
	if !strings.Contains(string(hdr), "\nROUTE: frontier\n") {
		t.Fatalf("header lacks ROUTE: frontier:\n%s", hdr)
	}
	_, err = pushIssue(t, c, "turbo-4300", "turbo", "internal/nsprint/taskcard/complete.go")
	if err == nil || !strings.Contains(err.Error(), "ROUTE") || !strings.Contains(err.Error(), "frontier, pro or flash") {
		t.Fatalf("ROUTE turbo: %v; want a refusal naming ROUTE and frontier, pro or flash", err)
	}
	if n, _ := c.Exists(ctx, taskcard.Key("turbo-4300")).Result(); n != 0 {
		t.Fatal("the refused push wrote a record")
	}
}
