package fleetbuild

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// testMachines is a machines registry in the fleet's shape, with a role the
// fleet package does not know (ingress) that convergence must read past.
const testMachines = "# name\tssh\tos/arch\troles\tseat\tcores\tnotes\n" +
	"studio\tstudio\tdarwin/arm64\tbench,coordination,runner\tstudio\t32\t-\n" +
	"hulk\thulk\tlinux/x64\tbench,runner\tswarm-hulk\t64\t-\n" +
	"space\tspace\tlinux/x64\tbench,runner,services\tswarm-space\t32\t-\n" +
	"batman\tbatman\tdarwin/amd64\tbench,runner\tswarm-batman\t8\t-\n" +
	"hetzner\thetzner\tlinux/x64\tbench,ingress\tswarm-hetzner\t8\t-\n"

func beat(mr *miniredis.Miniredis, bench, line string) {
	mr.HSet("bench:"+bench+":beat", "at", "1", "build", line)
	mr.SetTTL("bench:"+bench+":beat", 3*time.Second)
}

// TestConvergeFillsBuilderSelfAndPlatforms: nothing typed. builder is the one
// services machine, self the one coordination machine, and every bench's
// platform comes from bench:<b>:desired, else the registry, else its beat.
func TestConvergeFillsBuilderSelfAndPlatforms(t *testing.T) {
	ms, err := ParseMachines(strings.NewReader(testMachines))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 5 || ms[4].Name != "hetzner" || ms[4].Platform != "linux-amd64" || ms[3].Platform != "darwin-amd64" {
		t.Fatalf("machines = %+v", ms)
	}
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { c.Close() })
	mr.SAdd(BenchesKey, "hulk", "space", "batman", "vision", "studio")
	mr.HSet(ConfigKey, "platform:hulk", "linux-amd64") // already right: not rewritten
	mr.HSet("bench:batman:desired", "slots", "4", "platform", "darwin-arm64")
	beat(mr, "vision", "nova-sprint v0.16.0-dev.aaaaaaaa linux/arm64 go1.26.1")
	f, err := ReadFacts(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	f.Machines = ms
	got := Converge(f)
	want := map[string]string{"builder": "space", "self": "studio", "platform:space": "linux-amd64",
		"platform:batman": "darwin-arm64", "platform:vision": "linux-arm64", "platform:studio": "darwin-arm64"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("converge = %v, want %v", got, want)
	}
	if err := WriteConverged(context.Background(), c, got); err != nil {
		t.Fatal(err)
	}
	f, _ = ReadFacts(context.Background(), c)
	f.Machines = ms
	if again := Converge(f); len(again) != 0 {
		t.Fatalf("a converged release converges again: %v", again)
	}
	// Without a registry, builder and self keep what is stored.
	f.Machines = nil
	if again := Converge(f); len(again) != 0 {
		t.Fatalf("no registry changed %v", again)
	}
	if _, err := ParseMachines(strings.NewReader("studio\tstudio\n")); !errors.Is(err, ErrRefused) {
		t.Fatalf("a short line = %v, want refused", err)
	}
}

func TestParseManifestIsEveryNovaTool(t *testing.T) {
	got, err := ParseManifest(testManifest)
	if err != nil || !reflect.DeepEqual(got, manifestTools) {
		t.Fatalf("manifest = %v, %v", got, err)
	}
	if _, err := ParseManifest(strings.Repeat("a", 64) + "  nova-card\n"); !errors.Is(err, ErrRefused) {
		t.Fatalf("a manifest without nova-sprint = %v", err)
	}
}

