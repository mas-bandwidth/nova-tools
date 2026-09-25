package consume

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// carryRepo is a local repository standing in for the bench mirror: a is
// the PR head on the old dev, b the same change rebased onto a moved dev
// (an identical diff), c one more edit on top of b (a changed diff).
type carryRepo struct {
	dir     string
	a, b, c string
}

func newCarryRepo(t *testing.T) carryRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_AUTHOR_DATE=2026-09-25T00:00:00Z", "GIT_COMMITTER_DATE=2026-09-25T00:00:00Z", "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
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
	return carryRepo{dir: dir, a: a, b: b, c: c}
}

const (
	carrySprint = "s-carry-3806"
	carryRepoN  = "nova-tools"
	carryPR     = 7
	carryUnit   = "gh/mas-bandwidth/nova-tools/7"
	carryLabel  = "card-carry"
)

// seedCarry: emma typed APPROVE 10 at a (read record, disp row, her done
// review task), rowan is the author, the card waits in review-ready, CI is
// OK at the new head, and the PR record and the unit already stand at the
// new head, as ns_pr_head and ns_unit_head leave them. digest records the
// read-time digest at a the way `read digest` does.
func seedCarry(t *testing.T, ctx context.Context, client *redis.Client, r carryRepo, newHead string, digest bool) {
	t.Helper()
	S := carrySprint
	pipe := client.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.HSet(ctx, "s:"+S+":policy", "readers", "1", "readers_security", "2")
	pipe.SAdd(ctx, "friends", "emma", "stella", "rowan")
	pipe.SAdd(ctx, "s:"+S+":prs", "nova-tools#7")
	pipe.HSet(ctx, "s:"+S+":pr:nova-tools:7", "head", newHead, "author", "rowan", "land_bar", "8", "state", "reading")
	pipe.HSet(ctx, "s:"+S+":card:"+carryLabel, "repo", carryRepoN, "pr", "7", "paths", "b.txt",
		"author", "rowan", "state", "review-ready", "attempt", "1")
	pipe.SAdd(ctx, "s:"+S+":idx:card:review-ready", carryLabel)
	pipe.HSet(ctx, land.UnitKey(S, carryUnit), "repo", carryRepoN, "base", "dev", "head", newHead, "pr", "7", "author", "rowan")
	pipe.Set(ctx, land.PRUnitKey(S, carryRepoN, carryPR), carryUnit, 0)
	pipe.HSet(ctx, land.ReadKey(S, carryUnit, "emma"), "seq", "5", "head", r.a, "verdict", "APPROVE", "score", "10", "kind", "", "at", "1")
	pipe.HSet(ctx, "s:"+S+":disp:nova-tools:7", "emma@"+r.a, "APPROVE 10 https://example.com/r 1")
	tA := task.ReviewID(carryRepoN, carryPR, r.a, "emma")
	pipe.HSet(ctx, "s:"+S+":task:"+tA, "state", "done", "kind", "review", "repo", carryRepoN, "pr", "7", "head", r.a)
	seedCI(ctx, pipe, carryRepoN, newHead)
	pipe.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + S + ":log", Values: []any{
		"kind", "pr head", "repo", carryRepoN, "pr", "7", "head", newHead, "prev", r.a, "source", "ls-remote", "at", "1"}})
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if digest {
		d, err := line.DiffDigest(ctx, r.dir, "dev", r.a)
		if err != nil {
			t.Fatal(err)
		}
		if err := line.Record(ctx, client, land.UnitKey(S, carryUnit), r.a, d); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHeadChangeCarriesRead is #3806's DONE-WHEN on a throwaway server: the
// pr-to-read head-change path runs the carry, so a rebased head (identical
// diff) keeps emma's read: one CARRY CARRIED line, the read record and disp
// row at the new head with carried_from, no re-read queued for emma, and the
// card goes land-ready on the carried read. The digest at the old head is the
// recorded one (digest=true) or computed from the mirror (digest=false).
func TestHeadChangeCarriesRead(t *testing.T) {
	r := newCarryRepo(t)
	for _, tc := range []struct {
		name   string
		digest bool
	}{{"recorded-digest", true}, {"computed-digest", false}} {
		t.Run(tc.name, func(t *testing.T) {
			st, client := initTestRedis(t)
			ctx := context.Background()
			seedCarry(t, ctx, client, r, r.b, tc.digest)
			var out bytes.Buffer
			p := &PRRead{Store: st, Sprint: carrySprint, Consumer: "t", Instance: "t", Actor: "pr-to-read",
				Out: &out, Mirror: func(string) string { return r.dir }}
			if err := p.Once(ctx); err != nil {
				t.Fatalf("Once: %v\n%s", err, out.String())
			}
			want := "CARRY CARRIED nova-tools#7 " + r.a[:8] + "->" + r.b[:8] + " reads=1 who=emma"
			if !strings.Contains(out.String(), want+"\n") {
				t.Fatalf("output lacks %q:\n%s", want, out.String())
			}
			rec := client.HGetAll(ctx, land.ReadKey(carrySprint, carryUnit, "emma")).Val()
			if rec["head"] != r.b || rec["carried_from"] != r.a || rec["verdict"] != "APPROVE" || rec["score"] != "10" {
				t.Fatalf("read record %v", rec)
			}
			if v := client.HGet(ctx, "s:"+carrySprint+":disp:nova-tools:7", "emma@"+r.b).Val(); v == "" {
				t.Fatal("no disp row at the new head")
			}
			if n := client.Exists(ctx, "s:"+carrySprint+":task:"+task.ReviewID(carryRepoN, carryPR, r.b, "emma")).Val(); n != 0 {
				t.Fatal("a re-read was queued for emma at the new head")
			}
			if v := client.HGet(ctx, land.UnitKey(carrySprint, carryUnit), line.FieldHead).Val(); v != r.b {
				t.Fatalf("diff_head %q, want %s", v, r.b)
			}
			if s := client.HGet(ctx, "s:"+carrySprint+":card:"+carryLabel, "state").Val(); s != "land-ready" {
				t.Fatalf("card state %q, want land-ready (the carried read counts)\n%s", s, out.String())
			}
		})
	}
}

// TestHeadChangeRefusesChangedDiff: a head with one more edit is a changed
// diff: CARRY REFUSED names the file, the read stays at the old head, emma
// gets her re-read at the new head, and the card stays review-ready.
func TestHeadChangeRefusesChangedDiff(t *testing.T) {
	r := newCarryRepo(t)
	st, client := initTestRedis(t)
	ctx := context.Background()
	seedCarry(t, ctx, client, r, r.c, true)
	var out bytes.Buffer
	p := &PRRead{Store: st, Sprint: carrySprint, Consumer: "t", Instance: "t", Actor: "pr-to-read",
		Out: &out, Mirror: func(string) string { return r.dir }}
	if err := p.Once(ctx); err != nil {
		t.Fatalf("Once: %v\n%s", err, out.String())
	}
	want := "CARRY REFUSED nova-tools#7 " + r.a[:8] + "->" + r.c[:8] + " changed=b.txt"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
	if h := client.HGet(ctx, land.ReadKey(carrySprint, carryUnit, "emma"), "head").Val(); h != r.a {
		t.Fatalf("read head %q moved on a changed diff", h)
	}
	if s := client.HGet(ctx, "s:"+carrySprint+":task:"+task.ReviewID(carryRepoN, carryPR, r.c, "emma"), "state").Val(); s != "open" {
		t.Fatalf("emma's re-read at the new head state %q, want open", s)
	}
	if s := client.HGet(ctx, "s:"+carrySprint+":card:"+carryLabel, "state").Val(); s != "review-ready" {
		t.Fatalf("card state %q, want review-ready", s)
	}
}

// TestHeadChangeNoMirrorQueuesReRead: without a mirror the carry is skipped
// with its reason on one line and the re-read is queued as before.
func TestHeadChangeNoMirrorQueuesReRead(t *testing.T) {
	r := newCarryRepo(t)
	st, client := initTestRedis(t)
	ctx := context.Background()
	seedCarry(t, ctx, client, r, r.b, true)
	var out bytes.Buffer
	p := &PRRead{Store: st, Sprint: carrySprint, Consumer: "t", Instance: "t", Actor: "pr-to-read",
		Out: &out, Mirror: func(string) string { return "" }}
	if err := p.Once(ctx); err != nil {
		t.Fatalf("Once: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "CARRY SKIPPED nova-tools#7 why=no mirror of nova-tools\n") {
		t.Fatalf("no SKIPPED line:\n%s", out.String())
	}
	if s := client.HGet(ctx, "s:"+carrySprint+":task:"+task.ReviewID(carryRepoN, carryPR, r.b, "emma"), "state").Val(); s != "open" {
		t.Fatalf("emma's re-read state %q, want open", s)
	}
}
