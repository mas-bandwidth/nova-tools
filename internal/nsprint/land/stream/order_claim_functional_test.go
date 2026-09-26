//go:build functional

package stream

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestLandClaimHoldsTheDoors is land pr's fence (#4322 round 6): once
// ns_tcard_land_claim holds the landing slot for the head of a stream's
// live order, a push into the stream is refused ORDER CLAIMED in its own
// FCALL and writes nothing; a release opens the door again; the claimed
// card's landing clears the claim by itself; a claim whose lease lapsed
// holds nothing; a card that is not the head cannot claim.
func TestLandClaimHoldsTheDoors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newRedis(t)
	push := func(id string) error {
		_, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: strm, Kind: "build",
			Title: id, Ref: "nova-tools#" + id[1:], By: "test"})
		return err
	}
	if err := push("t1"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		to string
		o  taskcard.Opts
	}{{"ready", taskcard.Opts{}}, {"working", taskcard.Opts{As: "f1", Friend: "f1", SetFriend: true}}, {"merging", taskcard.Opts{}}} {
		step.o.By, step.o.Why = "test", "test"
		if _, err := taskcard.Move(ctx, c, "t1", step.to, step.o); err != nil {
			t.Fatalf("t1 -> %s: %v", step.to, err)
		}
	}
	until, err := taskcard.LandClaim(ctx, c, "t1", "land-pr", time.Minute)
	if err != nil || time.Until(until) < 50*time.Second {
		t.Fatalf("claim: %v until %v", err, until)
	}
	err = push("t2")
	var r *taskcard.Refused
	want := `ORDER CLAIMED stream="landing: streams + lander" by=t1 until=`
	if !errors.As(err, &r) || !strings.HasPrefix(r.Why, want) || c.Exists(ctx, "task:t2").Val() != 0 {
		t.Fatalf("push under the claim: %v; task:t2 exists=%d", err, c.Exists(ctx, "task:t2").Val())
	}
	t.Logf("push under the claim: REFUSED %s", r.Why)
	if err := taskcard.LandRelease(ctx, c, "t1"); err != nil {
		t.Fatal(err)
	}
	if err := push("t2"); err != nil {
		t.Fatalf("push after the release: %v", err)
	}
	// t2 (behind t1) cannot claim; t1 can, and its landing clears the claim
	if _, err := taskcard.LandClaim(ctx, c, "t2", "land-pr", time.Minute); !errors.As(err, &r) ||
		r.Why != `ORDER WAIT stream="landing: streams + lander" before=t1 where=merging held=t2` {
		t.Fatalf("t2's claim behind t1: %v", err)
	}
	if _, err := taskcard.LandClaim(ctx, c, "t1", "land-pr", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := push("t3"); !errors.As(err, &r) || !strings.HasPrefix(r.Why, want) {
		t.Fatalf("push under the second claim: %v", err)
	}
	if res, err := taskcard.Land(ctx, c, "t1", "land-pr", strings.Repeat("m", 40), ""); err != nil || res.To != "landed" {
		t.Fatalf("land t1: %+v %v", res, err)
	}
	if n := c.HLen(ctx, WSKeyAt(0, strm, "landing")).Val(); n != 0 {
		t.Fatalf("the landing left a claim: %v", c.HGetAll(ctx, WSKeyAt(0, strm, "landing")).Val())
	}
	if err := push("t3"); err != nil {
		t.Fatalf("push after the landing: %v", err)
	}
	// a lapsed claim holds nothing (the lease's end is written by hand: no wait)
	if _, err := taskcard.LandClaim(ctx, c, "t2", "land-pr", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, WSKeyAt(0, strm, "landing"), "t2", "1")
	if err := push("t4"); err != nil {
		t.Fatalf("push under a lapsed claim: %v", err)
	}
	t.Logf("claim, refused push, release, claim, landing clears it, lapsed claim holds nothing: t1 landed, t2 t3 t4 live")
}
