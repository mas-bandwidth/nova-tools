package config

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coverStore is the unit tier's store: an in-process miniredis reached through
// RedisApplier.Client, so redis.go's methods run their real code paths with no
// process, no external connection and no live Redis. It is the fake the tree
// already uses in the unit tier (internal/update).
func coverStore(t *testing.T) (*RedisApplier, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return &RedisApplier{Client: c}, c, mr
}

// TestRedisCoverKeyBuilders: every key a kind writes is its name under its one
// prefix, so a reader can point at the key a verb leaves.
func TestRedisCoverKeyBuilders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{name: "loop hash", got: LoopKey("l1"), want: "loop:l1"},
		{name: "tier hash", got: TierKey("flash"), want: "tier:flash"},
		{name: "fleet plain key", got: FleetKey("store"), want: "fleet:store"},
		{name: "sprint plain key", got: SprintKey("coordinator"), want: "sprint:coordinator"},
		{name: "friend beat", got: FriendBeatKey("ada"), want: "friend:ada:beat"},
		{name: "machine beat", got: BeatKey("m1"), want: "bench:m1:beat"},
		{name: "machine hash", got: MachineKey("m1"), want: "machine:m1"},
		{name: "machine ceiling", got: MachineCeilingKey("m1"), want: "machine:m1:ceiling"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// TestRedisCoverDeclFields: the stamp's revision and time fields are the kind
// under their prefixes, so config:decl holds one pair per kind.
func TestRedisCoverDeclFields(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "rev:friend", revField(KindFriend))
	assert.Equal(t, "at:friend", atField(KindFriend))
	assert.Equal(t, "rev:machine", revField(KindMachine))
	assert.Equal(t, "at:loop", atField(KindLoop))
}

// TestRedisCoverNow: the applier's stamp clock is the injected one when it has
// one and the wall clock when it has none.
func TestRedisCoverNow(t *testing.T) {
	t.Parallel()

	injected := &RedisApplier{Now: func() time.Time { return time.UnixMilli(1234) }}
	assert.Equal(t, int64(1234), injected.now(), "an injected clock is the stamp's clock")

	assert.NotZero(t, (&RedisApplier{}).now(), "with no clock the stamp is the wall clock")
}

// TestRedisCoverTextHelpers: the small readers that canonicalise a Redis reply
// or a word list, each at its own edge.
func TestRedisCoverTextHelpers(t *testing.T) {
	t.Parallel()

	t.Run("str reads a present, nil and out-of-range value", func(t *testing.T) {
		t.Parallel()
		vals := []any{"a", nil, 3}
		assert.Equal(t, "a", str(vals, 0))
		assert.Equal(t, "", str(vals, 1), "a nil value is empty")
		assert.Equal(t, "3", str(vals, 2), "a non-string is printed")
		assert.Equal(t, "", str(vals, 9), "an index past the end is empty")
	})

	t.Run("sortedList deduplicates and sorts", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "a,b", sortedList("b,a,b"))
		assert.Equal(t, "", sortedList(""))
	})

	t.Run("replyWords flattens a reply", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"a", "1"}, replyWords([]any{"a", 1}))
		assert.Equal(t, []string{"plain"}, replyWords("plain"), "a scalar reply is one word")
	})

	t.Run("word reads a present and absent word", func(t *testing.T) {
		t.Parallel()
		words := []string{"a", "b"}
		assert.Equal(t, "a", word(words, 0))
		assert.Equal(t, "", word(words, 5), "a word past the end is empty")
	})

	t.Run("tiersArg clears an empty list", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "-", tiersArg(""), "an empty list clears the stored one")
		assert.Equal(t, "flash,pro", tiersArg("flash,pro"))
	})

	t.Run("boolText canonicalises a bool", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "true", boolText("true"))
		assert.Equal(t, "true", boolText("1"), "every true spelling is true")
		assert.Equal(t, "false", boolText("garbage"), "anything else is false")
	})

	t.Run("revValue parses a stamp or is zero", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, int64(42), revValue(redis.NewStringResult("42", nil)))
		assert.Equal(t, int64(0), revValue(redis.NewStringResult("bad", nil)), "a non-number is zero")
		assert.Equal(t, int64(0), revValue(redis.NewStringResult("", redis.Nil)), "an absent stamp is zero")
	})
}

