package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestInstallRedisWhenPresentPublishesItsDirectoryAndTakesNoLock(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["redis-server"] = "/opt/r/bin/redis-server"
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if got := h.out.String(); got != "redis-server /opt/r/bin/redis-server\n" {
		t.Fatalf("stdout %q", got)
	}
	if got := h.published(t); got != "/opt/r/bin\n" {
		t.Fatalf("GITHUB_PATH got %q", got)
	}
	if len(h.runner.calls) != 0 || h.lockHeld() {
		t.Fatalf("an installed redis-server ran %v / left a lock", h.runner.lines())
	}
}

func TestInstallRedisWithAptInstallsUnderTheLockAndReleasesIt(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		if c.Name == "apt-get" && len(c.Args) > 0 && c.Args[0] == "install" {
			if !h.lockHeld() {
				t.Error("apt-get install ran without the lock held")
			}
			h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
		}
		return "", 0, nil
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	want := []string{"apt-get update -qq", "apt-get install -y -qq redis-server"}
	if got := h.runner.lines(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands %q, want %q", got, want)
	}
	if h.lockHeld() {
		t.Fatal("the lock is still held after the install")
	}
	if h.published(t) != "/usr/bin\n" || h.out.String() != "redis-server /usr/bin/redis-server\n" {
		t.Fatalf("published %q, stdout %q", h.published(t), h.out)
	}
}

func TestInstallRedisRetriesAptAsRootWhenTheFirstTryFails(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		if c.Name == "apt-get" {
			return "", 100, nil // not root
		}
		if c.Name == "sudo" && len(c.Args) > 2 && c.Args[2] == "install" {
			h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
		}
		return "", 0, nil
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	want := []string{
		"apt-get update -qq",
		"sudo -n apt-get update -qq",
		"sudo -n apt-get install -y -qq redis-server",
	}
	if got := h.runner.lines(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands %q, want %q", got, want)
	}
}

func TestInstallRedisReportsTheFailingAptCommandsCode(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
	h.runner.answer = func(c cmdSpec) (string, int, error) { return "", 100, nil }
	if code := installRedisServer(h.installHost); code != 100 {
		t.Fatalf("exit %d, want the last command's 100", code)
	}
	if h.lockHeld() {
		t.Fatal("a failed install left the lock held")
	}
}

func TestInstallRedisWithHomebrewInstallsAndPublishesItsBin(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["brew"] = "/opt/homebrew/bin/brew"
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		switch {
		case c.Name == "brew" && c.Args[0] == "install":
			if got := strings.Join(c.Env, " "); got != "HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_UPGRADE=1" {
				t.Errorf("brew install env %q", got)
			}
			h.runner.onPath["redis-server"] = "/opt/homebrew/bin/redis-server"
		case c.Name == "brew" && c.Args[0] == "--prefix":
			return "/opt/homebrew\n", 0, nil
		}
		return "", 0, nil
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if got := strings.Join(h.runner.prepends, ","); got != "/opt/homebrew/bin" {
		t.Fatalf("PATH prepended with %q", got)
	}
	if got := h.published(t); got != "/opt/homebrew/bin\n/opt/homebrew/bin\n" {
		t.Fatalf("GITHUB_PATH got %q (the brew bin, then the binary's directory)", got)
	}
}

func TestInstallRedisBuildsThePinnedSourceWhereThereIsNoPackageManager(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		if c.Name == "make" {
			// make -C <tree> -jN redis-server: produce the binary the build would.
			tree := c.Args[1]
			if err := os.MkdirAll(filepath.Join(tree, "src"), 0o755); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(tree, "src", "redis-server"), []byte("built"), 0o600); err != nil {
				t.Error(err)
			}
			h.runner.onPath["redis-server"] = filepath.Join(h.home, ".local", "bin", "redis-server")
		}
		return "", 0, nil
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	lines := h.runner.lines()
	if len(lines) != 3 ||
		!strings.HasPrefix(lines[0], "curl -fsSL https://download.redis.io/releases/redis-"+redisSourceVersion+".tar.gz -o ") ||
		!strings.HasPrefix(lines[1], "tar -xzf ") ||
		!strings.HasPrefix(lines[2], "make -C ") || !strings.HasSuffix(lines[2], " redis-server") || !strings.Contains(lines[2], "redis-"+redisSourceVersion) {
		t.Fatalf("commands %q", lines)
	}
	dest := filepath.Join(h.home, ".local", "bin", "redis-server")
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "built" || fi.Mode().Perm() != 0o755 {
		t.Fatalf("installed %q mode %v", b, fi.Mode())
	}
	if got := strings.Join(h.runner.prepends, ","); got != filepath.Join(h.home, ".local", "bin") {
		t.Fatalf("PATH prepended with %q", got)
	}
}

func TestInstallRedisSourceBuildWithoutABinaryIsRefused(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	if code := installRedisServer(h.installHost); code != 1 {
		t.Fatalf("exit %d, want 1 (the build produced nothing)", code)
	}
	if !strings.Contains(h.errb.String(), "the build produced no redis-server") {
		t.Fatalf("stderr %q", h.errb)
	}
}

func TestInstallRedisStillMissingAfterTheInstallIsRefused(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
	if code := installRedisServer(h.installHost); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(h.errb.String(), "redis-server still not on PATH after install") {
		t.Fatalf("stderr %q", h.errb)
	}
}

func TestInstallLockWaiterTakesTheBinaryAnotherRunnerInstalled(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
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
			h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
			h.runner.mu.Unlock()
		}
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if polls != 3 {
		t.Fatalf("polled %d times, want 3", polls)
	}
	if len(h.runner.calls) != 0 {
		t.Fatalf("a waiter ran %v", h.runner.lines())
	}
	if !h.lockHeld() {
		t.Fatal("a waiter released a lock it never took")
	}
}

func TestInstallLockTimesOutAfterFortyPolls(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
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
	if len(h.runner.calls) != 0 {
		t.Fatalf("a timed-out waiter ran %v", h.runner.lines())
	}
}

func TestInstallLockOlderThanSixHundredSecondsIsTakenOver(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
	if err := os.Mkdir(h.lock, 0o755); err != nil {
		t.Fatal(err)
	}
	old := fixedNow.Add(-601 * time.Second)
	if err := os.Chtimes(h.lock, old, old); err != nil {
		t.Fatal(err)
	}
	h.runner.onPath["apt-get"] = "/usr/bin/apt-get"
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		if c.Name == "apt-get" && c.Args[0] == "install" {
			h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
		}
		return "", 0, nil
	}
	if code := installRedisServer(h.installHost); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.errb)
	}
	if *h.sleeps != 0 {
		t.Fatalf("waited %d polls on a stale lock", *h.sleeps)
	}
	if !h.runner.ran("apt-get install") {
		t.Fatalf("the stale lock was not taken over: %v", h.runner.lines())
	}
}

func TestInstallLockExactlySixHundredSecondsOldIsStillHeld(t *testing.T) {
	t.Parallel()
	h := newTestInstallHost(t)
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
