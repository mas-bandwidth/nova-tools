package main

// The verb face of our own CI (#3597, #3349): request, run and status
// --repo --sha through runCI, on the throwaway redis-server and a local bare
// repository. The package-level flow is in internal/nsprint/ci.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func ciGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCIVerbRequestRunStatus(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	remote := testutil.NewLocalRemote(t, "dev")
	ciGit(t, remote.Dir, "config", "uploadpack.allowAnySHA1InWant", "true")
	work := filepath.Join(t.TempDir(), "work")
	ciGit(t, t.TempDir(), "clone", "-q", remote.URL, work)
	if err := os.WriteFile(filepath.Join(work, "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ciGit(t, work, "add", "f")
	ciGit(t, work, "commit", "-q", "-m", "one")
	ciGit(t, work, "push", "-q", "origin", "HEAD:dev")
	sha := ciGit(t, work, "rev-parse", "HEAD")
	client.HSet(ctx, "cfg:ci:fx", "checks", "ok", "check:ok", "true")
	root := t.TempDir()

	var out, errOut bytes.Buffer
	if code := runCI(ctx, []string{"request", "--redis", addr, "--repo", "fx", "--sha", sha, "--url", remote.URL}, &out, &errOut); code != 0 {
		t.Fatalf("request code=%d stderr=%q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "CREATED fx@"+sha[:8]+" pending") {
		t.Fatalf("request: %q", out.String())
	}
	out.Reset()
	if code := runCI(ctx, []string{"request", "--redis", addr, "--repo", "fx", "--sha", sha, "--checks", "nope"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "EXISTS") {
		t.Fatalf("second request code=%d out=%q stderr=%q (create-only: EXISTS whatever it asks)", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runCI(ctx, []string{"run", "--redis", addr, "--bench", "b1", "--results", filepath.Join(root, "results"), "--scratch", filepath.Join(root, "scratch"), "--mirror-root", filepath.Join(root, "no-mirror")}, &out, &errOut); code != 0 {
		t.Fatalf("run code=%d stderr=%q out=%q", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "CI fx@"+sha[:8]+" green bench=b1 attempt=1") {
		t.Fatalf("run: %q", out.String())
	}
	out.Reset()
	if code := runCI(ctx, []string{"run", "--redis", addr, "--bench", "b1", "--results", filepath.Join(root, "results")}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "IDLE bench=b1") {
		t.Fatalf("idle run code=%d out=%q stderr=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runCI(ctx, []string{"status", "--redis", addr, "--repo", "fx", "--sha", sha}, &out, &errOut); code != 0 {
		t.Fatalf("status code=%d stderr=%q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "ci:fx:"+sha+" green bench=b1 attempt=1") || !strings.Contains(out.String(), "  ok green rc=0 ") {
		t.Fatalf("status: %q", out.String())
	}
	if code := runCI(ctx, []string{"status", "--redis", addr, "--repo", "fx"}, &out, &errOut); code != 2 {
		t.Fatalf("status with --repo alone code=%d, want 2", code)
	}
	if code := runCI(ctx, []string{"run", "--redis", addr, "--bench", "b1", "--results", "relative"}, &out, &errOut); code != 2 {
		t.Fatalf("run with a relative results root code=%d, want 2", code)
	}
}
