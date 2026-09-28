package main

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

// doctorNow is the store's TIME in every judged fact below: no test here
// reads a clock.
var doctorNow = time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

const doctorCommit = "c839379e4eabff79cc2e0000111122223333aaaa"

// healthyFacts is a store with every check green, the way doctor reads it.
func healthyFacts() doctorFacts {
	want := "0123456789abcdef"
	return doctorFacts{
		Addr: "h:6380", Machine: "studio", GOOS: "darwin", UID: 501, Me: "rowan",
		Have:       "v0.16.0-dev.c839379e",
		Seat:       doctorSeat{Name: "studio", User: "coordinator", Key: "NOVA_REDIS_COORDINATOR_PASSWORD"},
		Now:        doctorNow,
		Lib:        fn.State{Want: want, Loaded: want, Ping: "PONG"},
		Release:    map[string]string{"version": "v0.16.0-dev.c839379e", "commit": doctorCommit, "self": "studio"},
		Registered: true,
		Desired:    map[string]string{"role": "friends"},
		BeatAt:     msOf(doctorNow.Add(-time.Second)),
		Groups:     []redis.XInfoGroup{{Name: "ci-github", Lag: 0, Pending: 0}},
		LastID:     msOf(doctorNow.Add(-40*time.Second)) + "-0",
		LastSender: "glenn",
		Sprints:    []doctorSprint{{Name: "s1", Status: "open"}, {Name: "control-1", Status: "open"}},
		Epoch:      "7",
		Trips:      2, MS: 9,
	}
}

func msOf(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }

func lineOf(t *testing.T, lines []doctorLine, check string) doctorLine {
	t.Helper()
	for _, l := range lines {
		if l.Check == check {
			return l
		}
	}
	t.Fatalf("no %s line in %v", check, lines)
	return doctorLine{}
}

// assertOneCommand holds the remedy grammar: a FIX carries a why and ONE
// command a person pastes, never prose, alternatives or a second step, and
// an ansible fix names the fleet directory in full.
func assertOneCommand(t *testing.T, name string, l doctorLine) {
	t.Helper()
	if l.State != "FIX" {
		return
	}
	if l.Remedy == "" || l.Why == "" {
		t.Errorf("%s: %q; want a why and a remedy", name, l.String())
	}
	for _, prose := range []string{"; ", " (", ", then", " if ", " or ", " with the ", "rowan-tools)", "make -C fleet"} {
		if strings.Contains(l.Remedy, prose) {
			t.Errorf("%s: remedy %q holds %q: prose belongs in why=", name, l.Remedy, prose)
		}
	}
	if strings.HasPrefix(l.Remedy, "make ") && !strings.HasPrefix(l.Remedy, "make -C "+fleetDir+" ") {
		t.Errorf("%s: remedy %q; want make -C %s", name, l.Remedy, fleetDir)
	}
}

// TestDoctorAllGreenIsEightOKLines: a healthy store is eight OK lines in the
// fixed order, no remedy anywhere, and the summary is DOCTOR OK.
func TestDoctorAllGreenIsEightOKLines(t *testing.T) {
	t.Parallel()
	lines := doctorLines(healthyFacts())
	if len(lines) != len(doctorChecks) {
		t.Fatalf("%d lines, want %d", len(lines), len(doctorChecks))
	}
	want := []string{
		"DOCTOR seat OK seat=studio user=coordinator key=NOVA_REDIS_COORDINATOR_PASSWORD",
		"DOCTOR redis OK addr=h:6380 user=coordinator",
		"DOCTOR fn OK sha=0123456789abcdef ping=PONG",
		"DOCTOR version OK have=v0.16.0-dev.c839379e tip=c839379e4eab",
		"DOCTOR runners OK bench=studio role=friends legs=- beat=1s",
		"DOCTOR ingest OK stream=ev:github last=40s sender=glenn source=hook group=ci-github lag=0 pending=0",
		"DOCTOR pitstop OK none sprints=2",
		"DOCTOR sprint OK sprint=s1 epoch=7",
	}
	for i, l := range lines {
		if l.Check != doctorChecks[i] || l.String() != want[i] {
			t.Errorf("line %d = %q, want %q", i, l.String(), want[i])
		}
	}
	if got := doctorSummary(lines, 2, 9); got != "DOCTOR OK checks=8 trips=2 ms=9" {
		t.Fatalf("summary %q", got)
	}
}

