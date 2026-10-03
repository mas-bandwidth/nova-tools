//go:build functional

package config

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redisApplier starts a throwaway Redis with the nova_sprint library and
// returns the applier over it and the raw client.
func redisApplier(t *testing.T) (*RedisApplier, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	scopedErr28 := fn.Load(context.Background(), c)
	require.NoError(t, scopedErr28)
	return &RedisApplier{Client: c}, c
}

// applyKinds applies every kind in order from st into ap.
func applyKinds(t *testing.T, st Store, ap Applier, actor string) map[string]Result {
	t.Helper()
	out := map[string]Result{}
	for _, kind := range KindNames() {
		res, err := Apply(context.Background(), st, ap, kind, actor, false, func(Op) {})
		require.NoError(t, err, "apply %s: %v", kind, err)
		out[kind] = res
	}
	return out
}

// TestApplyLeavesTheKeysCapacityFriendWould: after apply, Redis holds for a
// friend exactly what `nova-sprint capacity friend --tiers` and `friend
// roles` would have left: the registry member, the desired hash under the
// ceiling of the machine she is charged to, and the roles hash with the
// sprint's coordinator role on the one friend the sprint row names. Her
// logins and wake path are her own presence's and are not written.
func TestApplyLeavesTheKeysCapacityFriendWould(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	// stella's beat says she runs on hulk; rowan has no beat and is charged
	// to the fleet's coordinator machine, studio.
	c.HSet(ctx, FriendBeatKey("stella"), "host", "hulk", "at", "1790000000000")
	res := applyKinds(t, st, ap, "rowan")
	assertionMsg64 := []any{"results %+v", res}
	require.Equal(t, 2, res[KindMachine].Add, assertionMsg64...)
	require.Equal(t, 2, res[KindFriend].Add, assertionMsg64...)
	require.Equal(t, 1, res[KindFleet].Set, assertionMsg64...)
	require.Equal(t, 1, res[KindSprint].Set, assertionMsg64...)

	// The machine: the ceiling ns_capacity_machine writes (slots alone:
	// cores and memory are never declared, so the ceiling carries none and
	// no budget is derived) and the registry hash nova-config owns, exactly
	// the five declared fields with the revision and time.
	{
		got := c.HGetAll(ctx, "machine:studio:ceiling").Val()
		assertionMsg76 := []any{"machine:studio:ceiling %v", got}
		func() {
			if !assert.Equal(t, "64", got["slots"], assertionMsg76...) {
				return
			}
			if !assert.Equal(t, "", got["cores"], assertionMsg76...) {
				return
			}
			if !assert.Equal(t, "", got["mem_gb"], assertionMsg76...) {
				return
			}
			assert.NotEqual(t, "", got["at"], assertionMsg76...)
		}()
	}
	assert.Equal(t, int64(0), c.Exists(ctx, "machine:studio:budget").Val(), "a budget was derived from cores nobody declared")
	{
		got := c.HGetAll(ctx, "machine:studio").Val()
		assertionMsg81 := []any{"machine:studio %v: want user, seat, slots, runners, width, tla, note, rev, at and nothing else", got}
		func() {
			if !assert.Equal(t, "glenn", got["user"], assertionMsg81...) {
				return
			}
			if !assert.Equal(t, "studio", got["seat"], assertionMsg81...) {
				return
			}
			if !assert.Equal(t, "64", got["slots"], assertionMsg81...) {
				return
			}
			if !assert.Equal(t, "1", got["runners"], assertionMsg81...) {
				return
			}
			if !assert.Equal(t, "2", got["rev"], assertionMsg81...) {
				return
			}
			if !assert.NotEqual(t, "", got["at"], assertionMsg81...) {
				return
			}
			if !assert.Equal(t, "", got["width"], assertionMsg81...) { // no width: the default
				return
			}
			if !assert.Equal(t, "false", got["tla"], assertionMsg81...) {
				return
			}
			if !assert.Equal(t, "", got["note"], assertionMsg81...) {
				return
			}
			assert.Len(t, got, 9, assertionMsg81...)
		}()
	}
	{
		got := c.HGetAll(ctx, "machine:hulk").Val()
		assertionMsg85 := []any{"machine:hulk %v", got}
		func() {
			if !assert.Equal(t, "gaffer", got["user"], assertionMsg85...) {
				return
			}
			if !assert.Equal(t, "swarm-hulk", got["seat"], assertionMsg85...) {
				return
			}
			assert.Equal(t, "0", got["runners"], assertionMsg85...)
		}()
	}
	scopedMembers128 := c.SMembers(ctx, MachinesKey).Val()
	assert.Len(t, scopedMembers128, 2, "machines %v", scopedMembers128)
	// The fleet and the sprint: one plain key per named field.
	gotCoordinator := c.Get(ctx, FleetKey("coordinator")).Val()
	gotStoreExists := c.Exists(ctx, FleetKey("store")).Val()
	assert.Equal(t, "studio", gotCoordinator, "fleet coordinator view")
	assert.Equal(t, int64(0), gotStoreExists, "fleet store should be absent")
	scopedGot137 := c.Get(ctx, SprintKey("coordinator")).Val()
	assert.Equal(t, "rowan", scopedGot137, "sprint:coordinator %q", scopedGot137)

	// The friend: what capacity friend and friend roles would have left,
	// and nothing her own presence writes.
	assertionMsg102 := []any{"friends registry lacks rowan or stella"}
	func() {
		if !assert.True(t, c.SIsMember(ctx, FriendsKey, "rowan").Val(), assertionMsg102...) {
			return
		}
		assert.True(t, c.SIsMember(ctx, FriendsKey, "stella").Val(), assertionMsg102...)
	}()
	{
		got := c.HGetAll(ctx, "friend:rowan:desired").Val()
		assertionMsg105 := []any{"friend:rowan:desired %v (charged to the coordinator machine: no beat)", got}
		func() {
			if !assert.Equal(t, "32", got["slots"], assertionMsg105...) {
				return
			}
			if !assert.Equal(t, "studio", got["machine"], assertionMsg105...) {
				return
			}
			if !assert.Equal(t, "frontier", got["tiers"], assertionMsg105...) {
				return
			}
			if !assert.Equal(t, "0", got["paused"], assertionMsg105...) {
				return
			}
			assert.NotEqual(t, "", got["at"], assertionMsg105...)
		}()
	}
	{
		got := c.HGetAll(ctx, "friend:stella:desired").Val()
		assertionMsg109 := []any{"friend:stella:desired %v (charged to the host her beat reports)", got}
		func() {
			if !assert.Equal(t, "32", got["slots"], assertionMsg109...) {
				return
			}
			if !assert.Equal(t, "hulk", got["machine"], assertionMsg109...) {
				return
			}
			assert.Equal(t, "frontier,pro", got["tiers"], assertionMsg109...)
		}()
	}
	{
		got := c.HGetAll(ctx, "friend:rowan:roles").Val()
		assertionMsg113 := []any{"friend:rowan:roles %v (the sprint's coordinator carries the role)", got}
		func() {
			if !assert.Equal(t, "builder,coordinator", got["roles"], assertionMsg113...) {
				return
			}
			assert.Equal(t, "rowan", got["by"], assertionMsg113...)
		}()
	}
	scopedGot193 := c.HGetAll(ctx, "friend:stella:roles").Val()
	assert.Equal(t, "builder,reader", scopedGot193["roles"], "friend:stella:roles %v", scopedGot193)
	for _, key := range []string{"friend:rowan:wakepath", "friend:stella:wakepath", "friends:login", "friend:rowan:config"} {
		assert.Equal(t, int64(0), c.Exists(ctx, key).Val(), "%s was written: it is the friend's own presence's, not configuration", key)
	}
	{
		got := c.HGetAll(ctx, DeclKey).Val()
		assertionMsg124 := []any{"config:decl %v", got}
		func() {
			if !assert.Equal(t, "4", got["rev:friend"], assertionMsg124...) {
				return
			}
			if !assert.Equal(t, "2", got["rev:machine"], assertionMsg124...) {
				return
			}
			if !assert.Equal(t, "5", got["rev:fleet"], assertionMsg124...) {
				return
			}
			if !assert.Equal(t, "6", got["rev:sprint"], assertionMsg124...) {
				return
			}
			assert.NotEqual(t, "", got["at:friend"], assertionMsg124...)
		}()
	}
	// The receipts every capacity write leaves.
	scopedN220 := c.XLen(ctx, CapLogKey).Val()
	assert.GreaterOrEqual(t, scopedN220, int64(4), "cap:log has %d receipts, want one per desired and ceiling write at least", scopedN220)

	// Read reads back exactly the views the rows are, for every kind, so a
	// second apply is a no-op and the stamp is unchanged.
	for _, kind := range KindNames() {
		views, _, err := ap.Read(ctx, kind)
		require.NoError(t, err, "read %s: %v", kind, err)
		k, _ := Lookup(kind)
		rows, _ := st.List(ctx, kind)
		if k.Derive != nil {
			rows, _ = k.Derive(ctx, st, rows)
		}
		for _, row := range rows {
			for f, want := range row.Fields {
				assert.Equal(t, want, views[row.Name][f], "view of %s %s.%s = %q, row has %q", kind, row.Name, f, views[row.Name][f], want)
			}
		}
	}
	{
		_, rev, err := ap.Read(ctx, KindFriend)
		assertionMsg146 := []any{"read friends: rev %d err %v", rev, err}
		require.NoError(t, err, assertionMsg146...)
		require.Equal(t, int64(4), rev, assertionMsg146...)
	}
	res = applyKinds(t, st, ap, "rowan")
	{
		r := res[KindFriend]
		assertionMsg151 := []any{"second apply %+v", r}
		require.Equal(t, 0, r.Add+r.Set+r.Remove, assertionMsg151...)
		require.Equal(t, int64(4), r.Rev, assertionMsg151...)
		require.Equal(t, int64(4), r.RedisRev, assertionMsg151...)
	}
	scopedGot255 := c.HGet(ctx, DeclKey, "rev:friend").Val()
	require.Equal(t, "4", scopedGot255, "stamp after a no-op apply %s", scopedGot255)
}

