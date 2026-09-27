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
	if !strings.HasPrefix(out, "CONFIG MIGRATE pg=postgres@127.0.0.1:") || !strings.HasSuffix(out, " from=0 to=3 applied=3\n") {
		t.Fatalf("migrate: %q", out)
	}
	out, _ = r.run(t, 0, "migrate")
	if !strings.HasSuffix(out, " from=3 to=3 applied=0\n") {
		t.Fatalf("migrate twice: %q", out)
	}
	out, _ = r.run(t, 0, "machine", "add", "studio", "--ssh", "studio", "--os_arch", "darwin/arm64", "--slots", "64", "--cores", "32", "--roles", "bench,coordination,runner", "--seat", "studio", "--user", "glenn", "--note", "Glenn's Mac Studio")
	if out != "CONFIG ADD kind=machine name=studio rev=1\n" {
		t.Fatalf("machine add: %q", out)
	}
	_, errs = r.run(t, 1, "machine", "add", "studio", "--ssh", "studio", "--os_arch", "darwin/arm64", "--slots", "64")
	if errs != "nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>\n" {
		t.Fatalf("duplicate machine: %q", errs)
	}
	out, _ = r.run(t, 0, "friend", "add", "rowan", "--machine", "studio", "--slots", "32", "--harness", "claude", "--wake", "unit:rowan@studio", "--roles", "coordinator,builder", "--logins", "rowan-claude", "--note", "the coordinator")
	if out != "CONFIG ADD kind=friend name=rowan rev=2\n" {
		t.Fatalf("friend add: %q", out)
	}
	_, errs = r.run(t, 1, "friend", "add", "stella", "--machine", "studio", "--slots", "32", "--logins", "rowan-claude")
	if !strings.Contains(errs, "--logins rowan-claude is friend rowan's login") {
		t.Fatalf("login taken: %q", errs)
	}
	out, _ = r.run(t, 0, "friend", "set", "rowan", "--slots", "64", "--note", "")
	if out != "CONFIG SET kind=friend name=rowan rev=3 changed=note,slots\n" {
		t.Fatalf("friend set: %q", out)
	}
	out, _ = r.run(t, 0, "friend", "list")
	if out != "FRIEND name=rowan machine=studio slots=64 harness=claude wake=unit:rowan@studio roles=builder,coordinator logins=rowan-claude note=-\nCONFIG LIST kind=friend rows=1\n" {
		t.Fatalf("friend list: %q", out)
	}
	out, _ = r.run(t, 0, "machine", "show", "studio")
	if !strings.HasPrefix(out, `MACHINE name=studio ssh=studio os_arch=darwin/arm64 slots=64 cores=32 roles=bench,coordination,runner seat=studio user=glenn note=Glenn's\x20Mac\x20Studio created=`) {
		t.Fatalf("machine show: %q", out)
	}
	out, _ = r.run(t, 0, "friend", "history", "rowan")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "HISTORY id=2 kind=friend name=rowan op=add actor=rowan at=") || !strings.HasSuffix(lines[1], " note=the\\x20coordinator>- slots=32>64") || lines[2] != "CONFIG HISTORY kind=friend name=rowan changes=2" {
		t.Fatalf("history:\n%s", out)
	}
	_, errs = r.run(t, 1, "machine", "remove", "studio")
	if !strings.Contains(errs, "machine studio is the --machine of friend rowan") {
		t.Fatalf("remove a machine in use: %q", errs)
	}
	out, _ = r.run(t, 0, "friend", "remove", "rowan")
	if out != "CONFIG REMOVE kind=friend name=rowan rev=4\n" {
		t.Fatalf("friend remove: %q", out)
	}
	out, _ = r.run(t, 0, "friend", "history", "rowan")
	if !strings.Contains(out, "op=remove actor=rowan") || !strings.HasSuffix(out, "CONFIG HISTORY kind=friend name=rowan changes=3\n") {
		t.Fatalf("history after remove:\n%s", out)
	}
	out, _ = r.run(t, 0, "machine", "remove", "studio")
	if out != "CONFIG REMOVE kind=machine name=studio rev=5\n" {
		t.Fatalf("machine remove: %q", out)
	}
	out, _ = r.run(t, 0, "status")
	if !strings.Contains(out, " schema=3 machine=0 machine_rev=5 friend=0 friend_rev=4 redis=-") {
		t.Fatalf("status: %q", out)
	}
}