// TestDoctorEveryFixNamesItsCommand: each broken fact is one FIX line whose
// remedy is the exact command, and the summary counts the fixes.
func TestDoctorEveryFixNamesItsCommand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		break_ func(*doctorFacts)
		check  string
		words  string
		remedy string
	}{
		{"library stale, binary at tip", func(f *doctorFacts) { f.Lib.Loaded, f.Lib.Ping = "ffffffffffffffff", fn.PingSkipped },
			"fn", "STALE loaded=ffffffffffffffff", "NOVA_SPRINT_REDIS_USER=admin NOVA_SPRINT_REDIS_PASSWORD_ENV=NS_ADMIN nova-sprint fn deploy --redis h:6380"},
		{"library missing, binary behind", func(f *doctorFacts) {
			f.Lib.Missing, f.Lib.Loaded = true, ""
			f.Have = "v0.16.0-dev.aaaaaaaa"
		}, "fn", "MISSING", "nova-sprint self update --sha c839379e4eab"},
		{"library noping", func(f *doctorFacts) { f.Lib.Ping = "ERR boom" }, "fn", `NOPING`, "fn deploy --redis h:6380"},
		{"binary behind on the coordinator", func(f *doctorFacts) { f.Have = "v0.16.0-dev.aaaaaaaa" },
			"version", `why="this build is not the dev tip"`, "nova-sprint self update --sha c839379e4eab"},
		{"binary behind on a bench", func(f *doctorFacts) { f.Have, f.Machine = "20260926154704-aaaaaaaaaaaa", "hetzner" },
			"version", `why="this build is not the dev tip"`, "nova-sprint fleet build --bench hetzner --redis h:6380"},
		{"binary dirty", func(f *doctorFacts) { f.Have = "20260926154704-c839379e4eab-dirty" }, "version", `why="this build is from an edited tree"`, "self update"},
		{"binary devel", func(f *doctorFacts) { f.Have = "devel" }, "version", `why="this build names no commit"`, "self update"},
		{"no dev tip", func(f *doctorFacts) { f.Release = map[string]string{} }, "version", "tip=none", "nova-sprint fleet build set version="},
		{"not registered", func(f *doctorFacts) { f.Machine, f.Registered = "hetzner", false }, "runners", "registered=no",
			"make -C ~/rowan-working/rowan-tools/fleet bench-register"},
		{"no beat", func(f *doctorFacts) { f.BeatAt = "" }, "runners", "beat=none", "launchctl kickstart -k gui/501/com.nova.loop.nova-sprint-bench-beat"},
		{"stale beat on linux", func(f *doctorFacts) {
			f.BeatAt, f.GOOS = msOf(doctorNow.Add(-3*time.Minute)), "linux"
		}, "runners", "beat=3m", "systemctl --user restart nova-loop-nova-sprint-bench-beat.service"},
		{"held bench", func(f *doctorFacts) { f.Desired["paused"] = "1" }, "runners", "paused=1", "nova-sprint fleet release --bench studio --redis h:6380"},
		{"ingest lag", func(f *doctorFacts) { f.Groups[0].Lag, f.Groups[0].Pending = 5, 2 }, "ingest", "lag=5 pending=2", "nova-sprint ci github --redis h:6380 --once"},
		{"ingest pending", func(f *doctorFacts) { f.Groups[0].Pending = 1 }, "ingest", "lag=0 pending=1", "nova-sprint ci github --redis h:6380 --once"},
		{"pit stop held", func(f *doctorFacts) {
			f.Sprints[0].Stop = pitstop.Stop{Sprint: "s1", Set: true, By: "glenn", Why: "rest", At: doctorNow.Add(-10 * time.Minute).UnixMilli()}
		}, "pitstop", `sprint=s1 by=glenn age=10m reason="rest" held=1`, "nova-sprint pitstop clear --sprint s1 --by rowan --redis h:6380"},
		{"legacy pit stop", func(f *doctorFacts) { f.Sprints[0].Legacy = true }, "pitstop", "by=legacy key=sprint:s1:pitstop", "pitstop clear --sprint s1"},
		{"no open sprint", func(f *doctorFacts) { f.Sprints = f.Sprints[1:] }, "sprint", "open=0", "nova-sprint sprint open --sprint <name> --from <work-set.lisp>"},
		{"two open sprints", func(f *doctorFacts) { f.Sprints = append(f.Sprints, doctorSprint{Name: "s2", Status: "open"}) },
			"sprint", "open=2 sprints=s1,s2", "nova-sprint sprint close --sprint s2"},
	}
	for _, c := range cases {
		f := healthyFacts()
		c.break_(&f)
		lines := doctorLines(f)
		l := lineOf(t, lines, c.check)
		if l.State != "FIX" || !strings.Contains(l.String(), c.words) || !strings.Contains(l.Remedy, c.remedy) {
			t.Errorf("%s: %q; want FIX with %q and a remedy naming %q", c.name, l.String(), c.words, c.remedy)
		}
		assertOneCommand(t, c.name, l)
		fixes := 0
		for _, x := range lines {
			if x.State == "FIX" {
				fixes++
			}
		}
		if s := doctorSummary(lines, 2, 9); !strings.HasPrefix(s, "DOCTOR FIX fixes="+strconv.Itoa(fixes)+" skipped=0 checks=8") {
			t.Errorf("%s: summary %q with %d fixes", c.name, s, fixes)
		}
	}
}

