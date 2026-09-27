//go:build functional

package config

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// redisApplier starts a throwaway Redis with the nova_sprint library and
// returns the applier over it and the raw client.
func redisApplier(t *testing.T) (*RedisApplier, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return &RedisApplier{Client: c}, c
}

// applyKinds applies machines then friends from st into ap.
func applyKinds(t *testing.T, st Store, ap Applier, actor string) map[string]Result {
	t.Helper()
	out := map[string]Result{}
	for _, kind := range KindNames() {
		res, err := Apply(context.Background(), st, ap, kind, actor, false, func(Op) {})
		if err != nil {
			t.Fatalf("apply %s: %v", kind, err)
		}
		out[kind] = res
	}
	return out
}

// TestApplyLeavesTheKeysCapacityFriendWould: after apply, Redis holds for a
// friend exactly what `nova-sprint capacity friend`, `friend roles`,
// `capacity friend --wake` and a hello's --login would have left: the
// registry member, the desired hash under the ceiling, the roles hash, the
// wakepath hash and the login map, plus nova-config's own config hash and
// the stamp in config:decl.
func TestApplyLeavesTheKeysCapacityFriendWould(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	res := applyKinds(t, st, ap, "rowan")
	if res[KindMachine].Add != 2 || res[KindFriend].Add != 2 {
		t.Fatalf("results %+v", res)
	}

	// The machine: the ceiling ns_capacity_machine writes and the registry
	// hash nova-config owns.
	if got := c.HGetAll(ctx, "machine:studio:ceiling").Val(); got["slots"] != "64" || got["cores"] != "32" || got["at"] == "" {
		t.Errorf("machine:studio:ceiling %v", got)
	}
	if got := c.HGetAll(ctx, "machine:hulk:ceiling").Val(); got["slots"] != "64" || got["cores"] != "" {
		t.Errorf("machine:hulk:ceiling %v (cores 0 writes no cores)", got)
	}
	if got := c.HGetAll(ctx, "machine:studio").Val(); got["ssh"] != "studio" || got["os_arch"] != "darwin/arm64" || got["rev"] != "2" || got["at"] == "" {
		t.Errorf("machine:studio %v", got)
	}
	if members := c.SMembers(ctx, MachinesKey).Val(); len(members) != 2 {
		t.Errorf("machines %v", members)
	}

	// The friend: what the four nova-sprint verbs would have left.
	if !c.SIsMember(ctx, FriendsKey, "rowan").Val() || !c.SIsMember(ctx, FriendsKey, "stella").Val() {
		t.Error("friends registry lacks rowan or stella")
	}
	if got := c.HGetAll(ctx, "friend:rowan:desired").Val(); got["slots"] != "32" || got["machine"] != "studio" || got["paused"] != "0" || got["at"] == "" {
		t.Errorf("friend:rowan:desired %v", got)
	}
	if got := c.HGetAll(ctx, "friend:rowan:roles").Val(); got["roles"] != "builder,coordinator" || got["by"] != "rowan" {
		t.Errorf("friend:rowan:roles %v", got)
	}
	if got := c.HGetAll(ctx, "friend:stella:roles").Val(); got["roles"] != "builder,reader" {
		t.Errorf("friend:stella:roles %v", got)
	}
	if got := c.HGetAll(ctx, "friend:rowan:wakepath").Val(); got["kind"] != "unit" || got["unit"] != "rowan" || got["host"] != "studio" || got["notify"] != "" || got["declared_at"] == "" {
		t.Errorf("friend:rowan:wakepath %v", got)
	}
	if c.Exists(ctx, "friend:stella:wakepath").Val() != 0 {
		t.Error("stella has no wake path and a wakepath hash was written")
	}
	if got := c.HGetAll(ctx, LoginsKey).Val(); len(got) != 1 || got["rowan-claude"] != "rowan" {
		t.Errorf("friends:login %v", got)
	}
	if got := c.HGetAll(ctx, FriendConfigKey("rowan")).Val(); got["harness"] != "" || got["note"] != "" || got["rev"] != "4" || got["at"] == "" {
		t.Errorf("friend:rowan:config %v", got)
	}
	if got := c.HGetAll(ctx, DeclKey).Val(); got["rev:friend"] != "4" || got["rev:machine"] != "2" || got["at:friend"] == "" {
		t.Errorf("config:decl %v", got)
	}
	// The receipts every capacity write leaves.
	if n := c.XLen(ctx, CapLogKey).Val(); n < 4 {
		t.Errorf("cap:log has %d receipts, want one per desired and ceiling write at least", n)
	}

	// Read reads back exactly the views the rows are, so a second apply is
	// a no-op and the stamp is unchanged.
	views, rev, err := ap.Read(ctx, KindFriend)
	if err != nil || rev != 4 {
		t.Fatalf("read friends: rev %d err %v", rev, err)
	}
	rows, _ := st.List(ctx, KindFriend)
	for _, row := range rows {
		for f, want := range row.Fields {
			if views[row.Name][f] != want {
				t.Errorf("view of %s.%s = %q, row has %q", row.Name, f, views[row.Name][f], want)
			}
		}
	}
	res = applyKinds(t, st, ap, "rowan")
	if r := res[KindFriend]; r.Add+r.Set+r.Remove != 0 || r.Rev != 4 || r.RedisRev != 4 {
		t.Fatalf("second apply %+v", r)
	}
	if got := c.HGet(ctx, DeclKey, "rev:friend").Val(); got != "4" {
		t.Fatalf("stamp after a no-op apply %s", got)
	}
}