// TestRedisCoverRedisRefusal: the refusal helper is a *RefusedError carrying
// the actor sentinel and the formatted detail.
func TestRedisCoverRedisRefusal(t *testing.T) {
	t.Parallel()

	err := RedisRefusal("roles of %s: --as %s is not a friend", "ada", "bob")
	require.Error(t, err)
	assert.True(t, Refused(err))
	assert.ErrorIs(t, err, ErrActor)
	assert.Equal(t, "roles of ada: --as bob is not a friend", err.Error())
}

// TestRedisCoverReadDispatch: Read routes each kind to its reader and refuses
// a kind with none.
func TestRedisCoverReadDispatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, _ := coverStore(t)
	c.HSet(ctx, DeclKey, revField(KindFriend), "3")
	c.SAdd(ctx, FriendsKey, "ada")
	c.HSet(ctx, "friend:ada:desired", "slots", "8", "tiers", "pro,flash", "width", "4")
	c.HSet(ctx, "friend:ada:roles", "roles", "builder")
	c.HSet(ctx, FriendBeatKey("ada"), "host", "m1")
	c.Set(ctx, FleetKey("coordinator"), "m9", 0)

	cases := []struct {
		kind  string
		check func(t *testing.T, views map[string]View, rev int64)
	}{
		{kind: KindFriend, check: func(t *testing.T, views map[string]View, rev int64) {
			assert.Equal(t, "8", views["ada"]["slots"])
			assert.Equal(t, "flash,pro", views["ada"]["tiers"])
			assert.Equal(t, "builder", views["ada"]["roles"])
			assert.Equal(t, int64(3), rev)
		}},
		{kind: KindMachine, check: func(t *testing.T, views map[string]View, rev int64) {
			assert.Empty(t, views)
		}},
		{kind: KindFleet, check: func(t *testing.T, views map[string]View, rev int64) {
			assert.Equal(t, "m9", views[KindFleet]["coordinator"])
		}},
		{kind: KindSprint, check: func(t *testing.T, views map[string]View, rev int64) {
			assert.Equal(t, "", views[KindSprint]["coordinator"], "an absent sprint field is empty")
		}},
		{kind: KindLoop, check: func(t *testing.T, views map[string]View, rev int64) {
			assert.Empty(t, views, "no loop row reads as no rows")
		}},
		{kind: KindRoute, check: func(t *testing.T, views map[string]View, rev int64) {
			assert.Empty(t, views)
		}},
		{kind: KindTier, check: func(t *testing.T, views map[string]View, rev int64) {
			assert.Empty(t, views)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			views, rev, err := a.Read(ctx, tc.kind)
			require.NoError(t, err)
			tc.check(t, views, rev)
		})
	}

	_, _, err := a.Read(ctx, "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Redis reader for kind")
}

// TestRedisCoverWriteDispatch: Write routes each kind to its writer and refuses
// a kind with none.
func TestRedisCoverWriteDispatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("the fleet row is plain keys", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		row := Row{Name: KindFleet, Fields: map[string]string{"store": "hulk", "coordinator": "m9", "redis_port": "6380", "pg_dsn": ""}}
		require.NoError(t, a.Write(ctx, KindFleet, row, nil, "ada", "idem"))
		assert.Equal(t, "hulk", c.Get(ctx, FleetKey("store")).Val())
		assert.Equal(t, int64(0), c.Exists(ctx, FleetKey("pg_dsn")).Val(), "an empty field deletes its key")
	})

	t.Run("a loop row is one hash and a set member", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		row := Row{Name: "l1", Fields: map[string]string{
			"machine": "m1", "argv": `["/bin/true"]`, "seat": "", "keys": "",
			"every": "5", "keepalive": "false", "enabled": "true", "log": LoopLog("~/nova-bench/loops", "l1"),
		}}
		require.NoError(t, a.Write(ctx, KindLoop, row, nil, "ada", "config:loop:1"))
		assert.True(t, c.SIsMember(ctx, LoopsKey, "l1").Val())
		assert.Equal(t, "1", c.HGet(ctx, LoopKey("l1"), "rev").Val())
		assert.Equal(t, LoopLog("~/nova-bench/loops", "l1"), c.HGet(ctx, LoopKey("l1"), "log").Val())
	})

	t.Run("a friend with nobody to charge refuses", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		row := Row{Name: "ada", Fields: map[string]string{"slots": "8", "tiers": "flash", "roles": "", "width": "4"}}
		err := a.Write(ctx, KindFriend, row, nil, "ada", "idem")
		require.Error(t, err)
		assert.True(t, Refused(err))
		assert.ErrorIs(t, err, ErrCeiling)
	})

	t.Run("a machine whose ceiling call fails errors", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		row := Row{Name: "m1", Fields: map[string]string{"slots": "8"}}
		err := a.Write(ctx, KindMachine, row, nil, "ada", "idem")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "machine m1 ceiling")
	})

	t.Run("an unknown kind is refused", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		err := a.Write(ctx, "bogus", Row{}, nil, "ada", "idem")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no Redis writer for kind")
	})
}