// TestDoctorSeatAndRedisFailuresSkipTheRest: a check that needs a failed one
// prints SKIP, which the summary counts apart from the fixes.
func TestDoctorSeatAndRedisFailuresSkipTheRest(t *testing.T) {
	t.Parallel()
	f := healthyFacts()
	f.Seat = doctorSeat{Name: "nobody", Err: errors.New("seat nobody: store file x is absent"), Remedy: "nova-secrets check --as nobody", Why: "names it"}
	lines := doctorLines(f)
	if l := lineOf(t, lines, "seat"); l.State != "FIX" || l.Remedy != "nova-secrets check --as nobody" {
		t.Fatalf("seat %q", l.String())
	}
	if l := lineOf(t, lines, "redis"); l.String() != "DOCTOR redis SKIP needs=seat" {
		t.Fatalf("redis %q", l.String())
	}
	if s := doctorSummary(lines, 0, 1); s != "DOCTOR FIX fixes=1 skipped=7 checks=8 trips=0 ms=1" {
		t.Fatalf("summary %q", s)
	}

	// No seat, and the store wants a login: the seat is the one fix.
	f = healthyFacts()
	f.Seat = doctorSeat{Held: []string{"studio", "swarm-studio"}}
	f.PingErr = errors.New("NOAUTH Authentication required.")
	lines = doctorLines(f)
	if l := lineOf(t, lines, "seat"); l.State != "FIX" || l.Remedy != "export NOVA_SPRINT_SEAT=studio" || !strings.Contains(l.String(), "held=studio,swarm-studio") {
		t.Fatalf("seat %q", l.String())
	} else {
		assertOneCommand(t, "no seat", l)
	}
	if l := lineOf(t, lines, "redis"); l.State != "SKIP" {
		t.Fatalf("redis %q", l.String())
	}

	// A seat whose password the store refuses.
	f = healthyFacts()
	f.PingErr = errors.New("WRONGPASS invalid username-password pair")
	l := lineOf(t, doctorLines(f), "redis")
	if l.State != "FIX" || l.Remedy != "nova-secrets seal --store ~/nova-bench/secrets --as studio --key ~/.config/nova-secrets/studio.key --sops $(command -v sops) --name NOVA_REDIS_COORDINATOR_PASSWORD" {
		t.Fatalf("redis %q", l.String())
	}
	assertOneCommand(t, "wrongpass", l)
	f.Seat.Seal = "nova-secrets seal --store /s --as studio --key /k --sops /bin/sops --name NOVA_REDIS_COORDINATOR_PASSWORD"
	if l := lineOf(t, doctorLines(f), "redis"); l.Remedy != f.Seat.Seal {
		t.Fatalf("redis %q; want the seat's own seal line", l.String())
	}

	// No address at all.
	f = healthyFacts()
	f.Addr = ""
	l = lineOf(t, doctorLines(f), "redis")
	if l.State != "FIX" || l.Remedy != "export NOVA_SPRINT_REDIS=<host:port>" {
		t.Fatalf("redis %q", l.String())
	}
	assertOneCommand(t, "no address", l)

	// Unreachable.
	f = healthyFacts()
	f.PingErr = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	lines = doctorLines(f)
	if l := lineOf(t, lines, "redis"); l.State != "FIX" || l.Remedy != "make -C ~/rowan-working/rowan-tools/fleet store" {
		t.Fatalf("redis %q", l.String())
	} else {
		assertOneCommand(t, "unreachable", l)
	}
	for _, c := range doctorChecks[2:] {
		if l := lineOf(t, lines, c); l.String() != "DOCTOR "+c+" SKIP needs=redis" {
			t.Fatalf("%s %q", c, l.String())
		}
	}
}

