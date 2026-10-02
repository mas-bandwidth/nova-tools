package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memStore is config.Mem with the schema verbs a pgStore has: a fresh Mem
// is at the current version.
type memStore struct {
	*config.Mem
	version int
	ledger  []int
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

// Applied is the ledger: ledger when a test sets one (a gap), else every
// version up to the store's.
func (m *memStore) Applied(context.Context) ([]int, error) {
	if m.ledger != nil {
		return m.ledger, nil
	}
	var out []int
	for v := 1; v <= m.version; v++ {
		out = append(out, v)
	}
	return out, nil
}
func (m *memStore) Close() error { return nil }

// fakeRedis records what apply asked for, and holds the beats list and
// show read live.
type fakeRedis struct {
	views map[string]map[string]config.View
	revs  map[string]int64
	log   []string
	beats map[string]*config.Beat
	opens int
	// snapshots counts inventory's reads; hang holds every Snapshot until
	// its context ends, like a store that never answers.
	snapshots int
	hang      bool
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{views: map[string]map[string]config.View{}, revs: map[string]int64{}, beats: map[string]*config.Beat{}}
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
	// dir is where a --file path is opened ("" refuses one).
	dir string
}

func newHarness() *harness {
	return &harness{hostname: "elsewhere.example", store: &memStore{Mem: config.NewMem(), version: currentSchema()}, redis: newFakeRedis(), env: map[string]string{}}
}

// currentSchema is the version this binary's migrations reach: a fresh Mem
// is a store migrated to it.
func currentSchema() int {
	all, err := config.Migrations()
	if err != nil {
		panic(err)
	}
	return len(all)
}

func (h *harness) deps() deps {
	return deps{
		getenv: func(k string) string { return h.env[k] },
		openStore: func(_ context.Context, dsn string) (pgStore, error) {
			h.opens++
			if strings.Contains(dsn, "closed") {
				return nil, fmt.Errorf("postgres at %s: connection refused", config.Redact(dsn))
			}
			if path, ok := strings.CutPrefix(dsn, filePrefix); ok {
				// a --file is a real file store, under the test's own directory, on the fixed clock
				if h.dir == "" {
					return nil, fmt.Errorf("the harness has no directory for --file %s; set h.dir = t.TempDir()", path)
				}
				f, err := config.OpenFile(filepath.Join(h.dir, path))
				if err != nil {
					return nil, err
				}
				f.Now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
				return f, nil
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
		assert.Equal(t, 2, code, "%v: exit %d stdout %q stderr %q; want exit 2, one stderr line naming the door", args, code, out, errs)
		assert.Equal(t, "", out, "%v: exit %d stdout %q stderr %q; want exit 2, one stderr line naming the door", args, code, out, errs)
		assert.Equal(t, 1, strings.Count(errs, "\n"), "%v: exit %d stdout %q stderr %q; want exit 2, one stderr line naming the door", args, code, out, errs)
		door := "; run: nova-config help\n"
		if len(args) > 0 && args[0] != "nothing" && args[0] != "help" {
			door = "; run: nova-config " + args[0] + " -h\n"
		}
		assert.True(t, strings.HasSuffix(errs, door), "%v: exit %d stdout %q stderr %q; want exit 2, one stderr line naming the door %q", args, code, out, errs, door)
		assert.True(t, strings.HasPrefix(errs, "nova-config"), "%v: exit %d stdout %q stderr %q; want exit 2, one stderr line naming the door", args, code, out, errs)
		assert.Contains(t, errs, " REFUSED: ", "%v: the refusal carries its status word", args)
	}
	require.Equal(t, 0, h.opens, "a usage refusal opened the store")
}

func TestHelpEndsInRunnableExamplesAndVersionIsOneLine(t *testing.T) {
	t.Parallel()

	h := newHarness()
	code, out, _ := h.run(t, "help")
	require.Equal(t, 0, code, "help exit %d, tail %q", code, out[max(0, len(out)-80):])
	require.True(t, strings.HasSuffix(out, "\nexample:\n  "+strings.Join(firstRun(), "\n  ")+"\n"), "help exit %d, tail %q", code, out[max(0, len(out)-80):])
	assert.Less(t, len(out), 8000, "the banner is the three answers and the usage; each verb's detail is its -h")
	for _, k := range config.Kinds {
		for _, f := range k.Fields {
			assert.Contains(t, out, "--"+f.Name, "help does not name --%s of %s", f.Name, k.Name)
		}
	}
	for _, verb := range []string{"version", "--version"} {
		code, out, _ := h.run(t, verb)
		assert.Equal(t, 0, code, "%s: exit %d %q", verb, code, out)
		assert.Equal(t, 1, strings.Count(out, "\n"), "%s: exit %d %q", verb, code, out)
		assert.True(t, strings.HasPrefix(out, "nova-config "), "%s: exit %d %q", verb, code, out)
	}
}

func TestKindsAndMigratePrintNeedNoStore(t *testing.T) {
	t.Parallel()

	h := newHarness()
	code, out, errs := h.run(t, "kinds")
	require.Equal(t, 0, code, "kinds: %d %q %q", code, out, errs)
	require.Equal(t, "", errs, "kinds: %d %q %q", code, out, errs)
	require.True(t, strings.HasPrefix(out, "CONFIG KIND name=machine "), "kinds: %d %q %q", code, out, errs)
	require.True(t, strings.HasSuffix(out, "CONFIG KINDS count=7\n"), "kinds: %d %q %q", code, out, errs)
	code, out, errs = h.run(t, "migrate", "--print")
	require.Equal(t, 0, code, "migrate --print: %d %q %q", code, out, errs)
	require.Equal(t, "", errs, "migrate --print: %d %q %q", code, out, errs)
	require.True(t, strings.HasPrefix(out, "MIGRATION version=1 file=0001_schema.sql "), "migrate --print: %d %q %q", code, out, errs)
	require.True(t, strings.HasSuffix(out, fmt.Sprintf("CONFIG MIGRATE print=%d pg=-\n", currentSchema())), "migrate --print: %d %q %q", code, out, errs)
	require.Equal(t, 0, h.opens, "kinds or migrate --print opened the store")
}

func TestARefusalNamesEveryMissingFlagAtOnce(t *testing.T) {
	t.Parallel()

	h := newHarness()
	code, _, errs := h.run(t, "friend", "add", "rowan")
	require.Equal(t, 2, code, "exit %d", code)
	for _, want := range []string{"--as is required", "--pg is required", "--tiers is required", "--slots is required", "NOVA_FRIEND", "NOVA_PG_DSN"} {
		assert.Contains(t, errs, want, "the refusal does not say %q:\n%s", want, errs)
	}
	require.Equal(t, 1, strings.Count(errs, "\n"), "refusal is not one line:\n%s", errs)
	code, _, errs = h.run(t, "friend", "add", "--as", "rowan", "--pg", dsn, "--tiers", "pro", "--slots", "1")
	require.Equal(t, 2, code, "no name: %d %q", code, errs)
	require.Contains(t, errs, "the name is required", "no name: %d %q", code, errs)
	code, _, errs = h.run(t, "friend", "set", "rowan", "--as", "rowan", "--pg", dsn)
	require.Equal(t, 2, code, "set with no field: %d %q", code, errs)
	require.Contains(t, errs, "set names no field", "set with no field: %d %q", code, errs)
	code, _, errs = h.run(t, "friend", "list", "rowan", "--pg", dsn)
	require.Equal(t, 2, code, "list with a name: %d %q", code, errs)
	require.Contains(t, errs, "list takes no name", "list with a name: %d %q", code, errs)
	code, _, errs = h.run(t, "apply", "--kind", "lane", "--pg", dsn, "--redis", "127.0.0.1:6379", "--as", "rowan")
	require.Equal(t, 2, code, "apply --kind lane: %d %q", code, errs)
	require.Contains(t, errs, "--kind lane: want one of machine, fleet, friend, sprint, loop, route", "apply --kind lane: %d %q", code, errs)
	// The machine kind has no invented flag: an address is the name, a
	// measured fact is the beat's; the one free-text field is the note.
	for _, flag := range []string{"--ssh", "--os_arch", "--cores", "--roles", "--store", "--coordinator"} {
		code, _, errs = h.run(t, "machine", "add", "hulk", "--as", "rowan", "--pg", dsn, "--user", "gaffer", "--seat", "swarm-hulk", "--slots", "40", flag, "x")
		assert.Equal(t, 2, code, "machine add %s: %d %q", flag, code, errs)
		assert.Contains(t, errs, "REFUSED: unknown flag "+flag, "machine add %s: %d %q", flag, code, errs)
		assert.Contains(t, errs, "this verb takes --as, --dry-run, --file, --json, --note, --pg, --runners, --seat, --slots, --user, --width; run: nova-config machine add -h", "machine add %s: the flags it takes", flag)
		assert.NotContains(t, errs, "flag provided but not defined", "machine add %s: never the flag package's stock line", flag)
	}
	// The friend kind has no runtime fact and no coordinator role: what
	// she would just know is her presence's; who coordinates is the
	// sprint's.
	for _, flag := range []string{"--machine", "--harness", "--logins", "--wake", "--note"} {
		code, _, errs = h.run(t, "friend", "add", "emma", "--as", "rowan", "--pg", dsn, "--slots", "8", "--tiers", "flash", flag, "x")
		assert.Equal(t, 2, code, "friend add %s: %d %q", flag, code, errs)
		assert.Contains(t, errs, "REFUSED: unknown flag "+flag, "friend add %s: %d %q", flag, code, errs)
	}
	code, _, errs = h.run(t, "friend", "add", "emma", "--as", "rowan", "--pg", dsn, "--slots", "8", "--tiers", "flash", "--roles", "coordinator")
	assert.Equal(t, 2, code, "friend add --roles coordinator: %d %q", code, errs)
	assert.Contains(t, errs, "--roles \"coordinator\": want a comma list of builder, may-hold, reader", "friend add --roles coordinator: %d %q", code, errs)
	// A singleton takes no name.
	code, _, errs = h.run(t, "fleet", "set", "fleet", "--store", "hulk", "--as", "rowan", "--pg", dsn)
	require.Equal(t, 2, code, "fleet set with a name: %d %q", code, errs)
	require.Contains(t, errs, "fleet takes no name: it is one row", "fleet set with a name: %d %q", code, errs)
	code, _, errs = h.run(t, "fleet", "show", "fleet", "--pg", dsn)
	require.Equal(t, 2, code, "fleet show with a name: %d %q", code, errs)
	require.Contains(t, errs, "fleet takes no name", "fleet show with a name: %d %q", code, errs)
	require.Equal(t, 0, h.opens, "a usage refusal opened the store")
}

// pgDSN is the store a verb given only --pg resolves.
func pgDSN(flagValue string, getenv func(string) string) (string, error) {
	file := ""
	return conn{pg: &flagValue, file: &file}.dsn(getenv)
}

// --file stands in for PostgreSQL, alone: with --pg it is refused, and with
// NOVA_PG_DSN set it still wins (the flag is the explicit choice).
func TestFileIsTheStoreAndExclusiveWithPg(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	pg, file := "", "try.json"
	got, err := conn{pg: &pg, file: &file}.dsn(env(map[string]string{"NOVA_PG_DSN": dsn}))
	require.NoError(t, err)
	assert.Equal(t, "file:try.json", got)
	pg = dsn
	_, err = conn{pg: &pg, file: &file}.dsn(env(nil))
	assert.ErrorContains(t, err, "--pg and --file are exclusive")
	pg, file = "", ""
	_, err = conn{pg: &pg, file: &file}.dsn(env(nil))
	assert.ErrorContains(t, err, "or --file <path> for a local file with no database")
}

func TestPgDSNKeepsThePasswordOffTheLine(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	_, errCheck321 := pgDSN("", env(nil))
	assert.ErrorContains(t, errCheck321, "--pg is required", "no dsn: %v", errCheck321)
	_, errCheck324 := pgDSN("postgres://u:secret@127.0.0.1/nova", env(nil))
	assert.ErrorContains(t, errCheck324, "carries a password", "password on the line: %v", errCheck324)
	got, err := pgDSN(dsn, env(map[string]string{"NOVA_PG_PASSWORD": "pw"}))
	assert.NoError(t, err, "default variable: %q %v", got, err)
	assert.Equal(t, "postgres://nova_config:pw@127.0.0.1:5432/nova", got, "default variable: %q %v", got, err)
	got, err = pgDSN(dsn, env(map[string]string{"NOVA_PG_PASSWORD_ENV": "NOVA_SECRET_PG", "NOVA_SECRET_PG": "p w"}))
	assert.NoError(t, err, "named variable: %q %v", got, err)
	assert.Equal(t, "postgres://nova_config:p%20w@127.0.0.1:5432/nova", got, "named variable: %q %v", got, err)
	_, err = pgDSN(dsn, env(map[string]string{"NOVA_PG_PASSWORD_ENV": "NOVA_SECRET_PG"}))
	assert.ErrorContains(t, err, "NOVA_PG_PASSWORD_ENV=NOVA_SECRET_PG but NOVA_SECRET_PG is empty; run under nova-secrets exec --only NOVA_SECRET_PG", "named but empty: %v", err)
	got, err = pgDSN("", env(map[string]string{"NOVA_PG_DSN": dsn}))
	assert.NoError(t, err, "no password anywhere (a throwaway trusts): %q %v", got, err)
	assert.Equal(t, dsn, got, "no password anywhere (a throwaway trusts): %q %v", got, err)
	got, err = pgDSN("host=127.0.0.1 user=nova_config dbname=nova", env(map[string]string{"NOVA_PG_PASSWORD": "it's"}))
	assert.NoError(t, err, "keyword dsn: %q %v", got, err)
	assert.Equal(t, `host=127.0.0.1 user=nova_config dbname=nova password='it\'s'`, got, "keyword dsn: %q %v", got, err)
	_, errCheck347 := pgDSN("postgres://[bad", env(nil))
	assert.ErrorContains(t, errCheck347, "--pg:", "unparsable: %v", errCheck347)
}

func TestRedisAddressAndActorFallBackToTheEnvironment(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	gotCheck356, errCheck356 := redisAddress("127.0.0.1:1", env(map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:2"}))
	assert.NoError(t, errCheck356, "flag wins: %q %v", gotCheck356, errCheck356)
	assert.Equal(t, "127.0.0.1:1", gotCheck356, "flag wins: %q %v", gotCheck356, errCheck356)
	gotCheck359, errCheck359 := redisAddress("", env(map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:2", "NOVA_REDIS_ADDR": "127.0.0.1:3"}))
	assert.NoError(t, errCheck359, "sprint env first: %q %v", gotCheck359, errCheck359)
	assert.Equal(t, "127.0.0.1:2", gotCheck359, "sprint env first: %q %v", gotCheck359, errCheck359)
	gotCheck362, errCheck362 := redisAddress("", env(map[string]string{"NOVA_REDIS_ADDR": "127.0.0.1:3"}))
	assert.NoError(t, errCheck362, "redis addr env: %q %v", gotCheck362, errCheck362)
	assert.Equal(t, "127.0.0.1:3", gotCheck362, "redis addr env: %q %v", gotCheck362, errCheck362)
	_, errCheck365 := redisAddress("", env(nil))
	assert.ErrorContains(t, errCheck365, "--redis is required", "none: %v", errCheck365)
	gotCheck368, errCheck368 := actorName("", env(map[string]string{"NOVA_FRIEND": "stella"}))
	assert.NoError(t, errCheck368, "actor env: %q %v", gotCheck368, errCheck368)
	assert.Equal(t, "stella", gotCheck368, "actor env: %q %v", gotCheck368, errCheck368)
	_, errCheck371 := actorName("", env(nil))
	assert.ErrorContains(t, errCheck371, "--as is required", "no actor: %v", errCheck371)
}

func TestTheSixVerbsEndToEndOnTheFake(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "rowan"
	step := func(want int, args ...string) (string, string) {
		t.Helper()
		code, out, errs := h.run(t, args...)
		require.Equal(t, want, code, "%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		return out, errs
	}
	out, _ := step(0, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64", "--runners", "1")
	require.Equal(t, "CONFIG ADD kind=machine name=studio rev=1\nNOTE machine=studio width=0: no sprint member, so it is dealt no work; its width is set apart from its slots; run: nova-config machine set studio --width <n> --as rowan\n", out, "machine add: %q", out)
	_, errs := step(1, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64")
	require.Equal(t, "nova-config machine add REFUSED: machine studio exists; run: nova-config machine set studio --<field> <value>\n", errs, "duplicate: %q", errs)
	out, _ = step(0, "friend", "add", "rowan", "--slots", "32", "--tiers", "pro,frontier", "--roles", "builder")
	require.Equal(t, "CONFIG ADD kind=friend name=rowan rev=2\n", out, "friend add: %q", out)
	out, _ = step(0, "friend", "set", "rowan", "--slots", "64", "--roles", "builder,reader")
	require.Equal(t, "CONFIG SET kind=friend name=rowan rev=3 changed=roles,slots\n", out, "friend set: %q", out)
	_, errs = step(1, "friend", "set", "nobody", "--slots", "1")
	require.Equal(t, "nova-config friend set REFUSED: friend nobody not found; run: nova-config friend add nobody --<field> <value> ...\n", errs, "set nobody: %q", errs)
	out, _ = step(0, "friend", "list")
	require.Equal(t, "FRIEND name=rowan slots=64 tiers=frontier,pro roles=builder,reader\nCONFIG LIST kind=friend rows=1\n", out, "friend list: %q", out)
	out, _ = step(0, "friend", "show", "rowan")
	require.True(t, strings.HasPrefix(out, "FRIEND name=rowan slots=64 tiers=frontier,pro roles=builder,reader"), "friend show: %q", out)
	require.Contains(t, out, " created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z\n", "friend show: %q", out)
	_, errs = step(1, "friend", "show", "nobody")
	require.Equal(t, "nova-config friend show REFUSED: friend nobody not found; run: nova-config friend list\n", errs, "show nobody: %q", errs)
	out, _ = step(0, "friend", "history", "rowan")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3, "friend history:\n%s", out)
	require.True(t, strings.HasPrefix(lines[0], "HISTORY id=2 kind=friend name=rowan op=add actor=rowan at="), "friend history:\n%s", out)
	require.True(t, strings.HasPrefix(lines[1], "HISTORY id=3 kind=friend name=rowan op=set actor=rowan at="), "friend history:\n%s", out)
	require.True(t, strings.HasSuffix(lines[1], " roles=builder>builder,reader slots=32>64"), "friend history:\n%s", out)
	require.Equal(t, "CONFIG HISTORY kind=friend name=rowan changes=2", lines[2], "friend history:\n%s", out)
	_, errs = step(1, "friend", "history", "nobody")
	require.Contains(t, errs, "friend nobody has no history: it was never added", "history nobody: %q", errs)
	// The sprint row: who coordinates; a friend it names stays.
	_, errs = step(1, "sprint", "set", "--coordinator", "nobody")
	require.Equal(t, "nova-config sprint set REFUSED: --coordinator nobody names no friend row; run: nova-config friend list\n", errs, "sprint set naming no friend: %q", errs)
	out, _ = step(0, "sprint", "set", "--coordinator", "rowan")
	require.Equal(t, "CONFIG SET kind=sprint name=sprint rev=4 changed=coordinator\n", out, "sprint set: %q", out)
	_, errs = step(1, "friend", "remove", "rowan")
	require.Equal(t, "nova-config friend remove REFUSED: friend rowan is the --coordinator of the sprint; run: nova-config friend list\n", errs, "remove the coordinating friend: %q", errs)
	step(0, "sprint", "set", "--coordinator", "")
	out, _ = step(0, "friend", "remove", "rowan")
	require.Equal(t, "CONFIG REMOVE kind=friend name=rowan rev=6\n", out, "friend remove: %q", out)
	out, _ = step(0, "friend", "list")
	require.Equal(t, "CONFIG LIST kind=friend rows=0\n", out, "empty list: %q", out)

	// The machine's lines: declared fields only without a Redis, the live
	// measured facts after them with one (from the beat; none for a
	// machine that has not beaten).
	out, _ = step(0, "machine", "add", "hulk", "--user", "gaffer", "--seat", "swarm-hulk", "--slots", "40", "--runners", "0")
	require.True(t, strings.HasPrefix(out, "CONFIG ADD kind=machine name=hulk rev=7\nNOTE machine=hulk width=0: "), "machine add hulk: %q", out)
	out, _ = step(0, "machine", "list")
	require.Equal(t, "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 width=0 note=-\nMACHINE name=studio user=glenn seat=studio slots=64 runners=1 width=0 note=-\nCONFIG LIST kind=machine rows=2\n", out, "machine list: %q", out)
	require.Equal(t, 0, h.redis.opens, "a list with no --redis opened Redis")
	h.redis.beats["hulk"] = &config.Beat{Cores: "64", At: "2026-09-27T03:00:00Z"}
	out, _ = step(0, "machine", "list", "--redis", "127.0.0.1:6379")
	require.Equal(t, "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 width=0 note=- os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z\nMACHINE name=studio user=glenn seat=studio slots=64 runners=1 width=0 note=- beat=none\nCONFIG LIST kind=machine rows=2\n", out, "machine list --redis: %q", out)
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	out, _ = step(0, "machine", "show", "hulk")
	require.True(t, strings.HasPrefix(out, "MACHINE name=hulk user=gaffer seat=swarm-hulk slots=40 runners=0 width=0 note=- created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z loops=- os=- arch=- cores=64 memory_gb=- beat=2026-09-27T03:00:00Z\n"), "machine show with NOVA_SPRINT_REDIS: %q", out)
	delete(h.env, "NOVA_SPRINT_REDIS")
	out, _ = step(0, "machine", "show", "studio")
	require.Equal(t, "MACHINE name=studio user=glenn seat=studio slots=64 runners=1 width=0 note=- created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z loops=-\n", out, "machine show without a Redis: %q", out)

	// The fleet: one row, there from the start, set without a name, its
	// history the sets alone.
	out, _ = step(0, "fleet", "show")
	require.Equal(t, "FLEET name=fleet store=- coordinator=- redis_port=- pg_dsn=- created=2023-11-14T22:13:20Z updated=2023-11-14T22:13:20Z\n", out, "fleet show before a set: %q", out)
	out, _ = step(0, "fleet", "history")
	require.Equal(t, "CONFIG HISTORY kind=fleet name=fleet changes=0\n", out, "fleet history before a set: %q", out)
	_, errs = step(1, "fleet", "set", "--store", "space")
	require.Equal(t, "nova-config fleet set REFUSED: --store space names no machine row; run: nova-config machine list\n", errs, "fleet set naming no machine: %q", errs)
	out, _ = step(0, "fleet", "set", "--store", "hulk", "--coordinator", "studio")
	require.Equal(t, "CONFIG SET kind=fleet name=fleet rev=8 changed=coordinator,store\n", out, "fleet set: %q", out)
	out, _ = step(0, "fleet", "show")
	require.True(t, strings.HasPrefix(out, "FLEET name=fleet store=hulk coordinator=studio redis_port=- pg_dsn=- created="), "fleet show: %q", out)
	_, errs = step(1, "machine", "remove", "hulk")
	require.Equal(t, "nova-config machine remove REFUSED: machine hulk is the --store of the fleet; run: nova-config machine list\n", errs, "remove the store machine: %q", errs)
	out, _ = step(0, "fleet", "set", "--store", "")
	require.Equal(t, "CONFIG SET kind=fleet name=fleet rev=9 changed=store\n", out, "fleet clear: %q", out)
	out, _ = step(0, "fleet", "history")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3, "fleet history:\n%s", out)
	require.True(t, strings.HasSuffix(lines[0], " coordinator=->studio store=->hulk"), "fleet history:\n%s", out)
	require.True(t, strings.HasSuffix(lines[1], " store=hulk>-"), "fleet history:\n%s", out)
	require.Equal(t, "CONFIG HISTORY kind=fleet name=fleet changes=2", lines[2], "fleet history:\n%s", out)
	step(0, "machine", "remove", "hulk")
	// A store that does not answer is exit 2, not a refusal.
	h.env["NOVA_PG_DSN"] = "postgres://nova_config@127.0.0.1:5432/closed"
	_, errs = step(2, "friend", "list")
	require.Contains(t, errs, "connection refused", "closed store: %q", errs)
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
		require.Equal(t, want, code, "%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		return out, errs
	}
	h.store.version = 0
	out, errs := step(1, "status")
	require.Equal(t, "CONFIG STATUS pg=nova_config@127.0.0.1:5432/nova schema=0 redis=-\n", out, "status before migrate: %q %q", out, errs)
	require.Contains(t, errs, "run: nova-config migrate", "status before migrate: %q %q", out, errs)
	out, _ = step(0, "migrate")
	n := currentSchema()
	require.Equal(t, fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=0 to=%d applied=%d\n", n, n), out, "migrate: %q", out)
	out, _ = step(0, "migrate")
	require.Equal(t, fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=0\n", n, n), out, "migrate twice: %q", out)
	step(0, "machine", "add", "studio", "--user", "glenn", "--seat", "studio", "--slots", "64")
	step(0, "friend", "add", "rowan", "--slots", "32", "--tiers", "frontier", "--roles", "builder")
	step(0, "friend", "add", "stella", "--slots", "32", "--tiers", "frontier,pro")
	step(0, "fleet", "set", "--coordinator", "studio", "--redis_port", "6380", "--pg_dsn", dsn)
	step(0, "sprint", "set", "--coordinator", "rowan")
	out, errs = step(1, "status")
	require.Equal(t, "CONFIG STATUS pg=nova_config@127.0.0.1:5432/nova schema="+strconv.Itoa(n)+" machine=1 machine_rev=1 fleet_rev=4 friend=2 friend_rev=3 sprint_rev=5 loop=0 loop_rev=0 route=0 route_rev=0 tier=2 tier_rev=0 redis=127.0.0.1:6379 machine_applied=0 fleet_applied=0 friend_applied=0 sprint_applied=0 loop_applied=0 route_applied=0 tier_applied=0\n", out, "status behind: %q %q", out, errs)
	require.Contains(t, errs, "status REFUSED: Redis is not at the store's revision for 4 kind(s); run: nova-config apply", "status behind: %q %q", out, errs)
	delete(h.env, "NOVA_FRIEND")
	out, _ = step(0, "apply", "--check")
	want := "CHECK ADD kind=machine name=studio\nCONFIG CHECK kind=machine add=1 set=0 remove=0 rev=1 applied=0\nCHECK SET kind=fleet name=fleet changed=coordinator,redis_port,pg_dsn\nCONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=4 applied=0\nCHECK ADD kind=friend name=rowan\nCHECK ADD kind=friend name=stella\nCONFIG CHECK kind=friend add=2 set=0 remove=0 rev=3 applied=0\nCHECK SET kind=sprint name=sprint changed=coordinator\nCONFIG CHECK kind=sprint add=0 set=1 remove=0 rev=5 applied=0\nCONFIG CHECK kind=loop add=0 set=0 remove=0 rev=0 applied=0\nCONFIG CHECK kind=route add=0 set=0 remove=0 rev=0 applied=0\nCHECK ADD kind=tier name=flash\nCHECK ADD kind=tier name=pro\nCONFIG CHECK kind=tier add=2 set=0 remove=0 rev=0 applied=0\n"
	require.Equal(t, want, out, "apply --check without --as:\n%s\nwant:\n%s", out, want)
	require.Len(t, h.redis.log, 0, "--check wrote: %v %v", h.redis.log, h.redis.revs)
	require.Len(t, h.redis.revs, 0, "--check wrote: %v %v", h.redis.log, h.redis.revs)
	out, _ = step(0, "apply", "--check", "--as", "rowan")
	require.Equal(t, want, out, "apply --check with --as:\n%s\nwant:\n%s", out, want)
	// Real apply without --as or NOVA_FRIEND refuses.
	_, errs = step(2, "apply")
	require.Contains(t, errs, "apply REFUSED: --as is required: the name the write is recorded under (or NOVA_FRIEND); run: nova-config apply -h", "apply without --as refusal: %q", errs)
	h.env["NOVA_FRIEND"] = "rowan"
	out, _ = step(0, "apply")
	want = "APPLY ADD kind=machine name=studio\nCONFIG APPLY kind=machine add=1 set=0 remove=0 rev=1 ms=0\nAPPLY SET kind=fleet name=fleet changed=coordinator,redis_port,pg_dsn\nCONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0\nAPPLY ADD kind=friend name=rowan\nAPPLY ADD kind=friend name=stella\nCONFIG APPLY kind=friend add=2 set=0 remove=0 rev=3 ms=0\nAPPLY SET kind=sprint name=sprint changed=coordinator\nCONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=5 ms=0\nCONFIG APPLY kind=loop add=0 set=0 remove=0 rev=0 ms=0\nCONFIG APPLY kind=route add=0 set=0 remove=0 rev=0 ms=0\nAPPLY ADD kind=tier name=flash\nAPPLY ADD kind=tier name=pro\nCONFIG APPLY kind=tier add=2 set=0 remove=0 rev=0 ms=0\n"
	require.Equal(t, want, out, "apply:\n%s\nwant:\n%s", out, want)
	require.Equal(t, "write machine studio write fleet fleet write friend rowan write friend stella write sprint sprint write tier flash write tier pro", strings.Join(h.redis.log, " "), "redis after apply: %v %v", h.redis.log, h.redis.revs)
	require.Equal(t, int64(3), h.redis.revs["friend"], "redis after apply: %v %v", h.redis.log, h.redis.revs)
	require.Equal(t, int64(1), h.redis.revs["machine"], "redis after apply: %v %v", h.redis.log, h.redis.revs)
	require.Equal(t, int64(4), h.redis.revs["fleet"], "redis after apply: %v %v", h.redis.log, h.redis.revs)
	require.Equal(t, int64(5), h.redis.revs["sprint"], "redis after apply: %v %v", h.redis.log, h.redis.revs)
	// The sprint's coordinator carries the role in what apply wrote; the
	// stored row does not.
	gotCheck594 := h.redis.views["friend"]["rowan"]["roles"]
	require.Equal(t, "builder,coordinator", gotCheck594, "rowan's applied roles %q", gotCheck594)
	out, _ = step(0, "friend", "show", "rowan")
	require.True(t, strings.HasPrefix(out, "FRIEND name=rowan slots=32 tiers=frontier roles=builder created="), "rowan's stored row: %q", out)
	out, _ = step(0, "status")
	require.True(t, strings.HasSuffix(out, " machine_applied=1 fleet_applied=4 friend_applied=3 sprint_applied=5 loop_applied=0 route_applied=0 tier_applied=0\n"), "status after apply: %q", out)
	out, _ = step(0, "apply", "--kind", "friend")
	require.Equal(t, "CONFIG APPLY kind=friend add=0 set=0 remove=0 rev=3 ms=0\n", out, "second apply: %q", out)
	h.redis.revs["friend"] = 9
	_, errs = step(1, "apply", "--kind", "friend")
	require.True(t, strings.HasPrefix(errs, "nova-config apply REFUSED: CONFLICT friend: Redis holds rev 9 and this Postgres is at rev 3; a newer Postgres applied it; run: nova-config status"), "conflict: %q", errs)
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
		require.Equal(t, want, code, "%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		return out, errs
	}

	step(0, "migrate")
	step(0, "machine", "add", "bench-alpha", "--user", "user-a", "--seat", "seat-alpha", "--slots", "64", "--runners", "1")
	step(0, "machine", "add", "bench-beta", "--user", "user-b", "--seat", "seat-beta", "--slots", "40", "--runners", "0")
	step(0, "fleet", "set", "--store", "bench-beta", "--coordinator", "bench-alpha", "--redis_port", "6380", "--pg_dsn", dsn)

	// Apply so Redis and Postgres are synchronized
	step(0, "apply", "--kind", "machine")
	step(0, "apply", "--kind", "fleet")

	// Verify apply --check reports zero drift when synchronized
	out, _ := step(0, "apply", "--check", "--kind", "machine")
	require.Equal(t, "CONFIG CHECK kind=machine add=0 set=0 remove=0 rev=2 applied=2\n", out, "want no drift for machine, got:\n%s", out)
	out, _ = step(0, "apply", "--check", "--kind", "fleet")
	require.Equal(t, "CONFIG CHECK kind=fleet add=0 set=0 remove=0 rev=3 applied=3\n", out, "want no drift for fleet, got:\n%s", out)

	// 1. Detect drift: update machine slots in Postgres
	step(0, "machine", "set", "bench-beta", "--slots", "80")
	out, _ = step(0, "apply", "--check", "--kind", "machine")
	wantDrift := "CHECK SET kind=machine name=bench-beta changed=slots\nCONFIG CHECK kind=machine add=0 set=1 remove=0 rev=4 applied=2\n"
	require.Equal(t, wantDrift, out, "drift on machine slots:\ngot:\n%s\nwant:\n%s", out, wantDrift)

	// 2. Detect drift: add new machine in Postgres
	step(0, "machine", "add", "bench-gamma", "--user", "user-c", "--seat", "seat-gamma", "--slots", "32")
	out, _ = step(0, "apply", "--check", "--kind", "machine")
	wantDrift = "CHECK SET kind=machine name=bench-beta changed=slots\nCHECK ADD kind=machine name=bench-gamma\nCONFIG CHECK kind=machine add=1 set=1 remove=0 rev=5 applied=2\n"
	require.Equal(t, wantDrift, out, "drift on machine add+set:\ngot:\n%s\nwant:\n%s", out, wantDrift)

	// 3. Detect drift: change fleet coordinator in Postgres
	step(0, "fleet", "set", "--coordinator", "bench-beta")
	out, _ = step(0, "apply", "--check", "--kind", "fleet")
	wantDrift = "CHECK SET kind=fleet name=fleet changed=coordinator\nCONFIG CHECK kind=fleet add=0 set=1 remove=0 rev=6 applied=3\n"
	require.Equal(t, wantDrift, out, "drift on fleet coordinator:\ngot:\n%s\nwant:\n%s", out, wantDrift)

	// 4. Detect drift: machine removed from Postgres but present in Redis
	// Manually inject a stale machine into fake redis
	h.redis.views["machine"]["bench-retired"] = config.View{"user": "nobody", "seat": "none", "slots": "10", "runners": "0"}
	out, _ = step(0, "apply", "--check", "--kind", "machine")
	require.Contains(t, out, "CHECK REMOVE kind=machine name=bench-retired", "drift on machine remove missing:\ngot:\n%s", out)

	// Verify apply --check wrote NOTHING to redis
	// Redis revs should still be 2 and 3
	require.Equal(t, int64(2), h.redis.revs["machine"], "apply --check wrote to redis: %v", h.redis.revs)
	require.Equal(t, int64(3), h.redis.revs["fleet"], "apply --check wrote to redis: %v", h.redis.revs)
}

func TestApplyRefusesMissingFleetEndpointsBeforeAnyMutation(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"apply"}, {"apply", "--check"}, {"apply", "--kind", "fleet"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.env["NOVA_PG_DSN"], h.env["NOVA_FRIEND"], h.env["NOVA_SPRINT_REDIS"] = dsn, "operator", "bench-beta:6380"
			code, out, errs := h.run(t, args...)
			require.Equal(t, 1, code, errs)
			require.Empty(t, out)
			require.Contains(t, errs, "endpoints are unset: redis_port, pg_dsn")
			require.Contains(t, errs, "fleet set --redis_port <port> --pg_dsn <dsn>")
			require.Equal(t, 1, strings.Count(errs, "fleet set"), errs)
			require.Equal(t, 1, h.opens, "the authoritative fleet is read before its endpoints can be checked")
			require.Zero(t, h.redis.opens)
			require.Empty(t, h.redis.log)
			code, _, errs = h.run(t, "apply", "--kind", "machine", "--check")
			require.Zero(t, code, errs)
		})
	}
}

// A store older than this binary can lack a later table or fleet endpoint
// column: status, apply, machine show and the later kinds refuse with the
// versions and the migrate command, and never reach the
// store's own "relation does not exist".
func TestVerbsOnAnOlderSchemaRefuseWithMigrate(t *testing.T) {
	t.Parallel()

	all, err := config.Migrations()
	require.NoError(t, err)
	for _, have := range []int{1, len(all) - 1} {
		for _, args := range [][]string{{"status"}, {"apply"}, {"apply", "--kind", "loop"}, {"apply", "--check"}, {"machine", "show", "studio"},
			{"fleet", "set", "--redis_port", "6380"}, {"fleet", "show"}, {"fleet", "history"},
			{"loop", "add", "l1", "--machine", "studio", "--argv", `["/bin/prog"]`, "--every", "5"},
			{"loop", "set", "l1", "--every", "6"},
			{"loop", "remove", "l1"},
			{"loop", "list"},
			{"loop", "show", "l1"},
			{"loop", "history", "l1"},
			{"route", "add", "r1", "--tier", "pro", "--provider", "p", "--model", "m", "--deadline", "60"},
			{"route", "set", "r1", "--tokens", "2"},
			{"route", "remove", "r1"},
			{"route", "list"},
			{"route", "show", "r1"},
			{"route", "history", "r1"},
			// the machine row carries the note since 0015: every verb that reads or writes the row waits for it
			{"machine", "add", "m9", "--user", "u", "--seat", "s", "--slots", "8"},
			{"machine", "set", "studio", "--note", "held"},
			{"machine", "remove", "studio"},
			{"machine", "list"},
			{"machine", "history", "studio"},
		} {
			h := newHarness()
			h.env["NOVA_PG_DSN"] = dsn
			h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
			h.env["NOVA_FRIEND"] = "rowan"
			h.store.version = have
			code, _, errs := h.run(t, args...)
			verb := args[0]
			if args[0] == "machine" || args[0] == "loop" || args[0] == "route" || args[0] == "fleet" {
				verb = args[0] + " " + args[1]
			}
			want := fmt.Sprintf("nova-config %s REFUSED: schema config is at version %d and this binary carries %d; run: nova-config migrate\n", verb, have, len(all))
			assert.Equal(t, 1, code, "%v at version %d", args, have)
			assert.Equal(t, want, errs, "%v at version %d", args, have)
			assert.NotContains(t, errs, "does not exist")
			assert.Equal(t, 0, h.redis.opens, "%v at version %d: Redis is not opened", args, have)
			assert.Empty(t, h.redis.log, "%v at version %d: nothing is written", args, have)
		}
	}
}

// --pg is repeated in the command the refusal names.
func TestOlderSchemaRefusalRepeatsPG(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_SPRINT_REDIS"] = "127.0.0.1:6379"
	h.env["NOVA_FRIEND"] = "rowan"
	h.store.version = 5
	code, _, errs := h.run(t, "machine", "show", "studio", "--pg", dsn)
	all, err := config.Migrations()
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Equal(t, fmt.Sprintf("nova-config machine show REFUSED: schema config is at version 5 and this binary carries %d; run: nova-config migrate --pg %s\n", len(all), dsn), errs)
}