func TestApplySetsAndRemovesAFriend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")

	// A set in Postgres: slots, tiers, a role and her width.
	_, _, setupErr9031 := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "30", "tiers": "flash", "roles": "reader", "width": "4"}, "rowan")
	require.NoError(t, setupErr9031)
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg173 := []any{"apply after set: %+v %v", res, err}
	require.NoError(t, err, assertionMsg173...)
	require.Equal(t, 1, res.Set, assertionMsg173...)
	{
		got := c.HGetAll(ctx, "friend:stella:desired").Val()
		assertionMsg187 := []any{"stella desired %v", got}
		func() {
			if !assert.Equal(t, "30", got["slots"], assertionMsg187...) {
				return
			}
			assert.Equal(t, "flash", got["tiers"], assertionMsg187...)
			assert.Equal(t, "4", got["width"], assertionMsg187...)
		}()
	}
	scopedGot286 := c.HGet(ctx, "friend:stella:roles", "roles").Val()
	assert.Equal(t, "reader", scopedGot286, "stella roles %q", scopedGot286)

	// The handover: the sprint names stella; the next friend apply gives
	// her the role first and takes it from rowan, as rowan (who holds it).
	_, _, setupErr9967 := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "stella"}, "rowan")
	require.NoError(t, setupErr9967)
	var reported []string
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(op Op) { reported = append(reported, op.Name) })
	assertionMsg191 := []any{"handover: %+v %v reported %v", res, err, reported}
	require.NoError(t, err, assertionMsg191...)
	require.Equal(t, 2, res.Set, assertionMsg191...)
	require.Equal(t, "stella rowan", strings.Join(reported, " "), assertionMsg191...)
	scopedGot301 := c.HGet(ctx, "friend:stella:roles", "roles").Val()
	assert.Equal(t, "coordinator,reader", scopedGot301, "stella roles after the handover %q", scopedGot301)
	scopedGot305 := c.HGet(ctx, "friend:rowan:roles", "roles").Val()
	assert.Equal(t, "builder", scopedGot305, "rowan roles after the handover %q", scopedGot305)

	// A remove takes what apply wrote, leaving her presence's own keys
	// behind; removing a friend does not read sprint keys and succeeds
	// even if working copies exist.
	_, _, setupErr10964 := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "rowan"}, "stella")
	require.NoError(t, setupErr10964)
	{
		_, err := Apply(ctx, st, ap, KindFriend, "stella", false, func(Op) {})
		require.NoError(t, err, "handover back as stella: %v", err)
	}
	_, setupErr11244 := st.Delete(ctx, KindFriend, "stella", "rowan")
	require.NoError(t, setupErr11244)
	c.HSet(ctx, "friend:stella:wakepath", "kind", "human", "notify", "#stella")
	c.ZAdd(ctx, "friend:stella:cards:working", redis.Z{Score: 1, Member: "card:4410"}, redis.Z{Score: 2, Member: "card:4414"})
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg219 := []any{"remove: %+v %v", res, err}
	require.NoError(t, err, assertionMsg219...)
	require.Equal(t, 1, res.Remove, assertionMsg219...)
	for _, key := range []string{"friend:stella:desired", "friend:stella:roles"} {
		assert.Equal(t, int64(0), c.Exists(ctx, key).Val(), "%s survived the remove", key)
	}
	assert.Equal(t, int64(1), c.Exists(ctx, "friend:stella:wakepath").Val(), "the remove took her wake path, which is her presence's own")
	assert.False(t, c.SIsMember(ctx, FriendsKey, "stella").Val(), "stella is still in friends")
	scopedGot332 := c.HGet(ctx, DeclKey, "rev:friend").Val()
	require.Equal(t, "10", scopedGot332, "stamp after remove %s", scopedGot332)
	scopedGot336 := c.ZCard(ctx, "friend:stella:cards:working").Val()
	require.Equal(t, int64(2), scopedGot336, "working copies were modified: %d", scopedGot336)
}

