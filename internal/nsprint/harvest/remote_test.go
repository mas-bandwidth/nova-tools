package harvest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// The REST calls name the exact endpoints, and the push script is idempotent
// against a real git remote: same sha is success, another sha is refused.
func TestGitHubCallsAndIdempotentPush(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/bash\necho \"$*\" >> " + strconv.Quote(log) + "\n" +
		`case "$*" in
*"-X GET"*) echo '[{"number":7,"html_url":"u","head":{"sha":"abc","ref":"nova/s/l-a1"}}]';;
*"-X POST"*) echo '{"number":8,"html_url":"u","head":{"sha":"abc","ref":"nova/s/m-a1"}}';;
*) echo '{"number":7,"html_url":"u","head":{"sha":"abc","ref":"nova/s/l-a1"}}';;
esac
`
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	g := GitHub{Bin: gh}
	ctx := context.Background()
	if pr, ok, err := g.FindOpenPR(ctx, "nova-tools", "nova/s/l-a1"); err != nil || !ok || pr.Number != 7 {
		t.Fatalf("find = %v %v %v", pr, ok, err)
	}
	if _, ok, _ := g.FindOpenPR(ctx, "nova-tools", "nova/s/other-a1"); ok {
		t.Fatal("a PR on another head ref must not be found")
	}
	if pr, err := g.OpenPR(ctx, "nova-tools", "nova/s/m-a1", "dev", "t", "b"); err != nil || pr.Number != 8 {
		t.Fatalf("open = %v %v", pr, err)
	}
	if pr, err := g.ReadPR(ctx, "nova-tools", 7); err != nil || pr.Head != "abc" {
		t.Fatalf("read = %v %v", pr, err)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{
		"api -X GET -f state=open -f head=mas-bandwidth:nova/s/l-a1 repos/mas-bandwidth/nova-tools/pulls",
		"-f head=nova/s/m-a1 -f base=dev -f body=b repos/mas-bandwidth/nova-tools/pulls",
		"api repos/mas-bandwidth/nova-tools/pulls/7",
	} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("calls %q lack %q", calls, want)
		}
	}

	// The push script against a bare origin, run by bash directly.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	origin := filepath.Join(dir, "origin.git")
	repo := filepath.Join(dir, "res", "repo")
	run := func(d string, args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(dir, "git", "init", "-q", "--bare", origin)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "git", "init", "-q")
	run(repo, "git", "remote", "add", "origin", origin)
	run(repo, "git", "commit", "-q", "--allow-empty", "-m", "one")
	one := run(repo, "git", "rev-parse", "HEAD")
	run(repo, "git", "commit", "-q", "--allow-empty", "-m", "two")
	two := run(repo, "git", "rev-parse", "HEAD")
	push := func(sha, branch string) (string, error) {
		cmd := exec.Command("bash", "-s", "--", repo, sha, branch)
		cmd.Stdin = strings.NewReader(pushScript)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := push(one, "nova/s/l-a1"); err != nil || out != "PUSH OK nova/s/l-a1" {
		t.Fatalf("first push: %q %v", out, err)
	}
	if out, err := push(one, "nova/s/l-a1"); err != nil || out != "PUSH ALREADY nova/s/l-a1" {
		t.Fatalf("re-push: %q %v", out, err)
	}
	if out, err := push(two, "nova/s/l-a1"); err == nil || !strings.Contains(out, "PUSH REFUSED") {
		t.Fatalf("push of another sha must be refused: %q %v", out, err)
	}
	if out, err := push(one, "rowan/x"); err == nil || !strings.Contains(out, "not under nova/") {
		t.Fatalf("push outside nova/ must be refused: %q %v", out, err)
	}
	// repoDir is a guarded seam by receiver name alone -- it starts nothing
	// itself, but names the same seam Push reaches through it -- so a test
	// calling it directly, with no fake ssh on PATH, declares that out loud.
	func() {
		defer testguard.AllowHosts()()
		if d, err := (SSHPusher{}).repoDir(Card{Label: "l", Results: "s/l/09fbedc9/b/1"}); err == nil {
			t.Fatalf("relative results with no root = %q, want refused", d)
		}
	}()
}
