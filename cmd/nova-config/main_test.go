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

// fakeRedis records what apply asked for, and holds the beats list and
// show read live.
type fakeRedis struct {
	views map[string]map[string]config.View
	revs  map[string]int64
	log   []string
	beats map[string]*config.Beat
	// hosts is where each friend's own beat says she runs.
	hosts map[string]string
	opens int
	// snapshots counts inventory's reads; hang holds every Snapshot until
	// its context ends, like a store that never answers.
	snapshots int
	hang      bool
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{views: map[string]map[string]config.View{}, revs: map[string]int64{}, beats: map[string]*config.Beat{}, hosts: map[string]string{}}
}
func (f *fakeRedis) Read(_ context.Context, kind string) (map[string]config.View, int64, error) {
	out := map[string]config.View{}
	for n, v := range f.views[kind] {
		out[n] = v
	}
	if k, _ := config.Lookup(kind); k != nil && k.Singleton && out[kind] == nil {
		out[kind] = config.View{}
		for _, fl := range k.Fields {
			out[kind][fl.Name] = ""
		}
	}
	return out, f.revs[kind], nil
}
func (f *fakeRedis) Beats(_ context.Context, names []string) (map[string]*config.Beat, error) {
	out := map[string]*config.Beat{}
	for _, n := range names {
		if b := f.beats[n]; b != nil {
			out[n] = b
		}
	}
	return out, nil
}
func (f *fakeRedis) FriendHosts(_ context.Context, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		out[n] = f.hosts[n]
	}
	return out, nil
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

// Snapshot is the applied state the fake holds: its machine, fleet and
// loop views (loops only once rev:loop is stamped), its beats and revs.
func (f *fakeRedis) Snapshot(ctx context.Context) (*config.Snapshot, error) {
	f.snapshots++
	if f.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	snap := &config.Snapshot{Machines: map[string]config.View{}, Fleet: config.View{}, Beats: map[string]*config.Beat{}, Revs: map[string]int64{}}
	for n, v := range f.views[config.KindMachine] {
		snap.Machines[n] = v
	}
	for k, v := range f.views[config.KindFleet][config.KindFleet] {
		snap.Fleet[k] = v
	}
	for k, r := range f.revs {
		snap.Revs[k] = r
	}
	if _, ok := f.revs["loop"]; ok {
		snap.Loops = map[string]config.View{}
		for n, v := range f.views["loop"] {
			snap.Loops[n] = v
		}
	}
	for n, b := range f.beats {
		snap.Beats[n] = b
	}
	return snap, nil
}

// harness is one test's tool: a Mem store, a fake Redis, an environment
// map, a fixed clock.
type harness struct {
	store *memStore
	redis *fakeRedis
	env   map[string]string
	opens int
	// hostname is what the machine reports as its own name.
	hostname string
	// override, when set, is the store openStore hands out instead of store.
	override pgStore
	// tailnet is what `tailscale status --json` prints; "" is no tailnet.
	tailnet string
}

func newHarness() *harness {
	return &harness{hostname: "elsewhere.example", store: &memStore{Mem: config.NewMem(), version: 5}, redis: newFakeRedis(), env: map[string]string{}}
}

