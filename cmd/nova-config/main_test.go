package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// memStore is config.Mem with the schema verbs a pgStore has: a fresh Mem
// is at the current version.
type memStore struct {
	*config.Mem
	version int
}

func (m *memStore) Migrate(context.Context) (int, int, []int, error) {
	all, err := config.Migrations()
	if err != nil {
		return 0, 0, nil, err
	}
	from := m.version
	var applied []int
	for _, mg := range all {
		if mg.Version > from {
			applied = append(applied, mg.Version)
			m.version = mg.Version
		}
	}
	return from, m.version, applied, nil
}
func (m *memStore) Version(context.Context) (int, error) { return m.version, nil }
func (m *memStore) Close() error                         { return nil }

// fakeRedis records what apply asked for.
type fakeRedis struct {
	views map[string]map[string]config.View
	revs  map[string]int64
	log   []string
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{views: map[string]map[string]config.View{}, revs: map[string]int64{}}
}
func (f *fakeRedis) Read(_ context.Context, kind string) (map[string]config.View, int64, error) {
	out := map[string]config.View{}
	for n, v := range f.views[kind] {
		out[n] = v
	}
	return out, f.revs[kind], nil
}
func (f *fakeRedis) Prepare(context.Context) error { return nil }
func (f *fakeRedis) Write(_ context.Context, kind string, row config.Row, prev config.View, actor, idem string) error {
	if f.views[kind] == nil {
		f.views[kind] = map[string]config.View{}
	}
	f.views[kind][row.Name] = config.View(row.Clone().Fields)
	f.log = append(f.log, "write "+kind+" "+row.Name)
	return nil
}
func (f *fakeRedis) Remove(_ context.Context, kind, name, actor, idem string) error {
	delete(f.views[kind], name)
	f.log = append(f.log, "remove "+kind+" "+name)
	return nil
}
func (f *fakeRedis) Stamp(_ context.Context, kind string, prev, rev int64) error {
	if f.revs[kind] != prev {
		return config.Conflict(kind, f.revs[kind], rev)
	}
	f.revs[kind] = rev
	return nil
}
func (f *fakeRedis) Close() error { return nil }

// harness is one test's tool: a Mem store, a fake Redis, an environment
// map, a fixed clock.
type harness struct {
	store *memStore
	redis *fakeRedis
	env   map[string]string
	opens int
}

func newHarness() *harness {
	return &harness{store: &memStore{Mem: config.NewMem(), version: 3}, redis: newFakeRedis(), env: map[string]string{}}
}

func (h *harness) deps() deps {
	return deps{
		getenv: func(k string) string { return h.env[k] },
		openStore: func(_ context.Context, dsn string) (pgStore, error) {
			h.opens++
			if strings.Contains(dsn, "closed") {
				return nil, fmt.Errorf("postgres at %s: connection refused", config.Redact(dsn))
			}
			return h.store, nil
		},
		openRedis: func(_ context.Context, addr string) (redisSide, error) { return h.redis, nil },
		now:       func() time.Time { return time.Unix(1700000000, 0) },
	}
}

func (h *harness) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, h.deps())
	return code, out.String(), errb.String()
}

const dsn = "postgres://nova_config@127.0.0.1:5432/nova"

func TestBareAndUnknownVerbsNameTheDoor(t *testing.T) {
	t.Parallel()

	h := newHarness()
	for _, args := range [][]string{{}, {"nothing"}, {"friend"}, {"friend", "fly"}, {"help", "x"}, {"version", "x"}, {"kinds", "x"}} {
		code, out, errs := h.run(t, args...)
		if code != 2 || out != "" || strings.Count(errs, "\n") != 1 || !strings.HasSuffix(errs, "; run: nova-config help\n") || !strings.HasPrefix(errs, "nova-config") {
			t.Errorf("%v: exit %d stdout %q stderr %q; want exit 2, one stderr line naming the door", args, code, out, errs)
		}
	}
	if h.opens != 0 {
		t.Fatal("a usage refusal opened the store")
	}
}

func TestHelpEndsInRunnableExamplesAndVersionIsOneLine(t *testing.T) {
	t.Parallel()

	h := newHarness()
	code, out, _ := h.run(t, "help")
	if code != 0 || !strings.HasSuffix(out, "example:\n  nova-config kinds\n  nova-config migrate --print\n") {
		t.Fatalf("help exit %d, tail %q", code, out[max(0, len(out)-80):])
	}
	for _, k := range config.Kinds {
		for _, f := range k.Fields {
			if !strings.Contains(out, "--"+f.Name) {
				t.Errorf("help does not name --%s of %s", f.Name, k.Name)
			}
		}
	}
	for _, verb := range []string{"version", "--version"} {
		code, out, _ := h.run(t, verb)
		if code != 0 || strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "nova-config ") {
			t.Errorf("%s: exit %d %q", verb, code, out)
		}
	}
}