// TestApplyRefusesAFriendNobodyCanCharge: a friend with no beat and no
// coordinator machine in the fleet row cannot be charged to a ceiling; the
// refusal names the fleet set that fixes it and nothing is written.
func TestApplyRefusesAFriendNobodyCanCharge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	_, _, setupErr12770 := st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": ""}, "rowan")
	require.NoError(t, setupErr12770)
	for _, kind := range []string{KindMachine, KindFleet} {
		_, setupErr12958 := Apply(ctx, st, ap, kind, "rowan", false, func(Op) {})
		require.NoError(t, setupErr12958)
	}
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg255 := []any{"nobody to charge: %v", err}
	require.Error(t, err, assertionMsg255...)
	require.True(t, Refused(err), assertionMsg255...)
	require.ErrorContains(t, err, "friend rowan has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>", assertionMsg255...)
	require.False(t, c.SIsMember(ctx, FriendsKey, "rowan").Val(), "a refused friend was registered")
}

// TestApplyWritesTheFleetKeysAndTheLiveFactsAreTheBeat: the fleet row is
// plain keys of nova-config's own, set or deleted as the row says; a
// machine's measured facts are read from its beat and never written.
func TestApplyWritesTheFleetKeysAndTheLiveFactsAreTheBeat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	_, _, setupErr14007 := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "hulk"}, "rowan")
	require.NoError(t, setupErr14007)
	res, err := Apply(ctx, st, ap, KindFleet, "rowan", false, func(Op) {})
	assertionMsg274 := []any{"apply the fleet: %+v %v", res, err}
	require.NoError(t, err, assertionMsg274...)
	require.Equal(t, 1, res.Set, assertionMsg274...)
	require.Equal(t, int64(7), res.Rev, assertionMsg274...)
	scopedGot382 := c.Get(ctx, FleetKey("store")).Val()
	assert.Equal(t, "hulk", scopedGot382, "fleet:store %q", scopedGot382)
	scopedGot386 := c.Get(ctx, FleetKey("coordinator")).Val()
	assert.Equal(t, "studio", scopedGot386, "fleet:coordinator %q", scopedGot386)
	assert.Equal(t, "6380", c.Get(ctx, FleetKey("redis_port")).Val())
	assert.Equal(t, "postgres://nova_config@localhost:5432/nova", c.Get(ctx, FleetKey("pg_dsn")).Val())
	scopedGot390 := c.HGet(ctx, DeclKey, "rev:fleet").Val()
	assert.Equal(t, "7", scopedGot390, "rev:fleet %s", scopedGot390)
	// Clearing a field deletes its key.
	_, _, setupErr14789 := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": ""}, "rowan")
	require.NoError(t, setupErr14789)
	res, err = Apply(ctx, st, ap, KindFleet, "rowan", false, func(Op) {})
	assertionMsg293 := []any{"apply the cleared store: %+v %v", res, err}
	require.NoError(t, err, assertionMsg293...)
	require.Equal(t, 1, res.Set, assertionMsg293...)
	assertionMsg318 := []any{"clearing the store did not delete fleet:store, or took the coordinator with it"}
	func() {
		if !assert.Equal(t, int64(0), c.Exists(ctx, FleetKey("store")).Val(), assertionMsg318...) {
			return
		}
		assert.Equal(t, "studio", c.Get(ctx, FleetKey("coordinator")).Val(), assertionMsg318...)
	}()

	// The beats: hulk has beaten (today's fields), studio never has.
	c.HSet(ctx, BeatKey("hulk"), "host", "hulk", "at", "1790000000000", "load1", "0.5", "ncpu", "64", "cpu", "3")
	beats, err := ap.Beats(ctx, []string{"hulk", "studio"})
	require.NoError(t, err)
	{
		b := beats["hulk"]
		assertionMsg326 := []any{"hulk's beat %+v", b}
		func() {
			if !assert.NotNil(t, b, assertionMsg326...) {
				return
			}
			if !assert.Equal(t, "64", b.Cores, assertionMsg326...) {
				return
			}
			if !assert.Equal(t, "", b.OS, assertionMsg326...) {
				return
			}
			if !assert.Equal(t, "", b.Arch, assertionMsg326...) {
				return
			}
			if !assert.Equal(t, "", b.MemoryGB, assertionMsg326...) {
				return
			}
			assert.Equal(t, "2026-09-21T14:13:20Z", b.At, assertionMsg326...)
		}()
	}
	assert.Nil(t, beats["studio"], "studio has no beat and one was read: %+v", beats["studio"])
	// The follow-on fields are read when the beat carries them.
	c.HSet(ctx, BeatKey("hulk"), "os", "linux", "arch", "amd64", "memory_gb", "251")
	beats, _ = ap.Beats(ctx, []string{"hulk"})
	{
		b := beats["hulk"]
		assertionMsg334 := []any{"hulk's fuller beat %+v", b}
		func() {
			if !assert.Equal(t, "linux", b.OS, assertionMsg334...) {
				return
			}
			if !assert.Equal(t, "amd64", b.Arch, assertionMsg334...) {
				return
			}
			assert.Equal(t, "251", b.MemoryGB, assertionMsg334...)
		}()
	}
	assertionMsg336 := []any{"reading a beat wrote something"}
	func() {
		if !assert.Equal(t, int64(1), c.Exists(ctx, BeatKey("hulk")).Val(), assertionMsg336...) {
			return
		}
		assert.Equal(t, "", c.HGet(ctx, "machine:hulk", "os").Val(), assertionMsg336...)
	}()
}

