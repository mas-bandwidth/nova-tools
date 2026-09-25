package main

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// #3320 DONE-WHEN: the preflight verb itself, not only the package's Open,
// authenticates through store.Open as the ACL user NOVA_SPRINT_REDIS_USER
// names, with the password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV
// names. Against an ACL Redis the coordinator seat gets past AUTH and the
// checks run; with no user the verb refuses (exit 2) and names the
// coordinator seat instead of printing a RED per check as the default user.
func TestPreflightAuthenticatesAsTheNamedUser(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("coordinator", "seat-secret")
	authFailures := []string{"WRONGPASS", "NOAUTH", "invalid username-password"}

	t.Run("coordinator seat gets past AUTH", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_REDIS_USER", "coordinator")
		t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD")
		t.Setenv("NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD", "seat-secret")
		t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "")
		code, stdout, stderr := runSprint("preflight", "--redis", mr.Addr())
		if code == 2 {
			t.Fatalf("exit 2 (could not run) as the coordinator seat; stderr %s", stderr)
		}
		if !strings.Contains(stdout, "7.1") {
			t.Fatalf("no 7.1 check line: the checks did not run as the seat\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		for _, bad := range authFailures {
			if strings.Contains(stdout+stderr, bad) {
				t.Fatalf("preflight did not get past AUTH as the coordinator seat (%s)\nstdout:\n%s\nstderr:\n%s", bad, stdout, stderr)
			}
		}
	})

	t.Run("no user refuses and names the coordinator seat", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_REDIS_USER", "")
		t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "")
		t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "seat-secret")
		code, stdout, stderr := runSprint("preflight", "--redis", mr.Addr())
		if code != 2 {
			t.Fatalf("exit %d, want 2: with no seat user preflight must refuse, not check as the default user\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("a refusal printed check lines on stdout:\n%s", stdout)
		}
		for _, want := range []string{"coordinator", "NOVA_SPRINT_REDIS_USER", "default user"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("refusal does not name %q:\n%s", want, stderr)
			}
		}
	})
}

