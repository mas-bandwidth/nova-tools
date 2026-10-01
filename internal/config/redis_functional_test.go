//go:build functional

package config

import (
	"bytes"
	"context"
	"errors"
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
	{
		err := fn.Load(context.Background(), c)
		require.NoError(t, err)
	}
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
	require.False(t, res[KindMachine].Add != 2 || res[KindFriend].Add != 2 || res[KindFleet].Set != 1 || res[KindSprint].Set != 1, "results %+v", res)

	// The machine: the ceiling ns_capacity_machine writes (slots alone:
	// cores and memory are never declared, so the ceiling carries none and
	// no budget is derived) and the registry hash nova-config owns, exactly
	// the four declared fields with the revision and time.
	{
		got := c.HGetAll(ctx, "machine:studio:ceiling").Val()
		assert.False(t, got["slots"] != "64" || got["cores"] != "" || got["mem_gb"] != "" || got["at"] == "", "machine:studio:ceiling %v", got)
	}
	assert.False(t, c.Exists(ctx, "machine:studio:budget").Val() != 0, "a budget was derived from cores nobody declared")
	{
		got := c.HGetAll(ctx, "machine:studio").Val()
		assert.False(t, got["user"] != "glenn" || got["seat"] != "studio" || got["slots"] != "64" || got["runners"] != "1" || got["rev"] != "2" || got["at"] == "" || len(got) != 6, "machine:studio %v: want user, seat, slots, runners, rev, at and nothing else", got)
	}
	{
		got := c.HGetAll(ctx, "machine:hulk").Val()
		assert.False(t, got["user"] != "gaffer" || got["seat"] != "swarm-hulk" || got["runners"] != "0", "machine:hulk %v", got)
	}
	{
		members := c.SMembers(ctx, MachinesKey).Val()
		assert.False(t, len(members) != 2, "machines %v", members)
	}
	// The fleet and the sprint: one plain key per named field.
	if got := c.Get(ctx, FleetKey("coordinator")).Val(); got != "studio" || c.Exists(ctx, FleetKey("store")).Val() != 0 {
		assert.Failf(t, "fleet view mismatch", "fleet:coordinator %q, fleet:store exists=%d", got, c.Exists(ctx, FleetKey("store")).Val())
	}
	{
		got := c.Get(ctx, SprintKey("coordinator")).Val()
		assert.False(t, got != "rowan", "sprint:coordinator %q", got)
	}

	// The friend: what capacity friend and friend roles would have left,
	// and nothing her own presence writes.
	assert.False(t, !c.SIsMember(ctx, FriendsKey, "rowan").Val() || !c.SIsMember(ctx, FriendsKey, "stella").Val(), "friends registry lacks rowan or stella")
	{
		got := c.HGetAll(ctx, "friend:rowan:desired").Val()
		assert.False(t, got["slots"] != "32" || got["machine"] != "studio" || got["tiers"] != "frontier" || got["paused"] != "0" || got["at"] == "", "friend:rowan:desired %v (charged to the coordinator machine: no beat)", got)
	}
	{
		got := c.HGetAll(ctx, "friend:stella:desired").Val()
		assert.False(t, got["slots"] != "32" || got["machine"] != "hulk" || got["tiers"] != "frontier,pro", "friend:stella:desired %v (charged to the host her beat reports)", got)
	}
	{
		got := c.HGetAll(ctx, "friend:rowan:roles").Val()
		assert.False(t, got["roles"] != "builder,coordinator" || got["by"] != "rowan", "friend:rowan:roles %v (the sprint's coordinator carries the role)", got)
	}
	{
		got := c.HGetAll(ctx, "friend:stella:roles").Val()
		assert.False(t, got["roles"] != "builder,reader", "friend:stella:roles %v", got)
	}
	for _, key := range []string{"friend:rowan:wakepath", "friend:stella:wakepath", "friends:login", "friend:rowan:config"} {
		assert.False(t, c.Exists(ctx, key).Val() != 0, "%s was written: it is the friend's own presence's, not configuration", key)
	}
	{
		got := c.HGetAll(ctx, DeclKey).Val()
		assert.False(t, got["rev:friend"] != "4" || got["rev:machine"] != "2" || got["rev:fleet"] != "5" || got["rev:sprint"] != "6" || got["at:friend"] == "", "config:decl %v", got)
	}
	// The receipts every capacity write leaves.
	{
		n := c.XLen(ctx, CapLogKey).Val()
		assert.False(t, n < 4, "cap:log has %d receipts, want one per desired and ceiling write at least", n)
	}

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
				assert.False(t, views[row.Name][f] != want, "view of %s %s.%s = %q, row has %q", kind, row.Name, f, views[row.Name][f], want)
			}
		}
	}
	{
		_, rev, err := ap.Read(ctx, KindFriend)
		require.False(t, err != nil || rev != 4, "read friends: rev %d err %v", rev, err)
	}
	res = applyKinds(t, st, ap, "rowan")
	{
		r := res[KindFriend]
		require.False(t, r.Add+r.Set+r.Remove != 0 || r.Rev != 4 || r.RedisRev != 4, "second apply %+v", r)
	}
	{
		got := c.HGet(ctx, DeclKey, "rev:friend").Val()
		require.False(t, got != "4", "stamp after a no-op apply %s", got)
	}
}

