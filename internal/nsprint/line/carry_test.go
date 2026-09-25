package line_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
)

// repoFixture is a local repository with dev and a feature branch: A is the
// feature's head on the old dev, B the same change rebased onto a moved dev
// (an identical diff), C one more edit on top of B (a changed diff).
type repoFixture struct {
	dir     string
	a, b, c string
}

func newRepoFixture(t *testing.T) repoFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_AUTHOR_DATE=2026-09-24T00:00:00Z", "GIT_COMMITTER_DATE=2026-09-24T00:00:00Z")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "dev")
	write("a.txt", "a\n")
	write("b.txt", "b\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", "feature")
	write("b.txt", "b changed\n")
	git("commit", "-q", "-am", "feature: b")
	a := git("rev-parse", "HEAD")
	git("checkout", "-q", "dev")
	write("c.txt", "c\n")
	git("add", "-A")
	git("commit", "-q", "-m", "dev moves")
	git("checkout", "-q", "feature")
	git("rebase", "-q", "dev")
	b := git("rev-parse", "HEAD")
	write("b.txt", "b changed twice\n")
	git("commit", "-q", "-am", "feature: b again")
	c := git("rev-parse", "HEAD")
	git("checkout", "-q", "dev")
	return repoFixture{dir: dir, a: a, b: b, c: c}
}

const (
	sprint = "s-carry"
	repo   = "nova-tools"
	unit   = "gh/mas-bandwidth/nova-tools/7"
)

var id = land.ID{Repo: repo, N: 7}

// storeFixture: the unit at head, one emma APPROVE 10 typed at A with its
// disp row, and the digest recorded at A the way a read does.
func storeFixture(t *testing.T, f repoFixture, head string) (context.Context, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	c.SAdd(ctx, "friends", "emma", "rowan")
	c.HSet(ctx, land.UnitKey(sprint, unit), "repo", repo, "base", "dev", "head", head, "pr", "7", "branch", "feature")
	c.Set(ctx, land.PRUnitKey(sprint, repo, 7), unit, 0)
	c.HSet(ctx, land.ReadKey(sprint, unit, "emma"), "seq", "5", "head", f.a, "verdict", "APPROVE", "score", "10", "kind", "", "at", "1")
	c.HSet(ctx, "s:"+sprint+":disp:"+repo+":7", "emma@"+f.a, "APPROVE 10 url 1")
	d, err := line.DiffDigest(ctx, f.dir, "dev", f.a)
	if err != nil {
		t.Fatal(err)
	}
	if err := line.Record(ctx, c, land.UnitKey(sprint, unit), f.a, d); err != nil {
		t.Fatal(err)
	}
	return ctx, c
}

