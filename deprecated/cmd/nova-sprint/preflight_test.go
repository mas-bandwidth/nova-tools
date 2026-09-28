package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
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
	mr.ZAdd("bench:hulk:cards:working", 1, "card-1")
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