func TestApplySetsAndRemovesAFriend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")

	// A set in Postgres: slots, tiers and a role.
	{
		_, _, err := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "30", "tiers": "flash", "roles": "reader"}, "rowan")
		require.NoError(t, err)
	}
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.False(t, err != nil || res.Set != 1, "apply after set: %+v %v", res, err)
	{
		got := c.HGetAll(ctx, "friend:stella:desired").Val()
		assert.False(t, got["slots"] != "30" || got["tiers"] != "flash", "stella desired %v", got)
	}
	{
		got := c.HGet(ctx, "friend:stella:roles", "roles").Val()
		assert.False(t, got != "reader", "stella roles %q", got)
	}

	// The handover: the sprint names stella; the next friend apply gives
	// her the role first and takes it from rowan, as rowan (who holds it).
	{
		_, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "stella"}, "rowan")
		require.NoError(t, err)
	}
	var reported []string
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(op Op) { reported = append(reported, op.Name) })
	require.False(t, err != nil || res.Set != 2 || strings.Join(reported, " ") != "stella rowan", "handover: %+v %v reported %v", res, err, reported)
	{
		got := c.HGet(ctx, "friend:stella:roles", "roles").Val()
		assert.False(t, got != "coordinator,reader", "stella roles after the handover %q", got)
	}
	{
		got := c.HGet(ctx, "friend:rowan:roles", "roles").Val()
		assert.False(t, got != "builder", "rowan roles after the handover %q", got)
	}

	// A remove takes what apply wrote, leaving her presence's own keys
	// behind; removing a friend does not read sprint keys and succeeds
	// even if working copies exist.
	{
		_, _, err := st.Update(ctx, KindSprint, KindSprint, map[string]string{"coordinator": "rowan"}, "stella")
		require.NoError(t, err)
	}
	{
		_, err := Apply(ctx, st, ap, KindFriend, "stella", false, func(Op) {})
		require.NoError(t, err, "handover back as stella: %v", err)
	}
	{
		_, err := st.Delete(ctx, KindFriend, "stella", "rowan")
		require.NoError(t, err)
	}
	c.HSet(ctx, "friend:stella:wakepath", "kind", "human", "notify", "#stella")
	c.ZAdd(ctx, "friend:stella:cards:working", redis.Z{Score: 1, Member: "card:4410"}, redis.Z{Score: 2, Member: "card:4414"})
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.False(t, err != nil || res.Remove != 1, "remove: %+v %v", res, err)
	for _, key := range []string{"friend:stella:desired", "friend:stella:roles"} {
		assert.False(t, c.Exists(ctx, key).Val() != 0, "%s survived the remove", key)
	}
	assert.False(t, c.Exists(ctx, "friend:stella:wakepath").Val() != 1, "the remove took her wake path, which is her presence's own")
	assert.False(t, c.SIsMember(ctx, FriendsKey, "stella").Val(), "stella is still in friends")
	{
		got := c.HGet(ctx, DeclKey, "rev:friend").Val()
		require.False(t, got != "10", "stamp after remove %s", got)
	}
	{
		got := c.ZCard(ctx, "friend:stella:cards:working").Val()
		require.False(t, got != 2, "working copies were modified: %d", got)
	}
}

// TestApplyRefusesAFriendNobodyCanCharge: a friend with no beat and no
// coordinator machine in the fleet row cannot be charged to a ceiling; the
// refusal names the fleet set that fixes it and nothing is written.
func TestApplyRefusesAFriendNobodyCanCharge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	{
		_, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": ""}, "rowan")
		require.NoError(t, err)
	}
	for _, kind := range []string{KindMachine, KindFleet} {
		{
			_, err := Apply(ctx, st, ap, kind, "rowan", false, func(Op) {})
			require.NoError(t, err)
		}
	}
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.False(t, err == nil || !Refused(err) || !strings.Contains(err.Error(), "friend rowan has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>"), "nobody to charge: %v", err)
	require.False(t, c.SIsMember(ctx, FriendsKey, "rowan").Val(), "a refused friend was registered")
}