// fleetFixture is an ACL miniredis at a fixed store clock holding six
// registered benches, each clean against fleetYML: a fresh beat naming the
// batch launcher, the declared build and harness, all six mirrors, free disk
// above the floor and desired slots. It returns the server and the all.yml
// path; the caller breaks one bench to see its row fail.
func fleetFixture(t *testing.T) (*miniredis.Miniredis, string, time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("coordinator", "seat-secret")
	t.Setenv("NOVA_SPRINT_REDIS_USER", "coordinator")
	t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD")
	t.Setenv("NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD", "seat-secret")
	now := time.UnixMilli(1790000000000)
	mr.SetTime(now)
	for _, b := range fleetBenches {
		mr.SAdd("benches", b)
		mr.HSet("bench:"+b+":beat",
			"at", strconv.FormatInt(now.Add(-500*time.Millisecond).UnixMilli(), 10),
			"launcher", preflight.BatchLauncherName,
			"build", "nova-sprint v0.16.0-dev.aa87769b darwin/arm64 go1.26.6",
			"harness", "1.18.19,1.18.20",
			"mirrors", "message-bus,nova-tools,nova-work,rowan-tools,schema,serialize",
			"disk_gib", "812")
		mr.HSet("bench:"+b+":desired", "slots", "24", "paused", "0")
	}
	mr.ZAdd("bench:hulk:living", 1, "card-1")
	mr.SAdd("sprints", "s1", "s0")
	mr.HSet("s:s1", "status", "open")
	mr.HSet("s:s0", "status", "closed")
	yml := filepath.Join(t.TempDir(), "all.yml")
	body := "nova_build: \"v0.16.0-dev.aa87769b\"\nharness_version: \"1.18.20\"\n" +
		"mirrors: [nova-tools, schema, nova-work, rowan-tools, message-bus, serialize]   # the one declared list\n"
	if err := os.WriteFile(yml, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return mr, yml, now
}

var fleetBenches = []string{"batman", "hulk", "studio", "superman", "thor", "wolverine"}

// loadedLibrary stands in for FUNCTION LIST, which miniredis does not serve:
// the store holds the binary's own library.
func loadedLibrary(t *testing.T) {
	t.Helper()
	prev := fleetLibrary
	fleetLibrary = func(ctx context.Context, c *redis.Client) preflight.Library {
		return preflight.Library{Have: "0123abcd", Want: "0123abcd"}
	}
	t.Cleanup(func() { fleetLibrary = prev })
}

// rowOf returns the output line for a bench, failing when there is not
// exactly one.
func rowOf(t *testing.T, stdout, bench string) string {
	t.Helper()
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.HasPrefix(l, bench+" ") {
			rows = append(rows, l)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows for %s, want exactly one:\n%s", len(rows), bench, stdout)
	}
	return rows[0]
}

// TestPreflightFleetRows is #3646's DONE-WHEN over six benches: a clean fleet
// prints one PASS row per bench and a PASS fleet line and exits 0; one bench
// with a 45 s beat and one whose build differs from the declared nova_build
// each print FAIL naming the field, the others stay PASS, and the verb exits 1.
func TestPreflightFleetRows(t *testing.T) {
	loadedLibrary(t)

	t.Run("clean fleet passes", func(t *testing.T) {
		mr, yml, _ := fleetFixture(t)
		code, stdout, stderr := runSprint("preflight", "--fleet", "--redis", mr.Addr(), "--all-yml", yml, "--sprint", "s1")
		if code != 0 {
			t.Fatalf("exit %d, want 0 on a clean fleet\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) != len(fleetBenches)+1 {
			t.Fatalf("%d lines, want one row per bench and one fleet line:\n%s", len(lines), stdout)
		}
		for i, b := range fleetBenches {
			if !strings.HasPrefix(lines[i], b+" PASS ") {
				t.Errorf("line %d is not %s PASS: %s", i, b, lines[i])
			}
		}
		want := "hulk PASS harness=1.18.19,1.18.20 build=aa87769b beat=1s mirrors=6/6 slots=23/24 disk=812GiB"
		if got := rowOf(t, stdout, "hulk"); got != want {
			t.Errorf("hulk row\n got %s\nwant %s", got, want)
		}
		last := lines[len(lines)-1]
		for _, w := range []string{"fleet PASS", "benches=6/6", "open=1", "pitstop=none", "fnlib=0123abcd", "7.4=GREEN", "7.5=GREEN"} {
			if !strings.Contains(last, w) {
				t.Errorf("fleet line lacks %q: %s", w, last)
			}
		}
	})

	t.Run("stale beat and wrong build fail their benches", func(t *testing.T) {
		mr, yml, now := fleetFixture(t)
		mr.HSet("bench:thor:beat", "at", strconv.FormatInt(now.Add(-45*time.Second).UnixMilli(), 10))
		mr.HSet("bench:batman:beat", "build", "nova-sprint v0.16.0-dev.3403baa7 darwin/amd64 go1.26.6")
		code, stdout, stderr := runSprint("preflight", "--fleet", "--redis", mr.Addr(), "--all-yml", yml)
		if code != 1 {
			t.Fatalf("exit %d, want 1 with failing benches\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if r := rowOf(t, stdout, "thor"); !strings.HasPrefix(r, "thor FAIL ") || !strings.Contains(r, "beat=45s") || !strings.Contains(r, "fail=beat(45s old") {
			t.Errorf("thor row does not fail on its 45 s beat: %s", r)
		}
		if r := rowOf(t, stdout, "batman"); !strings.HasPrefix(r, "batman FAIL ") || !strings.Contains(r, "build=3403baa7") || !strings.Contains(r, "fail=build(want aa87769b)") {
			t.Errorf("batman row does not fail on its build: %s", r)
		}
		for _, b := range []string{"hulk", "studio", "superman", "wolverine"} {
			if r := rowOf(t, stdout, b); !strings.HasPrefix(r, b+" PASS ") {
				t.Errorf("clean bench %s did not pass: %s", b, r)
			}
		}
		if !strings.Contains(stdout, "fleet FAIL benches=4/6") || !strings.Contains(stdout, "7.5=RED") || !strings.Contains(stdout, "pitstop=unasked") {
			t.Errorf("fleet line does not count the failures:\n%s", stdout)
		}
	})

	// Each gated field fails its bench on its own, named on the row.
	for _, tc := range []struct {
		name, field string
		breakIt     func(mr *miniredis.Miniredis)
	}{
		{"harness not declared", "harness(want 1.18.20)", func(mr *miniredis.Miniredis) { mr.HSet("bench:studio:beat", "harness", "1.18.19") }},
		{"harness never measured", "harness(not on the beat)", func(mr *miniredis.Miniredis) { mr.HDel("bench:studio:beat", "harness") }},
		{"mirror absent", "mirrors(absent message-bus,serialize)", func(mr *miniredis.Miniredis) {
			mr.HSet("bench:studio:beat", "mirrors", "nova-tools,nova-work,rowan-tools,schema")
		}},
		{"no desired slots", "slots(no desired slots)", func(mr *miniredis.Miniredis) { mr.HDel("bench:studio:desired", "slots") }},
		{"disk under floor", "disk(150 GiB under the 200 GiB floor)", func(mr *miniredis.Miniredis) { mr.HSet("bench:studio:beat", "disk_gib", "150") }},
		{"disk never measured", "disk(not on the beat)", func(mr *miniredis.Miniredis) { mr.HDel("bench:studio:beat", "disk_gib") }},
		{"no beat", "beat(no beat)", func(mr *miniredis.Miniredis) { mr.Del("bench:studio:beat") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr, yml, _ := fleetFixture(t)
			tc.breakIt(mr)
			code, stdout, _ := runSprint("preflight", "--fleet", "--redis", mr.Addr(), "--all-yml", yml)
			if code != 1 {
				t.Fatalf("exit %d, want 1:\n%s", code, stdout)
			}
			if r := rowOf(t, stdout, "studio"); !strings.HasPrefix(r, "studio FAIL ") || !strings.Contains(r, tc.field) {
				t.Errorf("studio row does not name %s: %s", tc.field, r)
			}
		})
	}

	t.Run("pit stop fails the fleet line", func(t *testing.T) {
		mr, yml, _ := fleetFixture(t)
		mr.Set("sprint:s1:pitstop", "fixes day")
		code, stdout, _ := runSprint("preflight", "--fleet", "--redis", mr.Addr(), "--all-yml", yml, "--sprint", "s1")
		if code != 1 || !strings.Contains(stdout, `fleet FAIL benches=6/6`) || !strings.Contains(stdout, `pitstop=fixes\x20day`) {
			t.Fatalf("exit %d; a pit stop did not fail the fleet line:\n%s", code, stdout)
		}
	})

	t.Run("paused bench reports and never fails", func(t *testing.T) {
		mr, yml, _ := fleetFixture(t)
		mr.Del("bench:studio:beat")
		mr.HSet("bench:studio:desired", "paused", "1")
		code, stdout, _ := runSprint("preflight", "--fleet", "--redis", mr.Addr(), "--all-yml", yml)
		if r := rowOf(t, stdout, "studio"); code != 0 || !strings.HasPrefix(r, "studio PASS ") || !strings.HasSuffix(r, "paused=1") {
			t.Fatalf("exit %d; paused studio row: %s\n%s", code, r, stdout)
		}
	})

	t.Run("library unreadable is FAIL, never PASS", func(t *testing.T) {
		mr, yml, _ := fleetFixture(t)
		prev := fleetLibrary
		fleetLibrary = preflight.ReadLibrary // miniredis refuses FUNCTION LIST
		defer func() { fleetLibrary = prev }()
		code, stdout, _ := runSprint("preflight", "--fleet", "--redis", mr.Addr(), "--all-yml", yml)
		if code != 1 || !strings.Contains(stdout, "fnlib=MISSING") || !strings.Contains(stdout, "cannot list functions") {
			t.Fatalf("exit %d; an unread library passed:\n%s", code, stdout)
		}
	})

	t.Run("no all.yml refuses", func(t *testing.T) {
		mr, _, _ := fleetFixture(t)
		code, stdout, stderr := runSprint("preflight", "--fleet", "--redis", mr.Addr())
		if code != 2 || stdout != "" || !strings.Contains(stderr, "--all-yml") {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
	})
}

// TestPreflightVerbPrintsEveryCheckAndExitsOneOnRed is #3188's DONE-WHEN:
// without --fleet the verb prints the store lines and then one GREEN|RED
// line per check in FleetChecks order over the gathered registry, a bench
// whose beat is older than 2 s is RED 7.5 by name, the verb exits 1, and
// help lists preflight.
func TestPreflightVerbPrintsEveryCheckAndExitsOneOnRed(t *testing.T) {
	mr, _, now := fleetFixture(t)
	for _, b := range fleetBenches[1:] {
		mr.SRem("benches", b)
	}
	mr.HSet("bench:batman:beat", "at", strconv.FormatInt(now.Add(-3*time.Second).UnixMilli(), 10))
	code, stdout, stderr := runSprint("preflight", "--redis", mr.Addr())
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	var fleet []string
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || (f[0] != "GREEN" && f[0] != "RED") {
			t.Fatalf("line is not GREEN|RED <n>: %q", l)
		}
		switch f[1] {
		case "7.4", "7.5", "7.6", "7.8", "7.12", "7.14", "7.16", "7.17":
			fleet = append(fleet, f[1])
		}
	}
	if got, want := strings.Join(fleet, " "), "7.4 7.5 7.6 7.8 7.12 7.14 7.16 7.17"; got != want {
		t.Fatalf("fleet checks %q, want %q in FleetChecks order:\n%s", got, want, stdout)
	}
	if !strings.Contains(stdout, "RED 7.5 bench beats: batman beat 3.0s old") {
		t.Fatalf("no RED 7.5 naming batman:\n%s", stdout)
	}
	if !strings.Contains(stdout, "GREEN 7.4 batch launcher") {
		t.Fatalf("the gathered beat's launcher did not reach 7.4:\n%s", stdout)
	}
	if _, help, _ := runSprint("help"); !strings.Contains(help, "preflight") {
		t.Fatalf("help does not list preflight:\n%s", help)
	}
}

// ---- #2947 rev 3: the store matrix -----------------------------------------

// pfFixture is a throwaway redis-server with appendonly yes, the nova_sprint
// library loaded through fn.Load and the ACL user preflight on
// preflight.ACLRules. admin is the default user, which writes the fixture and
// the mutations; the verb runs as preflight.
type pfFixture struct {
	addr  string
	admin *redis.Client
	T     time.Time // the server's TIME when the fixture was written
}

const pfPassword = "pf-test-secret"

func startPreflightRedis(t *testing.T) *pfFixture {
	t.Helper()
	args := append([]string{"--appendonly", "yes", "--user", "preflight", "on", ">" + pfPassword},
		strings.Fields(preflight.ACLRules)...)
	addr := testutil.Start(t, args...)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	t.Setenv(store.UserEnv, "preflight")
	t.Setenv(store.PasswordEnvEnv, "NOVA_REDIS_PREFLIGHT_PASSWORD")
	t.Setenv("NOVA_REDIS_PREFLIGHT_PASSWORD", pfPassword)
	return &pfFixture{addr: addr, admin: admin}
}

func pfMS(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

// reset writes a fresh copy of the healthy fixture (the DONE-WHEN of #2947
// rev 3): sprint ctl open 300 s, benches b1 b2 on m1, friends f1 on m1 and
// f2 on m2, desired 4 each with two living entries beat 10 s ago, ceilings
// 16, a reconciler lease with a TTL, a pass 5 s ago, an ok ACL receipt at the
// binary's library, one queued card that lints clean with its receipt, one
// ready task f1 may take, and one capacity change from before the sprint.
func (f *pfFixture) reset(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	a := f.admin
	if err := a.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fn.Load(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigSet(ctx, "appendonly", "yes").Err(); err != nil {
		t.Fatal(err)
	}
	T, err := a.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	f.T = T
	ago := func(s int) string { return pfMS(T.Add(-time.Duration(s) * time.Second)) }
	source, err := fn.Source()
	if err != nil {
		t.Fatal(err)
	}
	policy := []any{"backpressure_missing", "open", "debt_cap", "40", "share", "rowan:1"}
	for k, v := range preflight.Defaults() {
		policy = append(policy, k, v)
	}
	p := a.Pipeline()
	p.SAdd(ctx, "sprints", "ctl")
	p.HSet(ctx, "s:ctl", "status", "open", "opened_at", ago(300))
	p.HSet(ctx, "s:ctl:policy", policy...)
	p.SAdd(ctx, "benches", "b1", "b2")
	p.SAdd(ctx, "friends", "f1", "f2")
	for _, c := range []struct{ key, machine, beat string }{
		{"bench:b1", "m1", "launcher"}, {"bench:b2", "m1", "launcher"},
		{"friend:f1", "m1", "harness"}, {"friend:f2", "m2", "harness"},
	} {
		name := strings.SplitN(c.key, ":", 2)[1]
		p.HSet(ctx, c.key+":desired", "slots", "4", "machine", c.machine, "paused", "0")
		value := "nova-sprint bench beat"
		if c.beat == "harness" {
			value = "claude"
		}
		p.HSet(ctx, c.key+":beat", "host", c.machine, c.beat, value, "session", "s-"+name, "at", ago(10))
		for i := 1; i <= 2; i++ {
			p.ZAdd(ctx, c.key+":living", redis.Z{Score: float64(T.Add(-10 * time.Second).UnixMilli()), Member: fmt.Sprintf("ctl/%s-%d/1", name, i)})
		}
	}
	p.HSet(ctx, "machine:m1:ceiling", "slots", "16")
	p.HSet(ctx, "machine:m2:ceiling", "slots", "16")
	p.HSet(ctx, "lease:reconciler", "instance", "r1", "token", "1.ab", "at", ago(1))
	p.PExpire(ctx, "lease:reconciler", 6000*time.Millisecond)
	p.HSet(ctx, "proc:reconciler", "pass_at", ago(5))
	p.HSet(ctx, "proc:acl-test", "result", "ok", "library_sha", fn.Sum(source), "at", ago(60))
	p.SAdd(ctx, "s:ctl:idx:card:queued", "c1")
	p.ZAdd(ctx, "s:ctl:pool", redis.Z{Score: float64(T.UnixMilli()), Member: "c1"})
	p.HSet(ctx, "s:ctl:card:c1", "label", "c1", "kind", card.KindModel, "repo", "mas-bandwidth/nova-tools",
		"base", "dev", "base_sha", strings.Repeat("a", 40), "paths", "internal/deal/testdata/probe/probe.txt",
		"depends_on", "", "done_when", "the probe file reads probe", "payload_sha", "p1", "state", "queued", "where", "ready")
	p.XAdd(ctx, &redis.XAddArgs{Stream: "s:ctl:log", Values: []any{"kind", "card", "id", "c1", "from", "", "to", "queued"}})
	p.ZAdd(ctx, "s:ctl:ready", redis.Z{Score: 1, Member: "t1"})
	p.HSet(ctx, "s:ctl:task:t1", "kind", "work", "state", "open")
	p.HSet(ctx, "friend:f1:roles", "roles", "builder")
	p.XAdd(ctx, &redis.XAddArgs{Stream: "cap:log", Values: []any{"kind", "capacity friend", "target", "friend:f1", "machine", "m1", "at", ago(400)}})
	if _, err := p.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *pfFixture) run() (int, string, string) {
	return runSprint("preflight", "--redis", f.addr, "--sprint", "ctl", "--only", "store")
}

// pfCase is one mutation of the healthy fixture and the one line it turns
// RED, with the text that line must contain.
type pfCase struct {
	name, line, text string
	mutate           func(ctx context.Context, f *pfFixture)
	row              string // the 7.2 table row that alone catches it
}

func pfRFC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func pfCases() []pfCase {
	sec := func(f *pfFixture, s int) string { return pfMS(f.T.Add(-time.Duration(s) * time.Second)) }
	return []pfCase{
		{name: "aof-off", line: "7.1", text: "AOF off", mutate: func(ctx context.Context, f *pfFixture) { f.admin.ConfigSet(ctx, "appendonly", "no") }},
		{name: "library-missing", line: "7.1", text: "not loaded", mutate: func(ctx context.Context, f *pfFixture) { f.admin.FunctionDelete(ctx, fn.Library) }},
		{name: "library-stale", line: "7.1", text: "not the binary's version", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.FunctionLoadReplace(ctx, "#!lua name="+fn.Library+"\nredis.register_function('ns_ping', function() return 'OLD' end)\n")
		}},
		{name: "acl-receipt-missing", line: "7.1", text: "proc:acl-test", mutate: func(ctx context.Context, f *pfFixture) { f.admin.Del(ctx, "proc:acl-test") }},
		{name: "acl-receipt-failed", line: "7.1", text: "proc:acl-test", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HSet(ctx, "proc:acl-test", "result", "failed") }},
		{name: "acl-receipt-old-sha", line: "7.1", text: "proc:acl-test", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.HSet(ctx, "proc:acl-test", "library_sha", "0000000000000000")
		}},
		{name: "state-file-policy", line: "7.2", text: "s:ctl:policy share", row: "policy", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.HSet(ctx, "s:ctl:policy", "share", "shares.tsv")
		}},
		{name: "state-file-card", line: "7.2", text: "BEAT", row: "card", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HSet(ctx, "s:ctl:card:c1", "BEAT", "1") }},
		{name: "state-file-bench-launcher", line: "7.2", text: "bench:b1:beat launcher", row: "bench-launcher", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.HSet(ctx, "bench:b1:beat", "launcher", "/var/run/launcher.pid")
		}},
		{name: "state-file-friend-harness", line: "7.2", text: "friend:f1:beat harness", row: "friend-harness", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.HSet(ctx, "friend:f1:beat", "harness", "~/.friend/BEAT")
		}},
		{name: "state-file-friend-session", line: "7.2", text: "friend:f2:beat session", row: "friend-session", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.HSet(ctx, "friend:f2:beat", "session", "/tmp/f2-session.lock")
		}},
		{name: "interim-bench-row", line: "7.2", text: "bench:b2", row: "bench-row", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.HSet(ctx, "bench:b2", "at", pfRFC(f.T.Add(-30*time.Second)))
			f.admin.Expire(ctx, "bench:b2", 60*time.Second)
		}},
		{name: "leases-not-beats", line: "7.3", text: "leased 8", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.ZRemRangeByRank(ctx, "friend:f1:living", 0, 0)
			for i := 1; i <= 7; i++ {
				f.admin.ZAdd(ctx, "friend:f1:starting", redis.Z{Score: float64(f.T.Add(-70 * time.Second).UnixMilli()), Member: fmt.Sprintf("ctl/s%d/1", i)})
			}
		}},
		{name: "starting-past-window", line: "7.3", text: "starting past", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.ZAdd(ctx, "friend:f2:starting", redis.Z{Score: float64(f.T.Add(-70 * time.Second).UnixMilli()), Member: "ctl/late/1"})
		}},
		{name: "reconciler-lease-missing", line: "7.7", text: "missing", mutate: func(ctx context.Context, f *pfFixture) { f.admin.Del(ctx, "lease:reconciler") }},
		{name: "reconciler-lease-stale", line: "7.7", text: "stale", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HSet(ctx, "lease:reconciler", "at", sec(f, 30)) }},
		{name: "reconciler-pass-old", line: "7.7", text: "last pass", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HSet(ctx, "proc:reconciler", "pass_at", sec(f, 25)) }},
		{name: "brake-missing", line: "7.9", text: "debt_cap", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HDel(ctx, "s:ctl:policy", "debt_cap") }},
		{name: "window-missing", line: "7.9", text: "start_window_s", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HDel(ctx, "s:ctl:policy", "start_window_s") }},
		{name: "receipt-mismatch", line: "7.10", text: "receipt dealt", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.XAdd(ctx, &redis.XAddArgs{Stream: "s:ctl:log", Values: []any{"kind", "card", "id", "c1", "from", "queued", "to", "dealt"}})
		}},
		{name: "receipt-none", line: "7.10", text: "receipt none", mutate: func(ctx context.Context, f *pfFixture) { f.admin.SAdd(ctx, "s:ctl:idx:task:open", "t9") }},
		{name: "supply-none", line: "7.11", text: "no eligible", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HSet(ctx, "s:ctl:task:t1", "kind", "read") }},
		{name: "guard-lint", line: "7.13", text: "DONE-WHEN", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HDel(ctx, "s:ctl:card:c1", "done_when") }},
		{name: "over-ceiling", line: "7.15", text: "over its ceiling", mutate: func(ctx context.Context, f *pfFixture) { f.admin.HSet(ctx, "machine:m1:ceiling", "slots", "4") }},
		{name: "no-ceiling", line: "7.15", text: "has no ceiling", mutate: func(ctx context.Context, f *pfFixture) { f.admin.Del(ctx, "machine:m2:ceiling") }},
		{name: "ttl-on-beat", line: "7.27", text: "bench:b1:beat", mutate: func(ctx context.Context, f *pfFixture) { f.admin.PExpire(ctx, "bench:b1:beat", time.Minute) }},
		{name: "changed-under-load", line: "7.28", text: "capacity machine", mutate: func(ctx context.Context, f *pfFixture) {
			f.admin.XAdd(ctx, &redis.XAddArgs{Stream: "cap:log", Values: []any{"kind", "capacity machine", "target", "machine:m1", "machine", "m1", "at", sec(f, 10)}})
		}},
	}
}

