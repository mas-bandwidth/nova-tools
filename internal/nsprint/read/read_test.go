package read_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x", "GIT_CONFIG_GLOBAL=/dev/null")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

// mirrorFixture builds a bare mirror the way mirror-refresh leaves one: dev
// at the base commit and refs/pull/7/head at the PR commit, which touches
// internal/x/x.go (inside PATHS) and, when outside is set, README.md too.
// It returns the mirror dir, the base sha and the PR head sha.
func mirrorFixture(t *testing.T, outside bool) (mirror, base, head string) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(filepath.Join(work, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "init", "-q", "-b", "dev")
	write(t, filepath.Join(work, "internal", "x", "x.go"), "package x\n")
	write(t, filepath.Join(work, "README.md"), "base\n")
	gitRun(t, work, "add", ".")
	gitRun(t, work, "commit", "-qm", "base")
	base = gitRun(t, work, "rev-parse", "HEAD")
	gitRun(t, work, "switch", "-qc", "rowan/7-thing")
	write(t, filepath.Join(work, "internal", "x", "x.go"), "package x\n\nfunc X() int { return 7 }\n")
	if outside {
		write(t, filepath.Join(work, "README.md"), "changed\n")
	}
	gitRun(t, work, "add", ".")
	gitRun(t, work, "commit", "-qm", "thing")
	head = gitRun(t, work, "rev-parse", "HEAD")
	mirror = filepath.Join(t.TempDir(), "nova-tools.git")
	gitRun(t, work, "clone", "-q", "--bare", work, mirror)
	gitRun(t, mirror, "update-ref", "refs/pull/7/head", head)
	gitRun(t, mirror, "update-ref", "-d", "refs/heads/rowan/7-thing")
	return mirror, base, head
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedRecord(t *testing.T, c *redis.Client, base, head string) {
	t.Helper()
	ctx := context.Background()
	if err := c.HSet(ctx, read.Key("nova-tools", "7"), map[string]any{
		"head": head, "base": "dev", "base_sha": base, "paths": "internal/x",
		"done_when": "go test ./internal/x/ -run TestX", "stream": "nova sprint migration",
		"depends_on": "none", "branch": "rowan/7-thing", "who": "any",
	}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.RPush(ctx, read.LinesKey("nova-tools", "7"), "JEV who=jev head="+head+" score=9/10 gates=ci:ok,base:ok,scope:ok").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, read.CIKey("nova-tools", head), "state", "success", "shards", "3/3").Err(); err != nil {
		t.Fatal(err)
	}
}

// client is a throwaway redis-server with the nova_sprint library loaded: a
// SCORE or CLOSE post is one library call (ns_read_post).
func client(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestReadBriefZeroGitHubCalls is the DONE-WHEN of #3599: the brief is built
// from Redis and the mirror with no HTTP call at all (the GitHub stub fails
// the test on any request and its counter stays 0).
func TestReadBriefZeroGitHubCalls(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	mirror, base, head := mirrorFixture(t, false)
	c := client(t)
	seedRecord(t, c, base, head)
	out := filepath.Join(t.TempDir(), "read-7")
	var stdout, stderr strings.Builder
	code := read.Brief(context.Background(), c, "nova-tools", "7", mirror, out, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if n := stub.Calls(); n != 0 {
		t.Fatalf("%d HTTP call(s) to GitHub; a read makes zero", n)
	}
	receipt := stdout.String()
	for _, want := range []string{"READ BRIEF repo=nova-tools n=7", "head=" + head[:8], "base_sha=" + base[:8], "files=1", "outside_paths=0", "lines=1", "ci=2", "github_calls=0"} {
		if !strings.Contains(receipt, want) {
			t.Fatalf("receipt %q lacks %q", receipt, want)
		}
	}
	brief, err := os.ReadFile(filepath.Join(out, "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	b := string(brief)
	for _, want := range []string{
		"HEAD: " + head, "base-sha: " + base, "PATHS: internal/x", "DONE-WHEN: go test ./internal/x/ -run TestX",
		"STREAM: nova sprint migration", "DEPENDS-ON: none", "- state=success", "- shards=3/3",
		"- JEV who=jev head=" + head, "- internal/x/x.go", "Files outside PATHS (0):\n- none",
		"nova-sprint read post --repo nova-tools --n 7", "git -C " + mirror + " diff " + base + ".." + head,
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("brief lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "HEAD MOVED") {
		t.Fatalf("brief says HEAD MOVED for a mirror ref at the record head:\n%s", b)
	}
	for _, bad := range []string{"gh api", "gh pr", "graphql", "GraphQL", "check-runs"} {
		if strings.Contains(b, bad) {
			t.Fatalf("brief tells the reader to call GitHub (%q):\n%s", bad, b)
		}
	}
	patch, err := os.ReadFile(filepath.Join(out, "diff.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "+func X() int { return 7 }") || !strings.Contains(string(patch), "internal/x/x.go") {
		t.Fatalf("diff.patch is not the mirror diff base..head:\n%s", patch)
	}
}

// TestReadBriefNamesFilesOutsidePaths: the scope gate is precomputed from
// the mirror diff and PATHS, so the reader does not redo it by hand.
func TestReadBriefNamesFilesOutsidePaths(t *testing.T) {
	mirror, base, head := mirrorFixture(t, true)
	c := client(t)
	seedRecord(t, c, base, head)
	out := filepath.Join(t.TempDir(), "read-7")
	var stdout, stderr strings.Builder
	if code := read.Brief(context.Background(), c, "nova-tools", "7", mirror, out, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "files=2 outside_paths=1") {
		t.Fatalf("receipt %q", stdout.String())
	}
	b, _ := os.ReadFile(filepath.Join(out, "brief.md"))
	if !strings.Contains(string(b), "Files outside PATHS (1):\n- README.md") {
		t.Fatalf("brief does not name README.md as outside PATHS:\n%s", b)
	}
}

// TestReadBriefSaysHeadMoved: the record head is what is read; a mirror
// refs/pull/<n>/head that moved on is named so the line can say so.
func TestReadBriefSaysHeadMoved(t *testing.T) {
	mirror, base, head := mirrorFixture(t, false)
	gitRun(t, mirror, "update-ref", "refs/pull/7/head", base)
	c := client(t)
	seedRecord(t, c, base, head)
	out := filepath.Join(t.TempDir(), "read-7")
	var stdout, stderr strings.Builder
	if code := read.Brief(context.Background(), c, "nova-tools", "7", mirror, out, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "mirror_head="+base[:8]) {
		t.Fatalf("receipt %q lacks mirror_head", stdout.String())
	}
	b, _ := os.ReadFile(filepath.Join(out, "brief.md"))
	if !strings.Contains(string(b), "HEAD MOVED: the mirror's refs/pull/7/head is "+base) {
		t.Fatalf("brief lacks HEAD MOVED:\n%s", b)
	}
}

// TestReadBriefRefusals: no record, no base_sha, and a head the mirror has
// not fetched yet are each one refusal with the remedy named, exit 1,
// nothing written.
func TestReadBriefRefusals(t *testing.T) {
	mirror, base, head := mirrorFixture(t, false)
	c := client(t)
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "read-7")
	try := func(want string) {
		t.Helper()
		var stdout, stderr strings.Builder
		code := read.Brief(ctx, c, "nova-tools", "7", mirror, out, &stdout, &stderr)
		if code != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "READ BRIEF REFUSED repo=nova-tools n=7") || !strings.Contains(stderr.String(), want) {
			t.Fatalf("exit %d stdout %q stderr %q; want exit 1 and %q", code, stdout.String(), stderr.String(), want)
		}
		if _, err := os.Stat(filepath.Join(out, "brief.md")); err == nil {
			t.Fatal("brief.md written on a refusal")
		}
	}
	try("MISSING pr:nova-tools:7: no head")
	if err := c.HSet(ctx, read.Key("nova-tools", "7"), "head", head).Err(); err != nil {
		t.Fatal(err)
	}
	try("no base_sha")
	if err := c.HSet(ctx, read.Key("nova-tools", "7"), "base_sha", base, "head", strings.Repeat("f", 40)).Err(); err != nil {
		t.Fatal(err)
	}
	try("has no commit " + strings.Repeat("f", 40) + " yet; wait one mirror-refresh tick")
	if err := c.HSet(ctx, read.Key("nova-tools", "7"), "head", head).Err(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := read.Brief(ctx, c, "nova-tools", "7", filepath.Join(t.TempDir(), "absent.git"), out, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "is not a directory; run mirror-refresh --once or pass --mirror") {
		t.Fatalf("absent mirror: exit %d stderr %q", code, stderr.String())
	}
}

// commentCounter is the HTTP-call counter: it records every request and
// answers a comment id.
type commentCounter struct {
	srv   *httptest.Server
	calls atomic.Int64
	path  string
	body  string
}

func startCounter(t *testing.T) *commentCounter {
	t.Helper()
	cc := &commentCounter{}
	cc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cc.calls.Add(1)
		cc.path = r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		cc.body = string(b)
		_, _ = w.Write([]byte(`{"id": 4242}`))
	}))
	t.Cleanup(cc.srv.Close)
	return cc
}

const line = "SCORE who=rowan head=%s score=9/10 gates=ci:ok,base:ok,scope:ok\n1. fine (internal/x/x.go:3)"

// TestReadPostStoresLineAndMirrorsOneComment: the line lands on the
// record's lines list in Redis and, with a poster, exactly one REST call
// makes the comment.
func TestReadPostStoresLineAndMirrorsOneComment(t *testing.T) {
	_, base, head := mirrorFixture(t, false)
	c := client(t)
	seedRecord(t, c, base, head)
	cc := startCounter(t)
	poster := &read.Poster{BaseURL: cc.srv.URL, Owner: "mas-bandwidth", Token: "t", HTTP: cc.srv.Client()}
	typed := strings.ReplaceAll(line, "%s", head)
	var stdout, stderr strings.Builder
	if code := read.Post(context.Background(), c, "nova-tools", "7", typed, poster, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	if got := cc.calls.Load(); got != 1 {
		t.Fatalf("%d HTTP calls, want exactly 1", got)
	}
	if cc.path != "POST /repos/mas-bandwidth/nova-tools/issues/7/comments" {
		t.Fatalf("request %q", cc.path)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(cc.body), &payload); err != nil || payload["body"] != typed {
		t.Fatalf("comment body %q (%v)", cc.body, err)
	}
	if !strings.Contains(stdout.String(), "READ POST repo=nova-tools n=7 kind=SCORE lines=2 github_calls=1 comment=4242") {
		t.Fatalf("receipt %q", stdout.String())
	}
	lines, err := c.LRange(context.Background(), read.LinesKey("nova-tools", "7"), 0, -1).Result()
	if err != nil || len(lines) != 2 || lines[1] != typed {
		t.Fatalf("lines %q (%v)", lines, err)
	}
	rec, err := c.HGetAll(context.Background(), read.Key("nova-tools", "7")).Result()
	if err != nil || !strings.HasPrefix(rec["last_line"], "SCORE who=rowan head="+head) || rec["last_line_at"] == "" {
		t.Fatalf("record %v (%v)", rec, err)
	}	// The same call appended the typed first line to the reads field the
	// stream lander reads (#4049); the body stays in the lines log only.
	if want := strings.SplitN(typed, "\n", 2)[0]; rec["reads"] != want {
		t.Fatalf("reads %q, want %q", rec["reads"], want)
	}
}

// TestReadPostNoGitHubMakesZeroCalls: --no-github is Redis only.
func TestReadPostNoGitHubMakesZeroCalls(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	_, base, head := mirrorFixture(t, false)
	c := client(t)
	seedRecord(t, c, base, head)
	typed := strings.ReplaceAll(line, "%s", head)
	var stdout, stderr strings.Builder
	if code := read.Post(context.Background(), c, "nova-tools", "7", typed, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	if stub.Calls() != 0 {
		t.Fatalf("%d HTTP calls with --no-github", stub.Calls())
	}
	if !strings.Contains(stdout.String(), "kind=SCORE lines=2 github_calls=0") {
		t.Fatalf("receipt %q", stdout.String())
	}
}

// TestReadPostRefusals: an untyped line, a line with no who= or head=, and
// a PR with no record are refused (exit 1) with nothing written and no call.
func TestReadPostRefusals(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	_, base, head := mirrorFixture(t, false)
	c := client(t)
	ctx := context.Background()
	poster := &read.Poster{BaseURL: stub.URL, Owner: "mas-bandwidth", Token: "t"}
	try := func(n, l, want string) {
		t.Helper()
		var stdout, stderr strings.Builder
		code := read.Post(ctx, c, "nova-tools", n, l, poster, &stdout, &stderr)
		if code != 1 || stdout.String() != "" || !strings.Contains(stderr.String(), "READ POST REFUSED") || !strings.Contains(stderr.String(), want) {
			t.Fatalf("%q: exit %d stdout %q stderr %q; want exit 1 and %q", l, code, stdout.String(), stderr.String(), want)
		}
	}
	try("7", "looks good who=rowan head="+head, `first word "looks" is not a typed line`)
	try("7", "SCORE head="+head+" score=9/10", "no who=")
	try("7", "SCORE who=rowan score=9/10", "no head=")
	try("7", "", "empty line")
	try("7", "SCORE who=rowan head="+head+" score=9/10", "pr:nova-tools:7 has no head")
	seedRecord(t, c, base, head)
	try("8", "SCORE who=rowan head="+head+" score=9/10", "pr:nova-tools:8 has no head")
	if n, _ := c.LLen(ctx, read.LinesKey("nova-tools", "7")).Result(); n != 1 {
		t.Fatalf("%d lines after refusals, want the seeded 1", n)
	}
	if stub.Calls() != 0 {
		t.Fatalf("%d HTTP calls on refusals", stub.Calls())
	}
}

// TestReadPostGitHubDownKeepsTheRedisLine: a failed comment mirror is
// reported (exit 1) and the Redis line stays; the record is Redis.
func TestReadPostGitHubDownKeepsTheRedisLine(t *testing.T) {
	_, base, head := mirrorFixture(t, false)
	c := client(t)
	seedRecord(t, c, base, head)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "rate limited", 403) }))
	defer srv.Close()
	poster := &read.Poster{BaseURL: srv.URL, Owner: "mas-bandwidth", Token: "t", HTTP: srv.Client()}
	typed := strings.ReplaceAll(line, "%s", head)
	var stdout, stderr strings.Builder
	code := read.Post(context.Background(), c, "nova-tools", "7", typed, poster, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "redis=ok github=github 403") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	if n, _ := c.LLen(context.Background(), read.LinesKey("nova-tools", "7")).Result(); n != 2 {
		t.Fatalf("%d lines, want 2: the Redis write is the record", n)
	}
}

func TestOutsidePaths(t *testing.T) {
	got := read.OutsidePaths("internal/x, docs/CLI.md", []string{"internal/x/a.go", "internal/xy/b.go", "docs/CLI.md", "docs/other.md"})
	if strings.Join(got, ",") != "internal/xy/b.go,docs/other.md" {
		t.Fatalf("outside %q", got)
	}
	if got := read.OutsidePaths("", []string{"a"}); len(got) != 1 {
		t.Fatalf("empty PATHS covers nothing, got %q", got)
	}
}