func TestKindsAndMigratePrintNeedNoStore(t *testing.T) {
	t.Parallel()

	h := newHarness()
	code, out, errs := h.run(t, "kinds")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "CONFIG KIND name=machine ") || !strings.HasSuffix(out, "CONFIG KINDS count=2\n") {
		t.Fatalf("kinds: %d %q %q", code, out, errs)
	}
	code, out, errs = h.run(t, "migrate", "--print")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "MIGRATION version=1 file=0001_schema.sql ") || !strings.HasSuffix(out, "CONFIG MIGRATE print=3 pg=-\n") {
		t.Fatalf("migrate --print: %d %q %q", code, out, errs)
	}
	if h.opens != 0 {
		t.Fatal("kinds or migrate --print opened the store")
	}
}

func TestARefusalNamesEveryMissingFlagAtOnce(t *testing.T) {
	t.Parallel()

	h := newHarness()
	code, _, errs := h.run(t, "friend", "add", "rowan")
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"--as is required", "--pg is required", "--machine is required", "--slots is required", "NOVA_FRIEND", "NOVA_PG_DSN"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, errs)
		}
	}
	if strings.Count(errs, "\n") != 1 {
		t.Fatalf("refusal is not one line:\n%s", errs)
	}
	code, _, errs = h.run(t, "friend", "add", "--as", "rowan", "--pg", dsn, "--machine", "studio", "--slots", "1")
	if code != 2 || !strings.Contains(errs, "the name is required") {
		t.Fatalf("no name: %d %q", code, errs)
	}
	code, _, errs = h.run(t, "friend", "set", "rowan", "--as", "rowan", "--pg", dsn)
	if code != 2 || !strings.Contains(errs, "set names no field") {
		t.Fatalf("set with no field: %d %q", code, errs)
	}
	code, _, errs = h.run(t, "friend", "list", "rowan", "--pg", dsn)
	if code != 2 || !strings.Contains(errs, "list takes no name") {
		t.Fatalf("list with a name: %d %q", code, errs)
	}
	code, _, errs = h.run(t, "apply", "--kind", "loop", "--pg", dsn, "--redis", "127.0.0.1:6379", "--as", "rowan")
	if code != 2 || !strings.Contains(errs, "--kind loop: want one of machine, friend") {
		t.Fatalf("apply --kind loop: %d %q", code, errs)
	}
	if h.opens != 0 {
		t.Fatal("a usage refusal opened the store")
	}
}

func TestPgDSNKeepsThePasswordOffTheLine(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := pgDSN("", env(nil)); err == nil || !strings.Contains(err.Error(), "--pg is required") {
		t.Errorf("no dsn: %v", err)
	}
	if _, err := pgDSN("postgres://u:secret@127.0.0.1/nova", env(nil)); err == nil || !strings.Contains(err.Error(), "carries a password") {
		t.Errorf("password on the line: %v", err)
	}
	got, err := pgDSN(dsn, env(map[string]string{"NOVA_PG_PASSWORD": "pw"}))
	if err != nil || got != "postgres://nova_config:pw@127.0.0.1:5432/nova" {
		t.Errorf("default variable: %q %v", got, err)
	}
	got, err = pgDSN(dsn, env(map[string]string{"NOVA_PG_PASSWORD_ENV": "NOVA_SECRET_PG", "NOVA_SECRET_PG": "p w"}))
	if err != nil || got != "postgres://nova_config:p%20w@127.0.0.1:5432/nova" {
		t.Errorf("named variable: %q %v", got, err)
	}
	_, err = pgDSN(dsn, env(map[string]string{"NOVA_PG_PASSWORD_ENV": "NOVA_SECRET_PG"}))
	if err == nil || !strings.Contains(err.Error(), "NOVA_PG_PASSWORD_ENV=NOVA_SECRET_PG but NOVA_SECRET_PG is empty; run under nova-secrets exec --only NOVA_SECRET_PG") {
		t.Errorf("named but empty: %v", err)
	}
	got, err = pgDSN("", env(map[string]string{"NOVA_PG_DSN": dsn}))
	if err != nil || got != dsn {
		t.Errorf("no password anywhere (a throwaway trusts): %q %v", got, err)
	}
	got, err = pgDSN("host=127.0.0.1 user=nova_config dbname=nova", env(map[string]string{"NOVA_PG_PASSWORD": "it's"}))
	if err != nil || got != `host=127.0.0.1 user=nova_config dbname=nova password='it\'s'` {
		t.Errorf("keyword dsn: %q %v", got, err)
	}
	if _, err := pgDSN("postgres://[bad", env(nil)); err == nil || !strings.Contains(err.Error(), "--pg:") {
		t.Errorf("unparsable: %v", err)
	}
}