func TestApplyRefusesConflictWhenRedisIsAheadOfPostgres(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	c.HSet(ctx, DeclKey, "rev:friend", "40")
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg324 := []any{"Redis ahead: %v", err}
	require.Error(t, err, assertionMsg324...)
	require.True(t, IsConflict(err), assertionMsg324...)
	require.ErrorContains(t, err, "Redis holds rev 40", "conflict %q", err)
	// The stamp is compare-and-set: one that moves between the read and
	// the stamp is refused too.
	c.HSet(ctx, DeclKey, "rev:friend", "4")
	{
		err := ap.Stamp(ctx, KindFriend, 3, 5)
		assertionMsg331 := []any{"stamp with a moved prev: %v", err}
		require.Error(t, err, assertionMsg331...)
		require.True(t, IsConflict(err), assertionMsg331...)
	}
	scopedErr483 := ap.Stamp(ctx, KindFriend, 4, 5)
	require.NoError(t, scopedErr483, "stamp with the right prev: %v", scopedErr483)
	scopedGot487 := c.HGet(ctx, DeclKey, "rev:friend").Val()
	require.Equal(t, "5", scopedGot487, "stamp %s", scopedGot487)
}

func TestApplyRefusesACeilingAndAMachineInUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, _ := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	// stella 32 + rowan 32 = 64 on studio; one more slot is over the
	// ceiling, refused by ns_capacity_desired, and named with the remedy.
	_, _, setupErr18524 := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "33"}, "rowan")
	require.NoError(t, setupErr18524)
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg357 := []any{"over the ceiling: %v", err}
	require.Error(t, err, assertionMsg357...)
	require.ErrorIs(t, err, ErrCeiling, assertionMsg357...)
	require.ErrorContains(t, err, "CEILING studio: friend stella makes the sum 65 over the machine ceiling 64", "ceiling refusal %q", err)
	// A machine's ceiling below its friends' sum is refused by
	// ns_capacity_machine.
	_, _, setupErr19099 := st.Update(ctx, KindMachine, "studio", map[string]string{"slots": "10"}, "rowan")
	require.NoError(t, setupErr19099)
	_, err = Apply(ctx, st, ap, KindMachine, "rowan", false, func(Op) {})
	assertionMsg366 := []any{"ceiling below the sum: %v", err}
	require.Error(t, err, assertionMsg366...)
	require.ErrorIs(t, err, ErrCeiling, assertionMsg366...)
	require.ErrorContains(t, err, "CEILING studio: its friends desire 64 slots and the row says 10", assertionMsg366...)
	// A machine that Redis still has consumers on cannot be removed (here
	// the Redis side is exercised directly).
	err = ap.Remove(ctx, KindMachine, "studio", "rowan", "test")
	assertionMsg370 := []any{"remove a machine in use: %v", err}
	require.Error(t, err, assertionMsg370...)
	require.ErrorIs(t, err, ErrInUse, assertionMsg370...)
	require.ErrorContains(t, err, "machine studio still carries friend:rowan,friend:stella in Redis", assertionMsg370...)
}

