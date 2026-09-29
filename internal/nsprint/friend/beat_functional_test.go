//go:build functional

package friend_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

const (
	testSprint = "s-beat-test"
	testStream = "s:beat-test"
)

func seedTestFriend(t *testing.T, client *redis.Client, name string, slots int) taskcard.Consumer {
	t.Helper()
	ctx := context.Background()
	k := taskcard.Consumer{Kind: "friend", Name: name}
	_ = client.SAdd(ctx, "friends", name).Err()
	_ = client.SAdd(ctx, "sprints", testSprint).Err()
	_ = client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: testSprint}).Err()
	_ = client.HSet(ctx, "s:"+testSprint, "status", "open").Err()
	_ = client.HSet(ctx, k.DesiredKey(), "slots", strconv.Itoa(slots), "paused", "0").Err()
	_ = client.HSet(ctx, "friend:"+name+":beat", "host", "studio", "at", strconv.FormatInt(time.Now().UnixMilli(), 10)).Err()
	return k
}

func pushTestCard(t *testing.T, client *redis.Client, id string) string {
	t.Helper()
	ctx := context.Background()
	_, err := taskcard.Push(ctx, client, taskcard.PushRequest{
		ID:     id,
		Where:  "waiting",
		Stream: testStream,
		Sprint: testSprint,
		Kind:   "build",
		Title:  "card " + id,
		Repo:   "mas-bandwidth/nova-tools",
		By:     "rowan",
	})
	if err != nil {
		t.Fatalf("push %s: %v", id, err)
	}
	return id
}

