//go:build functional

package main

// Cold read of #4399 at 7c119d2db (rowan-opus): --checkout on card end and
// friend done with a real throwaway checkout, as a sequence: the retired
// --repo spelling, a directory that is no checkout, a checkout at the wrong
// head, a red gate, a green gate, the end, and the second end ALREADY; every
// refused step leaves the copy working and writes nothing.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestRead4399CheckoutSequence(t *testing.T) {
	t.Parallel()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	me := "rowan"
	friend, _ := taskcard.ParseConsumer("friend:" + me)
	c.HSet(ctx, friend.DesiredKey(), "slots", "1")

	co := filepath.Join(t.TempDir(), "co")
	git := func(args ...string) string {
		b, err := exec.Command("git", append([]string{"-C", co, "-c", "user.email=f@example.com", "-c", "user.name=f", "-c", "core.hooksPath=/dev/null"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	put := func(p, body string) {
		full := filepath.Join(co, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(co, 0o755)
	git("init", "-q")
	put("go.mod", "module example.com/m\n\ngo 1.26\n")
	put("x/x.go", "package x\n\nfunc Add(a, b int) int { return a - b }\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")
	put("x/x.go", "package x\n\nfunc Add(a, b int) int { return a + b }\n")
	put("x/x_test.go", "package x\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(\"2+2\")\n\t}\n}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "work")
	head := git("rev-parse", "HEAD")

	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "k1", Where: "waiting", Stream: "swarm: cards", Kind: "build",
		Title: "k1", Repo: "mas-bandwidth/nova-tools", Origin: "issue:nova-tools#4399", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", base, "paths", "x/x.go x/x_test.go", "test", "./x TestMissing",
			"done_when", "go test ./x -run TestAdd passes", "body", "b"}}); err != nil {
		t.Fatal(err)
	}
	if d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: friend, N: 1, By: "reconciler"}); err != nil || len(d) != 1 {
		t.Fatalf("deal: %v %v", d, err)
	}
	w, err := taskcard.Work(ctx, c, friend, "friend:"+me, 1, false)
	if err != nil || len(w.IDs) != 1 {
		t.Fatalf("work: %v %v", w, err)
	}
	cp := w.IDs[0]
	cardEnd := func(args ...string) (int, string) {
		var o, e bytes.Buffer
		code := runCard(ctx, append([]string{"end", "--redis", addr, "--ids", cp, "--ok", "--pr", "nova-tools#4502", "--head", head}, args...), &o, &e)
		return code, o.String() + e.String()
	}
	still := func(step string) {
		t.Helper()
		if where := c.HGet(ctx, taskcard.Key(cp), "where").Val(); where != "working" {
			t.Fatalf("%s: copy is %s, want working", step, where)
		}
		if wt := git("worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
			t.Fatalf("%s: a worktree is left:\n%s", step, wt)
		}
	}
	step := func(name string, wantCode int, want string, args ...string) {
		t.Helper()
		code, out := cardEnd(args...)
		t.Logf("%s -> %d %s", name, code, strings.TrimSpace(out))
		if code != wantCode || !strings.Contains(out, want) {
			t.Fatalf("%s: exit %d, want %d with %q:\n%s", name, code, wantCode, want, out)
		}
		still(name)
	}
	step("retired --repo <dir>", 2, "--checkout", "--repo", co)
	step("no checkout", 1, "wants --checkout")
	notGit := t.TempDir()
	step("not a git dir", 1, "not a git checkout", "--checkout", notGit)
	git("checkout", "-q", base)
	step("checkout at base", 1, "not --head", "--checkout", co)
	git("checkout", "-q", head)
	step("red gate", 1, "test-not-green", "--checkout", co)
	c.HSet(ctx, taskcard.Key(cp), "test", "./x TestAdd")
	// green: friend done ends it (card end wants the PR record)
	var o, e bytes.Buffer
	args := []string{"done", "--redis", addr, "--as", "friend:" + me, "--ids", cp, "--ok", "--pr", "nova-tools#4502", "--head", head, "--checkout", co}
	code := runFriend(ctx, args, &o, &e)
	t.Logf("friend done green -> %d %s%s", code, o.String(), e.String())
	if code != 0 || !strings.Contains(o.String(), "ENDED "+cp) {
		t.Fatalf("green end: %d %s%s", code, o.String(), e.String())
	}
	// the second end on the same head, checkout at head: ALREADY
	o.Reset()
	e.Reset()
	code = runFriend(ctx, args, &o, &e)
	t.Logf("friend done again at head -> %d %s%s", code, o.String(), e.String())
	if code != 0 || !strings.Contains(o.String(), "ALREADY "+cp) {
		t.Fatalf("second end: %d %s%s", code, o.String(), e.String())
	}
	// observation, not asserted: with the checkout moved off head, the resend
	// of an ended copy runs the gate before ALREADY and is refused
	git("checkout", "-q", base)
	o.Reset()
	e.Reset()
	code = runFriend(ctx, args, &o, &e)
	t.Logf("friend done again, checkout at base -> %d %s%s", code, o.String(), e.String())
}
