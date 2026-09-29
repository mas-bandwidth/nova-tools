//go:build functional

package friend_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func TestFriendInspectFunctional(t *testing.T) {
	t.Parallel()
	_, client := fsRedis(t)
	ctx := context.Background()
	now := time.Now()

	// 1. Seed friends registry
	fsMust(t, client.SAdd(ctx, "friends", "peer-alpha", "peer-beta", "peer-gamma").Err())

	// 2. Peer Alpha: up, 4 slots, 2 working copies, credits=$25
	fsMust(t, client.HSet(ctx, "friend:peer-alpha:desired", "slots", "4", "machine", "studio", "paused", "0").Err())
	fsMust(t, client.HSet(ctx, "friend:peer-alpha:beat",
		"host", "studio",
		"session", "sess-alpha",
		"harness", "nova-friend",
		"models", "model-pro,model-flash",
		"credits", "$25",
		"at", strconv.FormatInt(now.Add(-5*time.Second).UnixMilli(), 10),
	).Err())

	// Seed working copies for peer-alpha
	epoch, _ := ws.Epoch(ctx, client)
	workingKey := ws.ConsumerKeyAt(epoch, "friend:peer-alpha", "working")
	t1Lease := now.Add(-2 * time.Minute).UnixMilli()
	t2Lease := now.Add(-30 * time.Second).UnixMilli()
	fsMust(t, client.ZAdd(ctx, workingKey,
		redis.Z{Score: float64(t1Lease), Member: "task-alpha-1~1"},
		redis.Z{Score: float64(t2Lease), Member: "task-alpha-2~2"},
	).Err())

	fsMust(t, client.HSet(ctx, "task:task-alpha-1~1",
		"primary", "task-alpha-1",
		"stream", "ci",
		"leg", "work",
		"title", "Build CI pipeline",
		"model", "model-pro",
		"leased_at", strconv.FormatInt(t1Lease, 10),
	).Err())
	fsMust(t, client.HSet(ctx, "task:task-alpha-2~2",
		"primary", "task-alpha-2",
		"stream", "table",
		"leg", "read",
		"title", "Review table layout",
		"model", "model-flash",
		"leased_at", strconv.FormatInt(t2Lease, 10),
	).Err())

	// Seed receipts for peer-alpha (one OK, one FAIL)
	okKey := ws.ConsumerKeyAt(epoch, "friend:peer-alpha", "ok")
	failKey := ws.ConsumerKeyAt(epoch, "friend:peer-alpha", "fail")
	r1Ended := now.Add(-10 * time.Minute).UnixMilli()
	r2Ended := now.Add(-20 * time.Minute).UnixMilli()
	fsMust(t, client.ZAdd(ctx, okKey, redis.Z{Score: float64(r1Ended), Member: "task-alpha-0~1"}).Err())
	fsMust(t, client.ZAdd(ctx, failKey, redis.Z{Score: float64(r2Ended), Member: "task-alpha-prev~1"}).Err())

	fsMust(t, client.HSet(ctx, "task:task-alpha-0~1",
		"primary", "task-alpha-0",
		"stream", "ci",
		"leg", "work",
		"outcome", "ok",
		"why", "all tests green",
		"pr", "mas-bandwidth/nova-tools#4356",
		"ended_at", strconv.FormatInt(r1Ended, 10),
	).Err())
	fsMust(t, client.HSet(ctx, "task:task-alpha-prev~1",
		"primary", "task-alpha-prev",
		"stream", "table",
		"leg", "read",
		"outcome", "fail",
		"why", "SCORE 5/10 gates=ci:red",
		"ended_at", strconv.FormatInt(r2Ended, 10),
	).Err())

	// 3. Peer Beta: down (stale beat > 60s)
	fsMust(t, client.HSet(ctx, "friend:peer-beta:desired", "slots", "2", "machine", "studio", "paused", "0").Err())
	fsMust(t, client.HSet(ctx, "friend:peer-beta:beat",
		"host", "studio",
		"session", "sess-beta",
		"harness", "nova-friend",
		"at", strconv.FormatInt(now.Add(-90*time.Second).UnixMilli(), 10),
	).Err())

	// 4. Peer Gamma: out-of-credits
	resetTime := now.Add(90 * time.Minute)
	fsMust(t, client.HSet(ctx, "friend:peer-gamma:desired", "slots", "4", "machine", "studio", "paused", "0").Err())
	fsMust(t, client.HSet(ctx, "friend:peer-gamma:beat",
		"host", "studio",
		"session", "sess-gamma",
		"harness", "nova-friend",
		"at", strconv.FormatInt(now.Add(-2*time.Second).UnixMilli(), 10),
	).Err())
	fsMust(t, client.HSet(ctx, "friend:peer-gamma:state",
		"state", friend.StateOutOfCredits,
		"reason", "daily quota reached",
		"until", strconv.FormatInt(resetTime.UnixMilli(), 10),
	).Err())
	fsMust(t, client.Set(ctx, "friend:peer-gamma:down", "out-of-credits", 0).Err())

	t.Run("friend ls", func(t *testing.T) {
		summaries, err := friend.List(ctx, client, now)
		if err != nil {
			t.Fatalf("friend.List: %v", err)
		}
		if len(summaries) != 3 {
			t.Fatalf("got %d summaries, want 3", len(summaries))
		}

		// peer-alpha
		a := summaries[0]
		if a.Name != "peer-alpha" || a.Status != friend.StateUp || a.SlotsTotal != 4 || a.SlotsUsed != 2 || a.SlotsFree != 2 {
			t.Errorf("peer-alpha mismatch: %+v", a)
		}
		if a.Credits != "$25" || a.Harness != "nova-friend" {
			t.Errorf("peer-alpha credits/harness: credits=%q harness=%q", a.Credits, a.Harness)
		}
		if len(a.Working) != 2 {
			t.Errorf("peer-alpha working count = %d, want 2", len(a.Working))
		}

		// peer-beta (down because stale beat)
		b := summaries[1]
		if b.Name != "peer-beta" || b.Status != friend.StateDown {
			t.Errorf("peer-beta status = %q, want down", b.Status)
		}

		// peer-gamma (out-of-credits)
		g := summaries[2]
		if g.Name != "peer-gamma" || g.Status != friend.StateOutOfCredits {
			t.Errorf("peer-gamma status = %q, want out-of-credits", g.Status)
		}
		if g.ResetUntil.UnixMilli() != resetTime.UnixMilli() {
			t.Errorf("peer-gamma resetUntil = %v, want %v", g.ResetUntil, resetTime)
		}

		// Formatted list output
		formatted := friend.FormatList(summaries, now)
		if !strings.Contains(formatted, "peer-alpha") || !strings.Contains(formatted, "peer-beta") || !strings.Contains(formatted, "peer-gamma") {
			t.Errorf("FormatList output missing friend names:\n%s", formatted)
		}
		if !strings.Contains(formatted, "FRIENDS n=3 up=1 down=2 working=2") {
			t.Errorf("FormatList summary line mismatch:\n%s", formatted)
		}
	})

	t.Run("friend show", func(t *testing.T) {
		detail, err := friend.Show(ctx, client, "peer-alpha", now)
		if err != nil {
			t.Fatalf("friend.Show(peer-alpha): %v", err)
		}
		if detail.Name != "peer-alpha" || detail.Status != friend.StateUp {
			t.Fatalf("detail mismatch: %+v", detail)
		}
		if len(detail.Working) != 2 {
			t.Fatalf("working count = %d, want 2", len(detail.Working))
		}
		if detail.Working[0].ID != "task-alpha-1~1" && detail.Working[1].ID != "task-alpha-1~1" {
			t.Errorf("working copies missing task-alpha-1~1: %+v", detail.Working)
		}
		if len(detail.Receipts) != 2 {
			t.Fatalf("receipts count = %d, want 2", len(detail.Receipts))
		}
		if detail.Receipts[0].Outcome != "OK" && detail.Receipts[1].Outcome != "OK" {
			t.Errorf("receipts missing OK outcome: %+v", detail.Receipts)
		}

		formatted := friend.FormatShow(detail, now)
		for _, want := range []string{
			"FRIEND peer-alpha",
			"Status:       up",
			"Harness:      nova-friend",
			"WORKING COPIES (2):",
			"task-alpha-1~1",
			"stream=ci",
			"RECENT RECEIPTS (2):",
			"task-alpha-0~1",
			"pr=mas-bandwidth/nova-tools#4356",
		} {
			if !strings.Contains(formatted, want) {
				t.Errorf("FormatShow missing %q:\n%s", want, formatted)
			}
		}
	})

	t.Run("friend show unregistered", func(t *testing.T) {
		_, err := friend.Show(ctx, client, "nonexistent-peer", now)
		if err == nil || !strings.Contains(err.Error(), "unregistered friend") {
			t.Errorf("Show(nonexistent-peer) err = %v, want unregistered error", err)
		}
	})
}