// TestDoctorNoSeatOnAnOpenStoreIsOK: a store with the default user on (a
// throwaway or a bench's local) needs no seat.
func TestDoctorNoSeatOnAnOpenStoreIsOK(t *testing.T) {
	t.Parallel()
	f := healthyFacts()
	f.Seat = doctorSeat{}
	lines := doctorLines(f)
	if l := lineOf(t, lines, "seat"); l.String() != "DOCTOR seat OK seat=none user=default" {
		t.Fatalf("seat %q", l.String())
	}
	if l := lineOf(t, lines, "redis"); l.String() != "DOCTOR redis OK addr=h:6380 user=default" {
		t.Fatalf("redis %q", l.String())
	}
}

// TestDoctorCoordinatorsMachineIsRegistered: fleet:release self beats as a
// bench outside `benches` by design, so its runners line is judged on the
// beat alone (the 2026-09-26 cold read's false FIX).
func TestDoctorCoordinatorsMachineIsRegistered(t *testing.T) {
	t.Parallel()
	f := healthyFacts()
	f.Registered = false
	if l := lineOf(t, doctorLines(f), "runners"); l.String() != "DOCTOR runners OK bench=studio self=yes role=friends legs=- beat=1s" {
		t.Fatalf("runners %q", l.String())
	}
	f.BeatAt = ""
	if l := lineOf(t, doctorLines(f), "runners"); l.State != "FIX" || !strings.Contains(l.String(), "beat=none") {
		t.Fatalf("self with no beat %q; want the beat fix", l.String())
	}
}

// TestDoctorIngestAgeIsInformation: a quiet stream, a missing stream and a
// missing group are information, never a fix; only lag or pending is.
func TestDoctorIngestAgeIsInformation(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*doctorFacts){
		"quiet evening": func(f *doctorFacts) { f.LastID = msOf(doctorNow.Add(-9*time.Hour)) + "-0" },
		"no stream":     func(f *doctorFacts) { f.GroupsErr, f.LastID, f.LastSender = errors.New("ERR no such key"), "", "" },
		"no group":      func(f *doctorFacts) { f.Groups = nil },
	} {
		f := healthyFacts()
		change(&f)
		if l := lineOf(t, doctorLines(f), "ingest"); l.State != "OK" {
			t.Errorf("%s: %q; want OK", name, l.String())
		}
	}
	f := healthyFacts()
	f.GroupsErr, f.LastID, f.LastSender, f.Groups = errors.New("ERR no such key"), "", "", nil
	if l := lineOf(t, doctorLines(f), "ingest"); l.String() != "DOCTOR ingest OK stream=ev:github last=none sender=- source=none group=none" {
		t.Fatalf("no stream: %q", l.String())
	}
}

