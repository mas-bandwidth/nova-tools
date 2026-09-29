//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil/pg"
	"github.com/redis/go-redis/v9"
)

// One throwaway Postgres for the package, one database per test; one
// throwaway Redis per test that applies (docs/TESTING.md: frugal).
var server *pg.Server

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nova-config-pg-")
	if err != nil {
		panic(err)
	}
	server, err = pg.StartServer(dir)
	if err != nil {
		os.Stderr.WriteString("throwaway postgres: " + err.Error() + "\n")
		os.Exit(2)
	}
	code := m.Run()
	_ = server.Stop()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// real is the tool against a fresh database (and a throwaway Redis when
// withRedis), through the same entry point main uses, with the
// environment handed in rather than set on the process.
type real struct {
	env    map[string]string
	client *redis.Client
}

func newReal(t *testing.T, withRedis bool) *real {
	t.Helper()
	r := &real{env: map[string]string{"NOVA_PG_DSN": server.Database(t), "NOVA_FRIEND": "rowan"}}
	if withRedis {
		addr := testutil.Start(t)
		r.client = redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = r.client.Close() })
		if err := fn.Load(context.Background(), r.client); err != nil {
			t.Fatal(err)
		}
		r.env["NOVA_SPRINT_REDIS"] = addr
	}
	return r
}

func (r *real) deps() deps {
	d := realDeps()
	d.getenv = func(k string) string { return r.env[k] }
	return d
}

func (r *real) run(t *testing.T, want int, args ...string) (string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, r.deps())
	if code != want {
		t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out.String(), errb.String())
	}
	return out.String(), errb.String()
}

