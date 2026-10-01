//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil/pg"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// One throwaway Postgres for the package, one database per test; one
// throwaway Redis per test that applies (docs/TESTING.md: frugal).
var server *pg.Server

func TestMain(m *testing.M) {
	// The wrapper test's helper process (TestInventoryHelperProcess) is this
	// binary serving an in-memory store: it starts no Postgres.
	if os.Getenv("NOVA_CONFIG_TEST_HELPER") == "1" {
		os.Exit(m.Run())
	}
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
			require.NoError(t, err)
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
	require.Equal(t, want, code, "%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out.String(), errb.String())
	return out.String(), errb.String()
}

func TestMigrateTwiceThenTheSixVerbs(t *testing.T) {
	t.Parallel()

	r := newReal(t, false)
	out, errs := r.run(t, 1, "status")
	require.Contains(t, out, " schema=0 ", "status before migrate: %q %q", out, errs)
	require.Contains(t, errs, "run: nova-config migrate", "status before migrate: %q %q", out, errs)
	out, _ = r.run(t, 0, "migrate")
	require.True(t, strings.HasPrefix(out, "CONFIG MIGRATE pg=postgres@127.0.0.1:"), "migrate: %q", out)
	require.True(t, strings.HasSuffix(out, " from=0 to=6 applied=6\n"), "migrate: %q", out)
	out, _ = r.run(t, 0, "migrate")
	require.True(t, strings.HasSuffix(out, " from=6 to=6 applied=0\n"), "migrate twice: %q", out)
	out, _ = r.run(t, 0, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64", "--runners", "1")
	require.Equal(t, "CONFIG ADD kind=machine name=studio rev=1\n", out, "machine add: %q", out)
	_, errs = r.run(t, 1, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64")
	require.Equal(t, "nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>\n", errs, "duplicate machine: %q", errs)
	out, _ = r.run(t, 0, "friend", "add", "rowan", "--slots", "32", "--tiers", "frontier,pro", "--roles", "builder")
	require.Equal(t, "CONFIG ADD kind=friend name=rowan rev=2\n", out, "friend add: %q", out)
	_, errs = r.run(t, 1, "friend", "add", "rowan", "--slots", "32", "--tiers", "frontier")
	require.Equal(t, "nova-config friend add: friend rowan exists; run: nova-config friend set rowan --<field> <value>\n", errs, "duplicate friend: %q", errs)
	out, _ = r.run(t, 0, "friend", "set", "rowan", "--slots", "64", "--roles", "")
	require.Equal(t, "CONFIG SET kind=friend name=rowan rev=3 changed=roles,slots\n", out, "friend set: %q", out)
	out, _ = r.run(t, 0, "friend", "list")
	require.Equal(t, "FRIEND name=rowan slots=64 tiers=frontier,pro roles=-\nCONFIG LIST kind=friend rows=1\n", out, "friend list: %q", out)
	out, _ = r.run(t, 0, "machine", "show", "studio")
	require.True(t, strings.HasPrefix(out, "MACHINE name=studio user=glenn seat=studio slots=64 runners=1 created="), "machine show: %q", out)
	// The fleet row: there since migrate, set without a name, a machine it
	// names cannot be removed.
	out, _ = r.run(t, 0, "fleet", "show")
	require.True(t, strings.HasPrefix(out, "FLEET name=fleet store=- coordinator=- created="), "fleet show: %q", out)
	_, errs = r.run(t, 1, "fleet", "set", "--store", "space")
	require.Equal(t, "nova-config fleet set: --store space names no machine row; run: nova-config machine list\n", errs, "fleet set naming no machine: %q", errs)
	out, _ = r.run(t, 0, "fleet", "set", "--coordinator", "studio")
	require.Equal(t, "CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator\n", out, "fleet set: %q", out)
	// The sprint row: who coordinates; the friend it names cannot go.
	_, errs = r.run(t, 1, "sprint", "set", "--coordinator", "nobody")
	require.Equal(t, "nova-config sprint set: --coordinator nobody names no friend row; run: nova-config friend list\n", errs, "sprint set naming no friend: %q", errs)
	out, _ = r.run(t, 0, "sprint", "set", "--coordinator", "rowan")
	require.Equal(t, "CONFIG SET kind=sprint name=sprint rev=5 changed=coordinator\n", out, "sprint set: %q", out)
	_, errs = r.run(t, 1, "friend", "remove", "rowan")
	require.Equal(t, "nova-config friend remove: friend rowan is the --coordinator of the sprint; run: nova-config friend list\n", errs, "remove the coordinating friend: %q", errs)
	r.run(t, 0, "sprint", "set", "--coordinator", "")
	out, _ = r.run(t, 0, "friend", "history", "rowan")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3, "history:\n%s", out)
	require.True(t, strings.HasPrefix(lines[0], "HISTORY id=2 kind=friend name=rowan op=add actor=rowan at="), "history:\n%s", out)
	require.True(t, strings.HasSuffix(lines[1], " roles=builder>- slots=32>64"), "history:\n%s", out)
	require.Equal(t, "CONFIG HISTORY kind=friend name=rowan changes=2", lines[2], "history:\n%s", out)
	out, _ = r.run(t, 0, "friend", "remove", "rowan")
	require.Equal(t, "CONFIG REMOVE kind=friend name=rowan rev=7\n", out, "friend remove: %q", out)
	out, _ = r.run(t, 0, "friend", "history", "rowan")
	require.Contains(t, out, "op=remove actor=rowan", "history after remove:\n%s", out)
	require.True(t, strings.HasSuffix(out, "CONFIG HISTORY kind=friend name=rowan changes=3\n"), "history after remove:\n%s", out)
	_, errs = r.run(t, 1, "machine", "remove", "studio")
	require.Equal(t, "nova-config machine remove: machine studio is the --coordinator of the fleet; run: nova-config machine list\n", errs, "remove the coordinator machine: %q", errs)
	out, _ = r.run(t, 0, "fleet", "set", "--coordinator", "")
	require.Equal(t, "CONFIG SET kind=fleet name=fleet rev=8 changed=coordinator\n", out, "fleet clear: %q", out)
	out, _ = r.run(t, 0, "fleet", "history")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3, "fleet history:\n%s", out)
	require.True(t, strings.HasSuffix(lines[0], " coordinator=->studio"), "fleet history:\n%s", out)
	require.True(t, strings.HasSuffix(lines[1], " coordinator=studio>-"), "fleet history:\n%s", out)
	require.Equal(t, "CONFIG HISTORY kind=fleet name=fleet changes=2", lines[2], "fleet history:\n%s", out)
	out, _ = r.run(t, 0, "machine", "remove", "studio")
	require.Equal(t, "CONFIG REMOVE kind=machine name=studio rev=9\n", out, "machine remove: %q", out)
	out, _ = r.run(t, 0, "status")
	require.Contains(t, out, " schema=6 machine=0 machine_rev=9 fleet_rev=8 friend=0 friend_rev=7 sprint_rev=6 loop=0 loop_rev=0 redis=-", "status: %q", out)
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
	want := "CHECK ADD kind=machine name=hulk\nCHECK ADD kind=machine name=studio\nCONFIG CHECK kind=machine add=2 set=0 remove=0 rev=2 applied=0\nCHECK SET kind=fleet name=fleet changed=store,coordinator\nCONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=5 applied=0\nCHECK ADD kind=friend name=rowan\nCHECK ADD kind=friend name=stella\nCONFIG CHECK kind=friend add=2 set=0 remove=0 rev=4 applied=0\nCHECK SET kind=sprint name=sprint changed=coordinator\nCONFIG CHECK kind=sprint add=0 set=1 remove=0 rev=6 applied=0\nCONFIG CHECK kind=loop add=0 set=0 remove=0 rev=0 applied=0\n"
	require.Equal(t, want, out, "apply --check:\n%s\nwant:\n%s", out, want)
	nCheck221, _ := r.client.DBSize(ctx).Result()
	require.Equal(t, int64(1), nCheck221, "--check wrote %d keys (the beat is the one)", nCheck221-1)

	// apply writes everything, one CONFIG APPLY line per kind.
	out, _ = r.run(t, 0, "apply")
	for _, want := range []string{"APPLY ADD kind=machine name=studio\n", "CONFIG APPLY kind=machine add=2 set=0 remove=0 rev=2 ms=", "APPLY SET kind=fleet name=fleet changed=store,coordinator\nCONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=5 ms=", "APPLY ADD kind=friend name=rowan\nAPPLY ADD kind=friend name=stella\nCONFIG APPLY kind=friend add=2 set=0 remove=0 rev=4 ms=", "APPLY SET kind=sprint name=sprint changed=coordinator\nCONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=6 ms="} {
		require.Contains(t, out, want, "apply output lacks %q:\n%s", want, out)
	}
	gotCheck232 := r.client.HGetAll(ctx, "friend:rowan:desired").Val()
	require.Equal(t, "32", gotCheck232["slots"], "friend:rowan:desired %v (charged to the coordinator machine)", gotCheck232)
	require.Equal(t, "studio", gotCheck232["machine"], "friend:rowan:desired %v (charged to the coordinator machine)", gotCheck232)
	require.Equal(t, "frontier", gotCheck232["tiers"], "friend:rowan:desired %v (charged to the coordinator machine)", gotCheck232)
	gotCheck235 := r.client.HGetAll(ctx, "friend:stella:desired").Val()
	require.Equal(t, "hulk", gotCheck235["machine"], "friend:stella:desired %v (charged to the host her beat reports)", gotCheck235)
	require.Equal(t, "frontier,pro", gotCheck235["tiers"], "friend:stella:desired %v (charged to the host her beat reports)", gotCheck235)
	gotCheck238 := r.client.HGet(ctx, "friend:rowan:roles", "roles").Val()
	require.Equal(t, "builder,coordinator", gotCheck238, "rowan roles %q: the sprint's coordinator carries the role", gotCheck238)
	gotCheck241 := r.client.HGet(ctx, "friend:stella:roles", "roles").Val()
	require.Equal(t, "reader", gotCheck241, "stella roles %q", gotCheck241)
	gotCheck244 := r.client.Get(ctx, config.SprintKey("coordinator")).Val()
	require.Equal(t, "rowan", gotCheck244, "sprint:coordinator %q", gotCheck244)
	require.Zero(t, r.client.Exists(ctx, "friends:login", "friend:rowan:wakepath", "friend:rowan:config").Val(), "apply wrote a key that is the friend's own presence's")
	gotCheck250 := r.client.HGetAll(ctx, "machine:hulk").Val()
	require.Equal(t, "gaffer", gotCheck250["user"], "machine:hulk %v", gotCheck250)
	require.Equal(t, "swarm-hulk", gotCheck250["seat"], "machine:hulk %v", gotCheck250)
	require.Equal(t, "64", gotCheck250["slots"], "machine:hulk %v", gotCheck250)
	require.Equal(t, "2", gotCheck250["runners"], "machine:hulk %v", gotCheck250)
	require.Len(t, gotCheck250, 6, "machine:hulk %v", gotCheck250)
	gotCheck253 := r.client.HGet(ctx, "machine:hulk:ceiling", "slots").Val()
	require.Equal(t, "64", gotCheck253, "hulk ceiling %q", gotCheck253)
	gotCheck256 := r.client.Get(ctx, config.FleetKey("store")).Val()
	require.Equal(t, "hulk", gotCheck256, "fleet:store %q", gotCheck256)
	gotCheck259 := r.client.Get(ctx, config.FleetKey("coordinator")).Val()
	require.Equal(t, "studio", gotCheck259, "fleet:coordinator %q", gotCheck259)
	gotCheck262 := r.client.HGetAll(ctx, config.DeclKey).Val()
	require.Equal(t, "2", gotCheck262["rev:machine"], "config:decl %v", gotCheck262)
	require.Equal(t, "4", gotCheck262["rev:friend"], "config:decl %v", gotCheck262)
	require.Equal(t, "5", gotCheck262["rev:fleet"], "config:decl %v", gotCheck262)
	require.Equal(t, "6", gotCheck262["rev:sprint"], "config:decl %v", gotCheck262)
	out, _ = r.run(t, 0, "status")
	require.Contains(t, out, " machine_applied=2 fleet_applied=5 friend_applied=4 sprint_applied=6 loop_applied=0\n", "status after apply: %q", out)

	// The live measured facts: list and show with the Redis at hand print
	// what the beat says after the declared fields, beat=none for a
	// machine that has not beaten; nothing is stored.
	r.client.HSet(ctx, config.BeatKey("hulk"), "host", "hulk", "at", "1790000000000", "load1", "0.5", "ncpu", "64", "cpu", "3")
	out, _ = r.run(t, 0, "machine", "list")
	require.Equal(t, "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=64 runners=2 os=- arch=- cores=64 memory_gb=- beat=2026-09-21T14:13:20Z\nMACHINE name=studio user=glenn seat=studio slots=64 runners=0 beat=none\nCONFIG LIST kind=machine rows=2\n", out, "machine list with the live facts: %q", out)
	out, _ = r.run(t, 0, "machine", "show", "hulk")
	require.True(t, strings.HasPrefix(out, "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=64 runners=2 created="), "machine show with the live facts: %q", out)
	require.True(t, strings.HasSuffix(out, " os=- arch=- cores=64 memory_gb=- beat=2026-09-21T14:13:20Z\n"), "machine show with the live facts: %q", out)
	gotCheck282 := r.client.HGet(ctx, "machine:hulk", "cores").Val()
	require.Equal(t, "", gotCheck282, "a live fact was stored in the registry hash")

	// A second apply is a no-op with the revision unchanged.
	out, _ = r.run(t, 0, "apply", "--kind", "friend")
	require.True(t, strings.HasPrefix(out, "CONFIG APPLY kind=friend add=0 set=0 remove=0 rev=4 ms="), "second apply: %q", out)

	// The handover: sprint set names stella; the next apply writes her
	// roles with the coordinator role first, then rowan's without it.
	r.run(t, 0, "sprint", "set", "--coordinator", "stella")
	out, _ = r.run(t, 0, "apply")
	require.Contains(t, out, "APPLY SET kind=friend name=stella changed=roles\nAPPLY SET kind=friend name=rowan changed=roles\nCONFIG APPLY kind=friend add=0 set=2 remove=0 rev=4 ms=", "handover apply:\n%s", out)
	require.Contains(t, out, "APPLY SET kind=sprint name=sprint changed=coordinator\n", "handover apply:\n%s", out)
	gotCheck299 := r.client.HGet(ctx, "friend:stella:roles", "roles").Val()
	require.Equal(t, "coordinator,reader", gotCheck299, "stella roles after the handover %q", gotCheck299)
	gotCheck302 := r.client.HGet(ctx, "friend:rowan:roles", "roles").Val()
	require.Equal(t, "builder", gotCheck302, "rowan roles after the handover %q", gotCheck302)
	gotCheck305 := r.client.Get(ctx, config.SprintKey("coordinator")).Val()
	require.Equal(t, "stella", gotCheck305, "sprint:coordinator after the handover %q", gotCheck305)
	r.env["NOVA_FRIEND"] = "stella"
	r.run(t, 0, "sprint", "set", "--coordinator", "rowan")
	r.run(t, 0, "apply")

	// Removing a friend goes through and takes what apply wrote.
	r.run(t, 0, "friend", "remove", "stella")
	out, _ = r.run(t, 0, "apply", "--kind", "friend")
	require.True(t, strings.HasPrefix(out, "APPLY REMOVE kind=friend name=stella\nCONFIG APPLY kind=friend add=0 set=0 remove=1 rev=9 ms="), "remove: %q", out)
	require.False(t, r.client.SIsMember(ctx, "friends", "stella").Val(), "stella survived the remove")
	require.Zero(t, r.client.Exists(ctx, "friend:stella:desired").Val(), "stella survived the remove")

	// CONFLICT when Redis's stamp is ahead of Postgres.
	r.client.HSet(ctx, config.DeclKey, "rev:friend", "50")
	_, errs := r.run(t, 1, "apply", "--kind", "friend")
	require.True(t, strings.HasPrefix(errs, "nova-config apply: CONFLICT friend: Redis holds rev 50 and this Postgres is at rev 9"), "conflict: %q", errs)
	r.client.HSet(ctx, config.DeclKey, "rev:friend", "9")

	// Machines: a set is applied, a removal of a machine with no friend
	// left on it and no fleet field naming it goes through and takes its
	// keys; the fleet's cleared store deletes fleet:store.
	r.run(t, 0, "machine", "set", "hulk", "--slots", "96", "--runners", "0")
	out, _ = r.run(t, 0, "apply", "--kind", "machine")
	require.True(t, strings.HasPrefix(out, "APPLY SET kind=machine name=hulk changed=slots,runners\nCONFIG APPLY kind=machine add=0 set=1 remove=0 rev=10 ms="), "machine set: %q", out)
	gotCheck338 := r.client.HGet(ctx, "machine:hulk:ceiling", "slots").Val()
	require.Equal(t, "96", gotCheck338, "hulk ceiling %q", gotCheck338)
	_, errs = r.run(t, 1, "machine", "remove", "hulk")
	require.Contains(t, errs, "machine hulk is the --store of the fleet", "remove the store machine: %q", errs)
	r.run(t, 0, "fleet", "set", "--store", "")
	r.run(t, 0, "machine", "remove", "hulk")
	out, _ = r.run(t, 0, "apply")
	require.Contains(t, out, "APPLY REMOVE kind=machine name=hulk\n", "machine remove and fleet clear: %q", out)
	require.Contains(t, out, "APPLY SET kind=fleet name=fleet changed=store\n", "machine remove and fleet clear: %q", out)
	require.Zero(t, r.client.Exists(ctx, "machine:hulk:ceiling", "machine:hulk", config.FleetKey("store")).Val(), "hulk's keys or fleet:store survived, or the coordinator went with them")
	require.Equal(t, "studio", r.client.Get(ctx, config.FleetKey("coordinator")).Val(), "hulk's keys or fleet:store survived, or the coordinator went with them")
}

// The wrapper the help prints, run with the built binary against a store
// apply wrote, is a working inventory script.
func TestInventoryWrapperFromTheHelpRunsWithTheBuiltBinary(t *testing.T) {
	t.Parallel()

	r := newReal(t, true)
	r.run(t, 0, "migrate")
	r.run(t, 0, "machine", "add", "bench-alpha", "--user", "user-a", "--seat", "seat-alpha", "--slots", "4", "--as", "operator")
	r.run(t, 0, "apply", "--as", "operator")

	help, _ := r.run(t, 0, "inventory", "-h")
	printf, chmod := helpCommands(t, help)

	dir := t.TempDir()
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, "nova-config"), ".")
	build.Env = goenv.Clean(os.Environ())
	outCheck372, errCheck372 := build.CombinedOutput()
	require.NoError(t, errCheck372, "go build: %v\n%s", errCheck372, outCheck372)
	run := func(script string) string {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "NOVA_SPRINT_REDIS="+r.env["NOVA_SPRINT_REDIS"], "NOVA_PG_DSN=postgres://nobody@127.0.0.1:1/none")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %v\n%s", script, err, out)
		return string(out)
	}
	run(printf + "\n" + chmod)
	for _, args := range []string{"--list", "--host bench-alpha"} {
		out := run("./nova-inventory " + args)
		var v map[string]any
		errCheck390 := json.Unmarshal([]byte(out), &v)
		require.NoError(t, errCheck390, "./nova-inventory %s: %v\n%s", args, errCheck390, out)
		require.Contains(t, out, `"ansible_host": "bench-alpha"`, "./nova-inventory %s: %v\n%s", args, errCheck390, out)
		require.Contains(t, out, `"nova_seat": "seat-alpha"`, "./nova-inventory %s: %v\n%s", args, errCheck390, out)
	}
}

