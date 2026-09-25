package mirror

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// allArgv is every git argv any test in this package ran, for
// TestNoShallowFlagsOnMirrorPaths.
var (
	allMu   sync.Mutex
	allArgv [][]string
)

// logGit is the real git with every argv recorded.
type logGit struct {
	mu   sync.Mutex
	argv [][]string
}

func (g *logGit) Run(ctx context.Context, args ...string) (string, error) {
	g.mu.Lock()
	g.argv = append(g.argv, append([]string(nil), args...))
	g.mu.Unlock()
	allMu.Lock()
	allArgv = append(allArgv, append([]string(nil), args...))
	allMu.Unlock()
	return ExecGit{Timeout: 30 * time.Second}.Run(ctx, args...)
}

// fetchURLs are the URLs this runner cloned or fetched from.
func (g *logGit) fetchURLs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var urls []string
	for _, a := range g.argv {
		for i, w := range a {
			if w == "fetch" || w == "clone" {
				for _, x := range a[i+1:] {
					if !strings.HasPrefix(x, "-") && !strings.HasPrefix(x, "+") {
						urls = append(urls, x)
						break
					}
				}
			}
		}
	}
	return urls
}

func (g *logGit) reset() { g.mu.Lock(); g.argv = nil; g.mu.Unlock() }

func gitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.invalid")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixture is a bare "GitHub" <base>/nova-tools.git with dev, and a work tree
// that pushes to it.
type fixture struct{ base, work string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	gitEnv(t)
	root := t.TempDir()
	f := fixture{base: filepath.Join(root, "github"), work: filepath.Join(root, "work")}
	git(t, root, "init", "-q", "--bare", "--initial-branch=dev", filepath.Join(f.base, "nova-tools.git"))
	git(t, root, "init", "-q", "--initial-branch=dev", f.work)
	git(t, f.work, "remote", "add", "origin", filepath.Join(f.base, "nova-tools.git"))
	f.commit(t, "one")
	return f
}

// commit makes a commit on dev, pushes it and a pull ref, and returns its sha.
func (f fixture) commit(t *testing.T, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.work, "f.txt"), []byte(msg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, f.work, "add", "f.txt")
	git(t, f.work, "commit", "-q", "-m", msg)
	git(t, f.work, "push", "-q", "origin", "dev", "dev:refs/pull/1/head")
	return git(t, f.work, "rev-parse", "HEAD")
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func benchConfig(t *testing.T, bench string, f fixture, srcDir string, g Git) Config {
	return Config{
		Bench: bench, Source: "src", SourceURL: srcDir, Upstream: f.base,
		Dir: filepath.Join(t.TempDir(), bench), Repos: []Repo{{"nova-tools", "dev"}}, Git: g,
	}
}

