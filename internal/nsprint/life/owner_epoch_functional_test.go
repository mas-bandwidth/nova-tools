//go:build functional

package life_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// Clearing a sprint changes which working set can authorize a heartbeat.
// A live OS process does not authorize renewing a copy in the old epoch.
func TestOwnerBeatFollowsSprintEpochAndRefusesClearDuringObservation(t *testing.T) {
	t.Parallel()
	st, c := rdRedis(t)
	ctx := context.Background()
	as := taskcard.Consumer{Kind: "friend", Name: "epoch-owner"}
	if err := c.HSet(ctx, ws.EpochKey, ws.EpochField, 7).Err(); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Minute).UnixMilli()
	for i, id := range []string{"old~1", "current~1"} {
		epoch := uint64(0)
		if i == 1 {
			epoch = 7
		}
		if err := c.ZAdd(ctx, ws.ConsumerKeyAt(epoch, as.String(), "working"), redis.Z{Score: 1, Member: id}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := c.HSet(ctx, taskcard.Key(id), "where", "working", "consumer", as.String(), "token", id, "lease_until", until, "epoch", epoch).Err(); err != nil {
			t.Fatal(err)
		}
	}
	owner := taskcard.ProcessOwner{Host: "fixture", PID: 42, Start: "creation"}
	if err := taskcard.BindOwner(ctx, c, as, "old~1", "old~1", owner); err == nil || !strings.Contains(err.Error(), "NOTWORKING") {
		t.Fatalf("old epoch bind: %v", err)
	}
	if err := taskcard.BindOwner(ctx, c, as, "current~1", "current~1", owner); err != nil {
		t.Fatal(err)
	}
	req := life.FriendBeatRequest{Friend: as.Name, Host: "fixture", ObserverHost: "fixture", Process: func(int) life.ProcessSample { return life.ProcessSample{Start: "creation"} }}
	got, err := life.FriendBeat(ctx, st, req)
	if err != nil || got.Working != 1 || len(got.Unknown) != 0 {
		t.Fatalf("current epoch beat: %+v %v", got, err)
	}
	if got := c.HGet(ctx, taskcard.Key("old~1"), "lease_until").Val(); got != strconv.FormatInt(until, 10) {
		t.Fatal("old epoch lease changed")
	}
	before := c.HGetAll(ctx, taskcard.Key("current~1")).Val()
	presence := c.HGet(ctx, as.BeatKey(), "at").Val()
	req.At = time.Now().Add(time.Second)
	req.Process = func(int) life.ProcessSample {
		if err := c.HSet(ctx, ws.EpochKey, ws.EpochField, 8).Err(); err != nil {
			t.Fatal(err)
		}
		return life.ProcessSample{Start: "creation"}
	}
	got, err = life.FriendBeat(ctx, st, req)
	if err != nil || got.Working != 0 || len(got.Refused) != 1 || !strings.Contains(got.Refused[0].Why, "NOTWORKING") {
		t.Fatalf("clear raced observation: %+v %v", got, err)
	}
	after := c.HGetAll(ctx, taskcard.Key("current~1")).Val()
	if after["lease_until"] != before["lease_until"] || after["owner_at"] != before["owner_at"] || c.HGet(ctx, as.BeatKey(), "at").Val() != presence {
		t.Fatal("old epoch observation changed lease, owner, or presence")
	}
	// An in-flight raw call that already captured the old key also refuses.
	reply, err := c.FCall(ctx, taskcard.FnOwner, []string{ws.ConsumerKeyAt(7, as.String(), "working"), taskcard.Key("current~1")}, "bind", as.String(), "current~1", "current~1", owner.Host, owner.PID, owner.Start, "", 0).StringSlice()
	if err != nil || len(reply) != 2 || reply[0] != "REFUSED" || !strings.Contains(reply[1], "EPOCH current~1") {
		t.Fatalf("stale epoch key: %v %v", reply, err)
	}
	// Corrupt epoch state is an explicit store error, never a fallback to zero.
	if err := c.Del(ctx, ws.EpochKey).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, ws.EpochKey, "broken", 0).Err(); err != nil {
		t.Fatal(err)
	}
	err = taskcard.BindOwner(ctx, c, as, "old~1", "old~1", owner)
	if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") || !strings.Contains(err.Error(), "old~1") {
		t.Fatalf("epoch read cause lost: %v", err)
	}
}
