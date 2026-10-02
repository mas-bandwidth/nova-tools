package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testHolderPID = 4242

var fixedNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// testInstallHost is a host whose lock is under the test's own directory, whose
// clock is fixed, whose sleeps are counted and whose GITHUB_PATH is a file of
// its own. Nothing it does reaches the machine.
type testInstallHost struct {
	installHost
	out, errb  *bytes.Buffer
	runner     *fakeCmdRunner
	githubPath string
	sleeps     *int
	home       string
}

func newTestInstallHost(t *testing.T) testInstallHost {
	t.Helper()
	dir := t.TempDir()
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	gp := filepath.Join(dir, "github_path")
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	sleeps := new(int)
	r := &fakeCmdRunner{onPath: map[string]string{}}
	h := installHost{
		run: r,
		getenv: func(k string) string {
			switch k {
			case "GITHUB_PATH":
				return gp
			case "HOME":
				return home
			}
			return ""
		},
		stdout: out, stderr: errb,
		sleep:  func(time.Duration) { *sleeps++ },
		now:    func() time.Time { return fixedNow },
		lock:   filepath.Join(dir, "install.lock"),
		isExec: func(string) bool { return false },
		glob:   func(string) []string { return nil },
		// this process is pid 4242, every other pid is alive, no signal ever
		// arrives and an exit is recorded, never taken: nothing reaches the
		// test process or the machine.
		pid:    func() int { return testHolderPID },
		alive:  func(int) bool { return true },
		sigCtx: func(parent context.Context) (context.Context, context.CancelFunc) { return context.WithCancel(parent) },
		exit:   func(int) {},
	}
	return testInstallHost{installHost: h, out: out, errb: errb, runner: r, githubPath: gp, sleeps: sleeps, home: home}
}

func (h testInstallHost) published(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(h.githubPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func (h testInstallHost) lockHeld() bool { _, err := os.Stat(h.lock); return err == nil }

// redisVersionLine is what `redis-server --version` prints for version v.
func redisVersionLine(v string) string {
	return "Redis server v=" + v + " sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=e53ff17674aa6190\n"
}

// redisTestHost is a test host whose redis-servers answer --version from
// versions (path -> version; a path not there prints nothing and exits 1), and
// whose `make` leaves the binary a build would, reporting built, first on PATH
// in $HOME/.local/bin ("" builds nothing). The fake PATH is runner.onPath.
type redisTestHost struct {
	testInstallHost
	versions map[string]string
	local    string // $HOME/.local/bin/redis-server
}

func newRedisTestHost(t *testing.T, built string) redisTestHost {
	t.Helper()
	h := redisTestHost{testInstallHost: newTestInstallHost(t), versions: map[string]string{}}
	h.local = filepath.Join(h.home, ".local", "bin", "redis-server")
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		switch {
		case len(c.Args) == 1 && c.Args[0] == "--version":
			h.runner.mu.Lock()
			v, ok := h.versions[c.Name]
			h.runner.mu.Unlock()
			if !ok {
				return "", 1, nil
			}
			return redisVersionLine(v), 0, nil
		case c.Name == "make" && built != "":
			// make -C <tree> -jN redis-server: produce the binary the build would.
			tree := c.Args[1]
			if err := os.MkdirAll(filepath.Join(tree, "src"), 0o755); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(tree, "src", "redis-server"), []byte("built"), 0o600); err != nil {
				t.Error(err)
			}
			h.runner.mu.Lock()
			h.versions[h.local] = built
			h.runner.onPath["redis-server"] = h.local
			h.runner.mu.Unlock()
		}
		return "", 0, nil
	}
	return h
}

// built says whether the source build ran, and checks its three steps.
func (h redisTestHost) built(t *testing.T) bool {
	t.Helper()
	var steps []string
	for _, l := range h.runner.lines() {
		if !strings.HasSuffix(l, " --version") {
			steps = append(steps, l)
		}
	}
	if len(steps) == 0 {
		return false
	}
	if len(steps) != 3 ||
		!strings.HasPrefix(steps[0], "curl -fsSL https://download.redis.io/releases/redis-"+redisSourceVersion+".tar.gz -o ") ||
		!strings.HasPrefix(steps[1], "tar -xzf ") ||
		!strings.HasPrefix(steps[2], "make -C ") || !strings.HasSuffix(steps[2], " redis-server") || !strings.Contains(steps[2], "redis-"+redisSourceVersion) {
		t.Fatalf("the build ran %q; want curl, tar and make of the pinned release and nothing else", steps)
	}
	return true
}