func TestRedisAddressAndActorFallBackToTheEnvironment(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got, err := redisAddress("127.0.0.1:1", env(map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:2"})); err != nil || got != "127.0.0.1:1" {
		t.Errorf("flag wins: %q %v", got, err)
	}
	if got, err := redisAddress("", env(map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:2", "NOVA_REDIS_ADDR": "127.0.0.1:3"})); err != nil || got != "127.0.0.1:2" {
		t.Errorf("sprint env first: %q %v", got, err)
	}
	if got, err := redisAddress("", env(map[string]string{"NOVA_REDIS_ADDR": "127.0.0.1:3"})); err != nil || got != "127.0.0.1:3" {
		t.Errorf("redis addr env: %q %v", got, err)
	}
	if _, err := redisAddress("", env(nil)); err == nil || !strings.Contains(err.Error(), "--redis is required") {
		t.Errorf("none: %v", err)
	}
	if got, err := actorName("", env(map[string]string{"NOVA_FRIEND": "stella"})); err != nil || got != "stella" {
		t.Errorf("actor env: %q %v", got, err)
	}
	if _, err := actorName("", env(nil)); err == nil || !strings.Contains(err.Error(), "--as is required") {
		t.Errorf("no actor: %v", err)
	}
}

func TestTheSixVerbsEndToEndOnTheFake(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "rowan"
	step := func(want int, args ...string) (string, string) {
		t.Helper()
		code, out, errs := h.run(t, args...)
		if code != want {
			t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		}
		return out, errs
	}
	out, _ := step(0, "machine", "add", "studio", "--ssh", "studio", "--os_arch", "darwin/arm64", "--slots", "64", "--roles", "runner,bench", "--note", "Glenn's Mac Studio")
	if out != "CONFIG ADD kind=machine name=studio rev=1\n" {
		t.Fatalf("machine add: %q", out)
	}
	_, errs := step(1, "machine", "add", "studio", "--ssh", "studio", "--os_arch", "darwin/arm64", "--slots", "64")
	if errs != "nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>\n" {
		t.Fatalf("duplicate: %q", errs)
	}
	_, errs = step(1, "friend", "add", "rowan", "--machine", "hulk", "--slots", "32")
	if !strings.Contains(errs, "--machine hulk names no machine row") {
		t.Fatalf("no machine row: %q", errs)
	}
	out, _ = step(0, "friend", "add", "rowan", "--machine", "studio", "--slots", "32", "--roles", "coordinator,builder", "--logins", "rowan-claude", "--wake", "unit:rowan@studio", "--harness", "claude")
	if out != "CONFIG ADD kind=friend name=rowan rev=2\n" {
		t.Fatalf("friend add: %q", out)
	}
	out, _ = step(0, "friend", "set", "rowan", "--slots", "64", "--note", "wider")
	if out != "CONFIG SET kind=friend name=rowan rev=3 changed=note,slots\n" {
		t.Fatalf("friend set: %q", out)
	}
	_, errs = step(1, "friend", "set", "nobody", "--slots", "1")
	if errs != "nova-config friend set: friend nobody not found; run: nova-config friend add nobody --<field> <value> ...\n" {
		t.Fatalf("set nobody: %q", errs)
	}
	out, _ = step(0, "friend", "list")
	if out != "FRIEND name=rowan machine=studio slots=64 harness=claude wake=unit:rowan@studio roles=builder,coordinator logins=rowan-claude note=wider\nCONFIG LIST kind=friend rows=1\n" {
		t.Fatalf("friend list: %q", out)
	}
	out, _ = step(0, "friend", "show", "rowan")
	if !strings.HasPrefix(out, "FRIEND name=rowan machine=studio slots=64 ") || !strings.Contains(out, " created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z\n") {
		t.Fatalf("friend show: %q", out)
	}
	_, errs = step(1, "friend", "show", "nobody")
	if errs != "nova-config friend show: friend nobody not found; run: nova-config friend list\n" {
		t.Fatalf("show nobody: %q", errs)
	}
	out, _ = step(0, "friend", "history", "rowan")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "HISTORY id=2 kind=friend name=rowan op=add actor=rowan at=") || !strings.HasPrefix(lines[1], "HISTORY id=3 kind=friend name=rowan op=set actor=rowan at=") || !strings.HasSuffix(lines[1], " note=->wider slots=32>64") || lines[2] != "CONFIG HISTORY kind=friend name=rowan changes=2" {
		t.Fatalf("friend history:\n%s", out)
	}
	_, errs = step(1, "friend", "history", "nobody")
	if !strings.Contains(errs, "friend nobody has no history: it was never added") {
		t.Fatalf("history nobody: %q", errs)
	}
	_, errs = step(1, "machine", "remove", "studio")
	if errs != "nova-config machine remove: machine studio is the --machine of friend rowan; run: nova-config machine list\n" {
		t.Fatalf("remove a machine in use: %q", errs)
	}
	out, _ = step(0, "friend", "remove", "rowan")
	if out != "CONFIG REMOVE kind=friend name=rowan rev=4\n" {
		t.Fatalf("friend remove: %q", out)
	}
	out, _ = step(0, "friend", "list")
	if out != "CONFIG LIST kind=friend rows=0\n" {
		t.Fatalf("empty list: %q", out)
	}
	// A store that does not answer is exit 2, not a refusal.
	h.env["NOVA_PG_DSN"] = "postgres://nova_config@127.0.0.1:5432/closed"
	_, errs = step(2, "friend", "list")
	if !strings.Contains(errs, "connection refused") {
		t.Fatalf("closed store: %q", errs)
	}
}

func TestApplyStatusAndMigrateOnTheFakes(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "rowan"
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	step := func(want int, args ...string) (string, string) {
		t.Helper()
		code, out, errs := h.run(t, args...)
		if code != want {
			t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		}
		return out, errs
	}
	h.store.version = 0
	out, errs := step(1, "status")
	if out != "CONFIG STATUS pg=nova_config@127.0.0.1:5432/nova schema=0 redis=-\n" || !strings.Contains(errs, "run: nova-config migrate") {
		t.Fatalf("status before migrate: %q %q", out, errs)
	}
	out, _ = step(0, "migrate")
	if out != "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=0 to=3 applied=3\n" {
		t.Fatalf("migrate: %q", out)
	}
	out, _ = step(0, "migrate")
	if out != "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=3 to=3 applied=0\n" {
		t.Fatalf("migrate twice: %q", out)
	}
	step(0, "machine", "add", "studio", "--ssh", "studio", "--os_arch", "darwin/arm64", "--slots", "64")
	step(0, "friend", "add", "rowan", "--machine", "studio", "--slots", "32", "--roles", "coordinator")
	step(0, "friend", "add", "stella", "--machine", "studio", "--slots", "32")
	out, errs = step(1, "status")
	if out != "CONFIG STATUS pg=nova_config@127.0.0.1:5432/nova schema=3 machine=1 machine_rev=1 friend=2 friend_rev=3 redis=127.0.0.1:6379 machine_applied=0 friend_applied=0\n" || !strings.Contains(errs, "Redis is not at Postgres's revision for 2 kind(s); run: nova-config apply") {
		t.Fatalf("status behind: %q %q", out, errs)
	}
	out, _ = step(0, "apply", "--check")
	want := "CHECK ADD kind=machine name=studio\nCONFIG CHECK kind=machine add=1 set=0 remove=0 rev=1 applied=0\nCHECK ADD kind=friend name=rowan\nCHECK ADD kind=friend name=stella\nCONFIG CHECK kind=friend add=2 set=0 remove=0 rev=3 applied=0\n"
	if out != want {
		t.Fatalf("apply --check:\n%s\nwant:\n%s", out, want)
	}
	if len(h.redis.log) != 0 || len(h.redis.revs) != 0 {
		t.Fatalf("--check wrote: %v %v", h.redis.log, h.redis.revs)
	}
	out, _ = step(0, "apply")
	want = "APPLY ADD kind=machine name=studio\nCONFIG APPLY kind=machine add=1 set=0 remove=0 rev=1 ms=0\nAPPLY ADD kind=friend name=rowan\nAPPLY ADD kind=friend name=stella\nCONFIG APPLY kind=friend add=2 set=0 remove=0 rev=3 ms=0\n"
	if out != want {
		t.Fatalf("apply:\n%s\nwant:\n%s", out, want)
	}
	if strings.Join(h.redis.log, " ") != "write machine studio write friend rowan write friend stella" || h.redis.revs["friend"] != 3 || h.redis.revs["machine"] != 1 {
		t.Fatalf("redis after apply: %v %v", h.redis.log, h.redis.revs)
	}
	out, _ = step(0, "status")
	if !strings.HasSuffix(out, " machine_applied=1 friend_applied=3\n") {
		t.Fatalf("status after apply: %q", out)
	}
	out, _ = step(0, "apply", "--kind", "friend")
	if out != "CONFIG APPLY kind=friend add=0 set=0 remove=0 rev=3 ms=0\n" {
		t.Fatalf("second apply: %q", out)
	}
	h.redis.revs["friend"] = 9
	_, errs = step(1, "apply", "--kind", "friend")
	if !strings.HasPrefix(errs, "nova-config apply: CONFLICT friend: Redis holds rev 9 and this Postgres is at rev 3; a newer Postgres applied it; run: nova-config status") {
		t.Fatalf("conflict: %q", errs)
	}
}