// TestDutyWouldInstallEveryDriftingBench: benches beating another version
// drift, one on the release is current, one with no live beat is quiet; the
// dry run prints WOULD INSTALL per drifting bench and starts nothing; the
// live pass claims the version once and starts one deploy for exactly the
// drifting benches, and the next pass is HELD.
func TestDutyWouldInstallEveryDriftingBench(t *testing.T) {
	mr, c := seed(t)
	mr.SAdd(BenchesKey, "vision")
	beat(mr, "hulk", "nova-sprint v0.16.0-dev.7658e89c linux/amd64 go1.26.1")
	beat(mr, "space", "nova-sprint "+testV+" linux/amd64 go1.26.1")
	beat(mr, "batman", "nova-sprint 20260924090000-bbbbbbbbbbbb darwin/amd64 go1.26.1")
	// vision is registered and not beating.
	var out bytes.Buffer
	d := &Duty{Client: c, DryRun: true, Out: &out}
	r, err := d.Pass(context.Background())
	if err != nil || r.Outcome != "WOULD" {
		t.Fatalf("dry pass: %+v %v", r, err)
	}
	want := "FLEET DEPLOY WOULD INSTALL batman beat=20260924090000-bbbbbbbbbbbb want=" + testV + "\n" +
		"FLEET DEPLOY WOULD INSTALL hulk beat=v0.16.0-dev.7658e89c want=" + testV + "\n" +
		"FLEET DEPLOY DRY-RUN version=" + testV + " commit=" + testC[:12] + " drift=2 current=1 quiet=1\n"
	if out.String() != want {
		t.Fatalf("dry pass printed:\n%s\nwant:\n%s", out.String(), want)
	}
	if mr.Exists(DeployKey) {
		t.Fatal("a dry pass claimed the deploy")
	}

	var started [][]string
	out.Reset()
	d = &Duty{Client: c, Out: &out, Start: func(_ context.Context, v string, b []string) error {
		started = append(started, append([]string{v}, b...))
		return nil
	}}
	if r, err := d.Pass(context.Background()); err != nil || r.Outcome != "START" {
		t.Fatalf("live pass: %+v %v", r, err)
	}
	if !reflect.DeepEqual(started, [][]string{{testV, "batman", "hulk"}}) {
		t.Fatalf("started %v", started)
	}
	if got, _ := mr.Get(DeployKey); got != testV {
		t.Fatalf("claim = %q", got)
	}
	if r, err := d.Pass(context.Background()); err != nil || r.Outcome != "HELD" || len(started) != 1 {
		t.Fatalf("second pass: %+v %v started=%v", r, err, started)
	}
	if !strings.Contains(out.String(), "FLEET DEPLOY START version="+testV+" commit="+testC[:12]+" benches=batman,hulk\n") ||
		!strings.Contains(out.String(), "FLEET DEPLOY HELD version="+testV) {
		t.Fatalf("live output:\n%s", out.String())
	}
	// The benches catch up: the fleet is current and nothing starts.
	beat(mr, "hulk", "nova-sprint "+testV+" linux/amd64 go1.26.1")
	beat(mr, "batman", "nova-sprint "+testV+" darwin/amd64 go1.26.1")
	mr.Del(DeployKey)
	if r, err := d.Pass(context.Background()); err != nil || r.Outcome != "CURRENT" || len(started) != 1 {
		t.Fatalf("current pass: %+v %v", r, err)
	}
	// A failed start gives the claim back.
	beat(mr, "hulk", "nova-sprint v0.15.0-dev.00000000 linux/amd64 go1.26.1")
	d.Start = func(context.Context, string, []string) error { return errors.New("no exe") }
	if _, err := d.Pass(context.Background()); err == nil || mr.Exists(DeployKey) {
		t.Fatalf("failed start: err=%v claim kept=%t", err, mr.Exists(DeployKey))
	}
}

// TestDutyNoPlanWithoutARelease: before any landing there is nothing to
// converge the fleet to.
func TestDutyNoPlanWithoutARelease(t *testing.T) {
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { c.Close() })
	mr.SAdd(BenchesKey, "hulk")
	beat(mr, "hulk", "nova-sprint v0.16.0-dev.7658e89c linux/amd64 go1.26.1")
	d := &Duty{Client: c, Start: func(context.Context, string, []string) error { t.Fatal("started"); return nil }}
	if r, err := d.Pass(context.Background()); err != nil || r.Outcome != "NOPLAN" {
		t.Fatalf("%+v %v", r, err)
	}
	// The pass converged hulk's platform from its beat.
	if got := mr.HGet(ConfigKey, "platform:hulk"); got != "linux-amd64" {
		t.Fatalf("platform:hulk = %q", got)
	}
}
