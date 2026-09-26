//go:build functional

package ci

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestRedisBackedTestsDoNotSkipUnderCI is #3113. With NOVA_CI=1 and
// redis-server on PATH, a redis-backed control runs (in the functional tier,
// nova-tools#4328). A missing redis-server
// fails instead of skipping, and no other file keeps a private LookPath that
// can still skip. The workflows install the binary; the helper is the one gate.
func TestRedisBackedTestsDoNotSkipUnderCI(t *testing.T) {
	t.Parallel()
	switch os.Getenv("NOVA_REDIS_CI_CHILD") {
	case "fail":
		testutil.Absent(t, errors.New("redis-server: executable file not found"))
		t.Fatal("Absent returned under NOVA_CI=1; a missing redis-server must fail the test")
	case "skip":
		testutil.Absent(t, errors.New("redis-server: executable file not found"))
		t.Fatal("Absent returned with NOVA_CI unset; a missing redis-server must skip outside CI")
	}

	failOut, failCode := redisCIChild(t, "fail")
	if failCode == 0 || !strings.Contains(failOut, "redis-server is required under NOVA_CI=1") {
		t.Fatalf("missing redis-server under NOVA_CI=1: exit %d, want a failure\n%s", failCode, failOut)
	}
	if strings.Contains(failOut, "--- SKIP:") {
		t.Fatalf("missing redis-server under NOVA_CI=1 skipped:\n%s", failOut)
	}
	skipOut, skipCode := redisCIChild(t, "skip")
	if skipCode != 0 || !strings.Contains(skipOut, "--- SKIP:") || !strings.Contains(skipOut, "redis-server unavailable") {
		t.Fatalf("missing redis-server outside CI: exit %d, want a skip\n%s", skipCode, skipOut)
	}

	// NOVA_CI=1 comes from the functional job's environment (ci.yml), which
	// is where this file runs: it is behind the functional tag.
	addr := testutil.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("throwaway redis at %s did not answer: %v", addr, err)
	}

	root := repoRoot(t)
	helper := readFile(t, filepath.Join(root, "internal", "nsprint", "testutil", "redis.go"))
	fatalAt := strings.Index(helper, "Fatalf")
	skipAt := strings.Index(helper, "t.Skip")
	ciAt := strings.Index(helper, "NOVA_CI")
	if ciAt < 0 || fatalAt < 0 || skipAt < 0 || !(ciAt < fatalAt && fatalAt < skipAt) {
		t.Fatalf("helper must check NOVA_CI, fail, then skip; indexes ci=%d fatal=%d skip=%d", ciAt, fatalAt, skipAt)
	}
	if !strings.Contains(helper, `"--save", ""`) || !strings.Contains(helper, `"127.0.0.1"`) {
		t.Fatal("helper must start redis-server on loopback with --save \"\"")
	}
	if offenders := redisServerGates(t, root); len(offenders) > 0 {
		t.Fatalf("redis-server is started or skipped outside internal/nsprint/testutil: %s", strings.Join(offenders, ", "))
	}

	ci := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	if !strings.Contains(ci, `NOVA_CI: "1"`) {
		t.Fatal("ci.yml does not set NOVA_CI=1; a missing redis-server would skip and the run would stay green")
	}
	// The functional tier installs the server; the unit tier refuses one
	// (TestUnitTierRefusesRedisServer), so its `test` job must not install it.
	for _, job := range []string{"functional", "test-hosted"} {
		body := jobBody(ci, job)
		if body == "" {
			t.Fatalf("ci.yml has no job %s", job)
		}
		if !strings.Contains(body, "install-redis-server.sh") {
			t.Errorf("ci.yml job %s does not install redis-server", job)
		}
	}
	if strings.Contains(jobBody(ci, "test"), "install-redis-server.sh") {
		t.Error("ci.yml job test (the unit tier) installs redis-server; the unit tier refuses one")
	}
	cert := readFile(t, filepath.Join(root, ".github", "workflows", "certification.yml"))
	if !strings.Contains(cert, `NOVA_CI: "1"`) {
		t.Fatal("certification.yml does not set NOVA_CI=1")
	}
	for _, job := range []string{"test", "perf"} {
		body := jobBody(cert, job)
		if body == "" || !strings.Contains(body, "install-redis-server.sh") {
			t.Errorf("certification.yml job %s does not install redis-server", job)
		}
	}
	script := readFile(t, filepath.Join(root, ".github", "scripts", "install-redis-server.sh"))
	if !strings.Contains(script, "apt-get install -y -qq redis-server") {
		t.Fatal("the installer does not apt-get install redis-server for the hosted Linux row")
	}
	if !strings.Contains(script, "brew install redis") {
		t.Fatal("the installer does not brew install redis for the Studio")
	}
}

// redisCIChild re-executes this test as the missing-binary case. PATH is not
// stripped: on a Linux runner redis-server lives in /usr/bin, so the case is
// the helper's Absent, not an empty PATH.
func redisCIChild(t *testing.T, mode string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestRedisBackedTestsDoNotSkipUnderCI$", "-test.count=1", "-test.v")
	env := []string{"NOVA_REDIS_CI_CHILD=" + mode}
	if mode == "fail" {
		env = append(env, "NOVA_CI=1")
	}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("re-exec %s: %v\n%s", mode, err, out)
	return string(out), -1
}

// redisServerGates reports Go files other than the helper that look up
// redis-server themselves or skip because of it. Those skips are how a CI
// runner without the binary used to go green.
func redisServerGates(t *testing.T, root string) []string {
	t.Helper()
	const helper = "internal/nsprint/testutil/redis.go"
	const self = "internal/ci/redis_ci_test.go"
	look := "LookPath(" + `"redis-server"` + ")"
	spawn := "exec.Command(" + `"redis-server"`
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == helper || rel == self {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, look) || strings.Contains(line, spawn) ||
				(strings.Contains(line, "t.Skip") && strings.Contains(line, "redis-server")) {
				bad = append(bad, rel+":"+itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return bad
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestStartFailsClosedOnTheUnitTierShim is the other half of
// TestUnitTierRefusesRedisServer (unit_tier_class_test.go): with the unit
// tier's redis-server shim first on PATH and NOVA_CI=1, testutil.Start fails
// the test naming the shim's line, never skips and never starts a server. It
// calls testutil.Start, so it lives in this functional-tagged file; the shim
// is ci.yml's own step, run here.
func TestStartFailsClosedOnTheUnitTierShim(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_UNIT_TIER_SHIM_CHILD") == "1" {
		testutil.Start(t)
		t.Fatal("testutil.Start returned with the unit tier's shim first on PATH; it must fail closed")
	}
	dir := unitTierShim(t)
	tmp := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run", "^TestStartFailsClosedOnTheUnitTierShim$", "-test.count=1", "-test.v")
	child.Env = []string{"NOVA_UNIT_TIER_SHIM_CHILD=1", "NOVA_CI=1", "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + tmp, "TMPDIR=" + tmp}
	got, err := child.CombinedOutput()
	if err == nil || !strings.Contains(string(got), unitShimMessage) || strings.Contains(string(got), "--- SKIP") {
		t.Fatalf("testutil.Start under NOVA_CI=1 with the shim first on PATH: err %v; want a failure naming %q\n%s", err, unitShimMessage, got)
	}
}
