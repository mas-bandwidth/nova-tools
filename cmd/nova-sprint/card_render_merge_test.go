package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
)

// TestCardRenderFoldsTheMergeBriefIn (nova-tools #4324): a merge card's task
// record carries only the title (one writer, #3778); its body is the land
// watch's brief at land:brief:<card>, and `card render --brief` renders the
// merge template with that brief quoted.
func TestCardRenderFoldsTheMergeBriefIn(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := t.Context()
	const id = "merge-swarm-cards-1"
	c.HSet(ctx, "task:"+id, "kind", "merge", "state", "open", "repo", "mas-bandwidth/nova-tools",
		"title", "STREAM: swarm: cards | land stream swarm: cards: 2 members in work order into dev as one PR | PATHS: a b | BASE: dev | DONE-WHEN: nova-sprint land --repo mas-bandwidth/nova-tools --stream \"swarm: cards\" prints LANDED n=2")
	c.Set(ctx, reconcile.LandBriefKey(id), "Merge card for stream swarm: cards: land its 2 merging members into dev as ONE pull request from stream/swarm-cards.\n  1. t1 pr=nova-tools#1 order=10 merging_for=1m0s\n", 0)
	code, out, errOut := runSprint("card", "render", "--redis", mr.Addr(), "--id", id, "--brief", "--model", "opus-5.5")
	if code != 0 {
		t.Fatalf("render: %d %q %q", code, out, errOut)
	}
	for _, want := range []string{"CARD: " + id + "\n", "\nKIND: merge\n", "You are the merge card of ONE work stream",
		"> Merge card for stream swarm: cards: land its 2 merging members into dev as ONE pull request from stream/swarm-cards.\n",
		">   1. t1 pr=nova-tools#1 order=10 merging_for=1m0s\n", "PATHS: a b\n", "DONE-WHEN: nova-sprint land --repo mas-bandwidth/nova-tools"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render lacks %q:\n%s", want, out)
		}
	}
	// No brief stored: the card renders from the title alone, no fold.
	c.Del(ctx, reconcile.LandBriefKey(id))
	if code, out, _ := runSprint("card", "render", "--redis", mr.Addr(), "--id", id, "--brief", "--model", "opus-5.5"); code != 0 || strings.Contains(out, "> Merge card") {
		t.Fatalf("without a brief: %d\n%s", code, out)
	}
}