// TestRedisCoverRemoveDispatch: Remove routes each kind to its remover, refuses
// a singleton row and refuses a kind with none.
func TestRedisCoverRemoveDispatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("a loop hash and its set member go", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.SAdd(ctx, LoopsKey, "l1")
		c.HSet(ctx, LoopKey("l1"), "machine", "m1")
		require.NoError(t, a.Remove(ctx, KindLoop, "l1", "ada", "idem"))
		assert.False(t, c.SIsMember(ctx, LoopsKey, "l1").Val())
		assert.Equal(t, int64(0), c.Exists(ctx, LoopKey("l1")).Val())
	})

	t.Run("a friend's own keys are left", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.SAdd(ctx, FriendsKey, "ada")
		c.HSet(ctx, "friend:ada:desired", "slots", "8")
		c.HSet(ctx, "friend:ada:wakepath", "kind", "human")
		require.NoError(t, a.Remove(ctx, KindFriend, "ada", "actor", "idem"))
		assert.False(t, c.SIsMember(ctx, FriendsKey, "ada").Val())
		assert.Equal(t, int64(0), c.Exists(ctx, "friend:ada:desired").Val())
		assert.Equal(t, int64(1), c.Exists(ctx, "friend:ada:wakepath").Val(), "her presence's own key stays")
	})

	t.Run("a machine goes", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.SAdd(ctx, MachinesKey, "m1")
		c.HSet(ctx, MachineKey("m1"), "user", "u")
		require.NoError(t, a.Remove(ctx, KindMachine, "m1", "ada", "idem"))
		assert.False(t, c.SIsMember(ctx, MachinesKey, "m1").Val())
	})

	t.Run("a singleton row is never removed", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		for _, kind := range []string{KindFleet, KindSprint} {
			err := a.Remove(ctx, kind, kind, "ada", "idem")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is never removed")
		}
	})

	t.Run("an unknown kind is refused", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		err := a.Remove(ctx, "bogus", "b", "ada", "idem")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no Redis remover for kind")
	})
}

// TestRedisCoverPrepare: Prepare installs the library once and answers the same
// error on every later call (prepareOnce).
func TestRedisCoverPrepare(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, _, _ := coverStore(t)
	first := a.Prepare(ctx)
	second := a.Prepare(ctx)
	assert.Equal(t, first, second, "Prepare runs its body at most once per applier")
}

// TestRedisCoverDeclRev: the stamped revision reads as a number, an absent one
// as zero, and a non-number is refused.
func TestRedisCoverDeclRev(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, _ := coverStore(t)

	rev, err := a.declRev(ctx, KindFriend)
	require.NoError(t, err)
	assert.Equal(t, int64(0), rev, "a kind never stamped is at rev 0")

	c.HSet(ctx, DeclKey, revField(KindFriend), "7")
	rev, err = a.declRev(ctx, KindFriend)
	require.NoError(t, err)
	assert.Equal(t, int64(7), rev)

	c.HSet(ctx, DeclKey, revField(KindFriend), "not-a-revision")
	_, err = a.declRev(ctx, KindFriend)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a revision")
}

