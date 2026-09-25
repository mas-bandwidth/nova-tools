package life_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestServeCopyEndsFromTheChildsExit (#3998): a copy dealt to friend:emma is
// taken by her seat with card work, dispatched with its rendered card as the
// brief, and returned with card end at the child's exit: a child that dies
// with no typed line fails the copy with the exit reason and the last line
// it wrote, its primary goes back to waiting, the friend's working set is
// empty, and the copy's lease was renewed while it ran (the start-ack beat).
func TestServeCopyEndsFromTheChildsExit(t *testing.T) {
	t.Parallel()
	st, client := seedSeat(t, 2)
	ctx := context.Background()
	k := taskcard.Consumer{Kind: "friend", Name: "emma"}
	if err := taskcard.Enroll(ctx, client, k, true); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "p1", Where: "waiting", Stream: "swarm: cards",
		Kind: "build", Title: "primary p1", Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", serveHead, "paths", "internal/x.go", "done_when", "go test ./internal/x passes"}}); err != nil {
		t.Fatal(err)
	}
	d, err := taskcard.Deal(ctx, client, taskcard.DealRequest{To: k, IDs: []string{"p1"}, By: "rowan"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal %v %v", d, err)
	}
	cfg := serveConfig(t, "sess-1", 2, "die")
	cfg.Sprint = ""
	res, err := life.ServeOnce(ctx, st, cfg)
	if err != nil || res.Taken != 1 || res.Closed != 1 {
		t.Fatalf("serve %+v %v", res, err)
	}
	cp := client.HGetAll(ctx, taskcard.Key(d[0].Copy)).Val()
	if cp["where"] != "fail" || !strings.Contains(cp["why"], "exit=3") || !strings.Contains(cp["why"], "boom") {
		t.Fatalf("copy after a dying child: where=%s why=%q", cp["where"], cp["why"])
	}
	if cp["beat_at"] == "" {
		t.Fatalf("the copy was never beaten: %v", cp)
	}
	// a copy's fail moves its primary to review with the evidence (#4072)
	if w := client.HGet(ctx, taskcard.Key("p1"), "where").Val(); w != "review" {
		t.Fatalf("primary where=%s, want review after its copy failed", w)
	}
	if n := client.ZCard(ctx, k.Key("working")).Val(); n != 0 {
		t.Fatalf("friend:emma:cards:working holds %d after the close", n)
	}
	if kinds := strings.Join(logKinds(t, client), " "); !strings.Contains(kinds, "start") || !strings.Contains(kinds, "done") {
		t.Fatalf("friend:emma:log: %s", kinds)
	}
}
