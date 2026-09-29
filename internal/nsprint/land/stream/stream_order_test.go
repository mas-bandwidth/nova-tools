package stream

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=OrderTest", "-c", "user.email=ordertest@example.com", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=dev"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRunSkipsDependencyNotInStreamBranch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	bare := filepath.Join(root, "bare.git")
	_ = os.MkdirAll(src, 0o755)

	gitCmd(t, src, "init", "-q", "-b", "dev")
	writeFile(t, filepath.Join(src, "base.txt"), "base\n")
	gitCmd(t, src, "add", ".")
	gitCmd(t, src, "commit", "-q", "-m", "base commit")
	baseSHA := gitCmd(t, src, "rev-parse", "HEAD")

	// Create PR 2 branch
	gitCmd(t, src, "checkout", "-q", "-b", "pr2", "dev")
	writeFile(t, filepath.Join(src, "pr2.txt"), "pr2\n")
	gitCmd(t, src, "add", ".")
	gitCmd(t, src, "commit", "-q", "-m", "pr 2 commit")
	head2 := gitCmd(t, src, "rev-parse", "HEAD")
	gitCmd(t, src, "checkout", "-q", "dev")

	gitCmd(t, root, "clone", "-q", "--bare", src, bare)
	gitCmd(t, bare, "update-ref", "refs/pull/2/head", head2)

	var logBuf bytes.Buffer
	b := Build{
		Repo:        "owner/repo",
		Remote:      "file://" + bare,
		Base:        "dev",
		Branch:      "stream/test",
		Workdir:     filepath.Join(root, "workdir"),
		NoTest:      true,
		Log:         &logBuf,
		TestTimeout: time.Minute,
	}

	m2 := Member{
		Task:    "task-2",
		Stream:  "test-stream",
		N:       2,
		Head:    head2,
		BaseSHA: baseSHA,
		Deps:    []string{"missing-dep"},
	}

	res, err := b.Run(context.Background(), []Member{m2})
	if err != nil {
		t.Fatalf("Build.Run: %v", err)
	}

	if len(res.Kept) != 0 {
		t.Fatalf("expected 0 kept members, got %d", len(res.Kept))
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("expected 1 skipped member, got %d", len(res.Skipped))
	}
	if res.Skipped[0].Why != "dep:missing-dep:not-in-branch" {
		t.Errorf("expected Why dep:missing-dep:not-in-branch, got %s", res.Skipped[0].Why)
	}
	if !strings.Contains(logBuf.String(), "SKIP #2 dependency missing-dep not in stream branch") {
		t.Errorf("log does not contain expected skip message: %s", logBuf.String())
	}
}

func TestBuildRunDetectsOrderMiss(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	bare := filepath.Join(root, "bare.git")
	_ = os.MkdirAll(src, 0o755)

	gitCmd(t, src, "init", "-q", "-b", "dev")
	writeFile(t, filepath.Join(src, "pkg.txt"), "initial\n")
	gitCmd(t, src, "add", ".")
	gitCmd(t, src, "commit", "-q", "-m", "base commit")
	baseSHA := gitCmd(t, src, "rev-parse", "HEAD")

	// PR 1 branches from dev
	gitCmd(t, src, "checkout", "-q", "-b", "pr1", "dev")
	writeFile(t, filepath.Join(src, "pkg.txt"), "initial\nchange1\n")
	gitCmd(t, src, "add", ".")
	gitCmd(t, src, "commit", "-q", "-m", "pr 1 commit")
	head1 := gitCmd(t, src, "rev-parse", "HEAD")
	gitCmd(t, src, "checkout", "-q", "dev")

	// PR 2 also branches from dev (NOT built on head1!)
	gitCmd(t, src, "checkout", "-q", "-b", "pr2", "dev")
	writeFile(t, filepath.Join(src, "pkg.txt"), "initial\nchange2\n")
	gitCmd(t, src, "add", ".")
	gitCmd(t, src, "commit", "-q", "-m", "pr 2 commit")
	head2 := gitCmd(t, src, "rev-parse", "HEAD")
	gitCmd(t, src, "checkout", "-q", "dev")

	gitCmd(t, root, "clone", "-q", "--bare", src, bare)
	gitCmd(t, bare, "update-ref", "refs/pull/1/head", head1)
	gitCmd(t, bare, "update-ref", "refs/pull/2/head", head2)

	var logBuf bytes.Buffer
	b := Build{
		Repo:        "owner/repo",
		Remote:      "file://" + bare,
		Base:        "dev",
		Branch:      "stream/test",
		Workdir:     filepath.Join(root, "workdir"),
		NoTest:      true,
		Log:         &logBuf,
		TestTimeout: time.Minute,
	}

	m1 := Member{
		Task:    "task-1",
		Stream:  "test-stream",
		N:       1,
		Head:    head1,
		BaseSHA: baseSHA,
		Paths:   []string{"pkg.txt"},
	}
	m2 := Member{
		Task:    "task-2",
		Stream:  "test-stream",
		N:       2,
		Head:    head2,
		BaseSHA: baseSHA, // built on base, missing head1!
		Paths:   []string{"pkg.txt"},
	}

	res, err := b.Run(context.Background(), []Member{m1, m2})
	if err != nil {
		t.Fatalf("Build.Run: %v", err)
	}

	if len(res.Kept) != 1 || res.Kept[0].N != 1 {
		t.Fatalf("expected 1 kept member (#1), got %v", res.Kept)
	}
	if len(res.OrderMisses) != 1 {
		t.Fatalf("expected 1 order miss, got %d", len(res.OrderMisses))
	}
	miss := res.OrderMisses[0]
	if miss.Member.N != 2 || miss.Missing != "task-1" {
		t.Errorf("unexpected miss: %+v", miss)
	}
	wantWhy := fmt.Sprintf("ORDER: built on %s, missing task-1", short(baseSHA))
	if miss.Why != wantWhy {
		t.Errorf("miss.Why=%q, want %q", miss.Why, wantWhy)
	}
	if !strings.Contains(logBuf.String(), fmt.Sprintf("ORDER MISS #2 built on %s, missing task-1", short(baseSHA))) {
		t.Errorf("log missing expected line: %s", logBuf.String())
	}
}

func TestSprintCountsAndHeaderOrderMisses(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	_ = c.XAdd(ctx, &redis.XAddArgs{
		Stream: "ws:order:misses",
		Values: map[string]any{
			"stream":  "stream-a",
			"card":    "task-2",
			"missing": "task-1",
			"why":     "ORDER: built on 12345678, missing task-1",
		},
	}).Err()

	reader := &ws.CountsReader{}
	counts, err := reader.Read(ctx, c, time.Now())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.OrderMisses != 1 {
		t.Errorf("counts.OrderMisses=%d, want 1", counts.OrderMisses)
	}
	header := counts.Header()
	if !strings.Contains(header, ", order-miss 1") {
		t.Errorf("header %q does not contain ', order-miss 1'", header)
	}
}
