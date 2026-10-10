//go:build functional

package config

import (
	"context"
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
	scopedErr21 := c.Set(ctx, FriendBeatKey("stella"), "not a hash", 0).Err()
	require.NoError(t, scopedErr21)
	_, _, setupErr552 := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "24"}, "rowan")
	require.NoError(t, setupErr552)
	before := pipelineStoreImage(t, c)
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg33 := []any{"apply with malformed beat: %v; want WRONGTYPE", err}
	func() {
		if !assert.Error(t, err, assertionMsg33...) {
			return
		}
		assert.ErrorContains(t, err, "WRONGTYPE", assertionMsg33...)
	}()
	assert.Equal(t, before, pipelineStoreImage(t, c), "failed friend read changed Redis")
}

func TestFriendPrefetchRefusesLaterErrorWithoutPublishingCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	scopedErr43 := c.Set(ctx, FriendBeatKey("broken"), "not a hash", 0).Err()
	require.NoError(t, scopedErr43)
	ap.friendHosts = map[string]string{"cached": "original"}
	ap.coordinator = "original"
	err := ap.PrefetchFriends(ctx, []string{"missing", "broken"})
	assertionMsg48 := []any{"prefetch: %v; want WRONGTYPE", err}
	func() {
		if !assert.Error(t, err, assertionMsg48...) {
			return
		}
		assert.ErrorContains(t, err, "WRONGTYPE", assertionMsg48...)
	}()
	assertionMsg49 := []any{"failed prefetch published cache: hosts=%v coordinator=%s read=%v", ap.friendHosts, ap.coordinator, ap.coordinatorRead}
	func() {
		if !assert.Equal(t, map[string]string{"cached": "original"}, ap.friendHosts, assertionMsg49...) {
			return
		}
		if !assert.Equal(t, "original", ap.coordinator, assertionMsg49...) {
			return
		}
		assert.False(t, ap.coordinatorRead, assertionMsg49...)
	}()
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
				c.SAdd(ctx, registrySet(kind), "one", "two"),
			} {
				scopedErr83 := cmd.Err()
				require.NoError(t, scopedErr83)
			}
			// Use this server's set order; the set is unchanged until Remove reads it.
			names, err := c.SMembers(ctx, registrySet(kind)).Result()
			assertionMsg73 := []any{"members %v: %v", names, err}
			require.NoError(t, err, assertionMsg73...)
			require.Len(t, names, 2, assertionMsg73...)
			scopedErr93 := c.Set(ctx, kind+":"+names[1]+":desired", "not a hash", 0).Err()
			require.NoError(t, scopedErr93)
			before := pipelineStoreImage(t, c)
			err = ap.Remove(ctx, KindMachine, "fixture", "actor", "config:machine:1")
			assertionMsg82 := []any{"remove after failed dependency read: %v; want WRONGTYPE", err}
			func() {
				if !assert.Error(t, err, assertionMsg82...) {
					return
				}
				assert.ErrorContains(t, err, "WRONGTYPE", assertionMsg82...)
			}()
			assert.Equal(t, before, pipelineStoreImage(t, c), "failed dependency read deleted machine state or wrote a receipt")
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
				scopedErr156 := cmd.Err()
				require.NoError(t, scopedErr156)
			}
			before := pipelineStoreImage(t, c)
			var err error
			if scenario == "machine beats" {
				_, err = ap.Beats(ctx, []string{"missing", "broken"})
			} else {
				_, _, err = ap.Read(ctx, kind)
			}
			assertionMsg145 := []any{"read: %v; want WRONGTYPE", err}
			func() {
				if !assert.Error(t, err, assertionMsg145...) {
					return
				}
				assert.ErrorContains(t, err, "WRONGTYPE", assertionMsg145...)
			}()
			assert.Equal(t, before, pipelineStoreImage(t, c), "read modified Redis")
			assertionMsg147 := []any{"failed read published a friend cache"}
			func() {
				if !assert.Nil(t, ap.friendHosts, assertionMsg147...) {
					return
				}
				assert.False(t, ap.coordinatorRead, assertionMsg147...)
			}()
		})
	}
}

func TestFriendReadDoesNotHideDeniedBeatAfterMissingBeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := redisApplier(t)
	scopedErr191 := c.SAdd(ctx, FriendsKey, "rowan", "stella").Err()
	require.NoError(t, scopedErr191)
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
		assertionMsg169 := []any{"fixture did not deny the beat: %v", err}
		require.Error(t, err, assertionMsg169...)
		require.ErrorContains(t, err, "NOPERM", assertionMsg169...)
	}
	ap := &RedisApplier{Client: reader}
	before := pipelineStoreImage(t, c)
	_, _, err := ap.Read(ctx, KindFriend)
	assertionMsg178 := []any{"read: %v; want NOPERM", err}
	func() {
		if !assert.Error(t, err, assertionMsg178...) {
			return
		}
		assert.ErrorContains(t, err, "NOPERM", assertionMsg178...)
	}()
	assertionMsg179 := []any{"denied read published a cache"}
	func() {
		if !assert.Nil(t, ap.friendHosts, assertionMsg179...) {
			return
		}
		assert.False(t, ap.coordinatorRead, assertionMsg179...)
	}()
	assert.Equal(t, before, pipelineStoreImage(t, c), "denied read changed Redis")
}