// TestMirrorFanOut is #2922's DONE-WHEN: the source fetches the fixture GitHub
// once, the follower fetches only the source's mirror, and status reads both OK.
func TestMirrorFanOut(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	mr, rdb := newRedis(t)
	mr.SAdd("benches", "src", "f1")
	gs, gf := &logGit{}, &logGit{}
	src := benchConfig(t, "src", f, "", gs)
	f1 := benchConfig(t, "f1", f, src.Dir, gf)

	// 1. a commit is pushed to the fixture remote
	sha := f.commit(t, "two")

	// 2. one source tick: exactly one upstream fetch, the receipt carries the tip
	var out bytes.Buffer
	if ok, err := Pass(ctx, src, rdb, nil, true, &out); err != nil || !ok {
		t.Fatalf("source pass ok=%v err=%v\n%s", ok, err, out.String())
	}
	up := filepath.Join(f.base, "nova-tools.git")
	if urls := gs.fetchURLs(); len(urls) != 1 || urls[0] != up {
		t.Fatalf("source fetched %v, want exactly [%s]", urls, up)
	}
	if got := mr.HGet(Key("src"), "nova-tools"); got != sha {
		t.Fatalf("bench:src:mirrors nova-tools=%q, want %s", got, sha)
	}
	if got := mr.HGet(Key("src"), "nova-tools:from"); got != "github" {
		t.Fatalf("src from=%q", got)
	}

	// 3. one follower tick moves f1's dev, fetching only the source's mirror
	out.Reset()
	if ok, err := Pass(ctx, f1, rdb, nil, true, &out); err != nil || !ok {
		t.Fatalf("follower pass ok=%v err=%v\n%s", ok, err, out.String())
	}
	if got := git(t, "", "--git-dir", filepath.Join(f1.Dir, "nova-tools.git"), "rev-parse", "dev"); got != sha {
		t.Fatalf("f1 dev=%s, want %s", got, sha)
	}
	want := filepath.Join(src.Dir, "nova-tools.git")
	for _, u := range gf.fetchURLs() {
		if u != want {
			t.Fatalf("follower fetched %s, want only the source's mirror %s", u, want)
		}
	}
	if !strings.Contains(out.String(), "OK f1 nova-tools tip="+sha+" from=src pulls=1") {
		t.Fatalf("follower line: %q", out.String())
	}

	// a second commit: the next ticks refresh (fetch, not clone) both
	sha2 := f.commit(t, "three")
	gs.reset()
	gf.reset()
	for _, c := range []Config{src, f1} {
		if ok, err := Pass(ctx, c, rdb, map[string]string{}, false, &out); err != nil || !ok {
			t.Fatalf("%s second pass ok=%v err=%v", c.Bench, ok, err)
		}
	}
	if u := gs.fetchURLs(); len(u) != 1 || u[0] != up {
		t.Fatalf("source second tick fetched %v", u)
	}
	if u := gf.fetchURLs(); len(u) != 1 || u[0] != want {
		t.Fatalf("follower second tick fetched %v", u)
	}

	// 4. status with --expect: both OK, 2/2 100%, exit 0
	out.Reset()
	ok, err := Status(ctx, rdb, StatusOptions{Repos: []string{"nova-tools"}, Expect: map[string]string{"nova-tools": sha2}, Stale: time.Minute}, &out)
	if err != nil || !ok {
		t.Fatalf("status ok=%v err=%v\n%s", ok, err, out.String())
	}
	for _, w := range []string{
		"f1 nova-tools observed=" + sha2 + " expected=" + sha2 + " OK from=src",
		"src nova-tools observed=" + sha2 + " expected=" + sha2 + " OK from=github",
		"nova-tools 2/2 100%",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("status missing %q:\n%s", w, out.String())
		}
	}
}

