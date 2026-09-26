package fleetbuild

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const (
	relSha     = "c8178673f5e19ffbfe841e11826e611b74b6900d"
	relVersion = "v0.16.0-dev.c8178673"
	relOld     = "v0.16.0-dev.00000000"
	relPW      = "pw-4356-sekrit"
)

// call is one child a fake runner was asked to start.
type call struct {
	dir  string
	env  []string
	argv []string
}

const rollRecap = `PLAY [benches] *****

TASK [tools : install] *****
ok: [hulk]

PLAY RECAP *********************************************************************
batman                     : ok=9    changed=1    unreachable=0    failed=0
hulk                       : ok=9    changed=1    unreachable=0    failed=0
space                      : ok=9    changed=0    unreachable=0    failed=0
`

// relFake is the ExecRunner of a release test. It starts no process and
// reaches no host: the clone is a directory with a go.mod, `go build` writes
// the stamped version into its -o file and `<file> version` answers what the
// file holds (so a rerun sees what the last run built), the builder's compile
// and manifest, the play and the ad hoc beat restart answer as ansible does,
// and the step whose argv holds fail fails.
type relFake struct {
	home      string
	fail      string // an argv substring that fails, "" for none
	playErr   bool
	unreached string // a host the beat restart cannot reach
	tip       string // what git ls-remote answers
	restarted func(hosts []string)
	mu        sync.Mutex
	call      []call
}

func (f *relFake) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	f.mu.Lock()
	f.call = append(f.call, call{dir, env, argv})
	f.mu.Unlock()
	line := strings.Join(argv, " ")
	if f.fail != "" && strings.Contains(line, f.fail) {
		return "boom\n", errors.New("exit status 1")
	}
	switch {
	case argv[0] == "git" && argv[1] == "clone":
		d := argv[len(argv)-1]
		os.MkdirAll(filepath.Join(d, ".git"), 0o755)
		return "", os.WriteFile(filepath.Join(d, "go.mod"), []byte("module m\n\ngo 1.26.6\n"), 0o644)
	case argv[0] == "git" && argv[1] == "ls-remote":
		return f.tip, nil
	case argv[0] == "git" && argv[1] == "rev-parse":
		rev := strings.TrimSuffix(argv[len(argv)-1], "^{commit}")
		if rev == "origin/dev" || strings.HasPrefix(relSha, rev) {
			return relSha + "\n", nil
		}
		return "", errors.New("exit status 1")
	case argv[0] == "git":
		return "", nil
	case argv[0] == "go":
		var out, v string
		for i, a := range argv {
			if a == "-o" {
				out = argv[i+1]
			}
			if strings.HasPrefix(a, "-X main.version=") {
				v = strings.TrimPrefix(a, "-X main.version=")
			}
		}
		return "", os.WriteFile(out, []byte(v), 0o755)
	case len(argv) == 2 && argv[1] == "version":
		b, err := os.ReadFile(argv[0])
		if err != nil {
			return "", err
		}
		return "nova-sprint " + string(b) + " darwin/arm64 go1.26.6\n", nil
	case len(argv) > 2 && argv[1] == "fn" && argv[2] == "deploy":
		return "FN RECEIPT at=2026-09-26T16:00:00Z store=s load=LOADED sha=abc version=" + relVersion + " ping=PONG\n", nil
	case len(argv) > 2 && argv[1] == "fn" && argv[2] == "check":
		return "OK nova_sprint sha=abc ping=PONG\n", nil
	case argv[0] == "launchctl":
		return "", nil
	case argv[0] == "ssh" && strings.Contains(line, " fleet build compile "):
		return "FLEET COMPILE OK " + relVersion + "\n", nil
	case argv[0] == "ssh" && strings.HasPrefix(argv[len(argv)-1], "cat "):
		return strings.Repeat("a", 64) + "  nova-sprint\n" + strings.Repeat("b", 64) + "  nova-card\n", nil
	case argv[0] == "ansible-playbook":
		if f.playErr {
			return rollRecap + "hulk : ok=3 changed=0 unreachable=1 failed=0\n", errors.New("exit status 4")
		}
		return rollRecap, nil
	case argv[0] == "ansible":
		hosts := strings.Split(argv[1], ",")
		var b strings.Builder
		for _, h := range hosts {
			if h == f.unreached {
				b.WriteString(h + " | UNREACHABLE! => {\n    \"changed\": false\n}\n")
				continue
			}
			b.WriteString(h + " | CHANGED | rc=0 >>\n\n")
		}
		if f.restarted != nil {
			f.restarted(hosts)
		}
		return b.String(), nil
	}
	return "", errors.New("unexpected " + line)
}

