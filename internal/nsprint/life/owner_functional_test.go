//go:build functional

package life_test

import (
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCopyOwnerRenewalFencesAttemptProcessAndObservation(t *testing.T) {
	t.Parallel()
	_, c := rdRedis(t)
	ctx := context.Background()
	as := taskcard.Consumer{Kind: "friend", Name: "owner-probe"}
	const id = "owner-probe~1"
	const token = "attempt-a"
	now, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Minute).UnixMilli()
	if err := c.ZAdd(ctx, as.Key("working"), redis.Z{Score: 1, Member: id}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, taskcard.Key(id), "where", "working", "consumer", as.String(), "token", token, "lease_until", until).Err(); err != nil {
		t.Fatal(err)
	}
	p := taskcard.ProcessOwner{Host: "fixture", PID: 42, Start: "creation-1"}
	fresh := func() time.Time {
		t.Helper()
		v, e := c.Time(ctx).Result()
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	observe := func(owner taskcard.ProcessOwner, state string, at time.Time) (taskcard.OwnerReceipt, error) {
		return taskcard.ObserveOwner(ctx, c, as, id, token, taskcard.OwnerObservation{Owner: owner, State: state, At: at})
	}
	r, err := observe(taskcard.ProcessOwner{}, "unknown", fresh())
	if err != nil || r.State != "unknown" || r.LeaseUntil != until {
		t.Fatalf("unbound: %+v %v", r, err)
	}
	if err := taskcard.BindOwner(ctx, c, as, id, token, p); err != nil {
		t.Fatal(err)
	}
	if err := taskcard.BindOwner(ctx, c, as, id, token, p); err != nil {
		t.Fatalf("same binding: %v", err)
	}
	changed := p
	changed.Start = "creation-2"
	if err := taskcard.BindOwner(ctx, c, as, id, token, changed); err == nil || !strings.Contains(err.Error(), "CONFLICT") {
		t.Fatalf("rebind: %v", err)
	}
	for _, state := range []string{"dead", "unknown"} {
		r, err = observe(p, state, fresh())
		if err != nil || r.LeaseUntil != until {
			t.Fatalf("%s renewed: %+v %v", state, r, err)
		}
	}
	for _, at := range []time.Time{fresh().Add(-3 * time.Second), fresh().Add(time.Hour)} {
		if _, err = observe(p, "live", at); err == nil || !strings.Contains(err.Error(), "STALE") {
			t.Fatalf("bad time accepted: %v", err)
		}
	}
	if _, err = observe(changed, "live", fresh()); err == nil || !strings.Contains(err.Error(), "FENCED") {
		t.Fatalf("wrong process: %v", err)
	}
	r, err = observe(p, "live", fresh())
	if err != nil || r.State != "live" || r.LeaseUntil == until {
		t.Fatalf("live: %+v %v", r, err)
	}
	// A replacement attempt must not inherit the old process's observation.
	if err := c.HSet(ctx, taskcard.Key(id), "token", "attempt-b").Err(); err != nil {
		t.Fatal(err)
	}
	before := c.HGetAll(ctx, taskcard.Key(id)).Val()
	if _, err = observe(p, "live", fresh()); err == nil || !strings.Contains(err.Error(), "FENCED") {
		t.Fatalf("stale token: %v", err)
	}
	after := c.HGetAll(ctx, taskcard.Key(id)).Val()
	if after["lease_until"] != before["lease_until"] || after["owner_at"] != before["owner_at"] {
		t.Fatal("stale attempt changed owner/lease")
	}
	// Ended copies are neither observed nor resurrected, even with their token.
	if err := c.HSet(ctx, taskcard.Key(id), "token", token, "where", "done").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = observe(p, "live", fresh()); err == nil || !strings.Contains(err.Error(), "NOTWORKING") {
		t.Fatalf("ended: %v", err)
	}
}

// A dead/unknown copy must not poison a live sibling's renewal, and a legacy
// friend-queue task in the same index is not a consumer-copy observation.
func TestFriendBeatMixedOwnersAndDaemonRestart(t *testing.T) {
	t.Parallel()
	st, c := rdRedis(t)
	ctx := context.Background()
	as := taskcard.Consumer{Kind: "friend", Name: "mixed"}
	now, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Minute).UnixMilli()
	for i, id := range []string{"live~1", "dead~1", "unknown~1"} {
		if err := c.ZAdd(ctx, as.Key("working"), redis.Z{Score: float64(i), Member: id}).Err(); err != nil {
			t.Fatal(err)
		}
		if err := c.HSet(ctx, taskcard.Key(id), "consumer", as.String(), "where", "working", "token", id, "lease_until", until, "model", "test-model").Err(); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if err := taskcard.BindOwner(ctx, c, as, id, id, taskcard.ProcessOwner{Host: "fixture", PID: i + 1, Start: "creation"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := c.ZAdd(ctx, as.Key("working"), redis.Z{Score: 4, Member: "legacy-task"}).Err(); err != nil {
		t.Fatal(err)
	}
	req := life.FriendBeatRequest{Friend: as.Name, Host: "fixture", ObserverHost: "fixture", Process: func(pid int) life.ProcessSample {
		if pid == 1 {
			return life.ProcessSample{Start: "creation"}
		}
		return life.ProcessSample{Absent: true}
	}}
	for pass := 0; pass < 2; pass++ {
		r, err := life.FriendBeat(ctx, st, req)
		if err != nil || r.Working != 1 || len(r.Dead) != 1 || len(r.Unknown) != 1 {
			t.Fatalf("pass %d: %+v %v", pass, r, err)
		}
		if pass == 1 && len(r.Changed) != 0 {
			t.Fatalf("restart repeated notices: %v", r.Changed)
		}
		for _, id := range []string{"dead~1", "unknown~1"} {
			if v := c.HGet(ctx, taskcard.Key(id), "lease_until").Val(); v != strconv.FormatInt(until, 10) {
				t.Fatalf("renewed %s: %s", id, v)
			}
		}
	}
	if c.HGet(ctx, taskcard.Key("dead~1"), "owner_state").Val() != "dead" || c.HGet(ctx, taskcard.Key("unknown~1"), "owner_state").Val() != "unknown" {
		t.Fatal("missing distinct owner states")
	}
	at := c.HGet(ctx, as.BeatKey(), "at").Val()
	req.Process = func(int) life.ProcessSample { return life.ProcessSample{Absent: true} }
	r, err := life.FriendBeat(ctx, st, req)
	if err != nil || r.Working != 0 || c.HGet(ctx, as.BeatKey(), "at").Val() != at {
		t.Fatalf("dead owners refreshed presence: %+v %v", r, err)
	}
}
