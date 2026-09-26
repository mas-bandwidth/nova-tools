//go:build functional

package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
)

const doctorTip = "c839379e4eabff79cc2e0000111122223333aaaa"

// seedHealthy is a store every doctor check passes on for bench m1: the
// library loaded, the dev tip this binary names, m1 registered and beating
// at the store's own TIME, one fresh ev:github entry read by ci-github, and
// one open sprint with an epoch.
func seedHealthy(t *testing.T, c *redis.Client) {
	t.Helper()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	now, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	at := strconv.FormatInt(now.UnixMilli(), 10)
	pipe := c.Pipeline()
	pipe.HSet(ctx, "fleet:release", "version", "v0.16.0-dev.c839379e", "commit", doctorTip, "self", "m1")
	pipe.SAdd(ctx, "benches", "m1")
	pipe.HSet(ctx, "bench:m1:desired", "role", "fleet", "legs", "go")
	pipe.HSet(ctx, "bench:m1:beat", "at", at)
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "ev:github", ID: at + "-0", Values: []string{"kind", "ping", "sender", "glenn"}})
	pipe.XGroupCreate(ctx, "ev:github", "ci-github", "$")
	pipe.SAdd(ctx, "sprints", "s1", "old")
	pipe.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "s1"})
	pipe.HSet(ctx, "s:s1", "status", "open")
	pipe.HSet(ctx, "s:old", "status", "closed")
	pipe.Set(ctx, "sprint:epoch", "7", 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// runDoctorWith is `nova-sprint doctor args...` with its own environment
// (only what env names; HOME a fresh directory unless env names one), this
// binary stamped as the seeded dev tip, and its own seat selection: no
// t.Setenv, so the tests run in parallel.
func runDoctorWith(t *testing.T, env map[string]string, sel *seatcred.Selection, args ...string) (int, string, string) {
	t.Helper()
	if _, ok := env["HOME"]; !ok {
		env["HOME"] = t.TempDir()
	}
	if sel == nil {
		sel = &seatcred.Selection{}
	}
	d := doctorDeps{getenv: func(k string) string { return env[k] }, version: "v0.16.0-dev.c839379e", sel: sel}
	var out, errOut bytes.Buffer
	code := doctorRun(context.Background(), args, &out, &errOut, d)
	return code, out.String(), errOut.String()
}

// TestDoctorGreenStoreIsTwoTrips is the card's DONE-WHEN on a real store:
// every check OK, exit 0, in two pipelined round trips (the reads, then the
// sprints and ns_ping). Then each thing an operator breaks is one FIX line
// naming its command, and the exit is 1 with the count.
func TestDoctorGreenStoreIsTwoTrips(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	seedHealthy(t, c)

	code, out, errOut := runDoctorWith(t, map[string]string{}, nil, "--redis", addr, "--bench", "m1")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if code != 0 || errOut != "" || len(lines) != 9 {
		t.Fatalf("green store: exit %d stderr %q\n%s", code, errOut, out)
	}
	for i, c := range doctorChecks {
		if !strings.HasPrefix(lines[i], "DOCTOR "+c+" OK") {
			t.Errorf("line %d = %q, want %s OK", i, lines[i], c)
		}
	}
	if !strings.HasPrefix(lines[8], "DOCTOR OK checks=8 trips=2 ms=") {
		t.Fatalf("summary %q", lines[8])
	}
	for _, want := range []string{"DOCTOR sprint OK sprint=s1 epoch=7", "DOCTOR ingest OK stream=ev:github last=",
		" sender=glenn group=ci-github lag=0 pending=0\n", "DOCTOR runners OK bench=m1 role=fleet legs=go beat=",
		"DOCTOR version OK have=v0.16.0-dev.c839379e tip=c839379e4eab", "DOCTOR seat OK seat=none user=default"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}

	// Break four things: a pit stop, a stale library, a lagging ingest, a
	// second open sprint.
	ctx := context.Background()
	if err := c.FunctionDelete(ctx, fn.Library).Err(); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, "s:s1:pitstop", "by", "glenn", "why", "rest", "at", "1")
	c.XAdd(ctx, &redis.XAddArgs{Stream: "ev:github", Values: []string{"kind", "ping"}})
	c.SAdd(ctx, "sprints", "s2")
	c.HSet(ctx, "s:s2", "status", "open")

	code, out, _ = runDoctorWith(t, map[string]string{}, nil, "--redis", addr, "--bench", "m1")
	if code != 1 || !strings.Contains(out, "DOCTOR FIX fixes=4 skipped=0 checks=8 trips=2 ms=") {
		t.Fatalf("broken store: exit %d\n%s", code, out)
	}
	for _, want := range []string{
		`DOCTOR fn FIX MISSING want=`,
		`remedy="NOVA_SPRINT_REDIS_USER=admin NOVA_SPRINT_REDIS_PASSWORD_ENV=NS_ADMIN nova-sprint fn deploy --redis ` + addr + `"`,
		`DOCTOR pitstop FIX sprint=s1 by=glenn`,
		`remedy="nova-sprint pitstop clear --sprint s1 --by m1 --redis ` + addr + `"`,
		`DOCTOR ingest FIX stream=ev:github last=0s sender=- group=ci-github lag=1`,
		`DOCTOR sprint FIX open=2 sprints=s1,s2 why="one active sprint, many streams: close the other" remedy="nova-sprint sprint close --sprint s2 --redis ` + addr + `"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

// TestDoctorSeatOnTheFleetShape: the default user off and the password only
// in the studio seat's file. Under --seat every check reads as the seat; with
// no seat the one fix is exporting the seat whose key this machine holds.
func TestDoctorSeatOnTheFleetShape(t *testing.T) {
	t.Parallel()
	const pw = "doctor-test-pw-5b1e0c"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw})
	addr := testutil.Start(t, "--user", "default", "off", "--user", "coordinator", "on", ">"+pw, "~*", "&*", "+@all")
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "coordinator", Password: pw})
	t.Cleanup(func() { _ = c.Close() })
	seedHealthy(t, c)
	env := map[string]string{"HOME": home}
	getenv := func(k string) string { return env[k] }

	sel := &seatcred.Selection{}
	sel.SelectWith("studio", "", func(s string) (seatcred.Cred, error) { return seatcred.Resolve(s, getenv) })
	code, out, errOut := runDoctorWith(t, env, sel, "--redis", addr, "--bench", "m1")
	if code != 0 || !strings.Contains(out, "DOCTOR seat OK seat=studio user=coordinator key=NOVA_REDIS_COORDINATOR_PASSWORD\n") ||
		!strings.Contains(out, "DOCTOR redis OK addr="+addr+" user=coordinator\n") || !strings.Contains(out, "DOCTOR OK checks=8 trips=2") {
		t.Fatalf("under the seat: exit %d stderr %q\n%s", code, errOut, out)
	}
	if strings.Contains(out+errOut, pw) {
		t.Fatal("the password was printed")
	}

	code, out, errOut = runDoctorWith(t, env, nil, "--redis", addr, "--bench", "m1")
	if code != 1 || !strings.Contains(out, `DOCTOR seat FIX seat=none err="NOAUTH`) || !strings.Contains(out, `held=studio why="the store wants a login and no seat is named" remedy="export NOVA_SPRINT_SEAT=studio"`) ||
		!strings.Contains(out, "DOCTOR redis SKIP needs=seat\n") || !strings.Contains(out, "DOCTOR FIX fixes=1 skipped=7 checks=8") {
		t.Fatalf("no seat: exit %d stderr %q\n%s", code, errOut, out)
	}
}
