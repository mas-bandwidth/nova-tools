package fleetbuild

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const (
	relSha     = "c8178673f5e19ffbfe841e11826e611b74b6900d"
	relVersion = "v0.16.0-dev.c8178673"
)

// relFake is the ExecRunner of a release test: it records every call in
// order, writes the binary `go build` would, answers `nova-sprint version`
// from what the binary holds (so a rerun sees the moved build), and fails
// the step named in fail.
type relFake struct {
	home string
	fail string // an argv word that fails, "" for none
	mu   sync.Mutex
	call []call
}

type call struct {
	dir  string
	env  []string
	argv []string
}

func (f *relFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	f.call = append(f.call, call{dir, env, argv})
	f.mu.Unlock()
	line := strings.Join(argv, " ")
	if f.fail != "" && strings.Contains(line, f.fail) {
		return "boom\n", errors.New("exit status 1")
	}
	bin := filepath.Join(f.home, ".local", "bin", "nova-sprint")
	switch {
	case argv[0] == "git" && argv[1] == "clone":
		os.MkdirAll(filepath.Join(argv[len(argv)-1], ".git"), 0o755)
		return "", nil
	case argv[0] == "git" && argv[1] == "rev-parse":
		if strings.HasPrefix(relSha, strings.TrimSuffix(argv[len(argv)-1], "^{commit}")) {
			return relSha + "\n", nil
		}
		return "", errors.New("exit status 1")
	case argv[0] == "git":
		return "", nil
	case argv[0] == "go":
		for i, a := range argv {
			if a == "-o" {
				os.WriteFile(argv[i+1], []byte(relVersion), 0o755)
			}
		}
		return "", nil
	case argv[0] == bin && argv[1] == "version":
		b, err := os.ReadFile(bin)
		if err != nil {
			return "", err
		}
		return "nova-sprint " + string(b) + " darwin/arm64 go1.25.1\n", nil
	case argv[0] == bin && argv[1] == "fn":
		return "FN RECEIPT at=2026-09-26T15:00:00Z store=x load=UNCHANGED sha=abc version=" + relVersion + " ping=PONG\n", nil
	case argv[0] == "launchctl":
		return "", nil
	case argv[0] == "ssh" && strings.Contains(line, " fleet build compile "):
		return "FLEET COMPILE OK " + relVersion + "\n", nil
	case argv[0] == "ssh" && strings.HasPrefix(argv[len(argv)-1], "cat "):
		return strings.Repeat("a", 64) + "  nova-sprint\n" + strings.Repeat("b", 64) + "  nova-card\n", nil
	case argv[0] == "ssh" && strings.Contains(line, "bench-beat"):
		return "", nil
	case argv[0] == "ssh":
		return "nova-sprint " + relVersion + " linux/amd64 go1.25.1\n", nil
	}
	return "", errors.New("unexpected " + line)
}

func (f *relFake) heads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.call {
		a := c.argv
		switch {
		case a[0] == "ssh":
			out = append(out, "ssh "+a[6]+" "+strings.Fields(a[7])[0])
		case strings.HasSuffix(a[0], "nova-sprint"):
			out = append(out, "nova-sprint "+a[1])
		default:
			out = append(out, a[0]+" "+a[1])
		}
	}
	return out
}

var relMachines = []Machine{
	{Name: "space", SSH: "space", Platform: "linux-amd64", Roles: []string{"bench", "services"}},
	{Name: "hulk", SSH: "hulk", Platform: "linux-amd64", Roles: []string{"bench"}},
	{Name: "batman", SSH: "batman", Platform: "darwin-amd64", Roles: []string{"bench"}},
	{Name: "studio", SSH: "localhost", Platform: "darwin-arm64", Roles: []string{"coordination"}},
}

func newRelease(t *testing.T, f *relFake, mr *miniredis.Miniredis) (*Release, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	r := &Release{Runner: f, Home: f.home, UID: 501, GOOS: "darwin", Redis: "store.test:6380",
		AdminEnv: "NS_ADMIN", AdminPassword: "secret", Machines: relMachines, Out: &out}
	if mr != nil {
		c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { c.Close() })
		r.Client = c
	}
	return r, &out
}