func TestMigrateTwiceThenTheSixVerbs(t *testing.T) {
	t.Parallel()

	r := newReal(t, false)
	out, errs := r.run(t, 1, "status")
	if !strings.Contains(out, " schema=0 ") || !strings.Contains(errs, "run: nova-config migrate") {
		t.Fatalf("status before migrate: %q %q", out, errs)
	}
	out, _ = r.run(t, 0, "migrate")
	if !strings.HasPrefix(out, "CONFIG MIGRATE pg=postgres@127.0.0.1:") || !strings.HasSuffix(out, " from=0 to=6 applied=6\n") {
		t.Fatalf("migrate: %q", out)
	}
	out, _ = r.run(t, 0, "migrate")
	if !strings.HasSuffix(out, " from=6 to=6 applied=0\n") {
		t.Fatalf("migrate twice: %q", out)
	}
	out, _ = r.run(t, 0, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64", "--runners", "1")
	if out != "CONFIG ADD kind=machine name=studio rev=1\n" {
		t.Fatalf("machine add: %q", out)
	}
	_, errs = r.run(t, 1, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64")
	if errs != "nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>\n" {
		t.Fatalf("duplicate machine: %q", errs)
	}
	out, _ = r.run(t, 0, "friend", "add", "rowan", "--slots", "32", "--tiers", "frontier,pro", "--roles", "builder")
	if out != "CONFIG ADD kind=friend name=rowan rev=2\n" {
		t.Fatalf("friend add: %q", out)
	}
	_, errs = r.run(t, 1, "friend", "add", "rowan", "--slots", "32", "--tiers", "frontier")
	if errs != "nova-config friend add: friend rowan exists; run: nova-config friend set rowan --<field> <value>\n" {
		t.Fatalf("duplicate friend: %q", errs)
	}
	out, _ = r.run(t, 0, "friend", "set", "rowan", "--slots", "64", "--roles", "")
	if out != "CONFIG SET kind=friend name=rowan rev=3 changed=roles,slots\n" {
		t.Fatalf("friend set: %q", out)
	}
	out, _ = r.run(t, 0, "friend", "list")
	if out != "FRIEND name=rowan slots=64 tiers=frontier,pro roles=-\nCONFIG LIST kind=friend rows=1\n" {
		t.Fatalf("friend list: %q", out)
	}
	out, _ = r.run(t, 0, "machine", "show", "studio")
	if !strings.HasPrefix(out, "MACHINE name=studio user=glenn seat=studio slots=64 runners=1 tiers=- created=") {
		t.Fatalf("machine show: %q", out)
	}
	// The fleet row: there since migrate, set without a name, a machine it
	// names cannot be removed.
	out, _ = r.run(t, 0, "fleet", "show")
	if !strings.HasPrefix(out, "FLEET name=fleet store=- coordinator=- created=") {
		t.Fatalf("fleet show: %q", out)
	}
	_, errs = r.run(t, 1, "fleet", "set", "--store", "space")
	if errs != "nova-config fleet set: --store space names no machine row; run: nova-config fleet show\n" {
		t.Fatalf("fleet set naming no machine: %q", errs)
	}
	out, _ = r.run(t, 0, "fleet", "set", "--coordinator", "studio")
	if out != "CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator\n" {
		t.Fatalf("fleet set: %q", out)
	}
	// The sprint row: who coordinates; the friend it names cannot go.
	_, errs = r.run(t, 1, "sprint", "set", "--coordinator", "nobody")
	if errs != "nova-config sprint set: --coordinator nobody names no friend row; run: nova-config sprint show\n" {
		t.Fatalf("sprint set naming no friend: %q", errs)
	}
	out, _ = r.run(t, 0, "sprint", "set", "--coordinator", "rowan")
	if out != "CONFIG SET kind=sprint name=sprint rev=5 changed=coordinator\n" {
		t.Fatalf("sprint set: %q", out)
	}
	_, errs = r.run(t, 1, "friend", "remove", "rowan")
	if errs != "nova-config friend remove: friend rowan is the --coordinator of the sprint; run: nova-config friend list\n" {
		t.Fatalf("remove the coordinating friend: %q", errs)
	}
	r.run(t, 0, "sprint", "set", "--coordinator", "")
	out, _ = r.run(t, 0, "friend", "history", "rowan")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "HISTORY id=2 kind=friend name=rowan op=add actor=rowan at=") || !strings.HasSuffix(lines[1], " roles=builder>- slots=32>64") || lines[2] != "CONFIG HISTORY kind=friend name=rowan changes=2" {
		t.Fatalf("history:\n%s", out)
	}
	out, _ = r.run(t, 0, "friend", "remove", "rowan")
	if out != "CONFIG REMOVE kind=friend name=rowan rev=7\n" {
		t.Fatalf("friend remove: %q", out)
	}
	out, _ = r.run(t, 0, "friend", "history", "rowan")
	if !strings.Contains(out, "op=remove actor=rowan") || !strings.HasSuffix(out, "CONFIG HISTORY kind=friend name=rowan changes=3\n") {
		t.Fatalf("history after remove:\n%s", out)
	}
	_, errs = r.run(t, 1, "machine", "remove", "studio")
	if errs != "nova-config machine remove: machine studio is the --coordinator of the fleet; run: nova-config machine list\n" {
		t.Fatalf("remove the coordinator machine: %q", errs)
	}
	out, _ = r.run(t, 0, "fleet", "set", "--coordinator", "")
	if out != "CONFIG SET kind=fleet name=fleet rev=8 changed=coordinator\n" {
		t.Fatalf("fleet clear: %q", out)
	}
	out, _ = r.run(t, 0, "fleet", "history")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], " coordinator=->studio") || !strings.HasSuffix(lines[1], " coordinator=studio>-") || lines[2] != "CONFIG HISTORY kind=fleet name=fleet changes=2" {
		t.Fatalf("fleet history:\n%s", out)
	}
	out, _ = r.run(t, 0, "machine", "remove", "studio")
	if out != "CONFIG REMOVE kind=machine name=studio rev=9\n" {
		t.Fatalf("machine remove: %q", out)
	}
	out, _ = r.run(t, 0, "status")
	if !strings.Contains(out, " schema=5 machine=0 machine_rev=9 fleet_rev=8 friend=0 friend_rev=7 sprint_rev=6 redis=-") {
		t.Fatalf("status: %q", out)
	}
}