func TestApplyNeedsTheCoordinatorRoleForRoles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, _ := redisApplier(t)
	st := seed(t)
	// The actor is not a friend at all: the first roles write refuses.
	var reported bytes.Buffer
	for _, kind := range []string{KindMachine, KindFleet} {
		_, setupErr20335 := Apply(ctx, st, ap, kind, "nobody", false, func(Op) {})
		require.NoError(t, setupErr20335)
	}
	_, err := Apply(ctx, st, ap, KindFriend, "nobody", false, func(op Op) { reported.WriteString(op.Name + " ") })
	assertionMsg388 := []any{"roles as nobody: %v", err}
	require.Error(t, err, assertionMsg388...)
	require.ErrorIs(t, err, ErrActor, assertionMsg388...)
	require.ErrorContains(t, err, "--as nobody is not a registered friend", assertionMsg388...)
}

type testCmdHook struct {
	onCmd func(redis.Cmder)
}

func (h *testCmdHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *testCmdHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.onCmd != nil {
			h.onCmd(cmd)
		}
		return next(ctx, cmd)
	}
}

func (h *testCmdHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if h.onCmd != nil {
				h.onCmd(cmd)
			}
		}
		return next(ctx, cmds)
	}
}

// TestRemoveFriendDoesNotTouchSprintKeys asserts that removing a friend
// succeeds without reading or touching any sprint keys (sprint:epoch,
// <consumer>:cards:working), even when sprint keys exist, working copies
// are held, or sprint:epoch holds an invalid type that would fail an HGET.
func TestRemoveFriendDoesNotTouchSprintKeys(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)

	type loggedCmd struct {
		name string
		args []string
	}
	var (
		mu       sync.Mutex
		recorded []loggedCmd
		logging  bool
	)
	c.AddHook(&testCmdHook{
		onCmd: func(cmd redis.Cmder) {
			mu.Lock()
			defer mu.Unlock()
			if !logging {
				return
			}
			var args []string
			for _, a := range cmd.Args() {
				args = append(args, fmt.Sprint(a))
			}
			recorded = append(recorded, loggedCmd{name: cmd.Name(), args: args})
		},
	})

	// Populate friend keys.
	c.SAdd(ctx, FriendsKey, "stella")
	c.HSet(ctx, "friend:stella:desired", "slots", "30", "tiers", "flash")
	c.HSet(ctx, "friend:stella:roles", "roles", "reader")

	// Populate sprint keys:
	// 1. Poison sprint:epoch as a string key (HGET would fail with WRONGTYPE).
	c.Set(ctx, "sprint:epoch", "not-a-hash", 0)
	// 2. Populate working copies for stella.
	c.ZAdd(ctx, "friend:stella:cards:working", redis.Z{Score: 1, Member: "card:4410"})

	mu.Lock()
	logging = true
	mu.Unlock()

	scopedErr623 := ap.Remove(ctx, KindFriend, "stella", "rowan", "idem-remove-1")
	require.NoError(t, scopedErr623, "removeFriend failed: %v", scopedErr623)

	mu.Lock()
	logging = false
	cmds := make([]loggedCmd, len(recorded))
	copy(cmds, recorded)
	mu.Unlock()

	// Friend keys apply wrote are removed.
	assert.False(t, c.SIsMember(ctx, FriendsKey, "stella").Val(), "stella still in friends set")
	assert.Equal(t, int64(0), c.Exists(ctx, "friend:stella:desired").Val(), "friend:stella:desired still exists")
	assert.Equal(t, int64(0), c.Exists(ctx, "friend:stella:roles").Val(), "friend:stella:roles still exists")

	// Sprint keys were untouched.
	scopedGot640 := c.Get(ctx, "sprint:epoch").Val()
	assert.Equal(t, "not-a-hash", scopedGot640, "sprint:epoch was modified: %q", scopedGot640)
	scopedGot644 := c.ZCard(ctx, "friend:stella:cards:working").Val()
	assert.Equal(t, int64(1), scopedGot644, "friend:stella:cards:working was modified: %d", scopedGot644)

	// Assert that no command executed during removeFriend touched sprint keys.
	require.NotEmpty(t, cmds, "expected commands to be recorded during removeFriend")
	for _, cmd := range cmds {
		for _, arg := range cmd.args {
			assertionMsg537 := []any{"removeFriend touched sprint key in command %s %v", cmd.name, cmd.args}
			func() {
				if !assert.NotContains(t, arg, "sprint", assertionMsg537...) {
					return
				}
				assert.NotContains(t, arg, "cards", assertionMsg537...)
			}()
		}
	}
}

