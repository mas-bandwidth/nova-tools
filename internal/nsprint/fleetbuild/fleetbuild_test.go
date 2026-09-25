package fleetbuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/redis/go-redis/v9"
)

const (
	testV = "v0.16.0-dev.c8178673"
	testC = "c8178673f5e19ffbfe841e11826e611b74b6900d"
)

// fakeBench stands in for every child the deploy starts: the build, each
// bench's one ssh session, this machine's rsync, its version probe and fn
// deploy. No test here reaches a host.
type fakeBench struct {
	t         *testing.T
	home      string
	mu        sync.Mutex
	calls     [][]string
	failBuild bool
	answer    map[string]string // bench -> version its nova-sprint prints
	sshFail   map[string]bool
}

func (f *fakeBench) Run(ctx context.Context, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.mu.Unlock()
	bin := filepath.Join(f.home, BinDir)
	switch {
	case argv[0] == DefaultBuildCmd:
		if f.failBuild {
			return "SPACE BUILD REFUSED: checkout\n", errors.New("exit status 1")
		}
		return "SPACE BUILD OK " + testV + "\n", nil
	case argv[0] == "ssh":
		b := argv[6]
		if f.sshFail[b] {
			return "ssh: connect to host " + b + ": timed out\n", errors.New("exit status 255")
		}
		v := testV
		if a, ok := f.answer[b]; ok {
			v = a
		}
		return "nova-sprint " + v + " linux/amd64 go1.25.1\n", nil
	case argv[0] == "rsync":
		stage := strings.TrimSuffix(argv[len(argv)-1], "/")
		for _, a := range argv {
			if t, ok := strings.CutPrefix(a, "--include="); ok {
				if err := testbin.WriteExecutable(filepath.Join(stage, t), []byte(testV), 0o755); err != nil {
					return "", err
				}
			}
		}
		return "", nil
	case argv[0] == filepath.Join(bin, "nova-sprint") && argv[1] == "version":
		b, err := os.ReadFile(argv[0])
		if err != nil {
			return "", err
		}
		return "nova-sprint " + string(b) + " darwin/arm64 go1.25.1\n", nil
	case argv[0] == filepath.Join(bin, "nova-sprint") && argv[1] == "fn":
		return "FN RECEIPT at=x store=y load=LOADED sha=s version=" + testV + " ping=PONG\n", nil
	}
	f.t.Errorf("unexpected child %q", argv)
	return "", errors.New("unexpected")
}

func (f *fakeBench) byFirst(prog string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if c[0] == prog {
			out = append(out, c)
		}
	}
	return out
}

func seed(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { c.Close() })
	mr.SAdd(BenchesKey, "hulk", "batman", "space")
	mr.HSet(ConfigKey, "version", testV, "commit", testC, "builder", "space", "self", "studio",
		"platform:hulk", "linux-amd64", "platform:batman", "darwin-amd64", "platform:space", "linux-amd64",
		"platform:studio", "darwin-arm64")
	return mr, c
}

func deploy(t *testing.T, c *redis.Client, f *fakeBench, only ...string) (Result, string) {
	t.Helper()
	ctx := context.Background()
	cfg, err := ReadConfig(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	p, err := MakePlan(cfg, only)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	d := &Deployer{Client: c, Runner: f, Home: f.home, Redis: "store:6380", Out: &out,
		Now: func() time.Time { return time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC) }}
	r, err := d.Deploy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return r, out.String()
}