// heads is each call's first two words (a binary by its base name), the
// order a run is compared by.
func (f *relFake) heads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.call {
		a := c.argv
		switch {
		case a[0] == "ssh":
			out = append(out, "ssh "+a[6]+" "+strings.Fields(a[7])[0])
		case strings.HasPrefix(a[0], "/"):
			out = append(out, filepath.Base(a[0])+" "+a[1])
		default:
			out = append(out, a[0]+" "+a[1])
		}
	}
	return out
}

func (f *relFake) find(prefix string) (call, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.call {
		if strings.HasPrefix(strings.Join(c.argv, " "), prefix) {
			return c, true
		}
	}
	return call{}, false
}

var relMachines = []Machine{
	{Name: "space", SSH: "space", Platform: "linux-amd64", Roles: []string{"bench", "services"}},
	{Name: "hulk", SSH: "hulk", Platform: "linux-amd64", Roles: []string{"bench"}},
	{Name: "batman", SSH: "batman", Platform: "darwin-amd64", Roles: []string{"bench"}},
	{Name: "studio", SSH: "localhost", Platform: "darwin-arm64", Roles: []string{"coordination"}},
}

func beatLine(v, osArch string) string { return "nova-sprint " + v + " " + osArch + " go1.26.6" }

func playDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, f := range []string{PlayInventory, DefaultPlay} {
		if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// relStore is a store with three benches: space already on the release,
// hulk on an older build, batman with no beat.
func relStore(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	mr.SAdd("benches", "space", "hulk", "batman")
	mr.HSet("bench:space:beat", "build", beatLine(relVersion, "linux/amd64"))
	mr.HSet("bench:hulk:beat", "build", beatLine(relOld, "linux/amd64"))
	return mr
}

// catchUp makes a fake's beat restart put the restarted benches on the
// release, as a restarted beat does within a tick.
func catchUp(mr *miniredis.Miniredis) func([]string) {
	return func(hosts []string) {
		for _, h := range hosts {
			mr.HSet("bench:"+h+":beat", "build", beatLine(relVersion, "linux/amd64"))
		}
	}
}

func newRelease(t *testing.T, f *relFake, mr *miniredis.Miniredis) (*Release, *bytes.Buffer, *int) {
	t.Helper()
	var out bytes.Buffer
	sleeps := 0
	r := &Release{Runner: f, Home: f.home, UID: 501, GOOS: "darwin", PID: 7, Redis: "store.test:6380",
		AdminEnv: "NS_ADMIN", AdminPassword: relPW, Seat: "studio", Machines: relMachines,
		PlayDir: playDir(t), Registry: "/reg/machines.tsv", Wait: 10 * time.Second, Poll: 5 * time.Second,
		Sleep: func(context.Context, time.Duration) error { sleeps++; return nil }, Out: &out}
	if mr != nil {
		c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { c.Close() })
		r.Client = c
	}
	return r, &out, &sleeps
}

func states(res ReleaseResult) string {
	var s []string
	for _, st := range res.Steps {
		s = append(s, st.Name+"="+st.State)
	}
	return strings.Join(s, " ")
}