func TestInstallRedisWhenThePinnedOneIsFirstOnPathOnlyAsksItsVersion(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, redisSourceVersion)
	h.runner.onPath["redis-server"] = "/opt/r/bin/redis-server"
	h.versions["/opt/r/bin/redis-server"] = redisSourceVersion
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if got := h.out.String(); got != "redis-server /opt/r/bin/redis-server v="+redisSourceVersion+"\n" {
		t.Fatalf("stdout %q", got)
	}
	if got := h.published(t); got != "/opt/r/bin\n" {
		t.Fatalf("GITHUB_PATH got %q", got)
	}
	if got := strings.Join(h.runner.lines(), "|"); got != "/opt/r/bin/redis-server --version" || h.lockHeld() || len(h.runner.prepends) != 0 {
		t.Fatalf("the pinned redis-server first on PATH ran %q, prepended %q, lock held %v; want one version check and nothing else", got, h.runner.prepends, h.lockHeld())
	}
}

// #5151: the bench runners' apt server, 7.0.15 on hetzner and 8.0.5 on
// spacegame, was first on PATH and the installer kept it.
func TestInstallRedisBuildsThePinnedOneWhenTheFirstOnPathIsAnotherVersion(t *testing.T) {
	t.Parallel()
	for _, first := range []string{"7.0.15", "8.0.5", "8.10.3", ""} {
		h := newRedisTestHost(t, redisSourceVersion)
		h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
		h.runner.onPath["brew"] = "/opt/homebrew/bin/brew"
		h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
		if first != "" {
			h.versions["/usr/bin/redis-server"] = first
		}
		if code := installRedisServer(h.installHost); code != 0 {
			t.Fatalf("first on PATH %q: exit %d, stderr %q", first, code, h.errb)
		}
		if !h.built(t) {
			t.Fatalf("first on PATH %q: no build ran: %q", first, h.runner.lines())
		}
		if h.runner.ran("apt-get") || h.runner.ran("brew") || h.runner.ran("sudo") {
			t.Fatalf("first on PATH %q: a package manager ran: %q", first, h.runner.lines())
		}
		dir := filepath.Dir(h.local)
		if got := strings.Join(h.runner.prepends, ","); got != dir {
			t.Fatalf("first on PATH %q: PATH prepended with %q, want %q", first, got, dir)
		}
		if got := h.published(t); got != dir+"\n" {
			t.Fatalf("first on PATH %q: GITHUB_PATH got %q", first, got)
		}
		if got := h.out.String(); got != "redis-server "+h.local+" v="+redisSourceVersion+"\n" {
			t.Fatalf("first on PATH %q: stdout %q", first, got)
		}
		fi, err := os.Stat(h.local)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(h.local); string(b) != "built" || fi.Mode().Perm() != 0o755 {
			t.Fatalf("first on PATH %q: installed %q mode %v", first, b, fi.Mode())
		}
		if h.lockHeld() {
			t.Fatalf("first on PATH %q: the lock is still held after the build", first)
		}
	}
}

func TestInstallRedisBuildsThePinnedOneWhereThereIsNone(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, redisSourceVersion)
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if !h.built(t) || h.out.String() != "redis-server "+h.local+" v="+redisSourceVersion+"\n" {
		t.Fatalf("ran %q, stdout %q", h.runner.lines(), h.out)
	}
}

func TestInstallRedisPutsAnEarlierBuildFirstWithoutBuildingAgain(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, redisSourceVersion)
	h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
	h.versions["/usr/bin/redis-server"] = "7.0.15"
	h.versions[h.local] = redisSourceVersion
	h.installHost.isExec = func(p string) bool { return p == h.local }
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if h.built(t) || h.lockHeld() {
		t.Fatalf("an earlier build in $HOME/.local/bin was built again: %q", h.runner.lines())
	}
	dir := filepath.Dir(h.local)
	if got := strings.Join(h.runner.prepends, ","); got != dir || h.published(t) != dir+"\n" {
		t.Fatalf("PATH prepended with %q, GITHUB_PATH %q; want %s first", got, h.published(t), dir)
	}
}

func TestInstallRedisRefusesABuildThatReportsAnotherVersion(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, "8.0.5")
	h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
	h.versions["/usr/bin/redis-server"] = "8.0.5"
	if code := installRedisServer(h.installHost); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if want := "redis-server first on PATH is " + h.local + `, version "8.0.5", after the install; want ` + redisSourceVersion; !strings.Contains(h.errb.String(), want) {
		t.Fatalf("stderr %q, want %q", h.errb, want)
	}
	if h.published(t) != "" || h.out.Len() != 0 {
		t.Fatalf("a refused install published %q and said %q", h.published(t), h.out)
	}
}