// TestFleetBuildDeploysEveryBenchFromRedisConfig is the DONE-WHEN: the plan
// comes only from fleet:release and benches; the builder builds once for the
// distinct platforms; every bench rsyncs its platform from the builder (the
// builder from its own disk) and renames the tools into place in one ssh
// session; this machine does the same locally; every bench answering the
// release gets its bench:<b> build receipt; fn deploy runs last with the new
// nova-sprint.
func TestFleetBuildDeploysEveryBenchFromRedisConfig(t *testing.T) {
	t.Parallel()

	mr, c := seed(t)
	f := &fakeBench{t: t, home: t.TempDir()}
	r, out := deploy(t, c, f)
	if !r.OK() {
		t.Fatalf("result not OK: %+v\n%s", r, out)
	}

	builds := f.byFirst(DefaultBuildCmd)
	want := []string{DefaultBuildCmd, "--host", "space", "--version", testV, "--commit", testC,
		"--platform", "darwin-amd64,darwin-arm64,linux-amd64"}
	if len(builds) != 1 || !reflect.DeepEqual(builds[0], want) {
		t.Fatalf("build calls = %q, want exactly %q", builds, want)
	}
	if !reflect.DeepEqual(f.calls[0], want) {
		t.Fatalf("the build is not first: %q", f.calls[0])
	}

	ssh := map[string]string{}
	for _, a := range f.byFirst("ssh") {
		if a[1] != "-n" || a[3] != "BatchMode=yes" {
			t.Errorf("ssh argv %q lacks -n / BatchMode", a)
		}
		ssh[a[6]] = a[7]
	}
	if len(ssh) != 3 {
		t.Fatalf("ssh sessions = %v, want hulk, batman, space once each", ssh)
	}
	for b, src := range map[string]string{
		"hulk":   " space:nova-bench/release/" + testV + "/linux-amd64/ ",
		"batman": " space:nova-bench/release/" + testV + "/darwin-amd64/ ",
		"space":  " nova-bench/release/" + testV + "/linux-amd64/ ",
	} {
		s := ssh[b]
		if !strings.HasPrefix(s, "bash -c 'set -e; ") || !strings.Contains(s, src) {
			t.Errorf("%s script %q does not rsync from %q under bash", b, s, src)
		}
		for _, tool := range DefaultTools {
			if !strings.Contains(s, "--include="+tool) || !strings.Contains(s, " "+tool) {
				t.Errorf("%s script does not install %s: %q", b, tool, s)
			}
		}
		if !strings.Contains(s, "mv -f ~/.local/bin.new/$t ~/.local/bin/$t") || !strings.HasSuffix(s, "~/.local/bin/nova-sprint version | head -1'") {
			t.Errorf("%s script does not rename into place and probe: %q", b, s)
		}
	}

	rs := f.byFirst("rsync")
	if len(rs) != 1 || rs[0][len(rs[0])-2] != "space:nova-bench/release/"+testV+"/darwin-arm64/" {
		t.Fatalf("local rsync = %q, want one from space's darwin-arm64", rs)
	}
	for _, tool := range DefaultTools {
		b, err := os.ReadFile(filepath.Join(f.home, BinDir, tool))
		if err != nil || string(b) != testV {
			t.Errorf("local %s = %q, %v; want the release renamed into place", tool, b, err)
		}
		if _, err := os.Stat(filepath.Join(f.home, StageDir, tool)); !os.IsNotExist(err) {
			t.Errorf("local stage still holds %s", tool)
		}
	}

	for _, b := range []string{"hulk", "batman", "space", "studio"} {
		if got := mr.HGet("bench:"+b, "build"); got != testV {
			t.Errorf("bench:%s build = %q, want %s", b, got, testV)
		}
		if got := mr.HGet("bench:"+b, "build_sha"); got != testC {
			t.Errorf("bench:%s build_sha = %q", b, got)
		}
		if got := mr.HGet("bench:"+b, "build_at"); got != "2026-09-25T13:00:00Z" {
			t.Errorf("bench:%s build_at = %q", b, got)
		}
	}

	last := f.calls[len(f.calls)-1]
	if wantFn := []string{filepath.Join(f.home, BinDir, "nova-sprint"), "fn", "deploy", "--redis", "store:6380"}; !reflect.DeepEqual(last, wantFn) {
		t.Fatalf("last child = %q, want fn deploy with the new nova-sprint %q", last, wantFn)
	}
	for _, l := range []string{"BUILD OK builder=space", "OK hulk platform=linux-amd64", "OK studio platform=darwin-arm64", "FN RECEIPT"} {
		if !strings.Contains(out, l) {
			t.Errorf("output lacks %q:\n%s", l, out)
		}
	}
}

// TestFleetBuildMismatchAndFailureKeepOldReceipt: a bench answering another
// version, and a bench whose ssh fails, get no receipt (hulk keeps its old
// one); the rest are written and fn deploy still runs; the run is not OK.
func TestFleetBuildMismatchAndFailureKeepOldReceipt(t *testing.T) {
	t.Parallel()

	mr, c := seed(t)
	mr.HSet("bench:hulk", "build", "v0.15.2", "build_sha", "old")
	f := &fakeBench{t: t, home: t.TempDir(), answer: map[string]string{"hulk": "v0.15.2"}, sshFail: map[string]bool{"batman": true}}
	r, out := deploy(t, c, f)
	if r.OK() {
		t.Fatalf("result OK with a mismatch and a failure:\n%s", out)
	}
	st := map[string]string{}
	for _, l := range r.Lines {
		st[l.Bench] = l.Status
	}
	if want := map[string]string{"batman": "FAIL", "hulk": "MISMATCH", "space": "OK", "studio": "OK"}; !reflect.DeepEqual(st, want) {
		t.Fatalf("statuses = %v, want %v", st, want)
	}
	if got := mr.HGet("bench:hulk", "build"); got != "v0.15.2" {
		t.Errorf("hulk receipt = %q, want the old one kept", got)
	}
	if mr.Exists("bench:batman") {
		t.Errorf("batman got a receipt for a failed install")
	}
	if got := mr.HGet("bench:space", "build"); got != testV {
		t.Errorf("space receipt = %q", got)
	}
	if !r.FnOK {
		t.Errorf("fn deploy did not run although this machine installed: %s", r.FnLine)
	}
}

