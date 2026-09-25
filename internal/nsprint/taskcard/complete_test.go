package taskcard_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

const (
	cardStream = "swarm: cards"
	baseSHA    = "c8178673f0000000000000000000000000000000"
)

// issueText is an issue body as Rowan files one: header lines scattered
// through prose, DONE-WHEN last, and prose lines that look like keys only
// behind a quote.
func issueText(n int, paths string) string {
	lines := []string{"STREAM: " + cardStream, "WHO: any", "BASE: dev", "base-sha: " + baseSHA}
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

// mirror makes card push's repository probe local: a bare mirror directory
// for nova-tools, so the lint never asks the network.
func mirror(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nova-tools.git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVA_MIRROR_ROOT", root)
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
	mirror(t)
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
		"base_sha": baseSHA, "paths": "internal/nsprint/taskcard/complete.go", "stream": cardStream, "where": "waiting",
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
	for _, want := range []string{"RESULT: flash-3911 sha=" + baseSHA[:12] + "\n", "\nKIND: fix\n", "\nROUTE: flash\n",
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
	brief, err := taskcard.RenderBrief("friend-3911", frec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TASK: friend-3911\n", "STREAM: " + cardStream + "\n", "PATHS: -\n",
		"DONE-WHEN: `go test ./internal/nsprint/taskcard", "\n> Card 0: make the waiting record"} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
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

	// 100 waiting cards: 50 to the bench batman, 50 to the friend stella.
	var moves []taskcard.DealMove
	var ids []string
	for i := 0; i < 100; i++ {
		id, route, to := fmt.Sprintf("w-%03d", i), taskcard.RouteFlash, "batman"
		if i%2 == 1 {
			route, to = taskcard.RouteFriend, "stella"
		}
		if _, err := pushIssue(t, c, id, route, "docs/fixtures/w.txt"); err != nil {
			t.Fatalf("push %s: %v", id, err)
		}
		moves = append(moves, taskcard.DealMove{ID: id, To: to})
		ids = append(ids, id)
	}
	if n := c.ZCard(ctx, taskcard.StreamKey(cardStream, "waiting")).Val(); n != 102 {
		t.Fatalf("waiting = %d, want 102", n)
	}
	trips := &roundTrips{}
	c.AddHook(trips)
	res, err := taskcard.Deal(ctx, c, "rowan", moves)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := taskcard.Records(ctx, c, ids...)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range recs {
		render := taskcard.RenderHeader
		if moves[i].To == "stella" {
			render = taskcard.RenderBrief
		}
		if _, err := render(ids[i], r); err != nil {
			t.Fatalf("render %s: %v", ids[i], err)
		}
	}
	if n := trips.n.Load(); n != 2 {
		t.Errorf("deal and render of 100 took %d round trips, want 2", n)
	}
	for _, r := range res {
		if r.Refused != "" || r.Result.From != "waiting" || r.Result.To != "ready" {
			t.Errorf("deal %s: %+v", r.ID, r)
		}
	}
	for seat, want := range map[string]int64{"batman": 50, "stella": 50} {
		if n := c.ZCard(ctx, taskcard.FriendKey(seat, "ready")).Val(); n != want {
			t.Errorf("%s ready = %d, want %d", seat, n, want)
		}
	}
	if n := c.ZCard(ctx, taskcard.StreamKey(cardStream, "waiting")).Val(); n != 2 {
		t.Errorf("waiting after the deal = %d, want 2", n)
	}
	clean(t, c, "after the deal")
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