// TestRedisCoverStamp: the compare-and-set writes the new revision while the
// stamp still reads prev, and refuses one that moved.
func TestRedisCoverStamp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, _ := coverStore(t)
	a.Now = func() time.Time { return time.UnixMilli(1234) }

	require.NoError(t, a.Stamp(ctx, KindFriend, 0, 5))
	assert.Equal(t, "5", c.HGet(ctx, DeclKey, revField(KindFriend)).Val())
	assert.Equal(t, "1234", c.HGet(ctx, DeclKey, atField(KindFriend)).Val(), "the stamp's time is the applier's clock")

	err := a.Stamp(ctx, KindFriend, 3, 6)
	require.Error(t, err)
	assert.True(t, IsConflict(err), "a stamp that moved past prev is a conflict")
	assert.Equal(t, "5", c.HGet(ctx, DeclKey, revField(KindFriend)).Val(), "a refused stamp writes nothing")
}

// TestRedisCoverReadFriends: the friend views read the registry, the desired
// hash, the roles, the beats, the coordinator and the stamp.
func TestRedisCoverReadFriends(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, _ := coverStore(t)
	c.SAdd(ctx, FriendsKey, "b", "a")
	c.HSet(ctx, "friend:a:desired", "slots", "8", "tiers", "pro,flash", "width", "4")
	c.HSet(ctx, "friend:a:roles", "roles", "reader,builder")
	c.HSet(ctx, FriendBeatKey("a"), "host", "m1")
	c.Set(ctx, FleetKey("coordinator"), "m9", 0)
	c.HSet(ctx, DeclKey, revField(KindFriend), "3")

	views, rev, err := a.readFriends(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), rev)
	require.Contains(t, views, "a")
	assert.Equal(t, "8", views["a"]["slots"])
	assert.Equal(t, "flash,pro", views["a"]["tiers"], "the tiers list is sorted")
	assert.Equal(t, "builder,reader", views["a"]["roles"], "the roles list is sorted")
	assert.Equal(t, "4", views["a"]["width"])
	assert.Equal(t, "m1", a.friendHosts["a"], "the beat host is cached")
	assert.Equal(t, "m9", a.coordinator)
	assert.True(t, a.coordinatorRead)
}

// TestRedisCoverPrefetchFriends: only the friends the cache lacks are read, and
// the coordinator is read only until it is known.
func TestRedisCoverPrefetchFriends(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, mr := coverStore(t)
	a.friendHosts = map[string]string{"a": "m1"}
	a.coordinatorRead = false
	c.HSet(ctx, FriendBeatKey("b"), "host", "m2")
	c.Set(ctx, FleetKey("coordinator"), "m9", 0)

	require.NoError(t, a.PrefetchFriends(ctx, []string{"a", "b"}))
	assert.Equal(t, "m2", a.friendHosts["b"])
	assert.Equal(t, "m1", a.friendHosts["a"], "a cached host is left as it is")
	assert.Equal(t, "m9", a.coordinator)
	assert.True(t, a.coordinatorRead)

	before := mr.CommandCount()
	require.NoError(t, a.PrefetchFriends(ctx, []string{"a", "b"}), "nothing is missing")
	assert.Equal(t, before, mr.CommandCount(), "a full cache reads nothing")
}

