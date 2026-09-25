package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func readGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x", "GIT_CONFIG_GLOBAL=/dev/null")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

// readFixture is a bare mirror with dev and refs/pull/3/head, and a Redis
// record pr:nova-tools:3 for it. It returns the mirror and the Redis address.
func readFixture(t *testing.T) (mirror, addr, head string) {
	t.Helper()
	t.Setenv(store.UserEnv, "")
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	readGit(t, work, "init", "-q", "-b", "dev")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readGit(t, work, "add", ".")
	readGit(t, work, "commit", "-qm", "base")
	base := readGit(t, work, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readGit(t, work, "commit", "-qam", "pr")
	head = readGit(t, work, "rev-parse", "HEAD")
	readGit(t, work, "reset", "-q", "--hard", base)
	mirror = filepath.Join(t.TempDir(), "nova-tools.git")
	readGit(t, work, "clone", "-q", "--bare", work, mirror)
	readGit(t, mirror, "update-ref", "refs/pull/3/head", head)
	// A real store with the library loaded: read post is one ns_read_post call.
	addr = testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(context.Background(), read.Key("nova-tools", "3"), "head", head, "base", "dev", "base_sha", base, "paths", "a.txt", "done_when", "true", "stream", "s").Err(); err != nil {
		t.Fatal(err)
	}
	return mirror, addr, head
}

// TestReadUsageRefusalsOpenNoStore: every usage error is exit 2 on stderr
// before Redis is touched.
func TestReadUsageRefusalsOpenNoStore(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	t.Setenv("NOVA_SPRINT_REDIS", "")
	t.Setenv("NOVA_REDIS_ADDR", "")
	mr := miniredis.RunT(t)
	for _, args := range [][]string{
		{"read"},
		{"read", "brief"},
		{"read", "brief", "--repo", "nova-tools", "--n", "3", "--redis", mr.Addr()},
		{"read", "brief", "--repo", "mas-bandwidth/nova-tools/x", "--n", "3", "--out", "d", "--redis", mr.Addr()},
		{"read", "brief", "--repo", "nova-tools", "--n", "x", "--out", "d", "--redis", mr.Addr()},
		{"read", "brief", "--repo", "nova-tools", "--n", "3", "--out", "d"},
		{"read", "post", "--repo", "nova-tools", "--n", "3", "--redis", mr.Addr()},
		{"read", "post", "--repo", "nova-tools", "--n", "3", "--line", "SCORE who=r head=abcdef0 score=9/10", "--out", "d", "--redis", mr.Addr()},
		{"read", "lines", "--repo", "nova-tools", "--n", "3", "--redis", mr.Addr()},
		{"read", "brief", "--repo", "nova-tools", "--n", "3", "--out", "d", "--redis", mr.Addr(), "extra"},
	} {
		code, stdout, stderr := runSprint(args...)
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "nova-sprint read") {
			t.Fatalf("%v: exit %d stdout %q stderr %q; want 2 and a usage line", args, code, stdout, stderr)
		}
	}
	if n := mr.TotalConnectionCount(); n != 0 {
		t.Fatalf("%d store connection(s) on usage errors", n)
	}
}

// TestReadBriefVerbWritesTheBriefWithZeroGitHubCalls runs the verb end to
// end: Redis record + mirror in, brief.md and diff.patch out, no HTTP.
func TestReadBriefVerbWritesTheBriefWithZeroGitHubCalls(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	mirror, addr, head := readFixture(t)
	out := filepath.Join(t.TempDir(), "read-3")
	code, stdout, stderr := runSprint("read", "brief", "--repo", "nova-tools", "--n", "3", "--out", out, "--mirror", mirror, "--redis", addr)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "READ BRIEF repo=nova-tools n=3 head="+head[:8]) || !strings.Contains(stdout, "github_calls=0") {
		t.Fatalf("receipt %q", stdout)
	}
	if stub.Calls() != 0 {
		t.Fatalf("%d GitHub calls", stub.Calls())
	}
	b, err := os.ReadFile(filepath.Join(out, "brief.md"))
	if err != nil || !strings.Contains(string(b), "PATHS: a.txt") {
		t.Fatalf("brief.md: %v %s", err, b)
	}
	p, err := os.ReadFile(filepath.Join(out, "diff.patch"))
	if err != nil || !strings.Contains(string(p), "+b") {
		t.Fatalf("diff.patch: %v %s", err, p)
	}
}

// TestReadPostVerbNoGitHub: the line lands in Redis with zero HTTP calls;
// without --no-github and without a token the verb refuses (exit 1) naming
// the remedy, before any write.
func TestReadPostVerbNoGitHub(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	_, addr, head := readFixture(t)
	typed := "HOLD who=rowan head=" + head + " score=6/10 gates=ci:pending,base:ok,scope:ok"
	code, stdout, stderr := runSprint("read", "post", "--repo", "nova-tools", "--n", "3", "--line", typed, "--redis", addr)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "READ POST REFUSED repo=nova-tools n=3 why=no GH_TOKEN") || !strings.Contains(stderr, "--no-github") {
		t.Fatalf("no token: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = runSprint("read", "post", "--repo", "nova-tools", "--n", "3", "--line", typed, "--no-github", "--redis", addr)
	if code != 0 || !strings.Contains(stdout, "READ POST repo=nova-tools n=3 kind=HOLD lines=1 github_calls=0") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if stub.Calls() != 0 {
		t.Fatalf("%d GitHub calls", stub.Calls())
	}
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	lines, _ := c.LRange(context.Background(), read.LinesKey("nova-tools", "3"), 0, -1).Result()
	if len(lines) != 1 || lines[0] != typed {
		t.Fatalf("lines %q", lines)
	}
	// The same call wrote the lander's field: a HOLD holds the member (#4049).
	if reads := c.HGet(context.Background(), read.Key("nova-tools", "3"), "reads").Val(); reads != typed {
		t.Fatalf("reads %q, want the HOLD line", reads)
	}
}