// TestApplyWritesTheFleetKeysAndTheLiveFactsAreTheBeat: the fleet row is
// two plain keys of nova-config's own, set or deleted as the row says; a
// machine's measured facts are read from its beat and never written.
func TestApplyWritesTheFleetKeysAndTheLiveFactsAreTheBeat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	{
		_, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": "hulk"}, "rowan")
		require.NoError(t, err)
	}
	res, err := Apply(ctx, st, ap, KindFleet, "rowan", false, func(Op) {})
	require.False(t, err != nil || res.Set != 1 || res.Rev != 7, "apply the fleet: %+v %v", res, err)
	{
		got := c.Get(ctx, FleetKey("store")).Val()
		assert.False(t, got != "hulk", "fleet:store %q", got)
	}
	{
		got := c.Get(ctx, FleetKey("coordinator")).Val()
		assert.False(t, got != "studio", "fleet:coordinator %q", got)
	}
	{
		got := c.HGet(ctx, DeclKey, "rev:fleet").Val()
		assert.False(t, got != "7", "rev:fleet %s", got)
	}
	// Clearing a field deletes its key.
	{
		_, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"store": ""}, "rowan")
		require.NoError(t, err)
	}
	res, err = Apply(ctx, st, ap, KindFleet, "rowan", false, func(Op) {})
	require.False(t, err != nil || res.Set != 1, "apply the cleared store: %+v %v", res, err)
	assert.False(t, c.Exists(ctx, FleetKey("store")).Val() != 0 || c.Get(ctx, FleetKey("coordinator")).Val() != "studio", "clearing the store did not delete fleet:store, or took the coordinator with it")

	// The beats: hulk has beaten (today's fields), studio never has.
	c.HSet(ctx, BeatKey("hulk"), "host", "hulk", "at", "1790000000000", "load1", "0.5", "ncpu", "64", "cpu", "3")
	beats, err := ap.Beats(ctx, []string{"hulk", "studio"})
	require.NoError(t, err)
	{
		b := beats["hulk"]
		assert.False(t, b == nil || b.Cores != "64" || b.OS != "" || b.Arch != "" || b.MemoryGB != "" || b.At != "2026-09-21T14:13:20Z", "hulk's beat %+v", b)
	}
	assert.False(t, beats["studio"] != nil, "studio has no beat and one was read: %+v", beats["studio"])
	// The follow-on fields are read when the beat carries them.
	c.HSet(ctx, BeatKey("hulk"), "os", "linux", "arch", "amd64", "memory_gb", "251")
	beats, _ = ap.Beats(ctx, []string{"hulk"})
	{
		b := beats["hulk"]
		assert.False(t, b.OS != "linux" || b.Arch != "amd64" || b.MemoryGB != "251", "hulk's fuller beat %+v", b)
	}
	assert.False(t, c.Exists(ctx, BeatKey("hulk")).Val() != 1 || c.HGet(ctx, "machine:hulk", "os").Val() != "", "reading a beat wrote something")
}

func TestApplyRefusesConflictWhenRedisIsAheadOfPostgres(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	c.HSet(ctx, DeclKey, "rev:friend", "40")
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.False(t, err == nil || !IsConflict(err), "Redis ahead: %v", err)
	require.False(t, !strings.Contains(err.Error(), "Redis holds rev 40"), "conflict %q", err)
	// The stamp is compare-and-set: one that moves between the read and
	// the stamp is refused too.
	c.HSet(ctx, DeclKey, "rev:friend", "4")
	{
		err := ap.Stamp(ctx, KindFriend, 3, 5)
		require.False(t, err == nil || !IsConflict(err), "stamp with a moved prev: %v", err)
	}
	{
		err := ap.Stamp(ctx, KindFriend, 4, 5)
		require.NoError(t, err, "stamp with the right prev: %v", err)
	}
	{
		got := c.HGet(ctx, DeclKey, "rev:friend").Val()
		require.False(t, got != "5", "stamp %s", got)
	}
}

func TestApplyRefusesACeilingAndAMachineInUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, _ := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	// stella 32 + rowan 32 = 64 on studio; one more slot is over the
	// ceiling, refused by ns_capacity_desired, and named with the remedy.
	{
		_, _, err := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "33"}, "rowan")
		require.NoError(t, err)
	}
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.False(t, err == nil || !errors.Is(err, ErrCeiling), "over the ceiling: %v", err)
	require.False(t, !strings.Contains(err.Error(), "CEILING studio: friend stella makes the sum 65 over the machine ceiling 64"), "ceiling refusal %q", err)
	// A machine's ceiling below its friends' sum is refused by
	// ns_capacity_machine.
	{
		_, _, err := st.Update(ctx, KindMachine, "studio", map[string]string{"slots": "10"}, "rowan")
		require.NoError(t, err)
	}
	_, err = Apply(ctx, st, ap, KindMachine, "rowan", false, func(Op) {})
	require.False(t, err == nil || !errors.Is(err, ErrCeiling) || !strings.Contains(err.Error(), "CEILING studio: its friends desire 64 slots and the row says 10"), "ceiling below the sum: %v", err)
	// A machine that Redis still has consumers on cannot be removed (here
	// the Redis side is exercised directly).
	err = ap.Remove(ctx, KindMachine, "studio", "rowan", "test")
	require.False(t, err == nil || !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "machine studio still carries friend:rowan,friend:stella in Redis"), "remove a machine in use: %v", err)
}

func TestApplyNeedsTheCoordinatorRoleForRoles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, _ := redisApplier(t)
	st := seed(t)
	// The actor is not a friend at all: the first roles write refuses.
	var reported bytes.Buffer
	for _, kind := range []string{KindMachine, KindFleet} {
		{
			_, err := Apply(ctx, st, ap, kind, "nobody", false, func(Op) {})
			require.NoError(t, err)
		}
	}
	_, err := Apply(ctx, st, ap, KindFriend, "nobody", false, func(op Op) { reported.WriteString(op.Name + " ") })
	require.False(t, err == nil || !errors.Is(err, ErrActor) || !strings.Contains(err.Error(), "--as nobody is not a registered friend"), "roles as nobody: %v", err)
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

	{
		err := ap.Remove(ctx, KindFriend, "stella", "rowan", "idem-remove-1")
		require.NoError(t, err, "removeFriend failed: %v", err)
	}

	mu.Lock()
	logging = false
	cmds := make([]loggedCmd, len(recorded))
	copy(cmds, recorded)
	mu.Unlock()

	// Friend keys apply wrote are removed.
	assert.False(t, c.SIsMember(ctx, FriendsKey, "stella").Val(), "stella still in friends set")
	assert.False(t, c.Exists(ctx, "friend:stella:desired").Val() != 0, "friend:stella:desired still exists")
	assert.False(t, c.Exists(ctx, "friend:stella:roles").Val() != 0, "friend:stella:roles still exists")

	// Sprint keys were untouched.
	{
		got := c.Get(ctx, "sprint:epoch").Val()
		assert.False(t, got != "not-a-hash", "sprint:epoch was modified: %q", got)
	}
	{
		got := c.ZCard(ctx, "friend:stella:cards:working").Val()
		assert.False(t, got != 1, "friend:stella:cards:working was modified: %d", got)
	}

	// Assert that no command executed during removeFriend touched sprint keys.
	require.False(t, len(cmds) == 0, "expected commands to be recorded during removeFriend")
	for _, cmd := range cmds {
		for _, arg := range cmd.args {
			assert.False(t, strings.Contains(arg, "sprint") || strings.Contains(arg, "cards"), "removeFriend touched sprint key in command %s %v", cmd.name, cmd.args)
		}
	}
}