// TestRedisCoverCharge: a friend's slots charge to her beat's host, else the
// fleet's coordinator, else the refusal naming the fleet set that fixes it.
func TestRedisCoverCharge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("a cached host is the charge", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		a.friendHosts = map[string]string{"ada": "m1"}
		host, err := a.charge(ctx, "ada")
		require.NoError(t, err)
		assert.Equal(t, "m1", host)
	})

	t.Run("a beat's host is read and cached", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.HSet(ctx, FriendBeatKey("ada"), "host", "m2")
		host, err := a.charge(ctx, "ada")
		require.NoError(t, err)
		assert.Equal(t, "m2", host)
		assert.Equal(t, "m2", a.friendHosts["ada"])
	})

	t.Run("the coordinator is the default charge", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.Set(ctx, FleetKey("coordinator"), "m9", 0)
		host, err := a.charge(ctx, "ada")
		require.NoError(t, err)
		assert.Equal(t, "m9", host)
	})

	t.Run("beat host takes precedence over fleet coordinator", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		a, c, _ := coverStore(t)

		// Both fleet coordinator and friend beat host exist in Redis
		c.Set(ctx, FleetKey("coordinator"), "coord-host", 0)
		c.HSet(ctx, FriendBeatKey("ada"), "host", "beat-host")

		// 1. Friend's slots must bill to beat-host, NOT coord-host
		host, err := a.charge(ctx, "ada")
		require.NoError(t, err)
		assert.Equal(t, "beat-host", host, "friend beat host takes precedence over coordinator")
		assert.Equal(t, "beat-host", a.friendHosts["ada"], "beat host is cached")

		// 2. Friend without beat host falls back to coordinator
		bHost, err := a.charge(ctx, "bob")
		require.NoError(t, err)
		assert.Equal(t, "coord-host", bHost, "unreported beat host falls back to coordinator")

		// 3. Cleared beat host falls back to coordinator
		delete(a.friendHosts, "ada")
		c.HDel(ctx, FriendBeatKey("ada"), "host")
		fallbackHost, err := a.charge(ctx, "ada")
		require.NoError(t, err)
		assert.Equal(t, "coord-host", fallbackHost, "empty beat host falls back to coordinator")
	})

	t.Run("nobody to charge refuses with the remedy", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		_, err := a.charge(ctx, "ada")
		require.Error(t, err)
		assert.True(t, Refused(err))
		assert.ErrorIs(t, err, ErrCeiling)
		assert.Contains(t, err.Error(), "nova-config fleet set --coordinator")
	})
}

// TestRedisCoverWriteFriend: writeFriend charges the friend before its function
// call, so a friend nobody can charge refuses, and a charged one reaches the
// function with its host.
func TestRedisCoverWriteFriend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	row := Row{Name: "ada", Fields: map[string]string{"slots": "8", "tiers": "flash", "roles": "builder", "width": "4"}}

	t.Run("a friend nobody can charge refuses before any write", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		err := a.writeFriend(ctx, row, nil, "ada", "idem")
		require.Error(t, err)
		assert.True(t, Refused(err))
	})

	t.Run("a charged friend reaches the capacity function", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.HSet(ctx, FriendBeatKey("ada"), "host", "m1")
		err := a.writeFriend(ctx, row, nil, "ada", "idem")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "friend ada slots")
	})
}

// TestRedisCoverReadMachines: a machine's view reads its ceiling for slots and
// its registry for the declared fields.
func TestRedisCoverReadMachines(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, _ := coverStore(t)
	c.SAdd(ctx, MachinesKey, "m1")
	c.HSet(ctx, MachineKey("m1"), "user", "u", "seat", "s", "slots", "8", "runners", "1", "tla", "false", "note", "")
	c.HSet(ctx, MachineCeilingKey("m1"), "slots", "8")
	c.HSet(ctx, DeclKey, revField(KindMachine), "2")

	views, rev, err := a.readMachines(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rev)
	require.Contains(t, views, "m1")
	assert.Equal(t, "8", views["m1"]["slots"], "slots come from the ceiling the runtime guards on")
	assert.Equal(t, "u", views["m1"]["user"])
	assert.Equal(t, "", views["m1"]["width"], "an unset nullable int is empty")
}

// TestRedisCoverWriteMachine: with no prior view the ceiling function is called;
// with an unchanged ceiling the registry is written without it.
func TestRedisCoverWriteMachine(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	row := Row{Name: "m1", Fields: map[string]string{"user": "u", "seat": "s", "slots": "8", "runners": "1", "width": "", "tla": "false", "note": ""}}

	t.Run("a new machine calls the ceiling function", func(t *testing.T) {
		t.Parallel()
		a, _, _ := coverStore(t)
		err := a.writeMachine(ctx, row, nil, "ada", "config:machine:2")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "machine m1 ceiling")
	})

	t.Run("an unchanged ceiling writes the registry alone", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		require.NoError(t, a.writeMachine(ctx, row, View{"slots": "8"}, "ada", "config:machine:2"))
		assert.True(t, c.SIsMember(ctx, MachinesKey, "m1").Val())
		assert.Equal(t, "u", c.HGet(ctx, MachineKey("m1"), "user").Val())
		assert.Equal(t, "2", c.HGet(ctx, MachineKey("m1"), "rev").Val())
	})
}