// stallingListener accepts TCP connections and never writes, like a store
// that is up on its port and never answers.
func stallingListener(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		require.NoError(t, err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return l.Addr().String()
}

// The flag governs the wait for the store: a Redis that accepts and never
// answers is given up at the verb's own deadline, with the timed-out refusal
// naming the address and the stage (the connection is made by the first
// read, so the stage is the read).
func TestInventoryTimeoutFlagGovernsTheConnection(t *testing.T) {
	t.Parallel()

	addr := stallingListener(t)
	r := newReal(t, false)
	r.env["NOVA_SPRINT_REDIS"] = addr
	for flag, again := range map[string]string{"100ms": "300ms", "250ms": "750ms"} {
		var out, errb bytes.Buffer
		code := run([]string{"inventory", "--timeout", flag}, &out, &errb, r.deps())
		want := "nova-config inventory: timed out after " + flag + " waiting for the store at " + addr + " while reading the applied state; check that Redis answers there; run: nova-config inventory --timeout " + again + "\n"
		require.Equal(t, 2, code, "--timeout %s: exit %d stdout %q stderr %q\nwant 2, nothing, %q", flag, code, out.String(), errb.String(), want)
		require.Equal(t, "", out.String(), "--timeout %s: exit %d stdout %q stderr %q\nwant 2, nothing, %q", flag, code, out.String(), errb.String(), want)
		require.Equal(t, want, errb.String(), "--timeout %s: exit %d stdout %q stderr %q\nwant 2, nothing, %q", flag, code, out.String(), errb.String(), want)
	}
}

// ansible hides a failing inventory script unless told not to: the same
// wrapper whose nova-config refuses exits 0 with an empty inventory, and
// exits non-zero with the variable the help prints. The run reads only the
// wrapper: it contacts no machine.
func TestAnsibleInventoryFailsLoudlyWithTheVariableTheHelpPrints(t *testing.T) {
	t.Parallel()

	ansible, err := exec.LookPath("ansible-inventory")
	if err != nil {
		t.Skip("ansible-inventory is not installed on this machine")
	}
	r := newReal(t, false)
	help, _ := r.run(t, 0, "inventory", "-h")
	printf, chmod := helpCommands(t, help)
	var ansibleLine string
	for _, l := range strings.Split(help, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory ") {
			ansibleLine = l
		}
	}
	require.NotEqual(t, "", ansibleLine, "the help prints no ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory command:\n%s", help)

	dir := t.TempDir()
	// A nova-config that refuses, as a missing or older one would.
	if err := os.WriteFile(filepath.Join(dir, "nova-config"), []byte("#!/bin/sh\necho 'nova-config inventory: refused' >&2\nexit 1\n"), 0o755); err != nil {
		require.NoError(t, err)
	}
	sh := func(script string) (int, string) {
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir = dir
		cmd.Env = []string{
			"PATH=" + dir + ":" + filepath.Dir(ansible) + ":/usr/bin:/bin",
			"HOME=" + dir, "ANSIBLE_HOME=" + filepath.Join(dir, "ansible"),
		}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			require.NoError(t, err, "%s: command failed unexpectedly", script)
		}
		return code, out.String()
	}
	codeCheck495, outCheck495 := sh(printf + "\n" + chmod)
	require.Equal(t, 0, codeCheck495, "the printed commands failed: %s", outCheck495)
	plain := strings.TrimPrefix(ansibleLine, "ANSIBLE_INVENTORY_UNPARSED_FAILED=true ")
	codeCheck499, outCheck499 := sh(plain)
	require.Equal(t, 0, codeCheck499, "without the variable ansible exits %d, want 0 (it hides the failure): %s", codeCheck499, outCheck499)
	codeCheck502, outCheck502 := sh(ansibleLine)
	require.NotEqual(t, 0, codeCheck502, "with the variable ansible exits 0 on a failing inventory script: %s", outCheck502)
}
