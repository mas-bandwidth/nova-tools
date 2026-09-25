package task_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// TestBeatManyOneCallPerTick is #3915's harness beat: every live child's
// lease in one call, each request fenced on its own token. Three claims:
// the first beat of two is the start ack (WORKING), a later beat renews
// (BEAT), and a superseded token is FENCED without touching the others.
func TestBeatManyOneCallPerTick(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "control-3915beat"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	seedFriend(t, client, "ctl-b", 3)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("b%d", i)
		if got, err := task.Push(ctx, st, task.PushRequest{
			Sprint: sprint, ID: id, Kind: task.KindWork, Title: id, Effects: task.EffectsNone, To: "ctl-b",
		}); err != nil || got != task.PushCreated {
			t.Fatalf("push %s = %s, %v", id, got, err)
		}
	}
	claims, err := task.TakeAvailable(ctx, st, "ctl-b", sprint, "", 0, "ctl-b", "")
	if err != nil || len(claims) != 3 {
		t.Fatalf("take %d %v", len(claims), err)
	}
	reqs := make([]task.BeatRequest, len(claims))
	for i, c := range claims {
		reqs[i] = task.BeatRequest{Sprint: sprint, ID: c.ID, Token: c.Token, Actor: "ctl-b"}
	}
	got, err := task.BeatMany(ctx, st, reqs)
	if err != nil || len(got) != 3 || got[0] != task.BeatWorking || got[1] != task.BeatWorking || got[2] != task.BeatWorking {
		t.Fatalf("start acks %v %v", got, err)
	}
	if n := zcard(t, ctx, client, "friend:ctl-b:living"); n != 3 {
		t.Fatalf("living %d, want 3", n)
	}
	reqs[1].Token = "superseded"
	got, err = task.BeatMany(ctx, st, reqs)
	if err != nil || got[0] != task.BeatBeating || got[1] != task.BeatFenced || got[2] != task.BeatBeating {
		t.Fatalf("renewals %v %v", got, err)
	}
	if got, err := task.BeatMany(ctx, st, nil); err != nil || got != nil {
		t.Fatalf("empty beat %v %v", got, err)
	}
}