// TestReleaseRunsEveryStepInOrder is #4306's DONE-WHEN: the six hand steps
// as one run, each with its receipt, the bench already on the release
// SKIPPED, the rest rolled in one pair with their beats restarted.
func TestReleaseRunsEveryStepInOrder(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk", "batman")
	mr.HSet("bench:space:beat", "build", "nova-sprint "+relVersion+" linux/amd64 go1.25.1")
	mr.HSet("bench:hulk:beat", "build", "nova-sprint v0.16.0-dev.00000000 linux/amd64 go1.25.1")
	f := &relFake{home: t.TempDir()}
	r, out := newRelease(t, f, mr)

	res, err := r.Run(context.Background(), relSha[:8])
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if res.Version != relVersion || res.Commit != relSha || res.Studio != "BUILT" || res.Fn != "ok" ||
		strings.Join(res.Rolled, ",") != "batman,hulk" || strings.Join(res.Skipped, ",") != "space" || !res.OK() {
		t.Fatalf("result %+v", res)
	}
	heads := f.heads()
	// The installs of a pair run at once, so that block is compared sorted.
	installs := heads[13:15]
	sort.Strings(installs)
	// The version probe before the build is the idempotence check.
	want := []string{
		"git clone", "git fetch", "git rev-parse", "git checkout",
		"nova-sprint version", "go build", "nova-sprint version",
		"launchctl kickstart", "launchctl kickstart", "launchctl kickstart",
		"nova-sprint fn",
		"ssh space .local/bin/nova-sprint", "ssh space cat",
		"ssh batman bash", "ssh hulk bash",
		"ssh batman sudo", "ssh hulk systemctl",
	}
	if strings.Join(heads, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sequence:\n%s\nwant:\n%s", strings.Join(heads, "\n"), strings.Join(want, "\n"))
	}
	c := f.call
	if c[5].dir != filepath.Join(f.home, "nova-bench", "release-src", "nova-tools") ||
		strings.Join(c[5].argv, " ") != "go build -trimpath -ldflags -X main.version="+relVersion+" -o "+filepath.Join(f.home, ".local", "bin", "nova-sprint.new")+" ./cmd/nova-sprint" {
		t.Errorf("go build: dir=%s argv=%q", c[5].dir, c[5].argv)
	}
	for i, unit := range ReleaseUnits {
		if got := strings.Join(c[7+i].argv, " "); got != "launchctl kickstart -k gui/501/com.nova.loop."+unit {
			t.Errorf("kickstart %d: %s", i, got)
		}
	}
	if strings.Join(c[10].env, " ") != "NOVA_SPRINT_REDIS_USER=admin NOVA_SPRINT_REDIS_PASSWORD_ENV=NS_ADMIN" ||
		strings.Join(c[10].argv[1:], " ") != "fn deploy --redis store.test:6380" {
		t.Errorf("fn deploy: env=%q argv=%q", c[10].env, c[10].argv)
	}
	if got := strings.Join(c[16].argv, " "); got != "ssh -n -o BatchMode=yes -o ConnectTimeout=6 hulk systemctl --user restart nova-loop-nova-sprint-bench-beat.service" {
		t.Errorf("hulk beat: %s", got)
	}
	if got := strings.Join(c[15].argv, " "); got != "ssh -n -o BatchMode=yes -o ConnectTimeout=6 batman sudo -n launchctl kickstart -k system/com.nova.loop.nova-sprint-bench-beat" {
		t.Errorf("batman beat: %s", got)
	}
	for _, line := range []string{
		"SOURCE " + filepath.Join(f.home, "nova-bench", "release-src", "nova-tools") + " " + relSha + "\n",
		"BUILT " + relVersion + " " + filepath.Join(f.home, ".local", "bin", "nova-sprint.new") + "\n",
		"MOVED " + filepath.Join(f.home, ".local", "bin", "nova-sprint") + " " + relVersion + "\n",
		"KICKSTARTED nova-sprint-reconciler\nKICKSTARTED sprint-table-live\nKICKSTARTED nova-sprint-bench-beat\n",
		"FN FN RECEIPT at=2026-09-26T15:00:00Z store=x load=UNCHANGED sha=abc version=" + relVersion + " ping=PONG\n",
		"FLEET RELEASE SET version=" + relVersion + " commit=" + relSha + "\n",
		"SKIPPED space " + relVersion + "\n",
		"BEAT hulk restarted\nROLLED hulk " + relVersion + "\n",
		"BEAT batman restarted\nROLLED batman " + relVersion + "\n",
	} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("missing %q in\n%s", line, out.String())
		}
	}
	if mr.HGet(ConfigKey, "version") != relVersion || mr.HGet(ConfigKey, "commit") != relSha ||
		mr.HGet(ConfigKey, "builder") != "space" || mr.HGet(ConfigKey, "self") != "studio" ||
		mr.HGet("bench:hulk", "build") != relVersion || mr.HGet("bench:batman", "build") != relVersion {
		t.Errorf("store: release=%v hulk=%v", mr.Dump(), mr.HGet("bench:hulk", "build"))
	}

	// The rerun is idempotent: the Studio answers the version (no build,
	// move or kickstart), fn deploy runs again, every bench on it is SKIPPED
	// and no ssh session starts.
	mr.HSet("bench:hulk:beat", "build", "nova-sprint "+relVersion+" linux/amd64 go1.25.1")
	mr.HSet("bench:batman:beat", "build", "nova-sprint "+relVersion+" darwin/amd64 go1.25.1")
	f.call = nil
	out.Reset()
	res, err = r.Run(context.Background(), relSha)
	if err != nil {
		t.Fatalf("rerun: %v\n%s", err, out.String())
	}
	heads = f.heads()
	want = []string{"git fetch", "git rev-parse", "git checkout", "nova-sprint version", "nova-sprint fn"}
	if strings.Join(heads, "\n") != strings.Join(want, "\n") || res.Studio != "SKIPPED" || len(res.Rolled) != 0 ||
		strings.Join(res.Skipped, ",") != "space,hulk,batman" ||
		!strings.Contains(out.String(), "SKIPPED studio "+relVersion+"\n") {
		t.Fatalf("rerun: %v %+v\n%s", heads, res, out.String())
	}
}