// TestRedisCoverRemoveMachine: a machine its friends still name is refused,
// naming them; once none does, the machine's own keys go.
func TestRedisCoverRemoveMachine(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("a machine in use is refused", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.SAdd(ctx, FriendsKey, "ada")
		c.HSet(ctx, "friend:ada:desired", "machine", "m1")
		err := a.removeMachine(ctx, "m1", "ada", "idem")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrInUse)
		assert.Contains(t, err.Error(), "friend:ada")
	})

	t.Run("an unused machine is removed", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.SAdd(ctx, MachinesKey, "m1")
		c.HSet(ctx, MachineKey("m1"), "user", "u")
		c.HSet(ctx, MachineCeilingKey("m1"), "slots", "8")
		require.NoError(t, a.removeMachine(ctx, "m1", "ada", "idem"))
		assert.False(t, c.SIsMember(ctx, MachinesKey, "m1").Val())
		assert.Equal(t, int64(0), c.Exists(ctx, MachineKey("m1")).Val())
	})
}

// TestRemoveMachineIsRefusedWhileABenchDesiredHashNamesIt pins security#69
// finding 3: removeMachine must read the bench registry set as "benches", the
// name the function library writes, so a bench that still names the machine
// refuses the removal.
func TestRemoveMachineIsRefusedWhileABenchDesiredHashNamesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, c, _ := coverStore(t)
	c.SAdd(ctx, "benches", "b1")
	c.HSet(ctx, "bench:b1:desired", "machine", "m1")
	err := a.removeMachine(ctx, "m1", "ada", "idem")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInUse)
	assert.Contains(t, err.Error(), "bench:b1")
}

// TestRedisCoverHashes: readHashes reads a row's hash into a typed view,
// writeHash replaces it whole with its set member, and removeHash takes both.
func TestRedisCoverHashes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	h := hashKinds[KindLoop]
	row := Row{Name: "l1", Fields: map[string]string{
		"machine": "m1", "argv": `["/bin/true"]`, "seat": "", "keys": "",
		"every": "5", "keepalive": "false", "enabled": "true",
	}}

	t.Run("writeHash writes every field and the derived log", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		logged := row.Clone()
		logged.Fields["log"] = LoopLog("~/nova-bench/loops", "l1")
		require.NoError(t, a.writeHash(ctx, h, logged, "config:loop:1"))
		got := c.HGetAll(ctx, LoopKey("l1")).Val()
		assert.Equal(t, "l1", got["name"])
		assert.Equal(t, "1", got["rev"])
		assert.Equal(t, "5", got["every"])
		assert.Equal(t, LoopLog("~/nova-bench/loops", "l1"), got["log"])
		assert.True(t, c.SIsMember(ctx, LoopsKey, "l1").Val())
	})

	t.Run("readHashes canonicalises a hand-written hash", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.SAdd(ctx, LoopsKey, "l1")
		c.HSet(ctx, LoopKey("l1"), "machine", "m1", "every", "05", "keepalive", "1", "enabled", "yes")
		c.HSet(ctx, DeclKey, revField(KindLoop), "2")
		views, rev, err := a.readHashes(ctx, h)
		require.NoError(t, err)
		assert.Equal(t, int64(2), rev)
		assert.Equal(t, "5", views["l1"]["every"], "an int is canonicalised")
		assert.Equal(t, "true", views["l1"]["keepalive"], "a bool is canonicalised")
		assert.Equal(t, "false", views["l1"]["enabled"], "anything but a true spelling is false")
	})

	t.Run("removeHash takes the hash and the set member", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.SAdd(ctx, LoopsKey, "l1")
		c.HSet(ctx, LoopKey("l1"), "machine", "m1")
		require.NoError(t, a.removeHash(ctx, h, "l1", "ada", "idem"))
		assert.False(t, c.SIsMember(ctx, LoopsKey, "l1").Val())
		assert.Equal(t, int64(0), c.Exists(ctx, LoopKey("l1")).Val())
	})
}