func TestInstallRedisSourceBuildWithoutABinaryIsRefused(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, "")
	if code := installRedisServer(h.installHost); code != 1 {
		t.Fatalf("exit %d, want 1 (the build produced nothing)", code)
	}
	if !strings.Contains(h.errb.String(), "the build produced no redis-server") {
		t.Fatalf("stderr %q", h.errb)
	}
}

func TestInstallLockWaiterTakesTheBinaryAnotherRunnerInstalled(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, redisSourceVersion)
	if err := os.Mkdir(h.lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(h.lock, fixedNow, fixedNow); err != nil {
		t.Fatal(err)
	}
	polls := 0
	h.installHost.sleep = func(time.Duration) {
		polls++
		if polls == 3 {
			h.runner.mu.Lock()
			h.runner.onPath["redis-server"] = h.local
			h.versions[h.local] = redisSourceVersion
			h.runner.mu.Unlock()
		}
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if polls != 3 {
		t.Fatalf("polled %d times, want 3", polls)
	}
	if h.built(t) {
		t.Fatalf("a waiter built: %v", h.runner.lines())
	}
	if !h.lockHeld() {
		t.Fatal("a waiter released a lock it never took")
	}
}

func TestInstallLockTimesOutAfterFortyPolls(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, redisSourceVersion)
	if err := os.Mkdir(h.lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(h.lock, fixedNow, fixedNow); err != nil {
		t.Fatal(err)
	}
	if code := installRedisServer(h.installHost); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if *h.sleeps != 40 {
		t.Fatalf("slept %d times, want 40", *h.sleeps)
	}
	if !strings.Contains(h.errb.String(), "timed out waiting to install redis-server") {
		t.Fatalf("stderr %q", h.errb)
	}
	if h.built(t) {
		t.Fatalf("a timed-out waiter built: %v", h.runner.lines())
	}
}

func TestInstallLockOlderThanSixHundredSecondsIsTakenOver(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, redisSourceVersion)
	if err := os.Mkdir(h.lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := fixedNow.Add(-601 * time.Second)
	if err := os.Chtimes(h.lock, old, old); err != nil {
		t.Fatal(err)
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if *h.sleeps != 0 {
		t.Fatalf("waited %d polls on a stale lock", *h.sleeps)
	}
	if !h.built(t) {
		t.Fatalf("the stale lock was not taken over: %v", h.runner.lines())
	}
}

func TestInstallLockExactlySixHundredSecondsOldIsStillHeld(t *testing.T) {
	t.Parallel()
	h := newRedisTestHost(t, redisSourceVersion)
	if err := os.Mkdir(h.lock, 0o755); err != nil {
		t.Fatal(err)
	}
	edge := fixedNow.Add(-600 * time.Second)
	if err := os.Chtimes(h.lock, edge, edge); err != nil {
		t.Fatal(err)
	}
	if code := installRedisServer(h.installHost); code != 1 || *h.sleeps != 40 {
		t.Fatalf("exit %d after %d polls, want a timeout after 40 (a lock 600 s old is not stale: the rule is over 600)", code, *h.sleeps)
	}
}

func TestInstallVerbsTakeNoArguments(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"install-redis-server", "install-postgres", "ensure-sbcl"} {
		e, _, errb := testEnv()
		if code := run([]string{name, "extra"}, e); code != 2 {
			t.Errorf("%s extra: exit %d, want 2", name, code)
		}
		if !strings.Contains(errb.String(), "takes no arguments") {
			t.Errorf("%s extra: stderr %q", name, errb)
		}
	}
}

func TestInstallRedisPinIsAThreePartVersion(t *testing.T) {
	t.Parallel()
	parts := strings.Split(redisSourceVersion, ".")
	if len(parts) != 3 {
		t.Fatalf("pinned Redis version %q is not major.minor.patch", redisSourceVersion)
	}
}

// ---- postgres ----

func TestInstallPostgresWhenOnPathPublishesAndNamesTheVersion(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	for _, b := range postgresBinaries {
		h.runner.onPath[b] = "/usr/lib/postgresql/16/bin/" + b
	}
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		if strings.HasSuffix(c.Name, "postgres") && len(c.Args) == 1 && c.Args[0] == "--version" {
			return "postgres (PostgreSQL) 16.4\n", 0, nil
		}
		return "", 0, nil
	}
	if code := installPostgres(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	want := "postgres /usr/lib/postgresql/16/bin/postgres postgres (PostgreSQL) 16.4\n"
	if h.out.String() != want {
		t.Fatalf("stdout %q, want %q", h.out, want)
	}
	if h.published(t) != "/usr/lib/postgresql/16/bin\n" {
		t.Fatalf("GITHUB_PATH got %q", h.published(t))
	}
}

func TestInstallPostgresFindsAVersionedInstallOffPathAndPutsItOnPath(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.installHost.isExec = func(p string) bool { return strings.HasPrefix(p, "/usr/lib/postgresql/15/bin/") }
	if code := installPostgres(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if got := strings.Join(h.runner.prepends, ","); got != "/usr/lib/postgresql/15/bin" {
		t.Fatalf("PATH prepended with %q", got)
	}
	if h.published(t) != "/usr/lib/postgresql/15/bin\n" {
		t.Fatalf("GITHUB_PATH got %q", h.published(t))
	}
	if h.runner.ran("apt-get") || h.lockHeld() {
		t.Fatalf("an existing install ran %v", h.runner.lines())
	}
}

func TestInstallPostgresPrefersSixteenThenTheGlob(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.installHost.isExec = func(p string) bool {
		return strings.HasPrefix(p, "/usr/lib/postgresql/16/bin/") || strings.HasPrefix(p, "/usr/lib/postgresql/13/bin/")
	}
	if code := installPostgres(h.installHost); code != 0 || h.published(t) != "/usr/lib/postgresql/16/bin\n" {
		t.Fatalf("exit %d published %q, want sixteen first", code, h.published(t))
	}
	h = newTestInstallHost(t)
	h.installHost.isExec = func(p string) bool { return strings.HasPrefix(p, "/usr/lib/postgresql/13/bin/") }
	h.installHost.glob = func(pat string) []string {
		if pat != "/usr/lib/postgresql/*/bin" {
			t.Errorf("glob %q", pat)
		}
		return []string{"/usr/lib/postgresql/12/bin", "/usr/lib/postgresql/13/bin"}
	}
	if code := installPostgres(h.installHost); code != 0 || h.published(t) != "/usr/lib/postgresql/13/bin\n" {
		t.Fatalf("exit %d published %q, want the glob's first complete directory", code, h.published(t))
	}
}

func TestInstallPostgresAnIncompleteInstallDoesNotCount(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.installHost.isExec = func(p string) bool { return strings.HasSuffix(p, "/pg_ctl") } // initdb and postgres missing
	if code := installPostgres(h.installHost); code != 1 {
		t.Fatalf("exit %d, want 1: no apt-get and no brew", code)
	}
	if !strings.Contains(h.errb.String(), "no apt-get and no brew") {
		t.Fatalf("stderr %q", h.errb)
	}
}

func TestInstallPostgresAptPicksSixteenWhenTheArchiveHasIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		cacheRC  int
		wantPkg  string
		wantShow string
	}{
		{"archive has 16", 0, "postgresql-16", "apt-cache show postgresql-16"},
		{"archive lacks 16", 100, "postgresql", "apt-cache show postgresql-16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newTestInstallHost(t)
			h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
			installed := false
			h.runner.answer = func(c cmdSpec) (string, int, error) {
				if c.Name == "apt-cache" {
					return "", tc.cacheRC, nil
				}
				if c.Name == "apt-get" && c.Args[0] == "install" {
					installed = true
				}
				return "", 0, nil
			}
			h.installHost.isExec = func(p string) bool {
				return installed && strings.HasPrefix(p, "/usr/lib/postgresql/16/bin/")
			}
			if code := installPostgres(h.installHost); code != 0 {
				t.Fatalf("exit %d, stderr %q", code, h.errb)
			}
			want := []string{tc.wantShow, "apt-get update -qq", "apt-get install -y -qq " + tc.wantPkg}
			if got := h.runner.lines(); !strings.HasPrefix(strings.Join(got, "|"), strings.Join(want, "|")) {
				t.Fatalf("commands %q, want %q first", got, want)
			}
		})
	}
}