// TestReleaseRefusesOnTheFirstFailure: a failed step refuses with the
// remedy and nothing after it runs; a moved binary that does not answer the
// version is the same.
func TestReleaseRefusesOnTheFirstFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fail, want, last string }{
		{"go build", "go build " + relVersion, "go build"},
		{"gui/501/com.nova.loop.sprint-table-live", "kickstart the loop by hand", "launchctl kickstart"},
		{"fn deploy", "fn deploy --redis store.test:6380 as admin", "nova-sprint fn"},
		{"rev-parse", "is not a commit in dev's last 50", "git rev-parse"},
	} {
		f := &relFake{home: t.TempDir(), fail: tc.fail}
		r, out := newRelease(t, f, nil)
		r.StudioOnly = true
		_, err := r.Run(context.Background(), relSha[:10])
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v", tc.fail, err)
		}
		heads := f.heads()
		if heads[len(heads)-1] != tc.last {
			t.Errorf("%s: ran past the failure: %v\n%s", tc.fail, heads, out.String())
		}
	}
	// A short sha of 40 digits that dev's last 50 do not hold is fetched on
	// its own before the refusal.
	f := &relFake{home: t.TempDir(), fail: "rev-parse"}
	r, _ := newRelease(t, f, nil)
	r.StudioOnly = true
	if _, err := r.Run(context.Background(), strings.Repeat("d", 40)); !errors.Is(err, ErrRefused) {
		t.Fatalf("err=%v", err)
	}
	if heads := f.heads(); strings.Join(heads, ",") != "git clone,git fetch,git rev-parse,git fetch,git rev-parse" {
		t.Errorf("sequence %v", heads)
	}
	for _, sha := range []string{"", "abc", "c817867", "zz178673", relSha + "0"} {
		f := &relFake{home: t.TempDir()}
		r, _ := newRelease(t, f, nil)
		if _, err := r.Run(context.Background(), sha); !errors.Is(err, ErrRefused) || len(f.call) != 0 {
			t.Errorf("sha %q: err=%v calls=%d", sha, err, len(f.call))
		}
	}
	f = &relFake{home: t.TempDir()}
	r, _ = newRelease(t, f, nil)
	r.StudioOnly, r.BenchesOnly = true, true
	if _, err := r.Run(context.Background(), relSha); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "nothing to do") {
		t.Errorf("both only: %v", err)
	}
}