func dealTestWork(t *testing.T, client *redis.Client, k taskcard.Consumer, id string) string {
	t.Helper()
	ctx := context.Background()
	d, err := taskcard.Deal(ctx, client, taskcard.DealRequest{To: k, IDs: []string{id}, By: "rowan"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal %s to %s: %v %v", id, k, d, err)
	}
	w, err := taskcard.Work(ctx, client, k, "rowan", 0, false, d[0].Copy)
	if err != nil || len(w.IDs) != 1 {
		t.Fatalf("work %s: %+v %v", d[0].Copy, w, err)
	}
	return d[0].Copy
}

func TestFriendBeatSingleShot(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	k := seedTestFriend(t, client, "bf1", 4)
	cid := pushTestCard(t, client, "card-beat-1")
	cp := dealTestWork(t, client, k, cid)

	res, err := friend.Beat(ctx, client, friend.BeatRequest{
		Friend:  "bf1",
		Host:    "test-studio",
		Harness: "test-harness",
	})
	if err != nil {
		t.Fatalf("Beat failed: %v", err)
	}

	if res.Working != 1 {
		t.Fatalf("res.Working = %d, want 1", res.Working)
	}
	if res.LeaseUntil <= res.AtMS {
		t.Fatalf("res.LeaseUntil (%d) should be > AtMS (%d)", res.LeaseUntil, res.AtMS)
	}

	// Verify friend:<f>:beat hash in Redis
	host := client.HGet(ctx, "friend:bf1:beat", "host").Val()
	if host != "test-studio" {
		t.Fatalf("friend:bf1:beat host = %q, want 'test-studio'", host)
	}
	harness := client.HGet(ctx, "friend:bf1:beat", "harness").Val()
	if harness != "test-harness" {
		t.Fatalf("friend:bf1:beat harness = %q, want 'test-harness'", harness)
	}

	// Verify working copy lease extended in task, copy, and card:lease
	leaseStr := client.HGet(ctx, taskcard.Key(cp), "lease_until").Val()
	if leaseStr == "" || leaseStr == "0" {
		t.Fatalf("copy %s lease_until = %q, want non-empty timestamp", cp, leaseStr)
	}
	cardLease := client.Get(ctx, "card:"+cp+":lease").Val()
	if cardLease == "" || cardLease == "0" {
		t.Fatalf("card:%s:lease = %q, want non-empty timestamp", cp, cardLease)
	}
	copyLease := client.HGet(ctx, "copy:"+cp, "lease_until").Val()
	if copyLease == "" || copyLease == "0" {
		t.Fatalf("copy:%s lease_until = %q, want non-empty timestamp", cp, copyLease)
	}
}

func TestFriendBeatHandCopiesNeverLapse(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	k := seedTestFriend(t, client, "bf2", 4)
	cid := pushTestCard(t, client, "card-beat-2")
	cp := dealTestWork(t, client, k, cid)

	// Single beat under seat
	_, err := friend.Beat(ctx, client, friend.BeatRequest{
		Friend: "bf2",
		Seat:   "bf2",
	})
	if err != nil {
		t.Fatalf("Beat failed: %v", err)
	}

	// Verify lease is in the future
	leaseVal, _ := strconv.ParseInt(client.HGet(ctx, taskcard.Key(cp), "lease_until").Val(), 10, 64)
	if leaseVal <= time.Now().UnixMilli() {
		t.Fatalf("expected lease in the future, got %d", leaseVal)
	}

	// Expire pass should NOT expire hand copies whose lease was extended
	expired, err := taskcard.ExpireCopies(ctx, client, "reconciler", k)
	if err != nil {
		t.Fatalf("ExpireCopies failed: %v", err)
	}
	for _, e := range expired {
		if e.Copy == cp {
			t.Fatalf("copy %s was expired, but it should have been protected by friend beat", cp)
		}
	}

	// Verify copy is still working
	score := client.ZScore(ctx, "friend:bf2:cards:working", cp).Val()
	if score == 0 {
		t.Fatalf("copy %s missing from working set after expire pass", cp)
	}
}

func TestFriendBeatAutoRedealLapsedChildAlive(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	ctx := context.Background()

	k := seedTestFriend(t, client, "bf3", 4)
	cid := pushTestCard(t, client, "card-beat-3")
	cp := dealTestWork(t, client, k, cid)

	// Bind an owner to the copy
	token := client.HGet(ctx, taskcard.Key(cp), "token").Val()
	owner := taskcard.ProcessOwner{
		Host:  "studio",
		PID:   42424,
		Start: "proc-start-active",
	}
	if err := taskcard.BindOwner(ctx, client, k, cp, token, owner); err != nil {
		t.Fatalf("BindOwner failed: %v", err)
	}

	// Simulate lease lapse in the past
	client.HSet(ctx, taskcard.Key(cp), "lease_until", "1")

	// Custom probe returns alive for pid 42424
	probe := func(pid int) life.ProcessSample {
		if pid == 42424 {
			return life.ProcessSample{
				Start:  "proc-start-active",
				Absent: false,
			}
		}
		return life.ProcessSample{Absent: true}
	}

	res, err := friend.Beat(ctx, client, friend.BeatRequest{
		Friend:       "bf3",
		Host:         "studio",
		ProbeProcess: probe,
	})
	if err != nil {
		t.Fatalf("Beat failed: %v", err)
	}

	// Verify reassignment notice
	if len(res.Reassigned) != 1 {
		t.Fatalf("res.Reassigned len = %d, want 1; got %+v", len(res.Reassigned), res.Reassigned)
	}
	r := res.Reassigned[0]
	if r.Card != cid || r.Friend != "bf3" || r.Reason != "lapsed-child-alive" {
		t.Fatalf("unexpected reassigned card: %+v", r)
	}

	// Verify primary card is active and not stuck in review
	where := client.HGet(ctx, taskcard.Key(cid), "where").Val()
	if where == "review" {
		t.Fatalf("primary card %s still in review after auto-redeal", cid)
	}

	// Verify a new working copy was cut and has a valid lease
	newWorking, err := client.ZRange(ctx, "friend:bf3:cards:working", 0, -1).Result()
	if err != nil || len(newWorking) != 1 {
		t.Fatalf("friend:bf3:cards:working = %v, want 1 copy", newWorking)
	}
	newCp := newWorking[0]
	newLease, _ := strconv.ParseInt(client.HGet(ctx, taskcard.Key(newCp), "lease_until").Val(), 10, 64)
	if newLease <= time.Now().UnixMilli() {
		t.Fatalf("new copy %s lease_until (%d) should be in the future", newCp, newLease)
	}
}

func TestFriendBeatDaemonLoop(t *testing.T) {
	t.Parallel()

	_, client := fsRedis(t)
	_ = seedTestFriend(t, client, "bf4", 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var beats []*friend.BeatResult

	onBeat := func(r *friend.BeatResult) {
		mu.Lock()
		defer mu.Unlock()
		beats = append(beats, r)
		if len(beats) >= 2 {
			cancel()
		}
	}

	req := friend.BeatRequest{
		Friend:   "bf4",
		Interval: 20 * time.Millisecond,
		Daemon:   true,
	}

	err := friend.BeatLoop(ctx, client, req, onBeat)
	if err != nil && err != context.Canceled {
		t.Fatalf("BeatLoop failed: %v", err)
	}

	mu.Lock()
	count := len(beats)
	mu.Unlock()

	if count < 2 {
		t.Fatalf("BeatLoop recorded %d beats, want at least 2", count)
	}
}