// TestReleaseRollsTheWholeFleetInOrder is #4356 A's DONE-WHEN on the good
// path: `fleet release dev` resolves dev's tip, then build, fn, fn-check,
// the play with the beats behind restarted through ansible, self update and
// the verify, each with its receipt, and FLEET RELEASE OK last. The rerun
// keeps what is done: no build here, no beat restart, no kickstart.
func TestReleaseRollsTheWholeFleetInOrder(t *testing.T) {
	t.Parallel()
	mr := relStore(t)
	f := &relFake{home: t.TempDir(), tip: relSha + "\trefs/heads/dev\n"}
	f.restarted = catchUp(mr)
	r, out, sleeps := newRelease(t, f, mr)

	res, err := r.Run(context.Background(), "dev")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if !res.OK() || res.Version != relVersion || res.Commit != relSha || *sleeps != 0 ||
		states(res) != "build=OK fn=OK fn-check=OK play=OK self=OK" {
		t.Fatalf("result %s %+v sleeps=%d\n%s", states(res), res, *sleeps, out.String())
	}
	want := []string{
		"git ls-remote",
		"git clone", "git fetch", "git rev-parse", "git checkout",
		"nova-sprint-" + relVersion + " version", "go build", "nova-sprint-" + relVersion + " version",
		"ssh space .local/bin/nova-sprint", "ssh space cat",
		"nova-sprint-" + relVersion + " fn", "nova-sprint-" + relVersion + " fn",
		"ansible-playbook -i", "ansible hulk,batman",
		"git fetch", "git rev-parse", "git checkout", "git merge-base", "nova-sprint version", "go build", ".nova-sprint.self-update.7 version", "nova-sprint version",
		"launchctl kickstart", "launchctl kickstart", "launchctl kickstart",
	}
	if got := f.heads(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sequence:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	bin := filepath.Join(f.home, "nova-bench", "release-src", "bin", "nova-sprint-"+relVersion)
	if c, _ := f.find("go build"); c.dir != filepath.Join(f.home, "nova-bench", "release-src", "nova-tools") ||
		strings.Join(c.env, " ") != "GOTOOLCHAIN=go1.26.6" || c.argv[len(c.argv)-2] != bin {
		t.Errorf("release build: %+v", c)
	}
	// fn deploy and fn check run the release's binary as the admin user,
	// the password in their environment alone, never a seat login.
	for _, sub := range []string{"deploy", "check"} {
		c, ok := f.find(bin + " fn " + sub + " --redis store.test:6380")
		if !ok || strings.Join(c.env, " ") != "NOVA_SEAT= NOVA_SPRINT_REDIS_USER=admin NOVA_SPRINT_REDIS_PASSWORD_ENV=NS_ADMIN NS_ADMIN="+relPW {
			t.Errorf("fn %s: %v %q", sub, ok, c.env)
		}
	}
	// The play is limited to the benches, so the coordinator's fn-load
	// play never runs; the restart goes through the same inventory.
	if c, _ := f.find("ansible-playbook"); c.dir != r.PlayDir ||
		strings.Join(c.argv, " ") != "ansible-playbook -i inventory.py tools.yml --forks 16 --diff -e nova_build="+relVersion+" --limit space,hulk,batman" ||
		strings.Join(c.env, " ") != "ANSIBLE_NOCOWS=1 ANSIBLE_HOST_KEY_CHECKING=True FLEET_REGISTRY=/reg/machines.tsv" {
		t.Errorf("play: %+v", c)
	}
	if c, _ := f.find("ansible "); c.dir != r.PlayDir || strings.Join(c.argv[:8], " ") != "ansible hulk,batman -i inventory.py --forks 16 -m ansible.builtin.shell" {
		t.Errorf("beat restart: %q", c.argv)
	}
	s := out.String()
	for _, line := range []string{
		"DEV TIP " + relSha + "\n",
		"SOURCE " + filepath.Join(f.home, "nova-bench", "release-src", "nova-tools") + " " + relSha + "\n",
		"BUILT " + relVersion + " " + bin + "\n",
		"FLEET RELEASE SET version=" + relVersion + " commit=" + relSha + "\n",
		"BUILD OK builder=space version=" + relVersion + " platforms=darwin-amd64,linux-amd64: FLEET COMPILE OK " + relVersion + "\n",
		"MANIFEST tools=2 nova-card,nova-sprint\n",
		"RELEASE build OK version=" + relVersion + " commit=c8178673f5e1 builder=space platforms=darwin-amd64,linux-amd64 tools=2 bin=" + bin + "\n",
		"RELEASE fn OK FN RECEIPT at=2026-09-26T16:00:00Z store=s load=LOADED sha=abc version=" + relVersion + " ping=PONG\n",
		"RELEASE fn-check OK OK nova_sprint sha=abc ping=PONG\n",
		"RECAP batman : ok=9 changed=1 unreachable=0 failed=0\nRECAP hulk : ok=9 changed=1 unreachable=0 failed=0\nRECAP space : ok=9 changed=0 unreachable=0 failed=0\n",
		"BEAT hulk restarted\nBEAT batman restarted\n",
		"RELEASE play OK tools.yml version=" + relVersion + " hosts=3 beats-restarted=2\n",
		"KICKSTARTED nova-sprint-reconciler\nKICKSTARTED sprint-table-live\nKICKSTARTED nova-sprint-bench-beat\n",
		"RELEASE self OK none -> " + relVersion + " bin=" + filepath.Join(f.home, ".local", "bin", "nova-sprint") + "\n",
		"VERIFY space want=" + relVersion + " have=" + relVersion + " ok\nVERIFY hulk want=" + relVersion + " have=" + relVersion + " ok\nVERIFY batman want=" + relVersion + " have=" + relVersion + " ok\n",
	} {
		if !strings.Contains(s, line) {
			t.Errorf("missing %q in\n%s", line, s)
		}
	}
	if strings.Contains(s, relPW) {
		t.Errorf("the admin password is in the receipts:\n%s", s)
	}
	if got := res.Line(); got != "FLEET RELEASE OK version="+relVersion+" benches=3 behind=-" {
		t.Errorf("line %q", got)
	}
	if mr.HGet(ConfigKey, "version") != relVersion || mr.HGet(ConfigKey, "commit") != relSha ||
		mr.HGet(ConfigKey, "builder") != "space" || mr.HGet(ConfigKey, "self") != "studio" || mr.HGet(ConfigKey, "tools") != "nova-card,nova-sprint" {
		t.Errorf("store: %s", mr.Dump())
	}

	// The rerun by short sha: the release binary is KEPT, no bench needs a
	// restart, and this machine's binary and beat already answer the
	// version, so nothing is kickstarted.
	mr.HSet("bench:studio:beat", "build", beatLine(relVersion, "darwin/arm64"))
	f.call = nil
	out.Reset()
	res, err = r.Run(context.Background(), relSha[:8])
	if err != nil || !res.OK() {
		t.Fatalf("rerun: %v %s\n%s", err, states(res), out.String())
	}
	heads := strings.Join(f.heads(), "\n")
	if strings.Contains(heads, "go build") || strings.Contains(heads, "ansible hulk") || strings.Contains(heads, "launchctl") || strings.Contains(heads, "ls-remote") ||
		!strings.Contains(out.String(), "KEPT "+bin+" "+relVersion+"\n") ||
		!strings.Contains(out.String(), "RELEASE self OK "+filepath.Join(f.home, ".local", "bin", "nova-sprint")+" already answers "+relVersion+", its loops too\n") {
		t.Fatalf("rerun did work again:\n%s\n%s", heads, out.String())
	}
}

// TestReleaseStepRefusalLetsTheRestRun: each step's refusal is one line with
// its remedy, the steps that can still run run, the verify always runs, and
// the last line is FAIL.
func TestReleaseStepRefusalLetsTheRestRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		edit   func(r *Release, f *relFake)
		states string
		want   string
	}{
		{"no admin password", func(r *Release, f *relFake) { r.AdminPassword = "" },
			"build=OK fn=REFUSED fn-check=OK play=OK self=OK",
			"RELEASE fn REFUSED: no admin password for store.test:6380: NS_ADMIN is empty and no seat holds the admin password (seal it once: make -C ~/rowan-working/rowan-tools/fleet store-seal ROLE=admin SEAT=studio, then rerun with --seat studio (or with NS_ADMIN set))"},
		{"the seat gave none", func(r *Release, f *relFake) {
			r.AdminPassword, r.AdminWhy = "", "seat studio holds no NOVA_REDIS_ADMIN_PASSWORD (x)"
		},
			"build=OK fn=REFUSED fn-check=OK play=OK self=OK",
			"RELEASE fn REFUSED: no admin password for store.test:6380: seat studio holds no NOVA_REDIS_ADMIN_PASSWORD (x)"},
		{"fn deploy fails", func(r *Release, f *relFake) { f.fail = "fn deploy" },
			"build=OK fn=REFUSED fn-check=OK play=OK self=OK",
			"RELEASE fn REFUSED: fn deploy --redis store.test:6380 as admin: exit status 1: boom (check the store address and the admin password)"},
		{"fn check stale", func(r *Release, f *relFake) { f.fail = "fn check" },
			"build=OK fn=OK fn-check=REFUSED play=OK self=OK",
			"RELEASE fn-check REFUSED: boom: exit status 1 (the store does not hold " + relVersion + "'s library; fix the fn step and rerun)"},
		{"release binary", func(r *Release, f *relFake) { f.fail = "release-src/bin/" },
			"build=REFUSED fn=SKIPPED fn-check=SKIPPED play=OK self=OK",
			"RELEASE fn SKIPPED: no release binary to deploy the library from (the build line names why)"},
		{"builder compile", func(r *Release, f *relFake) { f.fail = "fleet build compile" },
			"build=REFUSED fn=OK fn-check=OK play=SKIPPED self=OK",
			"RELEASE build REFUSED: the builder space did not publish " + relVersion + ": read the BUILD FAIL or MANIFEST FAIL line above (rerun once it builds; what is done is kept)"},
		{"source", func(r *Release, f *relFake) { f.fail = "rev-parse" },
			"build=REFUSED fn=SKIPPED fn-check=SKIPPED play=SKIPPED self=SKIPPED",
			"RELEASE build REFUSED: " + relSha + " is not a commit in dev's last 50"},
		{"play fails", func(r *Release, f *relFake) { f.playErr = true },
			"build=OK fn=OK fn-check=OK play=REFUSED self=OK",
			"RELEASE play REFUSED: tools.yml " + relVersion + ": exit_status_4: hulk : ok=3 changed=0 unreachable=1 failed=0 (read the RECAP lines; a rerun converges what is left)"},
		{"beat unreachable", func(r *Release, f *relFake) { f.unreached = "batman" },
			"build=OK fn=OK fn-check=OK play=REFUSED self=OK",
			"BEAT batman failed unreachable\nRELEASE play REFUSED: the beat restart failed on batman (read the BEAT lines; a rerun restarts the beats still behind)"},
		{"no play dir", func(r *Release, f *relFake) { os.Remove(filepath.Join(r.PlayDir, PlayInventory)) },
			"build=OK fn=OK fn-check=OK play=REFUSED self=OK",
			"has no inventory.py (--play-dir <dir>, or NOVA_FLEET_PLAY_DIR)"},
		{"no registry", func(r *Release, f *relFake) { r.Registry = "" },
			"build=OK fn=OK fn-check=OK play=REFUSED self=OK",
			"RELEASE play REFUSED: the play's inventory reads the machines registry (--machines <file>, or NOVA_FLEET_MACHINES)"},
		{"self update", func(r *Release, f *relFake) { f.fail = ".nova-sprint.self-update" },
			"build=OK fn=OK fn-check=OK play=OK self=REFUSED",
			"RELEASE self REFUSED: go build " + relVersion + " with go1.26.6"},
		{"kickstart", func(r *Release, f *relFake) { f.fail = "sprint-table-live" },
			"build=OK fn=OK fn-check=OK play=OK self=REFUSED",
			"(the binary is installed; kickstart the loop by hand, then rerun)"},
	} {
		mr := relStore(t)
		f := &relFake{home: t.TempDir()}
		f.restarted = catchUp(mr)
		r, out, _ := newRelease(t, f, mr)
		tc.edit(r, f)
		res, err := r.Run(context.Background(), relSha)
		s := out.String()
		if err != nil || states(res) != tc.states || !strings.Contains(s, tc.want) {
			t.Errorf("%s: err=%v states=%s\n%s", tc.name, err, states(res), s)
			continue
		}
		if len(res.Checks) != 3 || !strings.Contains(s, "VERIFY batman want="+relVersion) || !strings.HasPrefix(res.Line(), "FLEET RELEASE FAIL version="+relVersion+" benches=3 ") {
			t.Errorf("%s: the verify did not run or the line is not FAIL: %q\n%s", tc.name, res.Line(), s)
		}
	}
}

