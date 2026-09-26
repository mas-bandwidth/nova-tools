//go:build functional

package life_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

func TestFriendCopyPlainBeatNeverSubstitutesForObservation(t *testing.T) {
	t.Parallel()
	_, c := rdRedis(t)
	ctx := context.Background()
	as := taskcard.Consumer{Kind: "friend", Name: "doors"}
	const id = "doors~1"
	now, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Minute).UnixMilli()
	if err := c.ZAdd(ctx, as.Key("working"), redis.Z{Score: 1, Member: id}).Err(); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"", "live", "dead", "unknown"} {
		if err := c.HSet(ctx, taskcard.Key(id), "where", "working", "consumer", as.String(), "token", "attempt", "lease_until", until, "owner_state", state).Err(); err != nil {
			t.Fatal(err)
		}
		for _, ids := range [][]string{nil, {id}} {
			if _, err := taskcard.BeatCopies(ctx, c, as, ids...); err == nil || !strings.Contains(err.Error(), "nova-sprint friend beat --as friend:doors --once") {
				t.Fatalf("state=%q ids=%v beat must refuse with the observed-owner door: %v", state, ids, err)
			}
			v, err := c.HGet(ctx, taskcard.Key(id), "lease_until").Int64()
			if err != nil || v != until {
				t.Fatalf("plain beat changed lease: %d %v", v, err)
			}
		}
	}
}

func TestFriendBeatPreservesPerCopyRefusalAndRenewsHealthySibling(t *testing.T) {
	t.Parallel()
	for _, why := range []string{"FENCED", "STALE"} {
		t.Run(why, func(t *testing.T) {
			t.Parallel()
			st, c := rdRedis(t)
			ctx := context.Background()
			as := taskcard.Consumer{Kind: "friend", Name: "refusal"}
			now, err := c.Time(ctx).Result()
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range []string{"bad~1", "healthy~1"} {
				if err := c.ZAdd(ctx, as.Key("working"), redis.Z{Score: float64(i), Member: id}).Err(); err != nil {
					t.Fatal(err)
				}
				if err := c.HSet(ctx, taskcard.Key(id), "where", "working", "consumer", as.String(), "token", "attempt", "lease_until", now.Add(time.Minute).UnixMilli()).Err(); err != nil {
					t.Fatal(err)
				}
				if err := taskcard.BindOwner(ctx, c, as, id, "attempt", taskcard.ProcessOwner{Host: "fixture", PID: 40 + i, Start: "creation"}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := life.FriendBeat(ctx, st, life.FriendBeatRequest{Friend: as.Name, Host: "fixture", ObserverHost: "fixture", Process: func(pid int) life.ProcessSample {
				if pid == 40 {
					field, value := "token", any("replacement-attempt")
					if why == "STALE" {
						field, value = "owner_at", now.Add(time.Hour).UnixMilli()
					}
					if err := c.HSet(ctx, taskcard.Key("bad~1"), field, value).Err(); err != nil {
						t.Fatal(err)
					}
				}
				return life.ProcessSample{Start: "creation"}
			}})
			if err != nil || result.Working != 1 || len(result.Refused) != 1 || result.Refused[0].ID != "bad~1" || !strings.Contains(result.Refused[0].Why, why) {
				t.Fatalf("lost per-copy cause or healthy sibling: %+v %v", result, err)
			}
		})
	}
}