// TestApplyRedisTripsReducedFromAuditBaseline measures round trips against the
// REDIS-TRIPS.md baseline from Rowan's audit (rowan-7fbdefecf56e), with the
// loop and route kinds' one read trip each on a store with none of their
// rows (readHashes), and the tier kind's two read trips (its flash and pro rows
// are made by migrate), added to every apply of all kinds:
//   - first run: 38 trips (32 before the tier kind, whose first apply also writes
//     its two rows and its stamp; 30 before the loop and route kinds; 42 before
//     Cuts 2, 3, 4)
//   - steady apply: 10 trips (8 before the tier kind; 6 before the loop and route
//     kinds; 18 before Cut 1)
//   - two changes: 15 trips (13 before the tier kind; 11 before the loop and route
//     kinds; 25 before Cut 2)
func TestApplyRedisTripsReducedFromAuditBaseline(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)

	trips := redisconn.CountTrips(c)

	// 1. First run: applies machine (2 adds), fleet (1 set), friend (2 adds), sprint (1 set)
	before := trips.N()
	applyKinds(t, st, ap, "rowan")
	firstRunTrips := trips.N() - before
	t.Logf("first run trips = %d (baseline was 42)", firstRunTrips)
	require.LessOrEqual(t, firstRunTrips, int64(38), "first run took %d trips, want <= 38: 30 for the four first kinds, 1 each for the loop and route kinds with no row, and 6 for the tier kind's two rows (was 42 before batching cuts)", firstRunTrips)

	// 2. Steady apply: nothing changed; Cut 1 skips the stamps (18 -> 6)
	before = trips.N()
	applyKinds(t, st, ap, "rowan")
	steadyTrips := trips.N() - before
	t.Logf("steady apply trips = %d (baseline was 18)", steadyTrips)
	require.Equal(t, int64(10), steadyTrips, "steady apply took %d trips, want 10: 6 for the four first kinds, 1 each for the loop and route kinds with no row, and 2 for the tier kind's read (was 18 before Cut 1)", steadyTrips)

	// 3. Two changes: update two friends (slots on stella, slots on rowan) (25 -> 11)
	_, _, setupErr25760 := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "24"}, "rowan")
	require.NoError(t, setupErr25760)
	_, _, setupErr25887 := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "24"}, "rowan")
	require.NoError(t, setupErr25887)
	before = trips.N()
	applyKinds(t, st, ap, "rowan")
	twoChangesTrips := trips.N() - before
	t.Logf("two changes trips = %d (baseline was 25)", twoChangesTrips)
	require.LessOrEqual(t, twoChangesTrips, int64(15), "two changes took %d trips, want <= 15: 11, the loop and route kinds' one each and the tier kind's two (was 25 before Cut 2)", twoChangesTrips)

	// 4. Machine removal: add third machine "air" to store and apply, then delete "air" and measure apply trips.
	machine, _ := Lookup(KindMachine)
	airRow, err := machine.NewRow("air", map[string]string{"user": "glenn", "seat": "air", "slots": "16"})
	require.NoError(t, err)
	_, setupErr26602 := st.Insert(ctx, KindMachine, airRow, "rowan")
	require.NoError(t, setupErr26602)
	applyKinds(t, st, ap, "rowan")

	_, setupErr26724 := st.Delete(ctx, KindMachine, "air", "rowan")
	require.NoError(t, setupErr26724)
	before = trips.N()
	applyKinds(t, st, ap, "rowan")
	machineRemovalTrips := trips.N() - before
	t.Logf("machine removal trips = %d", machineRemovalTrips)
	require.LessOrEqual(t, machineRemovalTrips, int64(17), "machine removal took %d trips, want <= 17: 15 and the loop and route kinds' one each (was 25 before Cut 5)", machineRemovalTrips)
}

