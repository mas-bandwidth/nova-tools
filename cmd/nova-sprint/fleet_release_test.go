package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

const (
	verbSha     = "c8178673f5e19ffbfe841e11826e611b74b6900d"
	verbVersion = "v0.16.0-dev.c8178673"
	verbPW      = "pw-seat-4356"
)

// verbRelFake is the verb test's ExecRunner: every child answers as it
// would, no process starts and no host is reached; a restarted beat catches
// up in the store.
type verbRelFake struct {
	home  string
	mr    *miniredis.Miniredis
	mu    sync.Mutex
	calls []string
	envs  map[string][]string // argv -> the extra environment it was given
}

func (f *verbRelFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	line := strings.Join(argv, " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	if f.envs == nil {
		f.envs = map[string][]string{}
	}
	f.envs[line] = env
	f.mu.Unlock()
	switch {
	case argv[0] == "git" && argv[1] == "clone":
		d := argv[len(argv)-1]
		os.MkdirAll(filepath.Join(d, ".git"), 0o755)
		return "", os.WriteFile(filepath.Join(d, "go.mod"), []byte("module m\n\ngo 1.26.6\n"), 0o644)
	case argv[0] == "git" && argv[1] == "ls-remote":
		return verbSha + "\trefs/heads/dev\n", nil
	case argv[0] == "git" && argv[1] == "rev-parse":
		return verbSha + "\n", nil
	case argv[0] == "git", argv[0] == "launchctl":
		return "", nil
	case argv[0] == "go":
		for i, a := range argv {
			if a == "-o" {
				return "", os.WriteFile(argv[i+1], []byte(verbVersion), 0o755)
			}
		}
	case len(argv) == 2 && argv[1] == "version":
		b, err := os.ReadFile(argv[0])
		return "nova-sprint " + string(b) + " darwin/arm64 go1.26.6\n", err
	case len(argv) > 2 && argv[1] == "fn":
		return "FN " + argv[2] + " ok\n", nil
	case argv[0] == "ssh" && strings.Contains(line, " fleet build compile "):
		return "FLEET COMPILE OK " + verbVersion + "\n", nil
	case argv[0] == "ssh" && strings.HasPrefix(argv[len(argv)-1], "cat "):
		return strings.Repeat("a", 64) + "  nova-sprint\n", nil
	case argv[0] == "ansible-playbook":
		return "PLAY RECAP ***\nhulk : ok=9 changed=1 unreachable=0 failed=0\n", nil
	case argv[0] == "ansible":
		for _, h := range strings.Split(argv[1], ",") {
			f.mr.HSet("bench:"+h+":beat", "build", "nova-sprint "+verbVersion+" linux/amd64 go1.26.6")
		}
		return argv[1] + " | CHANGED | rc=0 >>\n", nil
	}
	return "", errors.New("unexpected " + line)
}

func releaseRegistry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "space\tspace\tlinux/x64\tbench,services\nhulk\thulk\tlinux/x64\tbench\nstudio\tlocalhost\tdarwin/arm64\tcoordination\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func releasePlayDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, f := range []string{"inventory.py", "tools.yml"} {
		if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// verbDeps are the fakes: env is the environment, seat the run's seat, and
// the seat holds verbPW as its admin password.
func verbDeps(f *verbRelFake, env map[string]string, seat string, sleeps *int) releaseDeps {
	return releaseDeps{Runner: f, Home: func() (string, error) { return f.home, nil },
		Getenv: func(k string) string { return env[k] }, UID: 501, PID: 9, GOOS: "darwin", Open: openFleetStore,
		Seat: func() string { return seat },
		AdminSecret: func(s string) (string, error) {
			if s != "studio" {
				return "", errors.New("seat " + s + ": holds no NOVA_REDIS_ADMIN_PASSWORD")
			}
			return verbPW, nil
		},
		Sleep: func(context.Context, time.Duration) error { *sleeps++; return nil }}
}