// judgeRed is a RED case's assertion: exit 1, all 11 store lines, exactly
// one RED (the named line, holding the named text) and the other 10 GREEN.
func judgeRed(c pfCase, code int, stdout string) error {
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if code != 1 {
		return fmt.Errorf("exit %d, want 1", code)
	}
	if len(lines) != len(preflight.StoreNames) {
		return fmt.Errorf("%d lines, want %d", len(lines), len(preflight.StoreNames))
	}
	var reds []string
	for _, l := range lines {
		if strings.HasPrefix(l, "RED ") {
			reds = append(reds, l)
		} else if !strings.HasPrefix(l, "GREEN ") {
			return fmt.Errorf("line is not GREEN|RED: %q", l)
		}
	}
	if len(reds) != 1 {
		return fmt.Errorf("%d RED lines, want exactly one (%s)", len(reds), c.line)
	}
	if !strings.HasPrefix(reds[0], "RED "+c.line+" ") || !strings.Contains(reds[0], c.text) {
		return fmt.Errorf("RED line %q, want %s containing %q", reds[0], c.line, c.text)
	}
	return nil
}

var pfAgo = regexp.MustCompile(`[0-9]+s ago`)

func pfGolden(stdout string) string { return pfAgo.ReplaceAllString(stdout, "Ns ago") }

