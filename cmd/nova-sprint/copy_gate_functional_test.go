//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestEveryOkDoorRunsTheSpecGate is the nova-tools#4401 read's DOORS: every
// CLI door that ends a code copy ok with a PR runs the spec gate (#4313)
// in the checkout at --head, or refuses. task done --pr of a code copy has
// no checkout and is refused, naming the door that runs it; card end --ok
// --pr with no --repo is refused, and with --repo is held to TEST (a red
// refuses before the PR is recorded or the copy ends); friend done --ok --pr
// of a fix copy whose --test is `none cosmetic` is refused (e); the base
// worktree is removed after a red and a pass, and the PR's own checkout
// stays at its head, clean (f); twenty friend done --ok --pr at once on the
// same head end the copy exactly once, the other nineteen ALREADY (g).
func TestEveryOkDoorRunsTheSpecGate(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal("go is not on PATH: the gate runs go test")
	}
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	me := os.Getenv(seatEnv)
	if me == "" {
		me = "rowan"
	}
	friend, _ := taskcard.ParseConsumer("friend:" + me)
	c.HSet(ctx, friend.DesiredKey(), "slots", "2")

	// the checkout: Add wrong at base; the head fixes it and carries TestAdd
	checkout := filepath.Join(t.TempDir(), "g1")
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", checkout, "-c", "user.email=f@example.com", "-c", "user.name=f", "-c", "core.hooksPath=/dev/null"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	write := func(files map[string]string) {
		for p, body := range files {
			full := filepath.Join(checkout, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
	}
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	write(map[string]string{"go.mod": "module example.com/m\n\ngo 1.26\n", "x/x.go": "package x\n\nfunc Add(a, b int) int { return a - b }\n"})
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	write(map[string]string{"x/x.go": "package x\n\nfunc Add(a, b int) int { return a + b }\n",
		"x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(\"2+2\")\n\t}\n}\n"})
	git("commit", "-q", "-m", "the work")
	head := git("rev-parse", "HEAD")
	onlyTheCheckout := func(step string) {
		t.Helper()
		if wt := git("worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
			t.Fatalf("%s: a base worktree is left:\n%s", step, wt)
		}
		if at, st := git("rev-parse", "HEAD"), git("status", "--porcelain"); at != head || st != "" {
			t.Fatalf("%s: the PR's checkout is at %s with %q, want %s clean", step, at, st, head)
		}
	}

	for _, id := range []string{"g1", "g2"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: "swarm: cards", Kind: "build",
			Title: "gate " + id, Repo: "mas-bandwidth/nova-tools", Origin: "issue:nova-tools#4313", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", base, "paths", "x/x.go x/x_test.go", "test", "./x TestAdd",
				"done_when", "go test ./x -run TestAdd passes", "body", "the issue"}}); err != nil {
			t.Fatal(err)
		}
	}
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: friend, N: 2, By: "reconciler"}); err != nil || len(d) != 2 {
		t.Fatalf("deal: %v %v", d, err)
	}
	w, err := taskcard.Work(ctx, c, friend, "friend:"+me, 2, false)
	if err != nil || len(w.IDs) != 2 {
		t.Fatalf("work: %v %v", w, err)
	}
	copyOf := map[string]string{}
	for _, cid := range w.IDs {
		copyOf[c.HGet(ctx, taskcard.Key(cid), "primary").Val()] = cid
	}
	one, two := copyOf["g1"], copyOf["g2"]
	cardEnd := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := runCard(ctx, append([]string{"end", "--redis", addr}, args...), &out, &errOut)
		return code, out.String() + errOut.String()
	}
	still := func(step, cid string) {
		t.Helper()
		if where := c.HGet(ctx, taskcard.Key(cid), "where").Val(); where != "working" || c.Exists(ctx, "pr:nova-tools:4501").Val() != 0 {
			t.Fatalf("%s: the copy is %s or the PR is recorded; a refused door writes nothing", step, where)
		}
	}

	// task done --pr of a code copy: no checkout, refused, naming friend done
	var out, errOut bytes.Buffer
	if code := runTaskCard(ctx, "done", []string{"--redis", addr, "--id", one, "--pr", "4501", "--head", head, "--actor", "friend:" + me, "--evidence", "the work"}, &out, &errOut); code != 1 ||
		!strings.Contains(out.String(), "why=\"no-test task done --pr of a code copy skips the spec gate: nova-sprint friend done") {
		t.Fatalf("task done --pr: exit %d %s%s", code, out.String(), errOut.String())
	}
	still("task done --pr", one)
	// card end --ok --pr: no --repo is refused; a red TEST is refused
	if code, out := cardEnd("--id", one, "--ok", "--pr", "nova-tools#4501", "--head", head); code != 1 || !strings.Contains(out, "CARD END REFUSED ids="+one+" why=\"no-test --ok --pr wants --repo") {
		t.Fatalf("card end with no --repo: exit %d %s", code, out)
	}
	still("card end with no --repo", one)
	c.HSet(ctx, taskcard.Key(one), "test", "./x TestMissing")
	if code, out := cardEnd("--id", one, "--ok", "--pr", "nova-tools#4501", "--head", head, "--repo", checkout); code != 1 || !strings.Contains(out, "why=\"test-not-green ") {
		t.Fatalf("card end on a red gate: exit %d %s", code, out)
	}
	still("card end on a red gate", one)
	onlyTheCheckout("after a red gate")
	c.HSet(ctx, taskcard.Key(one), "test", "./x TestAdd")
	// the gate passes; card end then wants the PR record, as before the gate
	if code, out := cardEnd("--id", one, "--ok", "--pr", "nova-tools#4501", "--head", head, "--repo", checkout); code != 1 ||
		!strings.Contains(out, "GATE TEST ./x TestAdd at base ") || !strings.Contains(out, "why=\"NOPR ") {
		t.Fatalf("card end past a green gate: exit %d %s", code, out)
	}
	onlyTheCheckout("after a green gate")

	// (e) a fix copy's finding test is never none
	fix := map[string]any{"leg": "fix", "head": base}
	c.HSet(ctx, taskcard.Key(two), fix)
	if code, out, _ := runFriendDoneCLI(ctx, addr, me, two, head, checkout, "none cosmetic"); code != 1 || !strings.Contains(out, "a fix's finding test is never none (cosmetic)") {
		t.Fatalf("a fix with --test none cosmetic: exit %d %s", code, out)
	}
	c.HSet(ctx, taskcard.Key(two), "leg", "work", "head", "")

	// (g) twenty friend done --ok --pr at once on the same head: one end.
	// The race is on the end, so its copy's diff is one text file and its
	// TEST none with a why: each gate is CI's answer, no go test twenty
	// times over.
	race := filepath.Join(t.TempDir(), "g2")
	rgit := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", race, "-c", "user.email=f@example.com", "-c", "user.name=f", "-c", "core.hooksPath=/dev/null"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	if err := os.MkdirAll(race, 0o755); err != nil {
		t.Fatal(err)
	}
	rgit("init", "-q")
	rgit("commit", "-q", "--allow-empty", "-m", "base")
	raceBase := rgit("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(race, "notes.txt"), []byte("the work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rgit("add", "notes.txt")
	rgit("commit", "-q", "-m", "the work")
	raceHead := rgit("rev-parse", "HEAD")
	c.HSet(ctx, taskcard.Key(two), "base_sha", raceBase, "test", "none the race probes one end, not a test")
	const n = 20
	outs := make([]string, n)
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], outs[i], _ = runFriendDoneCLI(ctx, addr, me, two, raceHead, race, "")
		}(i)
	}
	wg.Wait()
	ended, already := 0, 0
	for i, o := range outs {
		switch {
		case codes[i] == 0 && strings.Contains(o, "\nENDED "+two+" primary=g2 from=working to=review"):
			ended++
		case codes[i] == 0 && strings.Contains(o, "\nALREADY "+two+" "):
			already++
		default:
			t.Errorf("gate %d: exit %d %s", i, codes[i], o)
		}
	}
	if ended != 1 || already != n-1 {
		t.Fatalf("%d ended and %d already of %d, want exactly one end", ended, already, n)
	}
	if where, ok := c.HGet(ctx, taskcard.Key(two), "where").Val(), c.ZCard(ctx, friend.Key("ok")).Val(); where != "ok" || ok != 1 {
		t.Fatalf("after the race the copy is %s and %d copies are ok, want ok and 1", where, ok)
	}
	if wt := rgit("worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
		t.Fatalf("after twenty gates a worktree is left:\n%s", wt)
	}
	onlyTheCheckout("at the end")
}

// runFriendDoneCLI is friend done --ok --pr nova-tools#4501 for copy id at
// head, the gate in checkout, test the fix's --test ("" for none given).
func runFriendDoneCLI(ctx context.Context, addr, me, id, head, checkout, test string) (int, string, string) {
	args := []string{"done", "--redis", addr, "--as", "friend:" + me, "--id", id, "--ok", "--pr", "nova-tools#4501", "--head", head, "--repo", checkout}
	if test != "" {
		args = append(args, "--test", test)
	}
	var out, errOut bytes.Buffer
	code := runFriend(ctx, args, &out, &errOut)
	return code, out.String(), errOut.String()
}