// TestFleetBuildFailureInstallsNothing: a failed build is the only child.
func TestFleetBuildFailureInstallsNothing(t *testing.T) {
	t.Parallel()

	mr, c := seed(t)
	f := &fakeBench{t: t, home: t.TempDir(), failBuild: true}
	r, out := deploy(t, c, f)
	if r.Built || r.OK() || len(f.calls) != 1 {
		t.Fatalf("build failure went on: calls=%q\n%s", f.calls, out)
	}
	if !strings.Contains(out, "BUILD FAIL builder=space") {
		t.Errorf("output lacks BUILD FAIL:\n%s", out)
	}
	for _, b := range []string{"hulk", "studio"} {
		if mr.Exists("bench:" + b) {
			t.Errorf("bench:%s written after a failed build", b)
		}
	}
}

// TestFleetBuildNamedBenchesOnly: --bench narrows the targets and the platforms built.
func TestFleetBuildNamedBenchesOnly(t *testing.T) {
	t.Parallel()

	mr, c := seed(t)
	f := &fakeBench{t: t, home: t.TempDir()}
	r, _ := deploy(t, c, f, "hulk", "studio")
	if !r.OK() || len(r.Lines) != 2 {
		t.Fatalf("lines = %+v", r.Lines)
	}
	if got := f.calls[0][len(f.calls[0])-1]; got != "darwin-arm64,linux-amd64" {
		t.Errorf("built %s, want only the named benches' platforms", got)
	}
	if mr.Exists("bench:batman") {
		t.Errorf("batman installed although not named")
	}
}

// TestFleetBuildRefusesIncompleteConfig: every gap in the plan is refused
// before any child, naming the remedy; set writes only valid fields.
func TestFleetBuildRefusesIncompleteConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		edit  func(mr *miniredis.Miniredis)
		only  []string
		match string
	}{
		{"no version", func(mr *miniredis.Miniredis) { mr.HDel(ConfigKey, "version") }, nil, "HSET fleet:release version"},
		{"commit not the version's", func(mr *miniredis.Miniredis) { mr.HSet(ConfigKey, "commit", strings.Repeat("a", 40)) }, nil, "not the version's sha"},
		{"short commit", func(mr *miniredis.Miniredis) { mr.HSet(ConfigKey, "commit", "c8178673") }, nil, "full 40-hex"},
		{"no builder", func(mr *miniredis.Miniredis) { mr.HDel(ConfigKey, "builder") }, nil, "builder"},
		{"no self", func(mr *miniredis.Miniredis) { mr.HDel(ConfigKey, "self") }, nil, "self"},
		{"bench without platform", func(mr *miniredis.Miniredis) { mr.HDel(ConfigKey, "platform:batman") }, nil, "platform:batman"},
		{"tools without nova-sprint", func(mr *miniredis.Miniredis) { mr.HSet(ConfigKey, "tools", "nova-card") }, nil, "leave out nova-sprint"},
		{"unknown bench", func(mr *miniredis.Miniredis) {}, []string{"vision"}, "neither in the benches set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr, c := seed(t)
			tc.edit(mr)
			cfg, err := ReadConfig(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			_, err = MakePlan(cfg, tc.only)
			if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("err = %v, want a refusal naming %q", err, tc.match)
			}
		})
	}

	mr, c := seed(t)
	for _, bad := range [][]string{{"colour=red"}, {"platform:vision=windows-amd64"}, {"version=dev-c8178673"}, {"builder=space;rm"}, {"novalue"}} {
		if _, err := SetFields(ctx, c, bad); !errors.Is(err, ErrRefused) {
			t.Errorf("SetFields(%q) = %v, want refused", bad, err)
		}
	}
	if n, err := SetFields(ctx, c, []string{"platform:vision=linux-arm64", "tools=nova-sprint,nova-card"}); err != nil || n != 2 {
		t.Fatalf("SetFields = %d, %v", n, err)
	}
	if got := mr.HGet(ConfigKey, "platform:vision"); got != "linux-arm64" {
		t.Errorf("platform:vision = %q", got)
	}
	cfg, _ := ReadConfig(ctx, c)
	if got := fmt.Sprint(cfg.Tools); got != "[nova-sprint nova-card]" {
		t.Errorf("tools = %s", got)
	}
}