// TestApplyRedisTripsReducedFromAuditBaseline measures round trips against the
// REDIS-TRIPS.md baseline from Rowan's audit (rowan-7fbdefecf56e), with the
// loop kind's one read trip on a store with no loop (readLoops) added to
// every apply of all kinds:
// - first run: 31 trips (30 before the loop kind; 42 before Cuts 2, 3, 4)
// - steady apply: 7 trips (6 before the loop kind; 18 before Cut 1)
// - two changes: 12 trips (11 before the loop kind; 25 before Cut 2)
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
	require.False(t, firstRunTrips > 31, "first run took %d trips, want <= 31: 30 for the four first kinds and 1 for the loop kind with no loop (was 42 before batching cuts)", firstRunTrips)

	// 2. Steady apply: nothing changed; Cut 1 skips the stamps (18 -> 6)
	before = trips.N()
	applyKinds(t, st, ap, "rowan")
	steadyTrips := trips.N() - before
	t.Logf("steady apply trips = %d (baseline was 18)", steadyTrips)
	require.False(t, steadyTrips != 7, "steady apply took %d trips, want 7: 6 for the four first kinds and 1 for the loop kind with no loop (was 18 before Cut 1)", steadyTrips)

	// 3. Two changes: update two friends (slots on stella, slots on rowan) (25 -> 11)
	{
		_, _, err := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "24"}, "rowan")
		require.NoError(t, err)
	}
	{
		_, _, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "24"}, "rowan")
		require.NoError(t, err)
	}
	before = trips.N()
	applyKinds(t, st, ap, "rowan")
	twoChangesTrips := trips.N() - before
	t.Logf("two changes trips = %d (baseline was 25)", twoChangesTrips)
	require.False(t, twoChangesTrips > 12, "two changes took %d trips, want <= 12: 11 and the loop kind's one (was 25 before Cut 2)", twoChangesTrips)

	// 4. Machine removal: add third machine "air" to store and apply, then delete "air" and measure apply trips.
	machine, _ := Lookup(KindMachine)
	airRow, err := machine.NewRow("air", map[string]string{"user": "glenn", "seat": "air", "slots": "16"})
	require.NoError(t, err)
	{
		_, err := st.Insert(ctx, KindMachine, airRow, "rowan")
		require.NoError(t, err)
	}
	applyKinds(t, st, ap, "rowan")

	{
		_, err := st.Delete(ctx, KindMachine, "air", "rowan")
		require.NoError(t, err)
	}
	before = trips.N()
	applyKinds(t, st, ap, "rowan")
	machineRemovalTrips := trips.N() - before
	t.Logf("machine removal trips = %d", machineRemovalTrips)
	require.False(t, machineRemovalTrips > 16, "machine removal took %d trips, want <= 16: 15 and the loop kind's one (was 25 before Cut 5)", machineRemovalTrips)
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
	require.False(t, len(beforeFields) == 0, "expected machine:studio to exist before update")

	// Try to lower studio's slots to 10 when its friends desire 64 slots.
	{
		_, _, err := st.Update(ctx, KindMachine, "studio", map[string]string{"slots": "10"}, "rowan")
		require.NoError(t, err)
	}
	_, err = Apply(ctx, st, ap, KindMachine, "rowan", false, func(Op) {})
	require.False(t, err == nil || !errors.Is(err, ErrCeiling), "expected ErrCeiling, got %v", err)

	// Capture the machine:studio hash fields after the refused apply.
	afterFields, err := c.HGetAll(ctx, MachineKey("studio")).Result()
	require.NoError(t, err, "read machine:studio after: %v", err)

	// Assert byte-identical / untouched.
	require.False(t, len(beforeFields) != len(afterFields), "field count changed: before %d, after %d", len(beforeFields), len(afterFields))
	for k, v := range beforeFields {
		assert.False(t, afterFields[k] != v, "machine:studio field %q was modified: before=%q, after=%q", k, v, afterFields[k])
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

	{
		got := c.Get(ctx, FleetKey("coordinator")).Val()
		require.False(t, got != "studio", "fleet:coordinator = %q, want studio", got)
	}

	// Clear fleet:coordinator in Redis.
	c.Del(ctx, FleetKey("coordinator"))

	// Update friend rowan's slots so an apply has an OpSet for rowan (who has no beat).
	{
		_, _, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "20"}, "rowan")
		require.NoError(t, err)
	}

	// Apply friend on the same applier. It must refuse ErrCeiling because fleet coordinator is now empty.
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	require.False(t, err == nil || !errors.Is(err, ErrCeiling), "expected ErrCeiling when coordinator cleared, got %v", err)
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

	{
		got := c.HGet(ctx, "friend:stella:desired", "machine").Val()
		require.False(t, got != "hulk", "initial stella machine = %q, want hulk", got)
	}

	// Delete stella from the store and apply to remove stella from the registered friends.
	{
		_, err := st.Delete(ctx, KindFriend, "stella", "rowan")
		require.NoError(t, err)
	}
	{
		_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
		require.NoError(t, err)
	}

	// stella's beat moves to studio.
	c.HSet(ctx, FriendBeatKey("stella"), "host", "studio", "at", "1790000001000")

	// Re-add stella to the store with 16 slots and apply.
	friendKind, ok := Lookup(KindFriend)
	require.True(t, ok, "KindFriend not found")
	row, err := friendKind.NewRow("stella", map[string]string{"slots": "16", "roles": "reader", "tiers": "frontier,pro"})
	require.NoError(t, err)
	{
		_, err := st.Insert(ctx, KindFriend, row, "rowan")
		require.NoError(t, err)
	}

	{
		_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
		require.NoError(t, err)
	}

	// stella must now be charged to studio, not hulk.
	{
		got := c.HGet(ctx, "friend:stella:desired", "machine").Val()
		require.False(t, got != "studio", "recharged stella machine = %q, want studio", got)
	}
}