// TestSourceStaleFallsBackToGitHub: with no fresh source receipt a follower
// fetches the upstream itself (from=github-fallback); with the source's mirror
// unreachable it does the same; with a fresh source it never touches upstream.
func TestSourceStaleFallsBackToGitHub(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	mr, rdb := newRedis(t)
	up := filepath.Join(f.base, "nova-tools.git")

	// no source receipt at all: stale
	g := &logGit{}
	f1 := benchConfig(t, "f1", f, filepath.Join(t.TempDir(), "src-mirror"), g)
	var out bytes.Buffer
	if ok, err := Pass(ctx, f1, rdb, nil, true, &out); err != nil || !ok {
		t.Fatalf("stale pass ok=%v err=%v\n%s", ok, err, out.String())
	}
	if !strings.Contains(out.String(), "from=github-fallback") || mr.HGet(Key("f1"), "nova-tools:from") != "github-fallback" {
		t.Fatalf("stale source: %s", out.String())
	}
	if u := g.fetchURLs(); len(u) != 1 || u[0] != up {
		t.Fatalf("stale follower fetched %v, want only %s", u, up)
	}

	// a fresh receipt but the source's mirror is unreachable: fall back too
	mr.HSet(Key("src"), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	g.reset()
	out.Reset()
	if ok, _ := Pass(ctx, f1, rdb, nil, true, &out); !ok || !strings.Contains(out.String(), "from=github-fallback") {
		t.Fatalf("unreachable source: %s", out.String())
	}
	if u := g.fetchURLs(); len(u) != 2 || u[0] != f1.SourceURL+"/nova-tools.git" || u[1] != up {
		t.Fatalf("unreachable source fetched %v", u)
	}

	// status marks the bench by its tip as usual, and names the fallback
	out.Reset()
	tip := mr.HGet(Key("f1"), "nova-tools")
	mr.SAdd("benches", "f1")
	if ok, _ := Status(ctx, rdb, StatusOptions{Repos: []string{"nova-tools"}, Expect: map[string]string{"nova-tools": tip}, Stale: time.Minute}, &out); !ok ||
		!strings.Contains(out.String(), "f1 nova-tools observed="+tip+" expected="+tip+" OK from=github-fallback") {
		t.Fatalf("status: %s", out.String())
	}
}

// TestFetchFailureLeavesMirror: the source vanishes; the pass is REFUSED
// reason=fetch, the refs are byte-identical, the tip in Redis is kept, and a
// --reference clone against the mirror still works.
func TestFetchFailureLeavesMirror(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	mr, rdb := newRedis(t)
	g := &logGit{}
	c := benchConfig(t, "src", f, "", g)
	var out bytes.Buffer
	if ok, _ := Pass(ctx, c, rdb, nil, true, &out); !ok {
		t.Fatalf("first pass: %s", out.String())
	}
	tip := mr.HGet(Key("src"), "nova-tools")
	m := filepath.Join(c.Dir, "nova-tools.git")
	before := git(t, "", "--git-dir", m, "for-each-ref")
	c.Upstream = filepath.Join(t.TempDir(), "gone")
	out.Reset()
	ok, err := Pass(ctx, c, rdb, nil, true, &out)
	if err != nil || ok {
		t.Fatalf("pass against a vanished remote ok=%v err=%v", ok, err)
	}
	if !strings.HasPrefix(out.String(), "REFUSED src nova-tools reason=fetch err=") {
		t.Fatalf("line %q", out.String())
	}
	if after := git(t, "", "--git-dir", m, "for-each-ref"); after != before {
		t.Fatalf("refs moved:\n%s\n---\n%s", before, after)
	}
	if got := mr.HGet(Key("src"), "nova-tools"); got != tip {
		t.Fatalf("tip %q after refusal, want kept %q", got, tip)
	}
	if e := mr.HGet(Key("src"), "nova-tools:err"); !strings.HasPrefix(e, "fetch: ") {
		t.Fatalf("err field %q", e)
	}
	dst := filepath.Join(t.TempDir(), "clone")
	git(t, "", "clone", "-q", "--depth", "50", "--reference", m, filepath.Join(f.base, "nova-tools.git"), dst)
}

// TestFirstCloneAtomic: a clone that fails leaves no <repo>.git and no tmp dir.
func TestFirstCloneAtomic(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	_, rdb := newRedis(t)
	c := benchConfig(t, "src", f, "", &logGit{})
	c.Upstream = filepath.Join(t.TempDir(), "nowhere")
	var out bytes.Buffer
	if ok, _ := Pass(ctx, c, rdb, nil, true, &out); ok {
		t.Fatal("clone from nowhere passed")
	}
	if !strings.Contains(out.String(), "reason=clone") {
		t.Fatalf("line %q", out.String())
	}
	entries, _ := os.ReadDir(c.Dir)
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
	// a clone whose ref is missing is refused too, and also leaves nothing
	c.Upstream = f.base
	c.Repos = []Repo{{"nova-tools", "no-such-branch"}}
	out.Reset()
	if ok, _ := Pass(ctx, c, rdb, nil, true, &out); ok || !strings.Contains(out.String(), "reason=ref") {
		t.Fatalf("missing ref: %s", out.String())
	}
	if entries, _ := os.ReadDir(c.Dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

// TestShallowMirrorRefusedAndRepaired is rowan-tools#275: check refuses a
// shallow mirror, a refresh repairs it, and a --reference clone then works.
func TestShallowMirrorRefusedAndRepaired(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.commit(t, "two")
	_, rdb := newRedis(t)
	c := benchConfig(t, "src", f, "", &logGit{})
	m := filepath.Join(c.Dir, "nova-tools.git")
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, "", "clone", "-q", "--mirror", "--depth", "1", "file://"+filepath.Join(f.base, "nova-tools.git"), m)
	res := Check(ctx, c)
	if len(res) != 1 || res[0].OK || res[0].Reason != "shallow" {
		t.Fatalf("check on a shallow mirror: %+v", res)
	}
	var out bytes.Buffer
	if ok, _ := Pass(ctx, c, rdb, nil, true, &out); !ok || !strings.Contains(out.String(), "repaired=shallow") {
		t.Fatalf("repair pass: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(m, "shallow")); err == nil {
		t.Fatal("shallow file still there")
	}
	if got := git(t, "", "--git-dir", m, "rev-parse", "--is-shallow-repository"); got != "false" {
		t.Fatalf("is-shallow %s", got)
	}
	if res := Check(ctx, c); !res[0].OK {
		t.Fatalf("check after repair: %+v", res)
	}
	git(t, "", "clone", "-q", "--reference", m, filepath.Join(f.base, "nova-tools.git"), filepath.Join(t.TempDir(), "c"))
}

// TestNoShallowFlagsOnMirrorPaths: no argv this package ran carries a shallow
// or partial-clone flag. It runs after the other tests in this file.
func TestNoShallowFlagsOnMirrorPaths(t *testing.T) {
	allMu.Lock()
	defer allMu.Unlock()
	if len(allArgv) == 0 {
		t.Skip("run with the package: it reads the other tests' argv")
	}
	for _, a := range allArgv {
		for _, w := range a {
			for _, bad := range []string{"--depth", "--deepen", "--shallow-since", "--shallow-exclude", "--filter"} {
				if w == bad || strings.HasPrefix(w, bad+"=") {
					t.Errorf("git %v carries %s", a, bad)
				}
			}
		}
	}
}

// TestStatusReceiptShapes: OK, MISMATCH, MISSING and UNREACHABLE, the total
// line, and ? for a stale field.
func TestStatusReceiptShapes(t *testing.T) {
	ctx := context.Background()
	mr, rdb := newRedis(t)
	now := time.Now()
	mr.SetTime(now)
	good, bad := strings.Repeat("a", 40), strings.Repeat("b", 40)
	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(-d).UnixMilli(), 10) }
	mr.SAdd("benches", "b1", "b2", "b3", "b4")
	mr.HSet(Key("b1"), "at", at(time.Second), "nova-tools", good, "nova-tools:from", "space")
	mr.HSet(Key("b2"), "at", at(time.Second), "nova-tools", bad, "nova-tools:from", "space")
	mr.HSet(Key("b4"), "at", at(time.Hour), "nova-tools", good, "nova-tools:from", "space")
	var out bytes.Buffer
	o := StatusOptions{Repos: []string{"nova-tools"}, Expect: map[string]string{"nova-tools": good}, Stale: time.Minute}
	ok, err := Status(ctx, rdb, o, &out)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	for _, w := range []string{
		"b1 nova-tools observed=" + good + " expected=" + good + " OK from=space age=1s",
		"b2 nova-tools observed=" + bad + " expected=" + good + " MISMATCH from=space",
		"b3 nova-tools observed=? expected=" + good + " MISSING from=? age=?s",
		"b4 nova-tools observed=? expected=" + good + " UNREACHABLE from=? age=3600s",
		"nova-tools 1/4 25%",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("missing %q in:\n%s", w, out.String())
		}
	}
	// the expected tip from the source's own receipt, and all OK exits clean
	mr.Del("benches")
	mr.SAdd("benches", "b1")
	out.Reset()
	ok, err = Status(ctx, rdb, StatusOptions{Repos: []string{"nova-tools"}, Source: "b1", Stale: time.Minute}, &out)
	if err != nil || !ok || !strings.Contains(out.String(), "nova-tools 1/1 100%") {
		t.Fatalf("all OK: ok=%v err=%v\n%s", ok, err, out.String())
	}
}