func TestApplySetsAndRemovesAFriend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")

	// A set in Postgres: slots, a new login, the wake path cleared, a note.
	if _, _, err := st.Update(ctx, KindFriend, "rowan", map[string]string{"slots": "30", "logins": "rowan-claude,rowan-bot", "wake": "", "note": "wider"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err != nil || res.Set != 1 {
		t.Fatalf("apply after set: %+v %v", res, err)
	}
	if got := c.HGet(ctx, "friend:rowan:desired", "slots").Val(); got != "30" {
		t.Errorf("slots %s", got)
	}
	if got := c.HGetAll(ctx, LoginsKey).Val(); got["rowan-bot"] != "rowan" || got["rowan-claude"] != "rowan" {
		t.Errorf("logins %v", got)
	}
	if c.Exists(ctx, "friend:rowan:wakepath").Val() != 0 {
		t.Error("a cleared wake path left its hash")
	}
	if got := c.HGet(ctx, FriendConfigKey("rowan"), "note").Val(); got != "wider" {
		t.Errorf("note %s", got)
	}

	// A remove is refused while the friend holds a working copy, naming it,
	// and nothing is written; the stamp stays behind.
	if _, err := st.Delete(ctx, KindFriend, "stella", "rowan"); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "friend:stella:cards:working", "card:4410", "card:4414")
	_, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err == nil || !errors.Is(err, ErrWorking) || !Refused(err) {
		t.Fatalf("remove with working copies: %v", err)
	}
	if !strings.Contains(err.Error(), "friend stella holds 2 working copies (card:4410,card:4414)") {
		t.Fatalf("refusal %q does not name the copies", err)
	}
	if !c.SIsMember(ctx, FriendsKey, "stella").Val() || c.Exists(ctx, "friend:stella:desired").Val() != 1 {
		t.Fatal("a refused remove removed stella")
	}
	if got := c.HGet(ctx, DeclKey, "rev:friend").Val(); got != "5" {
		t.Fatalf("stamp after a refused apply %s, want the previous 5", got)
	}
	// The copies gone, the remove goes through and every key with it.
	c.Del(ctx, "friend:stella:cards:working")
	res, err = Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err != nil || res.Remove != 1 {
		t.Fatalf("remove: %+v %v", res, err)
	}
	for _, key := range []string{"friend:stella:desired", "friend:stella:roles", "friend:stella:wakepath", FriendConfigKey("stella")} {
		if c.Exists(ctx, key).Val() != 0 {
			t.Errorf("%s survived the remove", key)
		}
	}
	if c.SIsMember(ctx, FriendsKey, "stella").Val() {
		t.Error("stella is still in friends")
	}
	if got := c.HGet(ctx, DeclKey, "rev:friend").Val(); got != "6" {
		t.Fatalf("stamp after remove %s", got)
	}
}

func TestApplyRefusesConflictWhenRedisIsAheadOfPostgres(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	applyKinds(t, st, ap, "rowan")
	c.HSet(ctx, DeclKey, "rev:friend", "40")
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err == nil || !IsConflict(err) {
		t.Fatalf("Redis ahead: %v", err)
	}
	if !strings.Contains(err.Error(), "Redis holds rev 40") {
		t.Fatalf("conflict %q", err)
	}
	// The stamp is compare-and-set: one that moves between the read and
	// the stamp is refused too.
	c.HSet(ctx, DeclKey, "rev:friend", "4")
	if err := ap.Stamp(ctx, KindFriend, 3, 5); err == nil || !IsConflict(err) {
		t.Fatalf("stamp with a moved prev: %v", err)
	}
	if err := ap.Stamp(ctx, KindFriend, 4, 5); err != nil {
		t.Fatalf("stamp with the right prev: %v", err)
	}
	if got := c.HGet(ctx, DeclKey, "rev:friend").Val(); got != "5" {
		t.Fatalf("stamp %s", got)
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
	if _, _, err := st.Update(ctx, KindFriend, "stella", map[string]string{"slots": "33"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(ctx, st, ap, KindFriend, "rowan", false, func(Op) {})
	if err == nil || !errors.Is(err, ErrCeiling) {
		t.Fatalf("over the ceiling: %v", err)
	}
	if !strings.Contains(err.Error(), "CEILING studio: friend stella makes the sum 65 over the machine ceiling 64") {
		t.Fatalf("ceiling refusal %q", err)
	}
	// A machine's ceiling below its friends' sum is refused by
	// ns_capacity_machine.
	if _, _, err := st.Update(ctx, KindMachine, "studio", map[string]string{"slots": "10"}, "rowan"); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, st, ap, KindMachine, "rowan", false, func(Op) {})
	if err == nil || !errors.Is(err, ErrCeiling) || !strings.Contains(err.Error(), "CEILING studio: its friends and benches desire 64 slots and the row says 10") {
		t.Fatalf("ceiling below the sum: %v", err)
	}
	// A machine that Redis still has consumers on cannot be removed
	// (Postgres refuses the row's removal first; here the Redis side is
	// exercised directly).
	err = ap.Remove(ctx, KindMachine, "studio", "rowan", "test")
	if err == nil || !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "machine studio still carries friend:rowan,friend:stella in Redis") {
		t.Fatalf("remove a machine in use: %v", err)
	}
}

func TestApplyNeedsTheCoordinatorRoleForRoles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, _ := redisApplier(t)
	st := seed(t)
	// The actor is not a friend at all: the first roles write refuses.
	var reported bytes.Buffer
	_, err := Apply(ctx, st, ap, KindMachine, "nobody", false, func(Op) {})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(ctx, st, ap, KindFriend, "nobody", false, func(op Op) { reported.WriteString(op.Name + " ") })
	if err == nil || !errors.Is(err, ErrActor) || !strings.Contains(err.Error(), "--as nobody is not a registered friend") {
		t.Fatalf("roles as nobody: %v", err)
	}
}