func (h *harness) deps() deps {
	return deps{
		getenv: func(k string) string { return h.env[k] },
		openStore: func(_ context.Context, dsn string) (pgStore, error) {
			h.opens++
			if strings.Contains(dsn, "closed") {
				return nil, fmt.Errorf("postgres at %s: connection refused", config.Redact(dsn))
			}
			if h.override != nil {
				return h.override, nil
			}
			return h.store, nil
		},
		openRedis: func(_ context.Context, addr string) (redisSide, error) { h.redis.opens++; return h.redis, nil },
		now:       func() time.Time { return time.Unix(1700000000, 0) },
		hostname:  func() (string, error) { return h.hostname, nil },
		tailscale: func(context.Context) ([]byte, error) {
			if h.tailnet == "" {
				return nil, config.ErrNoTailnet
			}
			return []byte(h.tailnet), nil
		},
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
	for _, args := range [][]string{{}, {"nothing"}, {"friend"}, {"friend", "fly"}, {"help", "x"}, {"version", "x"}, {"kinds", "x"}, {"fleet"}, {"fleet", "add"}, {"fleet", "remove"}, {"fleet", "list"}, {"sprint", "add"}, {"sprint", "list"}} {
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
	if code != 0 || !strings.HasSuffix(out, "example:\n  nova-config kinds\n  nova-config migrate --print\n  nova-config machine add -h\n") {
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
	if code != 0 || errs != "" || !strings.HasPrefix(out, "CONFIG KIND name=machine ") || !strings.HasSuffix(out, "CONFIG KINDS count=4\n") {
		t.Fatalf("kinds: %d %q %q", code, out, errs)
	}
	code, out, errs = h.run(t, "migrate", "--print")
	if code != 0 || errs != "" || !strings.HasPrefix(out, "MIGRATION version=1 file=0001_schema.sql ") || !strings.HasSuffix(out, "CONFIG MIGRATE print=5 pg=-\n") {
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
	for _, want := range []string{"--as is required", "--pg is required", "--tiers is required", "--slots is required", "NOVA_FRIEND", "NOVA_PG_DSN"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, errs)
		}
	}
	if strings.Count(errs, "\n") != 1 {
		t.Fatalf("refusal is not one line:\n%s", errs)
	}
	code, _, errs = h.run(t, "friend", "add", "--as", "rowan", "--pg", dsn, "--tiers", "pro", "--slots", "1")
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
	if code != 2 || !strings.Contains(errs, "--kind loop: want one of machine, fleet, friend, sprint") {
		t.Fatalf("apply --kind loop: %d %q", code, errs)
	}
	// The machine kind has no invented flag: an address is the name, a
	// measured fact is the beat's, a note is history.
	for _, flag := range []string{"--ssh", "--os_arch", "--cores", "--roles", "--note", "--store", "--coordinator"} {
		code, _, errs = h.run(t, "machine", "add", "hulk", "--as", "rowan", "--pg", dsn, "--user", "gaffer", "--seat", "swarm-hulk", "--slots", "40", flag, "x")
		if code != 2 || !strings.Contains(errs, "flag provided but not defined: "+strings.TrimPrefix(flag, "-")) {
			t.Errorf("machine add %s: %d %q", flag, code, errs)
		}
	}
	// The friend kind has no runtime fact and no coordinator role: what
	// she would just know is her presence's; who coordinates is the
	// sprint's.
	for _, flag := range []string{"--machine", "--harness", "--logins", "--wake", "--note"} {
		code, _, errs = h.run(t, "friend", "add", "emma", "--as", "rowan", "--pg", dsn, "--slots", "8", "--tiers", "flash", flag, "x")
		if code != 2 || !strings.Contains(errs, "flag provided but not defined: "+strings.TrimPrefix(flag, "-")) {
			t.Errorf("friend add %s: %d %q", flag, code, errs)
		}
	}
	code, _, errs = h.run(t, "friend", "add", "emma", "--as", "rowan", "--pg", dsn, "--slots", "8", "--tiers", "flash", "--roles", "coordinator")
	if code != 2 || !strings.Contains(errs, "--roles \"coordinator\": want a comma list of builder, may-hold, reader") {
		t.Errorf("friend add --roles coordinator: %d %q", code, errs)
	}
	// A singleton takes no name.
	code, _, errs = h.run(t, "fleet", "set", "fleet", "--store", "hulk", "--as", "rowan", "--pg", dsn)
	if code != 2 || !strings.Contains(errs, "fleet takes no name: it is one row") {
		t.Fatalf("fleet set with a name: %d %q", code, errs)
	}
	code, _, errs = h.run(t, "fleet", "show", "fleet", "--pg", dsn)
	if code != 2 || !strings.Contains(errs, "fleet takes no name") {
		t.Fatalf("fleet show with a name: %d %q", code, errs)
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
	out, _ := step(0, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64", "--runners", "1")
	if out != "CONFIG ADD kind=machine name=studio rev=1\n" {
		t.Fatalf("machine add: %q", out)
	}
	_, errs := step(1, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64")
	if errs != "nova-config machine add: machine studio exists; run: nova-config machine set studio --<field> <value>\n" {
		t.Fatalf("duplicate: %q", errs)
	}
	out, _ = step(0, "friend", "add", "rowan", "--slots", "32", "--tiers", "pro,frontier", "--roles", "builder")
	if out != "CONFIG ADD kind=friend name=rowan rev=2\n" {
		t.Fatalf("friend add: %q", out)
	}
	out, _ = step(0, "friend", "set", "rowan", "--slots", "64", "--roles", "builder,reader")
	if out != "CONFIG SET kind=friend name=rowan rev=3 changed=roles,slots\n" {
		t.Fatalf("friend set: %q", out)
	}
	_, errs = step(1, "friend", "set", "nobody", "--slots", "1")
	if errs != "nova-config friend set: friend nobody not found; run: nova-config friend add nobody --<field> <value> ...\n" {
		t.Fatalf("set nobody: %q", errs)
	}
	out, _ = step(0, "friend", "list")
	if out != "FRIEND name=rowan slots=64 tiers=frontier,pro roles=builder,reader\nCONFIG LIST kind=friend rows=1\n" {
		t.Fatalf("friend list: %q", out)
	}
	out, _ = step(0, "friend", "show", "rowan")
	if !strings.HasPrefix(out, "FRIEND name=rowan slots=64 tiers=frontier,pro roles=builder,reader") || !strings.Contains(out, " created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z\n") {
		t.Fatalf("friend show: %q", out)
	}
	_, errs = step(1, "friend", "show", "nobody")
	if errs != "nova-config friend show: friend nobody not found; run: nova-config friend list\n" {
		t.Fatalf("show nobody: %q", errs)
	}
	out, _ = step(0, "friend", "history", "rowan")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "HISTORY id=2 kind=friend name=rowan op=add actor=rowan at=") || !strings.HasPrefix(lines[1], "HISTORY id=3 kind=friend name=rowan op=set actor=rowan at=") || !strings.HasSuffix(lines[1], " roles=builder>builder,reader slots=32>64") || lines[2] != "CONFIG HISTORY kind=friend name=rowan changes=2" {
		t.Fatalf("friend history:\n%s", out)
	}
	_, errs = step(1, "friend", "history", "nobody")
	if !strings.Contains(errs, "friend nobody has no history: it was never added") {
		t.Fatalf("history nobody: %q", errs)
	}
	// The sprint row: who coordinates; a friend it names stays.
	_, errs = step(1, "sprint", "set", "--coordinator", "nobody")
	if errs != "nova-config sprint set: --coordinator nobody names no friend row; run: nova-config sprint show\n" {
		t.Fatalf("sprint set naming no friend: %q", errs)
	}
	out, _ = step(0, "sprint", "set", "--coordinator", "rowan")
	if out != "CONFIG SET kind=sprint name=sprint rev=4 changed=coordinator\n" {
		t.Fatalf("sprint set: %q", out)
	}
	_, errs = step(1, "friend", "remove", "rowan")
	if errs != "nova-config friend remove: friend rowan is the --coordinator of the sprint; run: nova-config friend list\n" {
		t.Fatalf("remove the coordinating friend: %q", errs)
	}
	step(0, "sprint", "set", "--coordinator", "")
	out, _ = step(0, "friend", "remove", "rowan")
	if out != "CONFIG REMOVE kind=friend name=rowan rev=6\n" {
		t.Fatalf("friend remove: %q", out)
	}
	out, _ = step(0, "friend", "list")
	if out != "CONFIG LIST kind=friend rows=0\n" {
		t.Fatalf("empty list: %q", out)
	}

	// The machine's lines: declared fields only without a Redis, the live
	// measured facts after them with one (from the beat; none for a
	// machine that has not beaten).
	out, _ = step(0, "machine", "add", "hulk", "--user", "gaffer", "--seat", "swarm-hulk", "--slots", "40", "--runners", "0")
	if out != "CONFIG ADD kind=machine name=hulk rev=7\n" {
		t.Fatalf("machine add hulk: %q", out)
	}
	out, _ = step(0, "machine", "list")
	if out != "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0\nMACHINE name=studio user=glenn seat=studio slots=64 runners=1\nCONFIG LIST kind=machine rows=2\n" {
		t.Fatalf("machine list: %q", out)
	}
	if h.redis.opens != 0 {
		t.Fatal("a list with no --redis opened Redis")
	}
	h.redis.beats["hulk"] = &config.Beat{Cores: "64", At: "2026-09-27T03:00:00Z"}
	out, _ = step(0, "machine", "list", "--redis", "127.0.0.1:6379")
	if out != "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z\nMACHINE name=studio user=glenn seat=studio slots=64 runners=1 beat=none\nCONFIG LIST kind=machine rows=2\n" {
		t.Fatalf("machine list --redis: %q", out)
	}
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	out, _ = step(0, "machine", "show", "hulk")
	if !strings.HasPrefix(out, "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z\n") {
		t.Fatalf("machine show with NOVA_SPRINT_REDIS: %q", out)
	}
	delete(h.env, "NOVA_SPRINT_REDIS")
	out, _ = step(0, "machine", "show", "studio")
	if out != "MACHINE name=studio user=glenn seat=studio slots=64 runners=1 created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z\n" {
		t.Fatalf("machine show without a Redis: %q", out)
	}

	// The fleet: one row, there from the start, set without a name, its
	// history the sets alone.
	out, _ = step(0, "fleet", "show")
	if out != "FLEET name=fleet store=- coordinator=- created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z\n" {
		t.Fatalf("fleet show before a set: %q", out)
	}
	out, _ = step(0, "fleet", "history")
	if out != "CONFIG HISTORY kind=fleet name=fleet changes=0\n" {
		t.Fatalf("fleet history before a set: %q", out)
	}
	_, errs = step(1, "fleet", "set", "--store", "space")
	if errs != "nova-config fleet set: --store space names no machine row; run: nova-config fleet show\n" {
		t.Fatalf("fleet set naming no machine: %q", errs)
	}
	out, _ = step(0, "fleet", "set", "--store", "hulk", "--coordinator", "studio")
	if out != "CONFIG SET kind=fleet name=fleet rev=8 changed=coordinator,store\n" {
		t.Fatalf("fleet set: %q", out)
	}
	out, _ = step(0, "fleet", "show")
	if !strings.HasPrefix(out, "FLEET name=fleet store=hulk coordinator=studio created=") {
		t.Fatalf("fleet show: %q", out)
	}
	_, errs = step(1, "machine", "remove", "hulk")
	if errs != "nova-config machine remove: machine hulk is the --store of the fleet; run: nova-config machine list\n" {
		t.Fatalf("remove the store machine: %q", errs)
	}
	out, _ = step(0, "fleet", "set", "--store", "")
	if out != "CONFIG SET kind=fleet name=fleet rev=9 changed=store\n" {
		t.Fatalf("fleet clear: %q", out)
	}
	out, _ = step(0, "fleet", "history")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasSuffix(lines[0], " coordinator=->studio store=->hulk") || !strings.HasSuffix(lines[1], " store=hulk>-") || lines[2] != "CONFIG HISTORY kind=fleet name=fleet changes=2" {
		t.Fatalf("fleet history:\n%s", out)
	}
	step(0, "machine", "remove", "hulk")
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
	if out != "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=0 to=5 applied=5\n" {
		t.Fatalf("migrate: %q", out)
	}
	out, _ = step(0, "migrate")
	if out != "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=5 to=5 applied=0\n" {
		t.Fatalf("migrate twice: %q", out)
	}
	step(0, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64")
	step(0, "friend", "add", "rowan", "--slots", "32", "--tiers", "frontier", "--roles", "builder")
	step(0, "friend", "add", "stella", "--slots", "32", "--tiers", "frontier,pro")
	step(0, "fleet", "set", "--coordinator", "studio")
	step(0, "sprint", "set", "--coordinator", "rowan")
	out, errs = step(1, "status")
	if out != "CONFIG STATUS pg=nova_config@127.0.0.1:5432/nova schema=5 machine=1 machine_rev=1 fleet_rev=4 friend=2 friend_rev=3 sprint_rev=5 redis=127.0.0.1:6379 machine_applied=0 fleet_applied=0 friend_applied=0 sprint_applied=0\n" || !strings.Contains(errs, "Redis is not at Postgres's revision for 4 kind(s); run: nova-config apply") {
		t.Fatalf("status behind: %q %q", out, errs)
	}
	delete(h.env, "NOVA_FRIEND")
	out, _ = step(0, "apply", "--check")
	want := "CHECK ADD kind=machine name=studio\nCONFIG CHECK kind=machine add=1 set=0 remove=0 rev=1 applied=0\nCHECK SET kind=fleet name=fleet changed=coordinator\nCONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=4 applied=0\nCHECK ADD kind=friend name=rowan\nCHECK ADD kind=friend name=stella\nCONFIG CHECK kind=friend add=2 set=0 remove=0 rev=3 applied=0\nCHECK SET kind=sprint name=sprint changed=coordinator\nCONFIG CHECK kind=sprint add=0 set=1 remove=0 rev=5 applied=0\n"
	if out != want {
		t.Fatalf("apply --check without --as:\n%s\nwant:\n%s", out, want)
	}
	if len(h.redis.log) != 0 || len(h.redis.revs) != 0 {
		t.Fatalf("--check wrote: %v %v", h.redis.log, h.redis.revs)
	}
	out, _ = step(0, "apply", "--check", "--as", "rowan")
	if out != want {
		t.Fatalf("apply --check with --as:\n%s\nwant:\n%s", out, want)
	}
	// Real apply without --as or NOVA_FRIEND refuses.
	_, errs = step(2, "apply")
	if !strings.Contains(errs, "--as is required: the friend making the change (or NOVA_FRIEND); run: nova-config help") {
		t.Fatalf("apply without --as refusal: %q", errs)
	}
	h.env["NOVA_FRIEND"] = "rowan"
	out, _ = step(0, "apply")
	want = "APPLY ADD kind=machine name=studio\nCONFIG APPLY kind=machine add=1 set=0 remove=0 rev=1 ms=0\nAPPLY SET kind=fleet name=fleet changed=coordinator\nCONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0\nAPPLY ADD kind=friend name=rowan\nAPPLY ADD kind=friend name=stella\nCONFIG APPLY kind=friend add=2 set=0 remove=0 rev=3 ms=0\nAPPLY SET kind=sprint name=sprint changed=coordinator\nCONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=5 ms=0\n"
	if out != want {
		t.Fatalf("apply:\n%s\nwant:\n%s", out, want)
	}
	if strings.Join(h.redis.log, " ") != "write machine studio write fleet fleet write friend rowan write friend stella write sprint sprint" || h.redis.revs["friend"] != 3 || h.redis.revs["machine"] != 1 || h.redis.revs["fleet"] != 4 || h.redis.revs["sprint"] != 5 {
		t.Fatalf("redis after apply: %v %v", h.redis.log, h.redis.revs)
	}
	// The sprint's coordinator carries the role in what apply wrote; the
	// stored row does not.
	if got := h.redis.views["friend"]["rowan"]["roles"]; got != "builder,coordinator" {
		t.Fatalf("rowan's applied roles %q", got)
	}
	out, _ = step(0, "friend", "show", "rowan")
	if !strings.HasPrefix(out, "FRIEND name=rowan slots=32 tiers=frontier roles=builder created=") {
		t.Fatalf("rowan's stored row: %q", out)
	}
	out, _ = step(0, "status")
	if !strings.HasSuffix(out, " machine_applied=1 fleet_applied=4 friend_applied=3 sprint_applied=5\n") {
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

func TestApplyCheckReportsDriftAgainstFleet(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "operator"
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"

	step := func(want int, args ...string) (string, string) {
		t.Helper()
		code, out, errs := h.run(t, args...)
		if code != want {
			t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		}
		return out, errs
	}

	step(0, "migrate")
	step(0, "machine", "add", "bench-alpha", "--user", "user-a", "--seat", "seat-alpha", "--slots", "64", "--runners", "1")
	step(0, "machine", "add", "bench-beta", "--user", "user-b", "--seat", "seat-beta", "--slots", "40", "--runners", "0")
	step(0, "fleet", "set", "--store", "bench-beta", "--coordinator", "bench-alpha")

	// Apply so Redis and Postgres are synchronized
	step(0, "apply", "--kind", "machine")
	step(0, "apply", "--kind", "fleet")

	// Verify apply --check reports zero drift when synchronized
	out, _ := step(0, "apply", "--check", "--kind", "machine")
	if out != "CONFIG CHECK kind=machine add=0 set=0 remove=0 rev=2 applied=2\n" {
		t.Fatalf("want no drift for machine, got:\n%s", out)
	}
	out, _ = step(0, "apply", "--check", "--kind", "fleet")
	if out != "CONFIG CHECK kind=fleet add=0 set=0 remove=0 rev=3 applied=3\n" {
		t.Fatalf("want no drift for fleet, got:\n%s", out)
	}

	// 1. Detect drift: update machine slots in Postgres
	step(0, "machine", "set", "bench-beta", "--slots", "80")
	out, _ = step(0, "apply", "--check", "--kind", "machine")
	wantDrift := "CHECK SET kind=machine name=bench-beta changed=slots\nCONFIG CHECK kind=machine add=0 set=1 remove=0 rev=4 applied=2\n"
	if out != wantDrift {
		t.Fatalf("drift on machine slots:\ngot:\n%s\nwant:\n%s", out, wantDrift)
	}

	// 2. Detect drift: add new machine in Postgres
	step(0, "machine", "add", "bench-gamma", "--user", "user-c", "--seat", "seat-gamma", "--slots", "32")
	out, _ = step(0, "apply", "--check", "--kind", "machine")
	wantDrift = "CHECK SET kind=machine name=bench-beta changed=slots\nCHECK ADD kind=machine name=bench-gamma\nCONFIG CHECK kind=machine add=1 set=1 remove=0 rev=5 applied=2\n"
	if out != wantDrift {
		t.Fatalf("drift on machine add+set:\ngot:\n%s\nwant:\n%s", out, wantDrift)
	}

	// 3. Detect drift: change fleet coordinator in Postgres
	step(0, "fleet", "set", "--coordinator", "bench-beta")
	out, _ = step(0, "apply", "--check", "--kind", "fleet")
	wantDrift = "CHECK SET kind=fleet name=fleet changed=coordinator\nCONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=6 applied=3\n"
	if out != wantDrift {
		t.Fatalf("drift on fleet coordinator:\ngot:\n%s\nwant:\n%s", out, wantDrift)
	}

	// 4. Detect drift: machine removed from Postgres but present in Redis
	// Manually inject a stale machine into fake redis
	h.redis.views["machine"]["bench-retired"] = config.View{"user": "nobody", "seat": "none", "slots": "10", "runners": "0"}
	out, _ = step(0, "apply", "--check", "--kind", "machine")
	if !strings.Contains(out, "CHECK REMOVE kind=machine name=bench-retired") {
		t.Fatalf("drift on machine remove missing:\ngot:\n%s", out)
	}

	// Verify apply --check wrote NOTHING to redis
	// Redis revs should still be 2 and 3
	if h.redis.revs["machine"] != 2 || h.redis.revs["fleet"] != 3 {
		t.Fatalf("apply --check wrote to redis: %v", h.redis.revs)
	}
}