// TestReleaseNamesTheBenchesBehind: beats that stay behind after the wait is
// spent (three reads of a 10 s wait at 5 s, two faked pauses) end BEHIND with
// the list; nothing waits on the clock.
func TestReleaseNamesTheBenchesBehind(t *testing.T) {
	t.Parallel()
	mr := relStore(t)
	f := &relFake{home: t.TempDir()}
	r, out, sleeps := newRelease(t, f, mr)
	res, err := r.Run(context.Background(), relSha)
	if err != nil || res.OK() || strings.Join(res.Behind(), ",") != "hulk,batman" || *sleeps != 2 {
		t.Fatalf("err=%v behind=%v sleeps=%d\n%s", err, res.Behind(), *sleeps, out.String())
	}
	for _, want := range []string{
		"VERIFY space want=" + relVersion + " have=" + relVersion + " ok\n",
		"VERIFY hulk want=" + relVersion + " have=" + relOld + " behind\n",
		"VERIFY batman want=" + relVersion + " have=none behind\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	if got := res.Line(); got != "FLEET RELEASE BEHIND version="+relVersion+" benches=3 behind=hulk,batman" {
		t.Errorf("line %q", got)
	}
}

// TestReleaseRefusesBeforeAnyStep: what leaves no roll to run is refused
// before the first child (dev's tip after its one ls-remote).
func TestReleaseRefusesBeforeAnyStep(t *testing.T) {
	t.Parallel()
	mr := relStore(t)
	for _, tc := range []struct {
		name, arg string
		edit      func(r *Release, f *relFake)
		want      string
		calls     string
	}{
		{"bad sha", "nothex", func(*Release, *relFake) {}, `"nothex" is not a commit sha of 8 to 40 hex digits`, ""},
		{"short sha", "c81786", func(*Release, *relFake) {}, "is not a commit sha", ""},
		{"no store", relSha, func(r *Release, _ *relFake) { r.Client = nil }, "needs the fleet store", ""},
		{"no benches", relSha, func(r *Release, _ *relFake) { r.Machines = nil }, "no benches to roll", ""},
		{"bad tip", "dev", func(_ *Release, f *relFake) { f.tip = "fatal: could not read\n" }, "not a dev tip (name the commit: fleet release <sha>)", "git ls-remote"},
		{"no tip", "dev", func(_ *Release, f *relFake) { f.fail = "ls-remote" }, "git ls-remote git@github.com:mas-bandwidth/nova-tools.git dev: exit status 1: boom", "git ls-remote"},
	} {
		f := &relFake{home: t.TempDir()}
		r, _, _ := newRelease(t, f, mr)
		tc.edit(r, f)
		_, err := r.Run(context.Background(), tc.arg)
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.want) || strings.Join(f.heads(), ",") != tc.calls {
			t.Errorf("%s: err=%v calls=%v", tc.name, err, f.heads())
		}
	}
}