// TestDoctorMeIsNeverAPlaceholder: the --by a remedy prints is NOVA_FRIEND,
// else the Redis user (coordinator under the wrapper's environment), else the
// seat, else the machine.
func TestDoctorMeIsNeverAPlaceholder(t *testing.T) {
	t.Parallel()
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	for _, c := range []struct {
		env  map[string]string
		seat doctorSeat
		want string
	}{
		{map[string]string{"NOVA_FRIEND": "rowan"}, doctorSeat{User: "coordinator"}, "rowan"},
		{map[string]string{}, doctorSeat{User: "coordinator"}, "coordinator"},
		{map[string]string{}, doctorSeat{Name: "studio"}, "studio"},
		{map[string]string{}, doctorSeat{}, "studio-mac"},
	} {
		if got := doctorMe(env(c.env), c.seat, "studio-mac"); got != c.want {
			t.Errorf("doctorMe(%v, %+v) = %q, want %q", c.env, c.seat, got, c.want)
		}
	}
}

func TestBuildRevisionReadsEveryBuildIdentity(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		v, rev    string
		dirty, ok bool
	}{
		{"v0.16.0-dev.c839379e", "c839379e", false, true},
		{"v0.16.0-dev.c839379e.0.20260926154704-4eabff79cc2e", "4eabff79cc2e", false, true},
		{"20260926154704-4eabff79cc2e", "4eabff79cc2e", false, true},
		{"20260926154704-4eabff79cc2e-dirty", "4eabff79cc2e", true, true},
		{"devel", "", false, false},
		{"v1.2.3", "", false, false},
	} {
		rev, dirty, ok := buildRevision(c.v)
		if rev != c.rev || dirty != c.dirty || ok != c.ok {
			t.Errorf("buildRevision(%q) = %q %v %v; want %q %v %v", c.v, rev, dirty, ok, c.rev, c.dirty, c.ok)
		}
	}
}

func TestAgeWordsReadAsAPersonReads(t *testing.T) {
	t.Parallel()
	for d, want := range map[time.Duration]string{
		-time.Second: "0s", 45 * time.Second: "45s", 3 * time.Minute: "3m", 2 * time.Hour: "2h",
		47 * time.Hour: "47h", 96 * time.Hour: "4d",
	} {
		if got := ageWords(d); got != want {
			t.Errorf("ageWords(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestExportSeatRemedyPrefersThisMachine(t *testing.T) {
	t.Parallel()
	if got := exportSeatRemedy([]string{"air", "studio"}, "studio"); got != "export NOVA_SPRINT_SEAT=studio" {
		t.Fatal(got)
	}
	if got := exportSeatRemedy([]string{"air", "studio"}, "hetzner"); got != "export NOVA_SPRINT_SEAT=air" {
		t.Fatal(got)
	}
	if got := exportSeatRemedy(nil, "hetzner"); !strings.HasPrefix(got, "nova-secrets keygen --as hetzner --key ~/.config/nova-secrets/hetzner.key") {
		t.Fatal(got)
	}
}

// TestDoctorRefusesPositionals: usage is exit 2 before anything is read.
func TestDoctorRefusesPositionals(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	d := doctorDeps{getenv: func(string) string { return "" }, sel: &seatcred.Selection{}}
	code := doctorRun(context.Background(), []string{"now"}, &out, &errOut, d)
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "takes flags, not positional arguments") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out.String(), errOut.String())
	}
}
