//go:build functional

package config

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// A missing field is normal; it must not hide a later error in the same batch.
func TestFriendApplyRefusesMalformedBeatAfterMissingBeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	if err := c.Set(ctx, FriendBeatKey("stella"), "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "24"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	before := pipelineStoreImage(t, c)
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Errorf("apply with malformed beat: %v; want WRONGTYPE", err)
	}
	if !reflect.DeepEqual(before, pipelineStoreImage(t, c)) {
		t.Error("failed friend read changed Redis")
	}
}

func TestFriendPrefetchRefusesLaterErrorWithoutPublishingCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	if err := c.Set(ctx, FriendBeatKey("broken"), "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	ap.friendHosts = map[string]string{"cached": "original"}
	ap.coordinator = "original"
	err := ap.PrefetchFriends(ctx, []string{"missing", "broken"})
	if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Errorf("prefetch: %v; want WRONGTYPE", err)
	}
	if !reflect.DeepEqual(ap.friendHosts, map[string]string{"cached": "original"}) || ap.coordinator != "original" || ap.coordinatorRead {
		t.Errorf("failed prefetch published cache: hosts=%v coordinator=%s read=%v", ap.friendHosts, ap.coordinator, ap.coordinatorRead)
	}
}

func TestMachineRemovalRefusesLaterDependencyReadError(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{KindFriend, "bench"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ap, c := redisApplier(t)
			for _, cmd := range []redis.Cmder{
				c.SAdd(ctx, MachinesKey, "fixture"),
				c.HSet(ctx, MachineKey("fixture"), "slots", "8"),
				c.HSet(ctx, MachineCeilingKey("fixture"), "slots", "8"),
				c.HSet(ctx, "machine:fixture:budget", "slots", "8"),
				c.SAdd(ctx, kind+"s", "one", "two"),
			} {
				if err := cmd.Err(); err != nil {
					t.Fatal(err)
				}
			}
			// Use this server's set order; the set is unchanged until Remove reads it.
			names, err := c.SMembers(ctx, kind+"s").Result()
			if err != nil || len(names) != 2 {
				t.Fatalf("members %v: %v", names, err)
			}
			if err := c.Set(ctx, kind+":"+names[1]+":desired", "not a hash", 0).Err(); err != nil {
				t.Fatal(err)
			}
			before := pipelineStoreImage(t, c)
			err = ap.Remove(ctx, KindMachine, "fixture", "actor", "config:machine:1")
			if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
				t.Errorf("remove after failed dependency read: %v; want WRONGTYPE", err)
			}
			if !reflect.DeepEqual(before, pipelineStoreImage(t, c)) {
				t.Error("failed dependency read deleted machine state or wrote a receipt")
			}
		})
	}
}

func pipelineStoreImage(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	image := make(map[string]string, len(keys))
	for _, key := range keys {
		value, err := c.Dump(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		image[key] = value
	}
	return image
}

// Cover every read shape sharing the pipeline check, including revision reads
// behind missing singleton fields and registry reads behind absent ceilings.
func TestBatchedReadRefusesErrorAfterAbsentValue(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"friend coordinator", "friend revision", "machine registry", "fleet coordinator", "sprint revision", "machine beats"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ap, c := redisApplier(t)
			var setup []redis.Cmder
			var kind string
			switch scenario {
			case "friend coordinator":
				kind = KindFriend
				setup = []redis.Cmder{c.SAdd(ctx, FriendsKey, "missing"), c.HSet(ctx, FleetKey("coordinator"), "invalid", "type")}
			case "friend revision":
				kind = KindFriend
				setup = []redis.Cmder{c.Set(ctx, DeclKey, "not a hash", 0)}
			case "machine registry":
				kind = KindMachine
				setup = []redis.Cmder{c.SAdd(ctx, MachinesKey, "missing"), c.Set(ctx, MachineKey("missing"), "not a hash", 0)}
			case "fleet coordinator":
				kind = KindFleet
				setup = []redis.Cmder{c.HSet(ctx, FleetKey("coordinator"), "invalid", "type")}
			case "sprint revision":
				kind = KindSprint
				setup = []redis.Cmder{c.Set(ctx, DeclKey, "not a hash", 0)}
			case "machine beats":
				setup = []redis.Cmder{c.Set(ctx, BeatKey("broken"), "not a hash", 0)}
			}
			for _, cmd := range setup {
				if err := cmd.Err(); err != nil {
					t.Fatal(err)
				}
			}
			before := pipelineStoreImage(t, c)
			var err error
			if scenario == "machine beats" {
				_, err = ap.Beats(ctx, []string{"missing", "broken"})
			} else {
				_, _, err = ap.Read(ctx, kind)
			}
			if err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
				t.Errorf("read: %v; want WRONGTYPE", err)
			}
			if !reflect.DeepEqual(before, pipelineStoreImage(t, c)) {
				t.Error("read modified Redis")
			}
			if ap.friendHosts != nil || ap.coordinatorRead {
				t.Error("failed read published a friend cache")
			}
		})
	}
}

func TestFriendReadDoesNotHideDeniedBeatAfterMissingBeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := redisApplier(t)
	if err := c.SAdd(ctx, FriendsKey, "rowan", "stella").Err(); err != nil {
		t.Fatal(err)
	}
	// Only Stella's beat is outside this reader's allowed key set. An absent
	// role/beat earlier in the pipeline must not convert NOPERM into absence.
	if err := c.Do(ctx, "ACL", "SETUSER", "fixture-reader", "on", ">fixture-password", "+@read",
		"~friends", "~friend:rowan:*", "~friend:stella:desired", "~friend:stella:roles", "~fleet:*", "~config:decl").Err(); err != nil {
		t.Fatal(err)
	}
	reader := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: "fixture-reader", Password: "fixture-password"})
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	// Prove this connection uses the restricted identity before testing the batch.
	if err := reader.HGet(ctx, FriendBeatKey("stella"), "host").Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("fixture did not deny the beat: %v", err)
	}
	ap := &RedisApplier{Client: reader}
	before := pipelineStoreImage(t, c)
	_, _, err := ap.Read(ctx, KindFriend)
	if err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("read: %v; want NOPERM", err)
	}
	if ap.friendHosts != nil || ap.coordinatorRead {
		t.Error("denied read published a cache")
	}
	if !reflect.DeepEqual(before, pipelineStoreImage(t, c)) {
		t.Error("denied read changed Redis")
	}
}