// TestRefusedMachineCeilingLeavesMachineHashUntouched proves that when
// ns_capacity_machine refuses a lower ceiling, the writeMachine execution stops
// immediately and leaves machine:<m> completely byte-identical / untouched.
func TestRefusedMachineCeilingLeavesMachineHashUntouched(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")

	// Capture the exact machine:studio hash fields before the refused apply.
	beforeFields, err := c.HGetAll(ctx, MachineKey("studio")).Result()
	require.NoError(t, err, "read machine:studio before: %v", err)
	require.NotEmpty(t, beforeFields, "expected machine:studio to exist before update")

	// Try to lower studio's slots to 10 when its friends desire 64 slots.
	_, _, setupErr27920 := st.Update(ctx, KindMachine, "studio", map[string]string{"slots": "10"}, "rowan")
	require.NoError(t, setupErr27920)
	_, err = Apply(ctx, st, ap, KindMachine, "rowan", false, func(Op) {})
	assertionMsg591 := []any{"expected ErrCeiling, got %v", err}
	require.Error(t, err, assertionMsg591...)
	require.ErrorIs(t, err, ErrCeiling, assertionMsg591...)

	// Capture the machine:studio hash fields after the refused apply.
	afterFields, err := c.HGetAll(ctx, MachineKey("studio")).Result()
	require.NoError(t, err, "read machine:studio after: %v", err)

	// Assert byte-identical / untouched.
	require.Len(t, afterFields, len(beforeFields), "field count changed: before %d, after %d", len(beforeFields), len(afterFields))
	for k, v := range beforeFields {
		assert.Equal(t, v, afterFields[k], "machine:studio field %q was modified: before=%q, after=%q", k, v, afterFields[k])
	}
}

