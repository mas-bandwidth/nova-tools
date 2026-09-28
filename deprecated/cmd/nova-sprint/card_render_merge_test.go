//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// TestCardRenderFoldsTheMergeBriefIn (nova-tools #4324, #4449): a merge
// card's task record carries only the title (one writer, #3778); its body
// is the stream's brief rendered when the card is read (the members in
// work order with PR and merging age, the rules, the MERGE-NOTEs), and
// `card render --brief` renders the merge template with that brief
// quoted. A stream with nothing merging gives no brief: the card renders
// from the title alone.
func TestCardRenderFoldsTheMergeBriefIn(t *testing.T) {
	t.Parallel()
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const id, s = "merge-swarm-cards-1", "swarm: cards"
	c.HSet(ctx, "task:"+id, "kind", "merge", "state", "open", "repo", "mas-bandwidth/nova-tools", "ref", "dev", "stream", s,
		"title", "STREAM: swarm: cards | land stream swarm: cards: 2 members in work order into dev as one PR | PATHS: a b | BASE: dev | DONE-WHEN: nova-sprint land --repo mas-bandwidth/nova-tools --stream \"swarm: cards\" prints LANDED n=2")
	c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: s})
	c.ZAdd(ctx, "ws:"+s+":merging", redis.Z{Score: 10, Member: "t1"}, redis.Z{Score: 20, Member: "t2"})
	c.HSet(ctx, "task:t1", "pr", "nova-tools#1", "paths", "a")
	c.HSet(ctx, "task:t2", "pr", "nova-tools#2", "paths", "b")
	c.RPush(ctx, "ws:"+s+":notes", "MERGE-NOTE by=rowan at=1 the moves API changed")
	code, out, errOut := runSprint("card", "render", "--redis", addr, "--id", id, "--brief", "--model", "opus-5.5")
	if code != 0 {
		t.Fatalf("render: %d %q %q", code, out, errOut)
	}
	for _, want := range []string{"CARD: " + id + "\n", "\nKIND: merge\n", "You are the merge card of ONE work stream",
		"> Merge card for stream swarm: cards: land its 2 merging members into dev as ONE pull request from stream/swarm-cards.\n",
		">   1. t1 pr=nova-tools#1 order=10 merging_for=", ">   2. t2 pr=nova-tools#2 order=20 merging_for=",
		"MERGE-NOTE by=rowan at=1 the moves API changed", "--card " + id, "PATHS: a b\n", "DONE-WHEN: nova-sprint land --repo mas-bandwidth/nova-tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	// Nothing merging: the card renders from the title alone, no fold.
	c.Del(ctx, "ws:"+s+":merging")
	if code, out, _ := runSprint("card", "render", "--redis", addr, "--id", id, "--brief", "--model", "opus-5.5"); code != 0 || strings.Contains(out, "> Merge card") {
		t.Fatalf("without members: %d\n%s", code, out)
	}
}
