//go:build functional

package config

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A missing field is normal; it must not hide a later error in the same batch.
func TestFriendApplyRefusesMalformedBeatAfterMissingBeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	{
		err := c.Set(ctx, FriendBeatKey("stella"), "not a hash", 0).Err()
		require.NoError(t, err)
	}
	{
		_, _, err := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "24"}, "rowan")
		require.NoError(t, err)
	}
	before := pipelineStoreImage(t, c)
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assert.False(t, err == nil || !strings.Contains(err.Error(), "WRONGTYPE"), "apply with malformed beat: %v; want WRONGTYPE", err)
	assert.False(t, !reflect.DeepEqual(before, pipelineStoreImage(t, c)), "failed friend read changed Redis")
}

func TestFriendPrefetchRefusesLaterErrorWithoutPublishingCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	{
		err := c.Set(ctx, FriendBeatKey("broken"), "not a hash", 0).Err()
		require.NoError(t, err)
	}
	ap.friendHosts = map[string]string{"cached": "original"}
	ap.coordinator = "original"
	err := ap.PrefetchFriends(ctx, []string{"missing", "broken"})
	assert.False(t, err == nil || !strings.Contains(err.Error(), "WRONGTYPE"), "prefetch: %v; want WRONGTYPE", err)
	assert.False(t, !reflect.DeepEqual(ap.friendHosts, map[string]string{"cached": "original"}) || ap.coordinator != "original" || ap.coordinatorRead, "failed prefetch published cache: hosts=%v coordinator=%s read=%v", ap.friendHosts, ap.coordinator, ap.coordinatorRead)
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
				{
					err := cmd.Err()
					require.NoError(t, err)
				}
			}
			// Use this server's set order; the set is unchanged until Remove reads it.
			names, err := c.SMembers(ctx, kind+"s").Result()
			require.False(t, err != nil || len(names) != 2, "members %v: %v", names, err)
			{
				err := c.Set(ctx, kind+":"+names[1]+":desired", "not a hash", 0).Err()
				require.NoError(t, err)
			}
			before := pipelineStoreImage(t, c)
			err = ap.Remove(ctx, KindMachine, "fixture", "actor", "config:machine:1")
			assert.False(t, err == nil || !strings.Contains(err.Error(), "WRONGTYPE"), "remove after failed dependency read: %v; want WRONGTYPE", err)
			assert.False(t, !reflect.DeepEqual(before, pipelineStoreImage(t, c)), "failed dependency read deleted machine state or wrote a receipt")
		})
	}
}

func pipelineStoreImage(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	require.NoError(t, err)
	image := make(map[string]string, len(keys))
	for _, key := range keys {
		value, err := c.Dump(ctx, key).Result()
		require.NoError(t, err)
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
				{
					err := cmd.Err()
					require.NoError(t, err)
				}
			}
			before := pipelineStoreImage(t, c)
			var err error
			if scenario == "machine beats" {
				_, err = ap.Beats(ctx, []string{"missing", "broken"})
			} else {
				_, _, err = ap.Read(ctx, kind)
			}
			assert.False(t, err == nil || !strings.Contains(err.Error(), "WRONGTYPE"), "read: %v; want WRONGTYPE", err)
			assert.False(t, !reflect.DeepEqual(before, pipelineStoreImage(t, c)), "read modified Redis")
			assert.False(t, ap.friendHosts != nil || ap.coordinatorRead, "failed read published a friend cache")
		})
	}
}

func TestFriendReadDoesNotHideDeniedBeatAfterMissingBeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := redisApplier(t)
	{
		err := c.SAdd(ctx, FriendsKey, "rowan", "stella").Err()
		require.NoError(t, err)
	}
	// Only Stella's beat is outside this reader's allowed key set. An absent
	// role/beat earlier in the pipeline must not convert NOPERM into absence.
	require.NoError(t, c.Do(ctx, "ACL", "SETUSER", "fixture-reader", "on", ">fixture-password", "+@read",
		"~friends", "~friend:rowan:*", "~friend:stella:desired", "~friend:stella:roles", "~fleet:*", "~config:decl").Err())
	reader := redis.NewClient(&redis.Options{Addr: c.Options().Addr, Username: "fixture-reader", Password: "fixture-password"})
	t.Cleanup(func() {
		assert.NoError(t, reader.Close())
	})
	// Prove this connection uses the restricted identity before testing the batch.
	{
		err := reader.HGet(ctx, FriendBeatKey("stella"), "host").Err()
		require.False(t, err == nil || !strings.Contains(err.Error(), "NOPERM"), "fixture did not deny the beat: %v", err)
	}
	ap := &RedisApplier{Client: reader}
	before := pipelineStoreImage(t, c)
	_, _, err := ap.Read(ctx, KindFriend)
	assert.False(t, err == nil || !strings.Contains(err.Error(), "NOPERM"), "read: %v; want NOPERM", err)
	assert.False(t, ap.friendHosts != nil || ap.coordinatorRead, "denied read published a cache")
	assert.False(t, !reflect.DeepEqual(before, pipelineStoreImage(t, c)), "denied read changed Redis")
}
