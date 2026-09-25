package ci_test

// ci run stages the claimed head from the bench mirror, never from the
// forge's ssh url (2026-09-25: space and vision have no GitHub key and
// released every head with Permission denied (publickey), 128 and 111
// times). The mirror is a throwaway bare mirror of a throwaway bare origin
// with two commits; every git call of the runner goes through a wrapper that
// logs its argv, so the test counts the fetches.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
)

// mirrorFixture is an origin with commits old and new on dev, a bench mirror
// root holding <runRepo>.git (a mirror clone of the origin), and a git
// wrapper whose argv log counts the runner's git calls.
type mirrorFixture struct {
	*runFixture
	origin, work, mirrorRoot, gitBin, gitLog string
	old, new                                 string
}

func newMirrorFixture(t *testing.T) *mirrorFixture {
	t.Helper()
	f := &mirrorFixture{runFixture: newRunFixture(t)}
	// One check that proves the checkout is at the sha, so a staged head ends
	// green and leaves the pool.
	f.client.HSet(f.ctx, ci.ConfigKey(runRepo), "checks", "head")
	tmp := t.TempDir()
	f.origin = filepath.Join(tmp, "origin.git")
	git(t, tmp, "init", "-q", "--bare", "--initial-branch=dev", f.origin)
	f.work = filepath.Join(tmp, "work")
	git(t, tmp, "clone", "-q", f.origin, f.work)
	f.old = f.commit(t, "old")
	f.new = f.commit(t, "new")
	f.mirrorRoot = filepath.Join(tmp, "mirror")
	git(t, tmp, "clone", "-q", "--mirror", f.origin, filepath.Join(f.mirrorRoot, runRepo+".git"))
	f.gitLog = filepath.Join(tmp, "git.log")
	f.gitBin = filepath.Join(tmp, "git-wrap")
	wrap := "#!/bin/sh\necho \"$*\" >> '" + f.gitLog + "'\nexec git \"$@\"\n"
	if err := os.WriteFile(f.gitBin, []byte(wrap), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *mirrorFixture) commit(t *testing.T, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.work, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, f.work, "add", name)
	git(t, f.work, "commit", "-q", "-m", name)
	git(t, f.work, "push", "-q", "origin", "HEAD:dev")
	return git(t, f.work, "rev-parse", "HEAD")
}

func (f *mirrorFixture) runMirror(t *testing.T, bench, root string) (ci.RunResult, string, error) {
	t.Helper()
	var out bytes.Buffer
	res, err := ci.Run(f.ctx, f.st, ci.RunOptions{Bench: bench, Scratch: filepath.Join(f.root, "scratch"),
		ResultsRoot: filepath.Join(f.root, "results"), MirrorRoot: root, Git: f.gitBin,
		Lease: time.Minute, Timeout: 30 * time.Second, Out: &out})
	return res, out.String(), err
}

// fetches is how many `fetch` calls the runner made through the wrapper.
func (f *mirrorFixture) fetches(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(f.gitLog)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(" "+l+" ", " fetch ") {
			n++
		}
	}
	return n
}

func (f *mirrorFixture) request(t *testing.T, repo, sha string) {
	t.Helper()
	if r, err := ci.Request(f.ctx, f.st, ci.RequestRequest{Repo: repo, SHA: sha}); err != nil || r.Status != "CREATED" {
		t.Fatalf("request %s@%s = %v, %v", repo, sha[:8], r, err)
	}
}

// headLog is the head check's log: `git log -1 --format=%H` in the clone.
func (f *mirrorFixture) headLog(t *testing.T, sha string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, "results", "ci", runRepo, sha, "head.log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestCIRunStagesTheOlderShaFromTheMirrorWithoutNetwork(t *testing.T) {
	f := newMirrorFixture(t)
	// The mirror's origin goes nowhere: any fetch or forge read would fail.
	git(t, f.mirrorRoot, "-C", filepath.Join(f.mirrorRoot, runRepo+".git"), "remote", "set-url", "origin", "git@github.com:mas-bandwidth/nowhere.git")
	f.request(t, runRepo, f.old)
	res, out, err := f.runMirror(t, "space", f.mirrorRoot)
	if err != nil || !res.Claimed || res.Clone.From != "mirror" || res.Clone.Fetched {
		t.Fatalf("run = %+v, %v\n%s", res, err, out)
	}
	if !strings.Contains(out, "CLONE from=mirror sha="+f.old[:8]+" fetched=no ms=") {
		t.Fatalf("no CLONE line from the mirror:\n%s", out)
	}
	if got := f.headLog(t, f.old); got != f.old {
		t.Fatalf("clone HEAD = %q, want the older sha %s", got, f.old)
	}
	if n := f.fetches(t); n != 0 {
		t.Fatalf("%d fetches for a sha the mirror holds", n)
	}
	b, _ := os.ReadFile(f.gitLog)
	if strings.Contains(string(b), "github.com") {
		t.Fatalf("the runner named the forge:\n%s", b)
	}
}