// TestRedisCoverSingletons: readSingleton reads one view of a singleton row,
// writeSingleton sets a named field and deletes an empty one.
func TestRedisCoverSingletons(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("writeSingleton sets and deletes", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.Set(ctx, FleetKey("pg_dsn"), "postgres://old", 0)
		row := Row{Name: KindFleet, Fields: map[string]string{"store": "hulk", "coordinator": "", "redis_port": "6380", "pg_dsn": ""}}
		require.NoError(t, a.writeSingleton(ctx, KindFleet, FleetKey, row))
		assert.Equal(t, "hulk", c.Get(ctx, FleetKey("store")).Val())
		assert.Equal(t, int64(0), c.Exists(ctx, FleetKey("coordinator")).Val(), "an empty field deletes its key")
		assert.Equal(t, int64(0), c.Exists(ctx, FleetKey("pg_dsn")).Val())
	})

	t.Run("readSingleton reads every field and the stamp", func(t *testing.T) {
		t.Parallel()
		a, c, _ := coverStore(t)
		c.Set(ctx, FleetKey("store"), "hulk", 0)
		c.Set(ctx, FleetKey("coordinator"), "m9", 0)
		c.HSet(ctx, DeclKey, revField(KindFleet), "7")
		views, rev, err := a.readSingleton(ctx, KindFleet, FleetKey)
		require.NoError(t, err)
		assert.Equal(t, int64(7), rev)
		assert.Equal(t, "hulk", views[KindFleet]["store"])
		assert.Equal(t, "m9", views[KindFleet]["coordinator"])
		assert.Equal(t, "", views[KindFleet]["redis_port"], "an absent field is empty")
	})
}

// TestRedisCoverSnapshot: the applied state is read in two pipelines: the names
// and declared fields first, every machine, beat and loop next.
func TestRedisCoverSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, _ := coverStore(t)
	c.SAdd(ctx, MachinesKey, "m1")
	c.SAdd(ctx, LoopsKey, "l1")
	c.Set(ctx, FleetKey("store"), "hulk", 0)
	c.HSet(ctx, DeclKey, revField(KindLoop), "1", revField(KindMachine), "2")
	c.HSet(ctx, MachineKey("m1"), "user", "u", "seat", "s", "slots", "8")
	c.HSet(ctx, MachineCeilingKey("m1"), "slots", "8")
	c.HSet(ctx, BeatKey("m1"), "os", "linux", "arch", "amd64", "ncpu", "64", "memory_gb", "251", "at", "1790000000000")
	c.HSet(ctx, LoopKey("l1"), "machine", "m1", "every", "5")

	snap, err := a.Snapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, "hulk", snap.Fleet["store"])
	assert.Equal(t, int64(1), snap.Revs[KindLoop])
	assert.Equal(t, int64(2), snap.Revs[KindMachine])
	require.Contains(t, snap.Machines, "m1")
	assert.Equal(t, "8", snap.Machines["m1"]["slots"])
	require.Contains(t, snap.Beats, "m1")
	assert.Equal(t, "linux", snap.Beats["m1"].OS)
	assert.Equal(t, "amd64", snap.Beats["m1"].Arch)
	assert.Equal(t, "64", snap.Beats["m1"].Cores)
	assert.Equal(t, "251", snap.Beats["m1"].MemoryGB)
	require.Contains(t, snap.Loops, "l1")
	assert.Equal(t, "m1", snap.Loops["l1"]["machine"])
}

// TestRedisCoverBeats: a named machine's beat reads its measured facts and its
// time; a machine with no beat is absent.
func TestRedisCoverBeats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	a, c, _ := coverStore(t)
	c.HSet(ctx, BeatKey("m1"), "os", "linux", "arch", "amd64", "ncpu", "64", "memory_gb", "251", "at", "1790000000000")

	beats, err := a.Beats(ctx, []string{"m1", "m2"})
	require.NoError(t, err)
	require.Contains(t, beats, "m1")
	assert.Equal(t, "linux", beats["m1"].OS)
	assert.Equal(t, "amd64", beats["m1"].Arch)
	assert.Equal(t, "64", beats["m1"].Cores)
	assert.Equal(t, "251", beats["m1"].MemoryGB)
	assert.Equal(t, "2026-09-21T14:13:20Z", beats["m1"].At)
	assert.NotContains(t, beats, "m2", "a machine with no beat is absent")
}