// TestRollPieces holds the pure parts: the tip parse, the recap, the play
// and restart argv, the ad hoc parse and the verify line.
func TestRollPieces(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		relSha + "\trefs/heads/dev\n":  relSha,
		relSha + "\trefs/heads/main\n": "",
		"deadbeef\trefs/heads/dev\n":   "",
		"":                             "",
	} {
		if got, ok := ParseDevTip(in); got != want || ok != (want != "") {
			t.Errorf("ParseDevTip(%q) = %q %v", in, got, ok)
		}
	}
	if got := Recap(rollRecap); len(got) != 3 || got[0] != "batman : ok=9 changed=1 unreachable=0 failed=0" {
		t.Errorf("recap %q", got)
	}
	if got := Recap("no recap here\n"); got != nil {
		t.Errorf("recap of nothing %q", got)
	}
	if got := strings.Join(PlayArgv("bench.yml", relVersion, []string{"a", "b"}), " "); got != "ansible-playbook -i inventory.py bench.yml --forks 16 --diff -e nova_build="+relVersion+" --limit a,b" {
		t.Errorf("argv %q", got)
	}
	if got := BeatRestartScript(); got != `if [ "$(uname)" = Darwin ]; then sudo -n launchctl kickstart -k system/com.nova.loop.nova-sprint-bench-beat; else systemctl --user restart nova-loop-nova-sprint-bench-beat.service; fi` {
		t.Errorf("script %q", got)
	}
	st := ParseAdhoc("hulk | CHANGED | rc=0 >>\n\nbatman | UNREACHABLE! => {\n    \"msg\": \"a | b | c\"\n}\nspace | FAILED | rc=1 >>\nboom\n")
	if len(st) != 3 || st["hulk"] != "CHANGED" || st["batman"] != "UNREACHABLE!" || st["space"] != "FAILED" {
		t.Errorf("adhoc %v", st)
	}
	if got := (BeatCheck{Bench: "x", Want: relVersion, Have: beatVersion("odd  build\n")}).Line(); got != "VERIFY x want="+relVersion+" have=odd_build behind" {
		t.Errorf("line %q", got)
	}
	if got := (ReleaseResult{Version: relVersion}).Line(); got != "FLEET RELEASE FAIL version="+relVersion+" benches=0 behind=-" {
		t.Errorf("empty line %q", got)
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

// TestNoReleaseCodeReadsASecretOverSsh is the class test of #4356 A's "never
// ssh cat": no string in the fleetbuild package or the fleet release verb
// names the store host's password files or a sudo cat, so the admin password
// can only come from the environment or the seat.
func TestNoReleaseCodeReadsASecretOverSsh(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "..", "..", "cmd", "nova-sprint", "fleet_release.go"))
	fset := token.NewFileSet()
	n := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		n++
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				s = lit.Value
			}
			for _, bad := range []string{".pass", "sudo cat", "sudo -n cat", "/var/lib/nova-redis"} {
				if strings.Contains(s, bad) {
					t.Errorf("%s: %q names %q; the admin password comes from the environment or the seat, never an ssh read", fset.Position(lit.Pos()), s, bad)
				}
			}
			return true
		})
	}
	if n < 5 {
		t.Fatalf("read %d files; the glob missed the package", n)
	}
}