func TestApplyEndToEnd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := newReal(t, true)
	r.run(t, 0, "migrate")
	r.run(t, 0, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64")
	r.run(t, 0, "machine", "add", "hulk", "--user", "gaffer", "--seat", "swarm-hulk", "--slots", "64", "--runners", "2")
	r.run(t, 0, "friend", "add", "rowan", "--slots", "32", "--tiers", "frontier", "--roles", "builder")
	r.run(t, 0, "friend", "add", "stella", "--slots", "32", "--tiers", "frontier,pro", "--roles", "reader")
	r.run(t, 0, "fleet", "set", "--store", "hulk", "--coordinator", "studio")
	r.run(t, 0, "sprint", "set", "--coordinator", "rowan")
	// stella's beat says she runs on hulk; rowan has none and is charged to
	// the coordinator machine.
	r.client.HSet(ctx, config.FriendBeatKey("stella"), "host", "hulk", "at", "1790000000000")

	// --check prints the plan and writes nothing but that beat.
	out, _ := r.run(t, 0, "apply", "--check")
	want := "CHECK ADD kind=machine name=hulk\nCHECK ADD kind=machine name=studio\nCONFIG CHECK kind=machine add=2 set=0 remove=0 rev=2 applied=0\nCHECK SET kind=fleet name=fleet changed=store,coordinator\nCONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=5 applied=0\nCHECK ADD kind=friend name=rowan\nCHECK ADD kind=friend name=stella\nCONFIG CHECK kind=friend add=2 set=0 remove=0 rev=4 applied=0\nCHECK SET kind=sprint name=sprint changed=coordinator\nCONFIG CHECK kind=sprint add=0 set=1 remove=0 rev=6 applied=0\n"
	if out != want {
		t.Fatalf("apply --check:\n%s\nwant:\n%s", out, want)
	}
	if n, _ := r.client.DBSize(ctx).Result(); n != 1 {
		t.Fatalf("--check wrote %d keys (the beat is the one)", n-1)
	}

	// apply writes everything, one CONFIG APPLY line per kind.
	out, _ = r.run(t, 0, "apply")
	for _, want := range []string{"APPLY ADD kind=machine name=studio\n", "CONFIG APPLY kind=machine add=2 set=0 remove=0 rev=2 ms=", "APPLY SET kind=fleet name=fleet changed=store,coordinator\nCONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=5 ms=", "APPLY ADD kind=friend name=rowan\nAPPLY ADD kind=friend name=stella\nCONFIG APPLY kind=friend add=2 set=0 remove=0 rev=4 ms=", "APPLY SET kind=sprint name=sprint changed=coordinator\nCONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=6 ms="} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply output lacks %q:\n%s", want, out)
		}
	}
	if got := r.client.HGetAll(ctx, "friend:rowan:desired").Val(); got["slots"] != "32" || got["machine"] != "studio" || got["tiers"] != "frontier" {
		t.Fatalf("friend:rowan:desired %v (charged to the coordinator machine)", got)
	}
	if got := r.client.HGetAll(ctx, "friend:stella:desired").Val(); got["machine"] != "hulk" || got["tiers"] != "frontier,pro" {
		t.Fatalf("friend:stella:desired %v (charged to the host her beat reports)", got)
	}
	if got := r.client.HGet(ctx, "friend:rowan:roles", "roles").Val(); got != "builder,coordinator" {
		t.Fatalf("rowan roles %q: the sprint's coordinator carries the role", got)
	}
	if got := r.client.HGet(ctx, "friend:stella:roles", "roles").Val(); got != "reader" {
		t.Fatalf("stella roles %q", got)
	}
	if got := r.client.Get(ctx, config.SprintKey("coordinator")).Val(); got != "rowan" {
		t.Fatalf("sprint:coordinator %q", got)
	}
	if r.client.Exists(ctx, "friends:login", "friend:rowan:wakepath", "friend:rowan:config").Val() != 0 {
		t.Fatal("apply wrote a key that is the friend's own presence's")
	}
	if got := r.client.HGetAll(ctx, "machine:hulk").Val(); got["user"] != "gaffer" || got["seat"] != "swarm-hulk" || got["slots"] != "64" || got["runners"] != "2" || len(got) != 6 {
		t.Fatalf("machine:hulk %v", got)
	}
	if got := r.client.HGet(ctx, "machine:hulk:ceiling", "slots").Val(); got != "64" {
		t.Fatalf("hulk ceiling %q", got)
	}
	if got := r.client.Get(ctx, config.FleetKey("store")).Val(); got != "hulk" {
		t.Fatalf("fleet:store %q", got)
	}
	if got := r.client.Get(ctx, config.FleetKey("coordinator")).Val(); got != "studio" {
		t.Fatalf("fleet:coordinator %q", got)
	}
	if got := r.client.HGetAll(ctx, config.DeclKey).Val(); got["rev:machine"] != "2" || got["rev:friend"] != "4" || got["rev:fleet"] != "5" || got["rev:sprint"] != "6" {
		t.Fatalf("config:decl %v", got)
	}
	out, _ = r.run(t, 0, "status")
	if !strings.Contains(out, " machine_applied=2 fleet_applied=5 friend_applied=4 sprint_applied=6\n") {
		t.Fatalf("status after apply: %q", out)
	}

	// The live measured facts: list and show with the Redis at hand print
	// what the beat says after the declared fields, beat=none for a
	// machine that has not beaten; nothing is stored.
	r.client.HSet(ctx, config.BeatKey("hulk"), "host", "hulk", "at", "1790000000000", "load1", "0.5", "ncpu", "64", "cpu", "3")
	out, _ = r.run(t, 0, "machine", "list")
	if out != "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=64 runners=2 tiers=- os=- arch=- cores=64 memory_gb=- beat=2026-09-21T14:13:20Z\nMACHINE name=studio user=glenn seat=studio slots=64 runners=0 tiers=- beat=none\nCONFIG LIST kind=machine rows=2\n" {
		t.Fatalf("machine list with the live facts: %q", out)
	}
	out, _ = r.run(t, 0, "machine", "show", "hulk")
	if !strings.HasPrefix(out, "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=64 runners=2 tiers=- created=") || !strings.HasSuffix(out, " os=- arch=- cores=64 memory_gb=- beat=2026-09-21T14:13:20Z\n") {
		t.Fatalf("machine show with the live facts: %q", out)
	}
	if got := r.client.HGet(ctx, "machine:hulk", "cores").Val(); got != "" {
		t.Fatal("a live fact was stored in the registry hash")
	}

	// A second apply is a no-op with the revision unchanged.
	out, _ = r.run(t, 0, "apply", "--kind", "friend")
	if !strings.HasPrefix(out, "CONFIG APPLY kind=friend add=0 set=0 remove=0 rev=4 ms=") {
		t.Fatalf("second apply: %q", out)
	}

	// The handover: sprint set names stella; the next apply writes her
	// roles with the coordinator role first, then rowan's without it.
	r.run(t, 0, "sprint", "set", "--coordinator", "stella")
	out, _ = r.run(t, 0, "apply")
	if !strings.Contains(out, "APPLY SET kind=friend name=stella changed=roles\nAPPLY SET kind=friend name=rowan changed=roles\nCONFIG APPLY kind=friend add=0 set=2 remove=0 rev=4 ms=") || !strings.Contains(out, "APPLY SET kind=sprint name=sprint changed=coordinator\n") {
		t.Fatalf("handover apply:\n%s", out)
	}
	if got := r.client.HGet(ctx, "friend:stella:roles", "roles").Val(); got != "coordinator,reader" {
		t.Fatalf("stella roles after the handover %q", got)
	}
	if got := r.client.HGet(ctx, "friend:rowan:roles", "roles").Val(); got != "builder" {
		t.Fatalf("rowan roles after the handover %q", got)
	}
	if got := r.client.Get(ctx, config.SprintKey("coordinator")).Val(); got != "stella" {
		t.Fatalf("sprint:coordinator after the handover %q", got)
	}
	r.env["NOVA_FRIEND"] = "stella"
	r.run(t, 0, "sprint", "set", "--coordinator", "rowan")
	r.run(t, 0, "apply")

	// Removing a friend goes through and takes what apply wrote.
	r.run(t, 0, "friend", "remove", "stella")
	out, _ = r.run(t, 0, "apply", "--kind", "friend")
	if !strings.HasPrefix(out, "APPLY REMOVE kind=friend name=stella\nCONFIG APPLY kind=friend add=0 set=0 remove=1 rev=9 ms=") {
		t.Fatalf("remove: %q", out)
	}
	if r.client.SIsMember(ctx, "friends", "stella").Val() || r.client.Exists(ctx, "friend:stella:desired").Val() != 0 {
		t.Fatal("stella survived the remove")
	}

	// CONFLICT when Redis's stamp is ahead of Postgres.
	r.client.HSet(ctx, config.DeclKey, "rev:friend", "50")
	_, errs := r.run(t, 1, "apply", "--kind", "friend")
	if !strings.HasPrefix(errs, "nova-config apply: CONFLICT friend: Redis holds rev 50 and this Postgres is at rev 9") {
		t.Fatalf("conflict: %q", errs)
	}
	r.client.HSet(ctx, config.DeclKey, "rev:friend", "9")

	// Machines: a set is applied, a removal of a machine with no friend
	// left on it and no fleet field naming it goes through and takes its
	// keys; the fleet's cleared store deletes fleet:store.
	r.run(t, 0, "machine", "set", "hulk", "--slots", "96", "--runners", "0")
	out, _ = r.run(t, 0, "apply", "--kind", "machine")
	if !strings.HasPrefix(out, "APPLY SET kind=machine name=hulk changed=slots,runners\nCONFIG APPLY kind=machine add=0 set=1 remove=0 rev=10 ms=") {
		t.Fatalf("machine set: %q", out)
	}
	if got := r.client.HGet(ctx, "machine:hulk:ceiling", "slots").Val(); got != "96" {
		t.Fatalf("hulk ceiling %q", got)
	}
	_, errs = r.run(t, 1, "machine", "remove", "hulk")
	if !strings.Contains(errs, "machine hulk is the --store of the fleet") {
		t.Fatalf("remove the store machine: %q", errs)
	}
	r.run(t, 0, "fleet", "set", "--store", "")
	r.run(t, 0, "machine", "remove", "hulk")
	out, _ = r.run(t, 0, "apply")
	if !strings.Contains(out, "APPLY REMOVE kind=machine name=hulk\n") || !strings.Contains(out, "APPLY SET kind=fleet name=fleet changed=store\n") {
		t.Fatalf("machine remove and fleet clear: %q", out)
	}
	if r.client.Exists(ctx, "machine:hulk:ceiling", "machine:hulk", config.FleetKey("store")).Val() != 0 || r.client.Get(ctx, config.FleetKey("coordinator")).Val() != "studio" {
		t.Fatal("hulk's keys or fleet:store survived, or the coordinator went with them")
	}
}