// TestFleetReleaseIsOneCommand is #4356 A's DONE-WHEN through the verb:
// `fleet release dev --seat studio` runs every step with its receipt, reads
// the admin password from the seat (never an ssh), and ends FLEET RELEASE OK,
// exit 0; nothing waits on the clock.
func TestFleetReleaseIsOneCommand(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk")
	mr.HSet("bench:space:beat", "build", "nova-sprint "+verbVersion+" linux/amd64 go1.26.6")
	f := &verbRelFake{home: t.TempDir(), mr: mr}
	reg, dir := releaseRegistry(t), releasePlayDir(t)
	sleeps := 0
	var out, errOut bytes.Buffer
	code := runFleetReleaseWith(context.Background(), []string{"--sha", "dev", "--redis", mr.Addr(), "--machines", reg},
		&out, &errOut, verbDeps(f, map[string]string{fleetbuild.PlayDirEnv: dir}, "studio", &sleeps))
	if code != 0 || errOut.Len() != 0 || sleeps != 0 {
		t.Fatalf("code=%d sleeps=%d\n%s\nerr=%s", code, sleeps, out.String(), errOut.String())
	}
	var steps []string
	for _, l := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == "RELEASE" {
			steps = append(steps, f[1]+"="+strings.TrimSuffix(f[2], ":"))
		}
	}
	if got := strings.Join(steps, " "); got != "build=OK fn=OK fn-check=OK play=OK self=OK" {
		t.Errorf("steps %s\n%s", got, out.String())
	}
	if !strings.HasSuffix(out.String(), "VERIFY space want="+verbVersion+" have="+verbVersion+" ok\nVERIFY hulk want="+verbVersion+" have="+verbVersion+" ok\nFLEET RELEASE OK version="+verbVersion+" benches=2 behind=-\n") {
		t.Errorf("tail:\n%s", out.String())
	}
	bin := filepath.Join(f.home, "nova-bench", "release-src", "bin", "nova-sprint-"+verbVersion)
	deploy := bin + " fn deploy --redis " + mr.Addr()
	if got := strings.Join(f.envs[deploy], " "); got != "NOVA_SEAT= NOVA_SPRINT_REDIS_USER=admin NOVA_SPRINT_REDIS_PASSWORD_ENV=NS_ADMIN NS_ADMIN="+verbPW {
		t.Errorf("fn deploy env %q (calls %v)", got, f.calls)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "ssh ") && !strings.Contains(c, " fleet build compile ") && !strings.Contains(c, "cat nova-bench/release/") {
			t.Errorf("an ssh that is not the builder's: %s", c)
		}
	}
	if strings.Contains(out.String(), verbPW) {
		t.Errorf("the admin password is in the receipts")
	}
}

// TestFleetReleaseAdminPassword: NS_ADMIN wins; else the seat's; with
// neither the fn step alone is refused with the seal remedy, the rest runs,
// and the verb ends FAIL, exit 1.
func TestFleetReleaseAdminPassword(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, seat string
		env        map[string]string
		pw, why    string
	}{
		{"env wins", "studio", map[string]string{"NS_ADMIN": "from-env"}, "from-env", ""},
		{"seat", "studio", nil, verbPW, ""},
		{"no seat", "", nil, "", "NS_ADMIN is empty and no seat is named (seal it once: make -C ~/rowan-working/rowan-tools/fleet store-seal ROLE=admin SEAT=<seat>, then rerun with --seat <seat> (or with NS_ADMIN set))"},
		{"seat without it", "air", nil, "", "NS_ADMIN is empty and seat air gave none: seat air: holds no NOVA_REDIS_ADMIN_PASSWORD (seal it once: make -C ~/rowan-working/rowan-tools/fleet store-seal ROLE=admin SEAT=air, then rerun with --seat air (or with NS_ADMIN set))"},
	} {
		sleeps := 0
		pw, _, why := adminPassword("NS_ADMIN", verbDeps(&verbRelFake{}, tc.env, tc.seat, &sleeps))
		if pw != tc.pw || why != tc.why {
			t.Errorf("%s: pw=%q why=%q", tc.name, pw, why)
		}
	}

	mr := miniredis.RunT(t)
	mr.SAdd("benches", "hulk")
	f := &verbRelFake{home: t.TempDir(), mr: mr}
	sleeps := 0
	var out, errOut bytes.Buffer
	code := runFleetReleaseWith(context.Background(),
		[]string{"--sha", verbSha[:8], "--redis", mr.Addr(), "--machines", releaseRegistry(t), "--bench", "hulk", "--play-dir", releasePlayDir(t)},
		&out, &errOut, verbDeps(f, nil, "", &sleeps))
	if code != 1 || !strings.Contains(out.String(), "RELEASE fn REFUSED: no admin password for "+mr.Addr()+": NS_ADMIN is empty and no seat is named") ||
		!strings.Contains(out.String(), "RELEASE self OK ") ||
		!strings.HasSuffix(out.String(), "FLEET RELEASE FAIL version="+verbVersion+" benches=1 behind=-\n") {
		t.Fatalf("no password: code=%d\n%s", code, out.String())
	}
}