// TestCarryIdenticalDiff: a rebase that leaves the diff byte-identical
// carries emma's line to the new head as a record with a carried_from
// receipt; the disp row follows; a second carry is NOTHING.
func TestCarryIdenticalDiff(t *testing.T) {
	f := newRepoFixture(t)
	ctx, c := storeFixture(t, f, f.b)
	res, err := line.Carry(ctx, c, sprint, id, f.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != line.Carried || res.From != f.a || res.To != f.b || strings.Join(res.Who, ",") != "emma" {
		t.Fatalf("carry: %+v", res)
	}
	want := "CARRIED nova-tools#7 " + f.a[:8] + "->" + f.b[:8] + " reads=1 who=emma"
	if got := res.Line(id); got != want {
		t.Fatalf("line %q, want %q", got, want)
	}
	r := c.HGetAll(ctx, land.ReadKey(sprint, unit, "emma")).Val()
	if r["head"] != f.b || r["carried_from"] != f.a || r["verdict"] != "APPROVE" || r["score"] != "10" || r["carried_at"] == "" {
		t.Fatalf("read record %v", r)
	}
	if v := c.HGet(ctx, "s:"+sprint+":disp:"+repo+":7", "emma@"+f.b).Val(); v != "APPROVE 10 url 1" {
		t.Fatalf("disp row at new head %q", v)
	}
	if v := c.HGet(ctx, land.UnitKey(sprint, unit), line.FieldHead).Val(); v != f.b {
		t.Fatalf("diff_head %q, want %s", v, f.b)
	}
	res, err = line.Carry(ctx, c, sprint, id, f.dir, "")
	if err != nil || res.Outcome != line.Nothing {
		t.Fatalf("second carry: %+v %v", res, err)
	}
}

// TestCarryRefusesChangedFiles: one more edit on top of the rebased head is
// a changed diff; the carry refuses naming the file and writes nothing.
func TestCarryRefusesChangedFiles(t *testing.T) {
	f := newRepoFixture(t)
	ctx, c := storeFixture(t, f, f.c)
	res, err := line.Carry(ctx, c, sprint, id, f.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != line.Refused || strings.Join(res.Changed, ",") != "b.txt" {
		t.Fatalf("carry: %+v", res)
	}
	if !strings.Contains(res.Line(id), "REFUSED nova-tools#7 "+f.a[:8]+"->"+f.c[:8]+" changed=b.txt remedy=re-read at "+f.c[:8]) {
		t.Fatalf("line %q", res.Line(id))
	}
	r := c.HGetAll(ctx, land.ReadKey(sprint, unit, "emma")).Val()
	if r["head"] != f.a || r["carried_from"] != "" {
		t.Fatalf("read record moved on a refusal: %v", r)
	}
	if c.HExists(ctx, "s:"+sprint+":disp:"+repo+":7", "emma@"+f.c).Val() {
		t.Fatal("disp row written on a refusal")
	}
}

// TestLanderReadsCarriedLine: the lander's unit load sees the carried line
// at the unit's head, with CarriedFrom set, so it counts as a read.
func TestLanderReadsCarriedLine(t *testing.T) {
	f := newRepoFixture(t)
	ctx, c := storeFixture(t, f, f.b)
	if _, err := line.Carry(ctx, c, sprint, id, f.dir, ""); err != nil {
		t.Fatal(err)
	}
	u, err := land.LoadUnit(ctx, c, sprint, unit)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Reads) != 1 {
		t.Fatalf("reads %+v", u.Reads)
	}
	r := u.Reads[0]
	if r.Friend != "emma" || r.Head != f.b || r.CarriedFrom != f.a || r.Verdict != "APPROVE" || r.Score != 10 {
		t.Fatalf("read %+v", r)
	}
}

// TestCarryNeedsARecordedDigest: a unit read before digests were recorded
// is refused with the remedy, never carried on faith.
func TestCarryNeedsARecordedDigest(t *testing.T) {
	f := newRepoFixture(t)
	ctx, c := storeFixture(t, f, f.b)
	c.HDel(ctx, land.UnitKey(sprint, unit), line.FieldSHA, line.FieldHead, line.FieldFiles)
	_, err := line.Carry(ctx, c, sprint, id, f.dir, "")
	if err == nil || !strings.Contains(err.Error(), "read digest") {
		t.Fatalf("err %v", err)
	}
}

// TestDigestOfSplitsSections: the per-file digests are the sections, and
// Changed names the differing one.
func TestDigestOfSplitsSections(t *testing.T) {
	one := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"
	two := "diff --git a/y b/y\n--- a/y\n+++ b/y\n@@ -1 +1 @@\n-c\n+d\n"
	d1 := line.DigestOf([]byte(one + two))
	d2 := line.DigestOf([]byte(one + strings.Replace(two, "+d", "+e", 1)))
	if len(d1.Files) != 2 || d1.Files["x"] != d2.Files["x"] || d1.Files["y"] == d2.Files["y"] || d1.SHA256 == d2.SHA256 {
		t.Fatalf("d1 %+v d2 %+v", d1, d2)
	}
	if got := line.Changed(d1, d2); strings.Join(got, ",") != "y" {
		t.Fatalf("changed %v", got)
	}
	if empty := line.DigestOf(nil); len(empty.Files) != 0 || empty.SHA256 == "" {
		t.Fatalf("empty %+v", empty)
	}
}

// TestHasCommit: HasCommit reports true for an existing commit and false for
// a missing commit, non-existent directory, or empty SHA.
func TestHasCommit(t *testing.T) {
	f := newRepoFixture(t)
	ctx := context.Background()
	if !line.HasCommit(ctx, f.dir, f.a) {
		t.Fatalf("HasCommit(%s) = false, want true", f.a)
	}
	if line.HasCommit(ctx, f.dir, "0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("HasCommit with bogus sha = true, want false")
	}
	if line.HasCommit(ctx, f.dir, "") {
		t.Fatal("HasCommit with empty sha = true, want false")
	}
	if line.HasCommit(ctx, "/no/such/dir", f.a) {
		t.Fatal("HasCommit with bad dir = true, want false")
	}
}