func TestApplyEndToEnd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := newReal(t, true)
	r.run(t, 0, "migrate")
	r.run(t, 0, "machine", "add", "studio", "--ssh", "studio", "--os_arch", "darwin/arm64", "--slots", "64")
	r.run(t, 0, "machine", "add", "hulk", "--ssh", "hulk", "--os_arch", "linux/x64", "--slots", "64", "--cores", "64")
	r.run(t, 0, "friend", "add", "rowan", "--machine", "studio", "--slots", "32", "--roles", "coordinator,builder", "--logins", "rowan-claude", "--wake", "unit:rowan@studio")
	r.run(t, 0, "friend", "add", "stella", "--machine", "studio", "--slots", "32", "--roles", "reader")

	// --check prints the plan and writes nothing.
	out, _ := r.run(t, 0, "apply", "--check")
	want := "CHECK ADD kind=machine name=hulk\nCHECK ADD kind=machine name=studio\nCONFIG CHECK kind=machine add=2 set=0 remove=0 rev=2 applied=0\nCHECK ADD kind=friend name=rowan\nCHECK ADD kind=friend name=stella\nCONFIG CHECK kind=friend add=2 set=0 remove=0 rev=4 applied=0\n"
	if out != want {
		t.Fatalf("apply --check:\n%s\nwant:\n%s", out, want)
	}
	if n, _ := r.client.DBSize(ctx).Result(); n != 0 {
		t.Fatalf("--check wrote %d keys", n)
	}

	// apply writes everything, one CONFIG APPLY line per kind.
	out, _ = r.run(t, 0, "apply")
	for _, want := range []string{"APPLY ADD kind=machine name=studio\n", "CONFIG APPLY kind=machine add=2 set=0 remove=0 rev=2 ms=", "APPLY ADD kind=friend name=rowan\nAPPLY ADD kind=friend name=stella\nCONFIG APPLY kind=friend add=2 set=0 remove=0 rev=4 ms="} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply output lacks %q:\n%s", want, out)
		}
	}
	if got := r.client.HGetAll(ctx, "friend:rowan:desired").Val(); got["slots"] != "32" || got["machine"] != "studio" {
		t.Fatalf("friend:rowan:desired %v", got)
	}
	if got := r.client.HGet(ctx, "friend:stella:roles", "roles").Val(); got != "reader" {
		t.Fatalf("stella roles %q", got)
	}
	if got := r.client.HGet(ctx, "machine:hulk:ceiling", "cores").Val(); got != "64" {
		t.Fatalf("hulk cores %q", got)
	}
	if got := r.client.HGetAll(ctx, config.DeclKey).Val(); got["rev:machine"] != "2" || got["rev:friend"] != "4" {
		t.Fatalf("config:decl %v", got)
	}
	out, _ = r.run(t, 0, "status")
	if !strings.Contains(out, " machine_applied=2 friend_applied=4\n") {
		t.Fatalf("status after apply: %q", out)
	}

	// A second apply is a no-op with the revision unchanged.
	out, _ = r.run(t, 0, "apply", "--kind", "friend")
	if !strings.HasPrefix(out, "CONFIG APPLY kind=friend add=0 set=0 remove=0 rev=4 ms=") {
		t.Fatalf("second apply: %q", out)
	}

	// Removing a friend is refused by apply while it holds working copies,
	// and goes through once they are gone.
	r.run(t, 0, "friend", "remove", "stella")
	r.client.SAdd(ctx, "friend:stella:cards:working", "card:4410")
	out, errs := r.run(t, 1, "apply", "--kind", "friend")
	if !strings.Contains(errs, "friend stella holds 1 working copies (card:4410)") || out != "APPLY REMOVE kind=friend name=stella\n" {
		t.Fatalf("remove with a working copy: %q %q", out, errs)
	}
	if !r.client.SIsMember(ctx, "friends", "stella").Val() {
		t.Fatal("a refused remove unregistered stella")
	}
	r.client.Del(ctx, "friend:stella:cards:working")
	out, _ = r.run(t, 0, "apply", "--kind", "friend")
	if !strings.HasPrefix(out, "APPLY REMOVE kind=friend name=stella\nCONFIG APPLY kind=friend add=0 set=0 remove=1 rev=5 ms=") {
		t.Fatalf("remove: %q", out)
	}
	if r.client.SIsMember(ctx, "friends", "stella").Val() || r.client.Exists(ctx, "friend:stella:desired").Val() != 0 {
		t.Fatal("stella survived the remove")
	}

	// CONFLICT when Redis's stamp is ahead of Postgres.
	r.client.HSet(ctx, config.DeclKey, "rev:friend", "50")
	_, errs = r.run(t, 1, "apply", "--kind", "friend")
	if !strings.HasPrefix(errs, "nova-config apply: CONFLICT friend: Redis holds rev 50 and this Postgres is at rev 5") {
		t.Fatalf("conflict: %q", errs)
	}

	// Machines: a set is applied, a removal of a machine with no friend
	// left on it goes through and takes its keys.
	r.run(t, 0, "machine", "set", "hulk", "--slots", "96", "--note", "wider")
	out, _ = r.run(t, 0, "apply", "--kind", "machine")
	if !strings.HasPrefix(out, "APPLY SET kind=machine name=hulk changed=slots,note\nCONFIG APPLY kind=machine add=0 set=1 remove=0 rev=6 ms=") {
		t.Fatalf("machine set: %q", out)
	}
	if got := r.client.HGet(ctx, "machine:hulk:ceiling", "slots").Val(); got != "96" {
		t.Fatalf("hulk ceiling %q", got)
	}
	r.run(t, 0, "machine", "remove", "hulk")
	out, _ = r.run(t, 0, "apply", "--kind", "machine")
	if !strings.HasPrefix(out, "APPLY REMOVE kind=machine name=hulk\n") || r.client.Exists(ctx, "machine:hulk:ceiling", "machine:hulk").Val() != 0 {
		t.Fatalf("machine remove: %q", out)
	}
}