const pfGoldenPath = "testdata/preflight-store-healthy.golden"

// judgeHealthy is the healthy case's assertion: exit 0 and the golden output.
func judgeHealthy(code int, stdout string) error {
	want, err := os.ReadFile(pfGoldenPath)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("exit %d, want 0", code)
	}
	if got := pfGolden(stdout); got != string(want) {
		return fmt.Errorf("stdout differs from %s:\n got:\n%s\nwant:\n%s", pfGoldenPath, got, want)
	}
	return nil
}

func pfDump(t *testing.T, f *pfFixture) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := f.admin.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, k := range keys {
		out[k] = f.admin.Dump(ctx, k).Val()
	}
	return out
}

// TestControl18StoreMatrix is #2947 rev 3's DONE-WHEN: every store line is
// shown able to fail. A healthy store prints the golden 11 GREEN lines and
// exits 0; each mutation turns exactly its one line RED and exits 1; each
// 7.2 row alone catches its case (blind-<case>); an unreachable Redis is
// every line RED; the verb writes nothing, reads in preflight.Exchanges round
// trips at any fleet size, opens no file and refuses a retired flag.
func TestControl18StoreMatrix(t *testing.T) {
	f := startPreflightRedis(t)
	ctx := context.Background()

	t.Run("healthy", func(t *testing.T) {
		f.reset(t)
		code, stdout, stderr := f.run()
		if os.Getenv("NOVA_UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(pfGoldenPath, []byte(pfGolden(stdout)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := judgeHealthy(code, stdout); err != nil {
			t.Fatalf("%v\nstderr: %s", err, stderr)
		}
	})

	for _, c := range pfCases() {
		t.Run(c.name, func(t *testing.T) {
			f.reset(t)
			c.mutate(ctx, f)
			code, stdout, stderr := f.run()
			if err := judgeRed(c, code, stdout); err != nil {
				t.Fatalf("%v\nstdout:\n%s\nstderr: %s", err, stdout, stderr)
			}
		})
	}

	// Each 7.2 row is the only thing catching its case: with that one row
	// removed the same mutation leaves every line GREEN.
	for _, c := range pfCases() {
		if c.row == "" {
			continue
		}
		t.Run("blind-"+c.name, func(t *testing.T) {
			sources, interim := preflight.StateSources, preflight.InterimKeys
			t.Cleanup(func() { preflight.StateSources, preflight.InterimKeys = sources, interim })
			found := false
			var keepS []preflight.StateSource
			for _, r := range sources {
				if r.Name == c.row {
					found = true
					continue
				}
				keepS = append(keepS, r)
			}
			var keepI []preflight.InterimKey
			for _, r := range interim {
				if r.Name == c.row {
					found = true
					continue
				}
				keepI = append(keepI, r)
			}
			if !found {
				t.Fatalf("no 7.2 row %q", c.row)
			}
			preflight.StateSources, preflight.InterimKeys = keepS, keepI
			f.reset(t)
			c.mutate(ctx, f)
			code, stdout, _ := f.run()
			if code != 0 || strings.Contains(stdout, "RED ") {
				t.Fatalf("without row %s the mutation must leave 7.2 GREEN (exit %d):\n%s", c.row, code, stdout)
			}
		})
	}

	// The library writes q:<f>, q:<f>:front and friend:<f> itself, and
	// presence.lua writes bench:<b> without a TTL or count fields: none of
	// those is a second writer.
	t.Run("library-writers", func(t *testing.T) {
		f.reset(t)
		at := pfRFC(f.T.Add(-30 * time.Second))
		f.admin.XAdd(ctx, &redis.XAddArgs{Stream: "q:f1", Values: []any{"id", "t9"}})
		f.admin.XAdd(ctx, &redis.XAddArgs{Stream: "q:f1:front", Values: []any{"id", "t8"}})
		f.admin.HSet(ctx, "friend:f2", "at", at, "up", "1", "ready", "0", "working", "0")
		f.admin.HSet(ctx, "bench:b1", "host", "b1", "load1", "0.5", "ncpu", "8", "at", at)
		code, stdout, _ := f.run()
		if code != 0 || strings.Contains(stdout, "RED ") {
			t.Fatalf("library-shaped writes must stay GREEN (exit %d):\n%s", code, stdout)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		code, stdout, _ := runSprint("preflight", "--redis", "127.0.0.1:1", "--sprint", "ctl", "--only", "store")
		lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
		if code != 1 || len(lines) != len(preflight.StoreNames) {
			t.Fatalf("exit %d with %d lines, want 1 and %d:\n%s", code, len(lines), len(preflight.StoreNames), stdout)
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, "RED ") {
				t.Fatalf("unreachable must be every line RED: %q", l)
			}
		}
		if !strings.HasPrefix(lines[0], "RED 7.1 ") || !strings.Contains(lines[0], "unreachable") {
			t.Fatalf("7.1 must read unreachable: %q", lines[0])
		}
	})

	t.Run("read-only", func(t *testing.T) {
		for _, name := range []string{"healthy", "interim-bench-row"} {
			f.reset(t)
			for _, c := range pfCases() {
				if c.name == name {
					c.mutate(ctx, f)
				}
			}
			before := pfDump(t, f)
			f.run()
			after := pfDump(t, f)
			if fmt.Sprint(before) != fmt.Sprint(after) {
				t.Fatalf("%s: preflight changed the store", name)
			}
		}
	})

	t.Run("exchanges", func(t *testing.T) {
		counts := map[string]int{}
		for _, size := range []struct {
			name             string
			benches, friends int
		}{{"fixture", 0, 0}, {"64 benches 16 friends", 64, 16}} {
			f.reset(t)
			p := f.admin.Pipeline()
			for i := 0; i < size.benches; i++ {
				b := fmt.Sprintf("xb%02d", i)
				p.SAdd(ctx, "benches", b)
				p.HSet(ctx, "bench:"+b+":desired", "slots", "0", "machine", fmt.Sprintf("xm%02d", i%8), "paused", "0")
			}
			for i := 0; i < size.friends; i++ {
				p.SAdd(ctx, "friends", fmt.Sprintf("xf%02d", i))
			}
			if _, err := p.Exec(ctx); err != nil {
				t.Fatal(err)
			}
			c := redis.NewClient(&redis.Options{Addr: f.addr, Username: "preflight", Password: pfPassword})
			hook := &pfCountHook{}
			c.AddHook(hook)
			// The connection's own handshake (HELLO, CLIENT SETINFO) runs
			// on the first command; warm it up and count from zero.
			if err := c.Ping(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			hook.calls = 0
			preflight.Run(ctx, c, preflight.Options{Sprint: "ctl"})
			_ = c.Close()
			counts[size.name] = hook.calls
		}
		for name, n := range counts {
			if n != preflight.Exchanges {
				t.Fatalf("%s: %d process or pipeline calls, want preflight.Exchanges=%d (%v)", name, n, preflight.Exchanges, counts)
			}
		}
	})

	t.Run("no-file", func(t *testing.T) {
		for _, name := range []string{"store.go", "preflight.go"} {
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "internal", "nsprint", "preflight", name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				if p := strings.Trim(imp.Path.Value, `"`); p == "os" || p == "io/ioutil" || p == "path/filepath" {
					t.Fatalf("%s imports %s: preflight reads Redis only", name, p)
				}
			}
		}
	})

	t.Run("retired-flag", func(t *testing.T) {
		code, stdout, _ := runSprint("preflight", "--redis", f.addr, "--retired", "x")
		if code != 2 || stdout != "" {
			t.Fatalf("--retired: exit %d stdout %q, want 2 and nothing", code, stdout)
		}
	})

	// The control: the assertions above reject a stub that prints 11 GREEN
	// lines on every RED case, and one that prints 11 RED lines on healthy.
	t.Run("stub-controls", func(t *testing.T) {
		stub := func(state string) string {
			var b strings.Builder
			for _, n := range preflight.StoreNames {
				fmt.Fprintf(&b, "%s %s %s: stub\n", state, n.N, n.Name)
			}
			return b.String()
		}
		for _, c := range pfCases() {
			if judgeRed(c, 0, stub("GREEN")) == nil || judgeRed(c, 1, stub("GREEN")) == nil {
				t.Fatalf("%s: an all-GREEN stub passes", c.name)
			}
		}
		if judgeHealthy(1, stub("RED")) == nil {
			t.Fatal("healthy: an all-RED stub passes")
		}
	})
}

// pfCountHook counts process and pipeline calls: each is one round trip.
type pfCountHook struct{ calls int }

func (h *pfCountHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *pfCountHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { h.calls++; return next(ctx, cmd) }
}
func (h *pfCountHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { h.calls++; return next(ctx, cmds) }
}