// TestOneLoopPerBench: a second loop on the same bench exits 2 while the lease
// is held; the holder's loop runs one pass per tick and stops with its ctx.
func TestOneLoopPerBench(t *testing.T) {
	f := newFixture(t)
	_, rdb := newRedis(t)
	c := benchConfig(t, "src", f, "", &logGit{})
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	var out, errOut bytes.Buffer
	done := make(chan int)
	go func() { done <- Loop(ctx, c, rdb, ticks, time.Minute, "first", &out, &errOut) }()
	ticks <- time.Now() // the first pass has run once this send is taken
	var out2, err2 bytes.Buffer
	if code := Loop(context.Background(), c, rdb, nil, time.Minute, "second", &out2, &err2); code != 2 {
		t.Fatalf("second loop exit %d, want 2", code)
	}
	if !strings.Contains(err2.String(), "REFUSED src - reason=lease err=held by first") {
		t.Fatalf("second loop said %q", err2.String())
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("first loop exit %d: %s", code, errOut.String())
	}
	if n := strings.Count(out.String(), "OK src nova-tools"); n != 1 {
		t.Fatalf("loop printed %d OK lines, want 1 (only the first pass changed a tip):\n%s", n, out.String())
	}
}

func TestParseReposAndRemote(t *testing.T) {
	r, err := ParseRepos("nova-tools:dev,schema:main")
	if err != nil || len(r) != 2 || r[1] != (Repo{"schema", "main"}) {
		t.Fatalf("%v %v", r, err)
	}
	for _, bad := range []string{"", "nova-tools", "a:dev,a:main", "../x:dev"} {
		if _, err := ParseRepos(bad); err == nil {
			t.Errorf("ParseRepos(%q) accepted", bad)
		}
	}
	for s, want := range map[string]bool{
		"space:nova-bench/mirror/nova-tools.git": true, "https://forge.example.invalid/o/x.git": true,
		"file:///tmp/x.git": false, "/tmp/x.git": false, "+refs/heads/*:refs/heads/*": false, "refs/pull": false,
	} {
		if IsRemoteURL(s) != want {
			t.Errorf("IsRemoteURL(%q) != %v", s, want)
		}
	}
}
