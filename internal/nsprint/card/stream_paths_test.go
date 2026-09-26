//go:build functional

package card_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func streamPaths(t *testing.T, ctx context.Context, client *redis.Client, stream string) string {
	t.Helper()
	v, err := client.HGet(ctx, ws.PathsKey, stream).Result()
	if err == redis.Nil {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestCardPushStreamsPathsDisjoint is the invariant through the real FCALL
// on a throwaway store (#4322): a card push whose PATHS overlap another
// open stream's is refused with the exact receipt and nothing is written;
// --join pushes it onto that stream; the prefix rule refuses a parent dir;
// disjoint passes; ws:paths is the union of each stream's live cards
// (a landing recomputes it, a task-card push and cancel keep it too); and
// ws check's LivePaths/RepairPaths rebuild a lost record and name two
// streams that already overlap.
func TestCardPushStreamsPathsDisjoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	card0 := func(label, stream, paths string) []byte {
		f := validCard(srv.URL + "/acme/public.git")
		f.label, f.paths = label, paths
		return withHeader(f, "STREAM: "+stream)
	}

	if res := card.Push(ctx, client, sprint, card0("sp-a", "work", "internal/nsprint/ws")); res.Code != 0 {
		t.Fatalf("push sp-a: exit %d stderr %q", res.Code, res.Stderr)
	}
	if got := streamPaths(t, ctx, client, "work"); got != "internal/nsprint/ws" {
		t.Fatalf("ws:paths work = %q after sp-a", got)
	}

	res := card.Push(ctx, client, sprint, card0("sp-b", "ci", "internal/nsprint/ws/check.go"))
	want := `REFUSED PATHS overlap stream=work paths=internal/nsprint/ws,internal/nsprint/ws/check.go remedy="--join work" card=sp-b` + "\n"
	if res.Code != 2 || res.Stdout != want {
		t.Fatalf("overlap: exit %d stdout %q; want exit 2 stdout %q", res.Code, res.Stdout, want)
	}
	assertAbsent(t, ctx, client, "sp-b")
	if got := streamPaths(t, ctx, client, "ci"); got != "" {
		t.Fatalf("refused push wrote ws:paths ci = %q", got)
	}

	res = card.PushWith(ctx, client, sprint, card0("sp-b", "ci", "internal/nsprint/ws/check.go"), card.PushOptions{Join: "work"})
	if res.Code != 0 {
		t.Fatalf("--join work: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	if s := client.HGet(ctx, keyCard("sp-b"), "stream").Val(); s != "work" {
		t.Fatalf("joined card stream = %q; want work", s)
	}
	if n := client.ZCard(ctx, "ws:ci:ready").Val() + client.ZCard(ctx, "ws:ci:waiting").Val(); n != 0 {
		t.Fatalf("joined card is in ci's sets (%d)", n)
	}
	if got := streamPaths(t, ctx, client, "work"); got != "internal/nsprint/ws,internal/nsprint/ws/check.go" {
		t.Fatalf("ws:paths work = %q after the join", got)
	}

	if res := card.Push(ctx, client, sprint, card0("sp-c", "ci", "cmd/nova-sprint/ci.go")); res.Code != 0 {
		t.Fatalf("disjoint sp-c: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	res = card.Push(ctx, client, sprint, card0("sp-d", "fleet", "cmd/nova-sprint"))
	want = `REFUSED PATHS overlap stream=ci paths=cmd/nova-sprint,cmd/nova-sprint/ci.go remedy="--join ci" card=sp-d` + "\n"
	if res.Code != 2 || res.Stdout != want {
		t.Fatalf("parent dir: exit %d stdout %q; want %q", res.Code, res.Stdout, want)
	}
	assertAbsent(t, ctx, client, "sp-d")

	// A landing frees the card's paths: the stream keeps its live cards'.
	mustLand(t, ctx, client, "sp-a", strings.Repeat("a", 40))
	if got := streamPaths(t, ctx, client, "work"); got != "internal/nsprint/ws/check.go" {
		t.Fatalf("ws:paths work = %q after sp-a landed; want sp-b's alone", got)
	}
	if res := card.Push(ctx, client, sprint, card0("sp-e", "docs", "internal/nsprint/ws/show.go")); res.Code != 0 {
		t.Fatalf("sp-e after the landing: exit %d stdout %q", res.Code, res.Stdout)
	}

	// The task-card path holds its paths the same way; a cancel frees them.
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "sp-t", Stream: "tasks", Title: "t", By: "test",
		Fields: []string{"paths", "`deploy/`, deploy/x.yml"}}); err != nil {
		t.Fatal(err)
	}
	if got := streamPaths(t, ctx, client, "tasks"); got != "deploy,deploy/x.yml" {
		t.Fatalf("ws:paths tasks = %q", got)
	}
	if _, err := taskcard.Move(ctx, client, "sp-t", "done", taskcard.Opts{By: "test", Why: "cancel", OK: "fail"}); err != nil {
		t.Fatal(err)
	}
	if got := streamPaths(t, ctx, client, "tasks"); got != "" {
		t.Fatalf("ws:paths tasks = %q after the cancel; want none", got)
	}

	// ws check: a store whose record is lost (cut before #4322) is stale
	// and repaired from the records; two streams that already overlap (a
	// task pushed past no gate) are named.
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "sp-u", Stream: "old", Title: "u", By: "test",
		Fields: []string{"paths", "cmd/nova-sprint/ci.go"}}); err != nil {
		t.Fatal(err)
	}
	client.Del(ctx, ws.PathsKey)
	client.HDel(ctx, keyCard("sp-c"), ws.PathsField)
	live, stored, cards, err := ws.LivePaths(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 || strings.Join(ws.Stale(live, stored), ",") != "ci,docs,old,work" || len(cards) != 4 {
		t.Fatalf("stale = %q cards %d; want ci,docs,old,work over 4 cards", ws.Stale(live, stored), len(cards))
	}
	var lines []string
	for _, p := range live.Overlaps() {
		lines = append(lines, p.Line())
	}
	if strings.Join(lines, "\n") != "PATHS OVERLAP stream=ci other=old paths=cmd/nova-sprint/ci.go" {
		t.Fatalf("overlaps = %q", lines)
	}
	streams, records, err := ws.RepairPaths(ctx, client, live, stored, cards)
	if err != nil || streams != 4 || records != 1 {
		t.Fatalf("repair: streams %d records %d err %v; want 4 and 1", streams, records, err)
	}
	if got := streamPaths(t, ctx, client, "work"); got != "internal/nsprint/ws/check.go" {
		t.Fatalf("repaired ws:paths work = %q", got)
	}
	if client.HGet(ctx, keyCard("sp-c"), ws.PathsField).Val() != "cmd/nova-sprint/ci.go" {
		t.Fatal("repair did not restore sp-c's stream_paths")
	}
	live, stored, _, _ = ws.LivePaths(ctx, client)
	if s := ws.Stale(live, stored); len(s) != 0 {
		t.Fatalf("stale after repair: %q", s)
	}
}