func TestCIRunFetchesTheMirrorOnceForAnAbsentSha(t *testing.T) {
	f := newMirrorFixture(t)
	newer := f.commit(t, "newer") // in the origin, not yet in the mirror
	f.request(t, runRepo, newer)
	res, out, err := f.runMirror(t, "vision", f.mirrorRoot)
	if err != nil || !res.Claimed || !res.Clone.Fetched {
		t.Fatalf("run = %+v, %v\n%s", res, err, out)
	}
	if !strings.Contains(out, "CLONE from=mirror sha="+newer[:8]+" fetched=yes ms=") {
		t.Fatalf("no fetched CLONE line:\n%s", out)
	}
	if n := f.fetches(t); n != 1 {
		t.Fatalf("%d fetches, want exactly one", n)
	}
	if got := f.headLog(t, newer); got != newer {
		t.Fatalf("clone HEAD = %q, want %s", got, newer)
	}

	// A sha no fetch can bring: one fetch, then released with the why, and
	// the attempt counts toward the cap.
	absent := strings.Repeat("ab", 20)
	f.request(t, runRepo, absent)
	res, out, err = f.runMirror(t, "vision", f.mirrorRoot)
	if !errors.Is(err, ci.ErrBlocked) || res.Blocked != "sha not in mirror after fetch" {
		t.Fatalf("absent sha: res=%+v err=%v\n%s", res, err, out)
	}
	if n := f.fetches(t); n != 2 {
		t.Fatalf("%d fetches after the absent sha, want 2 (one each)", n)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, absent)).Val()
	if rec["attempt"] != "1" || rec["blocked"] != "sha not in mirror after fetch" || rec["ci"] != ci.SummaryPending {
		t.Fatalf("record after the absent sha: %v", rec)
	}
}

func TestCIRunMissingMirrorReleasesWithTheWhyAndSkipsTheBench(t *testing.T) {
	f := newMirrorFixture(t)
	empty := filepath.Join(t.TempDir(), "mirror")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	f.request(t, runRepo, f.old)
	res, out, err := f.runMirror(t, "space", empty)
	want := "no mirror at " + filepath.Join(empty, runRepo+".git") + "; run mirror-refresh"
	if !errors.Is(err, ci.ErrBlocked) || res.Blocked != want || !res.Clone.NoMirror {
		t.Fatalf("missing mirror: res=%+v err=%v\n%s", res, err, out)
	}
	if !strings.Contains(out, "CLONE from=none sha="+f.old[:8]+" fetched=no") {
		t.Fatalf("no CLONE line:\n%s", out)
	}
	rec := f.client.HGetAll(f.ctx, ci.RecordKey(runRepo, f.old)).Val()
	if rec["attempt"] != "0" || rec["blocked"] != want || rec["ci"] != ci.SummaryPending {
		t.Fatalf("record after a missing mirror (a bench defect is not an attempt): %v", rec)
	}
	if !f.client.SIsMember(f.ctx, "ci:nomirror:space", runRepo).Val() {
		t.Fatal("the bench is not marked for the repo")
	}
	// The marked bench leaves the head in the pool; another bench takes it.
	if res, out, err := f.runMirror(t, "space", empty); err != nil || res.Claimed {
		t.Fatalf("marked bench claimed again: %+v %v\n%s", res, err, out)
	}
	if n := f.client.ZCard(f.ctx, ci.PoolKey).Val(); n != 1 {
		t.Fatalf("pool has %d, want the head still there", n)
	}
	// Once its mirror exists, the bench claims the repo again.
	git(t, empty, "clone", "-q", "--mirror", f.origin, filepath.Join(empty, runRepo+".git"))
	res, out, err = f.runMirror(t, "space", empty)
	if err != nil || !res.Claimed || res.Attempt != 1 || res.Clone.From != "mirror" {
		t.Fatalf("after mirror-refresh: %+v %v\n%s", res, err, out)
	}
	if f.client.SIsMember(f.ctx, "ci:nomirror:space", runRepo).Val() {
		t.Fatal("the mark outlived the mirror")
	}
}