// TestReleaseEmptyAdminPasswordRefusesFnAlone: with no password in the
// variable the FN step prints the exact ssh remedy and is the one refusal;
// the Studio and the roll still run, and the result is not OK.
func TestReleaseEmptyAdminPasswordRefusesFnAlone(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "hulk")
	f := &relFake{home: t.TempDir()}
	r, out := newRelease(t, f, mr)
	r.AdminPassword = ""
	r.Benches = []string{"hulk"}
	res, err := r.Run(context.Background(), relSha)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if res.OK() || res.Fn != "refused" || res.Studio != "BUILT" || strings.Join(res.Rolled, ",") != "hulk" {
		t.Fatalf("result %+v", res)
	}
	want := "FN REFUSED store=store.test:6380 reason=no-admin-password env=NS_ADMIN remedy=export NS_ADMIN=$(ssh space 'sudo cat /var/lib/nova-redis/admin.pass'), then rerun nova-sprint fleet release " + relSha + " (the Studio and every bench already on it are SKIPPED)\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("remedy missing in\n%s", out.String())
	}
	for _, h := range f.heads() {
		if h == "nova-sprint fn" {
			t.Fatal("fn deploy ran without a password")
		}
	}
}

// TestReleaseOnlyFlags: --studio-only never opens an ssh session and needs
// no store; --benches-only with a 40-digit sha touches neither git nor go.
func TestReleaseOnlyFlags(t *testing.T) {
	t.Parallel()
	f := &relFake{home: t.TempDir()}
	r, _ := newRelease(t, f, nil)
	r.StudioOnly = true
	res, err := r.Run(context.Background(), relSha)
	if err != nil || res.Studio != "BUILT" || res.Fn != "ok" || len(res.Rolled) != 0 {
		t.Fatalf("studio only: %v %+v", err, res)
	}
	for _, h := range f.heads() {
		if strings.HasPrefix(h, "ssh") {
			t.Fatalf("studio only opened %s", h)
		}
	}

	mr := miniredis.RunT(t)
	mr.SAdd("benches", "hulk", "batman")
	f = &relFake{home: t.TempDir()}
	r, _ = newRelease(t, f, mr)
	r.BenchesOnly = true
	r.Benches = []string{"batman", "hulk"}
	res, err = r.Run(context.Background(), relSha)
	if err != nil || res.Studio != "-" || res.Fn != "-" || strings.Join(res.Rolled, ",") != "batman,hulk" {
		t.Fatalf("benches only: %v %+v", err, res)
	}
	for _, h := range f.heads() {
		if !strings.HasPrefix(h, "ssh") {
			t.Fatalf("benches only ran %s", h)
		}
	}
	// A failed bench install stops the roll.
	f = &relFake{home: t.TempDir(), fail: "ConnectTimeout=10 hulk"}
	r, _ = newRelease(t, f, mr)
	r.BenchesOnly = true
	r.Benches = []string{"hulk"}
	if _, err := r.Run(context.Background(), relSha); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "bench hulk FAIL") {
		t.Fatalf("failed install: %v", err)
	}
	// No benches at all is a refusal before the store is written.
	r, _ = newRelease(t, &relFake{home: t.TempDir()}, mr)
	r.BenchesOnly, r.Machines = true, nil
	if _, err := r.Run(context.Background(), relSha); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "no benches to roll") {
		t.Fatalf("no benches: %v", err)
	}
}

func TestRestartArgv(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goos, domain, unit string
		sudo               bool
		want               string
	}{
		{"darwin", "gui/501", "sprint-table-live", false, "launchctl kickstart -k gui/501/com.nova.loop.sprint-table-live"},
		{"darwin", "system", BeatUnit, true, "sudo -n launchctl kickstart -k system/com.nova.loop.nova-sprint-bench-beat"},
		{"linux", "system", BeatUnit, true, "systemctl --user restart nova-loop-nova-sprint-bench-beat.service"},
	} {
		argv, err := RestartArgv(tc.goos, tc.domain, tc.unit, tc.sudo)
		if err != nil || strings.Join(argv, " ") != tc.want {
			t.Errorf("%s: %q %v", tc.goos, argv, err)
		}
	}
	if _, err := RestartArgv("windows", "system", BeatUnit, false); !errors.Is(err, ErrRefused) {
		t.Errorf("windows: %v", err)
	}
}