// TestFleetReleaseUsage: the held-bench form and the sha form share one flag
// set and refuse each other's flags; the retired spellings (fleet roll,
// --studio-only, --benches-only) each refuse in one line naming the
// survivor; all exit 2 before any child or store.
func TestFleetReleaseUsage(t *testing.T) {
	t.Parallel()
	f := &verbRelFake{home: t.TempDir()}
	opened := false
	sleeps := 0
	deps := verbDeps(f, nil, "", &sleeps)
	deps.Open = func(ctx context.Context, addr string) (*store.Store, error) {
		opened = true
		return nil, errors.New("no store")
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "wants --sha <sha>|dev (the roll) or --bench <b>"},
		{[]string{"--bench", "hulk", "--wait", "1s"}, "wants --sha <sha> with --wait"},
		{[]string{verbSha}, "takes flags, not positional arguments"},
		{[]string{"--sha", verbSha, "deadbeef"}, "takes flags, not positional arguments"},
		{[]string{"--sha", verbSha, "--studio-only"}, "--studio-only is retired: nova-sprint self update does this machine alone"},
		{[]string{"--sha", verbSha, "--benches-only"}, "--benches-only is retired: fleet release --sha <sha> is the whole roll"},
		{[]string{"--sha", verbSha, "--wait", "-1s"}, "--wait must be >= 0"},
		{[]string{"--sha", verbSha, "--play", "../x.yml"}, "--play names a play file"},
		{[]string{"--nope", "--sha", verbSha}, "--nope is not a flag of nova-sprint fleet release"},
		{[]string{"--sha", verbSha, "--benches", "hulk"}, "--benches is not a flag of nova-sprint fleet release"},
	} {
		var out, errOut bytes.Buffer
		code := runFleetReleaseWith(context.Background(), tc.args, &out, &errOut, deps)
		if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.want) || len(f.calls) != 0 || opened {
			t.Errorf("%v: code=%d out=%q err=%q calls=%v opened=%v", tc.args, code, out.String(), errOut.String(), f.calls, opened)
		}
	}
	var out, errOut bytes.Buffer
	if code := runFleet(context.Background(), []string{"roll", "--to", verbSha}, &out, &errOut); code != 2 ||
		!strings.Contains(errOut.String(), "fleet roll: is retired into fleet release: nova-sprint fleet release <sha>|dev is the whole roll") {
		t.Errorf("fleet roll: code=%d err=%q", code, errOut.String())
	}
	// A bad sha or a missing registry refuses before any child or store, exit 1.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--sha", "nothex"}, `"nothex" is not a commit sha of 8 to 40 hex digits`},
		{[]string{"--sha", verbSha}, "read the machines registry: --machines <file>, or NOVA_FLEET_MACHINES"},
		{[]string{"--machines", filepath.Join(t.TempDir(), "none.tsv"), "--sha", verbSha}, "machines registry:"},
	} {
		out.Reset()
		errOut.Reset()
		code := runFleetReleaseWith(context.Background(), tc.args, &out, &errOut, deps)
		if code != 1 || !strings.HasPrefix(errOut.String(), "FLEET RELEASE REFUSED: ") || !strings.Contains(errOut.String(), tc.want) || len(f.calls) != 0 || opened {
			t.Errorf("%v: code=%d err=%q calls=%v", tc.args, code, errOut.String(), f.calls)
		}
	}
	// The held form still dials the store with --bench alone.
	out.Reset()
	errOut.Reset()
	if code := runFleetReleaseWith(context.Background(), []string{"--bench", "hulk"}, &out, &errOut, deps); code != 5 || !opened {
		t.Errorf("held form: code=%d opened=%v err=%q", code, opened, errOut.String())
	}
}
