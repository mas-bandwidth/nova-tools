package land_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
)

// mergeFixture (#3612) is a repository where the feature branch took a
// merge of dev: a is the head a reader scored, b the merge of a moved dev
// into it (the PR's diff against its base is byte-identical), c one more
// edit on top of b (the diff changed).
type mergeFixture struct {
	dir     string
	a, b, c string
}

func newMergeFixture(t *testing.T) mergeFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t", "GIT_AUTHOR_DATE=2026-09-25T00:00:00Z", "GIT_COMMITTER_DATE=2026-09-25T00:00:00Z")
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
	git("merge", "-q", "--no-ff", "--no-edit", "dev")
	b := git("rev-parse", "HEAD")
	write("b.txt", "b changed twice\n")
	git("commit", "-q", "-am", "feature: b again")
	c := git("rev-parse", "HEAD")
	git("checkout", "-q", "dev")
	return mergeFixture{dir: dir, a: a, b: b, c: c}
}

const (
	mergeS    = "s-3612"
	mergeRepo = "nova-tools"
	mergeUnit = "gh/mas-bandwidth/nova-tools/3612"
)

// mergeStore: the unit at head (authored by johnny, one read required),
// emma's SCORE 10 recorded at a with the digest a read records.
func mergeStore(t *testing.T, f mergeFixture, head string) (context.Context, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = c.Close() })
	c.SAdd(ctx, "friends", "emma", "johnny")
	c.HSet(ctx, land.UnitKey(mergeS, mergeUnit), "repo", mergeRepo, "base", "dev", "head", head, "pr", "3612",
		"branch", "feature", "author", "johnny")
	c.Set(ctx, land.PRUnitKey(mergeS, mergeRepo, 3612), mergeUnit, 0)
	c.SAdd(ctx, land.ReadersKey(mergeS, mergeUnit), "emma")
	c.HSet(ctx, land.PolicyKey(mergeRepo, "dev"), "readers", "1", "land_bar", "8")
	c.HSet(ctx, land.ReadKey(mergeS, mergeUnit, "emma"), "seq", "5", "head", f.a, "verdict", "APPROVE", "score", "10", "kind", "", "at", "1")
	d, err := line.DiffDigest(ctx, f.dir, "dev", f.a)
	if err != nil {
		t.Fatal(err)
	}
	if err := line.Record(ctx, c, land.UnitKey(mergeS, mergeUnit), f.a, d); err != nil {
		t.Fatal(err)
	}
	return ctx, c
}

func whyReads(t *testing.T, ctx context.Context, c *redis.Client) string {
	t.Helper()
	u, err := land.LoadUnit(ctx, c, mergeS, mergeUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range land.Why(u, time.Now()) {
		if strings.HasPrefix(l, "reads ") {
			return l
		}
	}
	t.Fatal("why printed no reads line")
	return ""
}

// TestReadCarriesAcrossIdenticalDiffMerge: a merge of dev that leaves the
// PR's diff against its base identical carries emma's read to the new head;
// `why` counts it and prints carried_from=<h8>.
func TestReadCarriesAcrossIdenticalDiffMerge(t *testing.T) {
	f := newMergeFixture(t)
	ctx, c := mergeStore(t, f, f.b)
	if got := whyReads(t, ctx, c); !strings.HasPrefix(got, "reads 0/1") {
		t.Fatalf("before the carry: %q", got)
	}
	id := land.ID{Repo: mergeRepo, N: 3612}
	res, err := line.Carry(ctx, c, mergeS, id, f.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != line.Carried || strings.Join(res.Who, ",") != "emma" {
		t.Fatalf("carry across the dev merge: %s", res.Line(id))
	}
	if r := c.HGetAll(ctx, land.ReadKey(mergeS, mergeUnit, "emma")).Val(); r["head"] != f.b || r["carried_from"] != f.a {
		t.Fatalf("read record %v", r)
	}
	want := "reads 1/1 (emma 10 @" + f.b[:8] + " carried_from=" + f.a[:8] + ")"
	if got := whyReads(t, ctx, c); got != want {
		t.Fatalf("why %q, want %q", got, want)
	}
}

// TestReadDoesNotCarryAcrossChangedDiff: the merge of dev plus one more edit
// changes the diff; the carry refuses naming the file, the read stays at the
// head it was typed at, and `why` counts nothing at the new head.
func TestReadDoesNotCarryAcrossChangedDiff(t *testing.T) {
	f := newMergeFixture(t)
	ctx, c := mergeStore(t, f, f.c)
	id := land.ID{Repo: mergeRepo, N: 3612}
	res, err := line.Carry(ctx, c, mergeS, id, f.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != line.Refused || strings.Join(res.Changed, ",") != "b.txt" {
		t.Fatalf("carry across a changed diff: %s", res.Line(id))
	}
	if r := c.HGetAll(ctx, land.ReadKey(mergeS, mergeUnit, "emma")).Val(); r["head"] != f.a || r["carried_from"] != "" {
		t.Fatalf("read record moved on a refusal: %v", r)
	}
	if got := whyReads(t, ctx, c); !strings.HasPrefix(got, "reads 0/1") || strings.Contains(got, "carried_from") {
		t.Fatalf("why %q, want reads 0/1 with nothing carried", got)
	}
}