// TestApplyFriendRefusesWhenCoordinatorClearedAcrossApplies verifies that clearing
// fleet:coordinator across applies on the same RedisApplier causes an unbeat friend
// apply to refuse ErrCeiling, rather than retaining the old cached coordinator.
func TestApplyFriendRefusesWhenCoordinatorClearedAcrossApplies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")

	scopedGot768 := c.Get(ctx, FleetKey("coordinator")).Val()
	require.Equal(t, "studio", scopedGot768, "fleet:coordinator = %q, want studio", scopedGot768)

	// Clear fleet:coordinator in Redis.
	c.Del(ctx, FleetKey("coordinator"))

	// Update friend rowan's slots so an apply has an OpSet for rowan (who has no beat).
	_, _, setupErr29554 := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "20"}, "rowan")
	require.NoError(t, setupErr29554)

	// Apply friend on the same applier. It must refuse ErrCeiling because fleet coordinator is now empty.
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	assertionMsg631 := []any{"expected ErrCeiling when coordinator cleared, got %v", err}
	require.Error(t, err, assertionMsg631...)
	require.ErrorIs(t, err, ErrCeiling, assertionMsg631...)
}

// TestApplyFriendRechargesWhenBeatHostChangesAcrossApplies verifies that when a friend's
// beat moves to a new host across applies on the same RedisApplier, the friend is recharged
// to the new host rather than retaining the old cached host.
func TestApplyFriendRechargesWhenBeatHostChangesAcrossApplies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)

	// Initially stella's beat points to hulk.
	c.HSet(ctx, FriendBeatKey("stella"), "host", "hulk", "at", "1790000000000")
	applyKinds(t, st, ap, "rowan")

	scopedGot801 := c.HGet(ctx, "friend:stella:desired", "machine").Val()
	require.Equal(t, "hulk", scopedGot801, "initial stella machine = %q, want hulk", scopedGot801)

	// Delete stella from the store and apply to remove stella from the registered friends.
	_, setupErr30847 := st.Delete(ctx, KindFriend, "stella", "rowan")
	require.NoError(t, setupErr30847)
	_, setupErr30937 := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.NoError(t, setupErr30937)

	// stella's beat moves to studio.
	c.HSet(ctx, FriendBeatKey("stella"), "host", "studio", "at", "1790000001000")

	// Re-add stella to the store with 16 slots and apply.
	friendKind, ok := Lookup(KindFriend)
	require.True(t, ok, "KindFriend not found")
	row, err := friendKind.NewRow("stella", map[string]string{"slots": "16", "roles": "reader", "tiers": "frontier,pro"})
	require.NoError(t, err)
	_, setupErr31440 := st.Insert(ctx, KindFriend, row, "rowan")
	require.NoError(t, setupErr31440)

	_, setupErr31526 := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.NoError(t, setupErr31526)

	// stella must now be charged to studio, not hulk.
	scopedGot827 := c.HGet(ctx, "friend:stella:desired", "machine").Val()
	require.Equal(t, "studio", scopedGot827, "recharged stella machine = %q, want studio", scopedGot827)
}