func TestInstallPostgresWithHomebrewInstallsSixteen(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["brew"] = "/opt/homebrew/bin/brew"
	installed := false
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		if c.Name == "brew" {
			installed = true
			if got := strings.Join(c.Env, " "); got != "HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_UPGRADE=1" {
				t.Errorf("brew env %q", got)
			}
		}
		return "", 0, nil
	}
	h.installHost.isExec = func(p string) bool { return installed && strings.HasPrefix(p, "/opt/homebrew/opt/postgresql@16/bin/") }
	if code := installPostgres(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if got := h.runner.lines(); len(got) < 1 || got[0] != "brew install postgresql@16" {
		t.Fatalf("commands %q", got)
	}
	if h.published(t) != "/opt/homebrew/opt/postgresql@16/bin\n" {
		t.Fatalf("GITHUB_PATH got %q", h.published(t))
	}
}

func TestInstallPostgresStillMissingAfterTheInstallIsRefused(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
	if code := installPostgres(h.installHost); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(h.errb.String(), "postgres binaries still not found after install") {
		t.Fatalf("stderr %q", h.errb)
	}
}

func TestInstallPostgresLockTimeoutNamesPostgres(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	if err := os.Mkdir(h.lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(h.lock, fixedNow, fixedNow); err != nil {
		t.Fatal(err)
	}
	if code := installPostgres(h.installHost); code != 1 || !strings.Contains(h.errb.String(), "timed out waiting to install postgres") {
		t.Fatalf("exit %d stderr %q", code, h.errb)
	}
}

// ---- sbcl ----

func TestEnsureSbclPutsTheUsersOwnBinOnPathAndPublishesIt(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	want := filepath.Join(h.home, ".local", "bin")
	h.installHost.isExec = func(p string) bool { return p == filepath.Join(want, "sbcl") }
	h.runner.onPath["sbcl"] = filepath.Join(want, "sbcl")
	if code := ensureSbcl(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if h.published(t) != want+"\n" || strings.Join(h.runner.prepends, ",") != want {
		t.Fatalf("published %q, prepended %v", h.published(t), h.runner.prepends)
	}
	if got := h.runner.lines(); len(got) != 1 || got[0] != "sbcl --version" {
		t.Fatalf("commands %q", got)
	}
}

func TestEnsureSbclInstallsWithAptOnlyWhenAbsent(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	if code := ensureSbcl(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	want := []string{"apt-get update -qq", "apt-get install -y --no-install-recommends sbcl", "sbcl --version"}
	if got := h.runner.lines(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands %q, want %q", got, want)
	}
	if h.published(t) != "" {
		t.Fatalf("published %q with no sbcl of the user's own", h.published(t))
	}

	h = newTestInstallHost(t)
	h.runner.onPath["sbcl"] = "/usr/bin/sbcl"
	if code := ensureSbcl(h.installHost); code != 0 || h.runner.ran("apt-get") {
		t.Fatalf("exit %d, commands %q: an sbcl already on PATH is not reinstalled", code, h.runner.lines())
	}
}

func TestEnsureSbclAptFailureIsTheExitCode(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.answer = func(c cmdSpec) (string, int, error) { return "", 100, nil }
	if code := ensureSbcl(h.installHost); code != 100 {
		t.Fatalf("exit %d, want apt-get's 100", code)
	}
	if h.runner.ran("sbcl") {
		t.Fatalf("ran sbcl after a failed install: %q", h.runner.lines())
	}
}

// writeHolder makes a lock another install holds, naming holder, made at when.
func writeHolder(t *testing.T, h testInstallHost, holder int, when time.Time) {
	t.Helper()
	require.NoError(t, os.Mkdir(h.lock, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(h.lock, installLockPidFile), []byte(strconv.Itoa(holder)+"\n"), 0o644))
	require.NoError(t, os.Chtimes(h.lock, when, when))
}

func TestInstallLockNamesItsHolderWhileHeldAndIsGoneAfter(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	var during string
	code := lockedInstall(h.installHost, "thing", func() bool { return false }, func() int { return 0 }, func() int {
		b, err := os.ReadFile(filepath.Join(h.lock, installLockPidFile))
		require.NoError(t, err)
		during = string(b)
		return 0
	})
	assert.Equal(t, 0, code)
	assert.Equal(t, "4242\n", during)
	assert.False(t, h.lockHeld(), "the lock outlived its install")
}

// A holder killed outright leaves its lock and its pid: a fresh lock whose pid
// is gone is stale at once, without the ten minutes of the age rule.
func TestInstallLockWhoseHolderPidIsGoneIsTakenOverAtOnce(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	writeHolder(t, h, 99999, fixedNow) // fresh: the age rule says held
	h.installHost.alive = func(pid int) bool { return pid != 99999 }
	ran := false
	code := lockedInstall(h.installHost, "thing", func() bool { return false }, func() int { return 0 }, func() int { ran = true; return 0 })
	assert.Equal(t, 0, code, h.errb.String())
	assert.True(t, ran, "the dead holder's lock was not taken over")
	assert.Equal(t, 0, *h.sleeps, "polled on a lock whose holder is gone")
	assert.False(t, h.lockHeld())
}

func TestInstallLockWhoseHolderPidIsAliveIsHeldUntilTheTimeout(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	writeHolder(t, h, 99999, fixedNow)
	h.installHost.alive = func(int) bool { return true }
	ran := false
	code := lockedInstall(h.installHost, "thing", func() bool { return false }, func() int { return 0 }, func() int { ran = true; return 0 })
	assert.Equal(t, 1, code)
	assert.False(t, ran)
	assert.Equal(t, installLockTries, *h.sleeps)
	assert.True(t, h.lockHeld(), "a waiter took a live holder's lock")
}

// The holder dies while a waiter polls: the waiter takes the lock over on the
// poll after, not at the timeout.
func TestInstallLockWaiterTakesOverWhenTheHolderDiesWhilePolling(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	writeHolder(t, h, 99999, fixedNow)
	dead := false
	h.installHost.alive = func(pid int) bool { return !dead }
	h.installHost.sleep = func(time.Duration) { *h.sleeps++; dead = *h.sleeps == 2 }
	ran := false
	code := lockedInstall(h.installHost, "thing", func() bool { return false }, func() int { return 0 }, func() int { ran = true; return 0 })
	assert.Equal(t, 0, code)
	assert.True(t, ran)
	assert.Equal(t, 2, *h.sleeps)
}

// A SIGTERM or SIGINT to a holder mid-install: the lock is released before the
// process exits, and the exit is red. The signal is a cancelled context; none is
// sent to the test process.
func TestInstallLockIsReleasedWhenTheHolderIsSignalled(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	sig, raise := context.WithCancel(context.Background())
	h.installHost.sigCtx = func(context.Context) (context.Context, context.CancelFunc) { return sig, func() {} }
	exited := make(chan int, 1)
	lockAtExit := make(chan bool, 1)
	h.installHost.exit = func(code int) {
		_, err := os.Stat(h.lock)
		lockAtExit <- err == nil
		exited <- code
	}
	code := lockedInstall(h.installHost, "thing", func() bool { return false }, func() int { return 0 }, func() int {
		assert.True(t, h.lockHeld(), "the install runs with the lock held")
		raise()            // the signal arrives mid-install
		exited <- <-exited // the signal ended the install; the code stays for the assertion below
		return 1
	})
	assert.Equal(t, 1, code)
	assert.Equal(t, 1, <-exited)
	assert.False(t, <-lockAtExit, "the process exited with its lock still held")
	assert.False(t, h.lockHeld())
	assert.Contains(t, h.errb.String(), "install lock released")
}

// A waiter that is signalled stops waiting at once and takes nothing.
func TestInstallLockWaiterGivesUpWhenSignalled(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	writeHolder(t, h, 99999, fixedNow)
	sig, raise := context.WithCancel(context.Background())
	h.installHost.sigCtx = func(context.Context) (context.Context, context.CancelFunc) { return sig, func() {} }
	h.installHost.sleep = func(time.Duration) { *h.sleeps++; raise() }
	ran := false
	code := lockedInstall(h.installHost, "thing", func() bool { return false }, func() int { return 0 }, func() int { ran = true; return 0 })
	assert.Equal(t, 1, code)
	assert.False(t, ran)
	assert.Equal(t, 1, *h.sleeps, "kept waiting after the signal")
	assert.Contains(t, h.errb.String(), "interrupted while waiting")
	assert.True(t, h.lockHeld(), "a waiter released a lock it never took")
}

// An install that ran long enough to have its lock taken over as stale does not
// remove the new holder's lock when it finishes.
func TestInstallLockOfAnotherHolderIsNotReleasedByThisOne(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	code := lockedInstall(h.installHost, "thing", func() bool { return false }, func() int { return 0 }, func() int {
		require.NoError(t, os.WriteFile(filepath.Join(h.lock, installLockPidFile), []byte("777\n"), 0o644))
		return 0
	})
	assert.Equal(t, 0, code)
	assert.True(t, h.lockHeld(), "removed a lock that now names another holder")
}
