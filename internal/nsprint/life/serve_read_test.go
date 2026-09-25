package life_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func serveGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x", "GIT_CONFIG_GLOBAL=/dev/null")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

// TestServeReadTaskBriefCarriesTheMirrorDiff (#3599): a read task taken by
// a friend's seat is briefed from Redis and the mirror: brief.md is the read
// brief naming the diff saved beside it, diff.patch is the mirror's
// base_sha..head diff, the start receipt says brief=read, and the GitHub
// stub counts zero calls, so the child never needs GitHub for its diff.
func TestServeReadTaskBriefCarriesTheMirrorDiff(t *testing.T) {
	stub := testutil.StartGitHubStub(t)
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	serveGit(t, work, "init", "-q", "-b", "dev")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serveGit(t, work, "add", ".")
	serveGit(t, work, "commit", "-qm", "base")
	base := serveGit(t, work, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	serveGit(t, work, "commit", "-qam", "pr")
	head := serveGit(t, work, "rev-parse", "HEAD")
	mirror := filepath.Join(t.TempDir(), "nova-tools.git")
	serveGit(t, work, "clone", "-q", "--bare", work, mirror)
	serveGit(t, mirror, "update-ref", "refs/pull/7/head", head)

	st, client := seedSeat(t, 1)
	ctx := context.Background()
	if err := client.HSet(ctx, "s:s1:pr:nova-tools:7", "head", head).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, read.Key("nova-tools", "7"), "head", head, "base", "dev", "base_sha", base,
		"paths", "a.txt", "done_when", "true", "stream", "nova sprint migration").Err(); err != nil {
		t.Fatal(err)
	}
	if got, err := task.Push(ctx, st, task.PushRequest{
		Sprint: "s1", ID: "r1", Kind: task.KindRead, Title: "read nova-tools#7",
		Effects: task.EffectsNone, PayloadSHA: "r1", To: "emma",
		Repo: "nova-tools", PR: 7, Head: head,
	}); err != nil || got != task.PushCreated {
		t.Fatalf("push read: %s, %v", got, err)
	}
	cfg := serveConfig(t, "sess-read", 1, "done")
	cfg.Mirror = mirror
	if _, err := life.ServeOnce(ctx, st, cfg); err != nil {
		t.Fatalf("serve once: %v", err)
	}
	briefs, _ := filepath.Glob(filepath.Join(cfg.Dir, "s1", "r1-*", "brief.md"))
	if len(briefs) != 1 {
		t.Fatalf("briefs %v, want one", briefs)
	}
	b, err := os.ReadFile(briefs[0])
	if err != nil {
		t.Fatal(err)
	}
	diffPath := filepath.Join(filepath.Dir(briefs[0]), "diff.patch")
	for _, want := range []string{"# Read brief nova-tools#7 at " + head, "saved at " + diffPath, "base-sha: " + base} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("brief lacks %q:\n%s", want, b)
		}
	}
	p, err := os.ReadFile(diffPath)
	if err != nil || strings.TrimSpace(string(p)) != serveGit(t, mirror, "diff", base+".."+head) {
		t.Fatalf("diff.patch is not the mirror diff: %v\n%s", err, p)
	}
	if n := stub.Calls(); n != 0 {
		t.Fatalf("%d HTTP call(s) to GitHub; a read brief makes zero", n)
	}
	entries, err := client.XRange(ctx, life.LogKey("emma"), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		for _, v := range e.Values {
			if s, ok := v.(string); ok && strings.Contains(s, "brief=read ") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no start receipt with brief=read in friend:emma:log: %v", entries)
	}
}
