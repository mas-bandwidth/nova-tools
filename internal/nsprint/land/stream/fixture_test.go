package stream

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a local bare repo standing in for GitHub: a dev branch and
// refs/pull/<n>/head for each member branch. No network (CI-NET).
type fixture struct {
	t    *testing.T
	src  string // working clone the fixture commits in
	bare string // the remote
	URL  string // file:// URL of the bare repo
	Base string // dev tip sha
	Head map[int]string
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=dev"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newFixture builds dev with a.txt, then one branch per member:
// 1 adds one.txt (green), 2 adds red.txt (the batch test fails on it),
// 3 rewrites a.txt, 4 rewrites a.txt differently (conflicts with 3),
// 5 adds five.txt (green).
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, src: filepath.Join(root, "src"), bare: filepath.Join(root, "remote.git"), Head: map[int]string{}}
	if err := os.MkdirAll(f.src, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, f.src, "init", "-q", "-b", "dev")
	write(t, filepath.Join(f.src, "a.txt"), "base\n")
	gitT(t, f.src, "add", ".")
	gitT(t, f.src, "commit", "-q", "-m", "base")
	f.Base = gitT(t, f.src, "rev-parse", "HEAD")
	member := func(n int, file, body string) {
		gitT(t, f.src, "checkout", "-q", "-b", "m"+string(rune('0'+n)), "dev")
		write(t, filepath.Join(f.src, file), body)
		gitT(t, f.src, "add", ".")
		gitT(t, f.src, "commit", "-q", "-m", "member "+file)
		f.Head[n] = gitT(t, f.src, "rev-parse", "HEAD")
		gitT(t, f.src, "checkout", "-q", "dev")
	}
	member(1, "one.txt", "one\n")
	member(2, "red.txt", "red\n")
	member(3, "a.txt", "three\n")
	member(4, "a.txt", "four\n")
	member(5, "five.txt", "five\n")
	gitT(t, root, "clone", "-q", "--bare", f.src, f.bare)
	for n, h := range f.Head {
		gitT(t, f.bare, "update-ref", "refs/pull/"+string(rune('0'+n))+"/head", h)
	}
	f.URL = "file://" + f.bare
	return f
}

func (f *fixture) branchSHA(branch string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	cmd.Dir = f.bare
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// testCmd is green unless red.txt is in the tree.
const testCmd = "test ! -e red.txt"
