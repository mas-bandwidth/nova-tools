package definition

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gitIn runs git in dir with the machine's configuration off and an explicit
// identity, so a test repository never reads or writes the machine's git config.
func gitIn(dir string, args ...string) (string, error) { return gitInput(dir, "", args...) }

// gitInput is gitIn with standard input.
func gitInput(dir, stdin string, args ...string) (string, error) {
	full := append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitIn(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeFile(dir, rel, content string) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := writeFile(dir, rel, content); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a small repository with an origin and one commit holding a.md and
// dir/b.md, for the tests that change a repository. The tests that only read one
// share the fixture.
func newRepo(t *testing.T) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	testGit(t, dir, "init", "-q", "-b", "main")
	testGit(t, dir, "remote", "add", "origin", "https://user:secret@Example.COM/Owner/Repo.git")
	write(t, dir, "a.md", "committed a\n")
	write(t, dir, "dir/b.md", "committed b\n")
	testGit(t, dir, "add", "-A")
	testGit(t, dir, "commit", "-q", "-m", "one")
	return dir, testGit(t, dir, "rev-parse", "HEAD")
}

// fixture is one repository, built once, that the read-only tests share. Its
// history: c1 adds the files, c2 adds symlinks and a submodule entry, c3 renames
// a.md and deletes dir/b.md. No test writes to it.
type fixture struct {
	dir        string
	c1, c2, c3 string
	tree, tag  string
	cards      []string // the golden cards' paths at c1
	many       []string // 100 small files at c1
	huge       []string // files whose total is over MaxTotalBytes at c1
}

var (
	fx     fixture
	fxOnce sync.Once
	fxRoot string
	fxErr  error
)

// TestMain removes the shared fixture when the package's tests end.
func TestMain(m *testing.M) {
	code := m.Run()
	if fxRoot != "" {
		_ = os.RemoveAll(fxRoot)
	}
	os.Exit(code)
}

func sharedRepo(t *testing.T) *fixture {
	t.Helper()
	fxOnce.Do(func() { fxErr = buildFixture() })
	if fxErr != nil {
		t.Fatal(fxErr)
	}
	return &fx
}

func buildFixture() error {
	root, err := os.MkdirTemp("", "card-definition-*")
	if err != nil {
		return err
	}
	fxRoot = root
	dir := filepath.Join(root, "repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	g := func(args ...string) string {
		if err != nil {
			return ""
		}
		var out string
		out, err = gitIn(dir, args...)
		return out
	}
	w := func(rel, content string) {
		if err == nil {
			err = writeFile(dir, rel, content)
		}
	}
	g("init", "-q", "-b", "main")
	g("remote", "add", "origin", "https://user:secret@Example.COM/Owner/Repo.git")
	w("a.md", "committed a\n")
	w("dir/b.md", "committed b\n")
	w("x.md", "same bytes\n")
	w("y.md", "same bytes\n")
	w("big.md", strings.Repeat("x", MaxCardBytes+1))
	w("exact.md", strings.Repeat("y", MaxCardBytes))
	for _, n := range goldenNames {
		p := "cards/" + n + ".md"
		var b []byte
		if err == nil {
			b, err = os.ReadFile(filepath.Join("testdata", "cards", n+".md"))
		}
		w(p, string(b))
		fx.cards = append(fx.cards, p)
	}
	for i := 0; i < 100; i++ {
		p := fmt.Sprintf("many/card-%03d.md", i)
		w(p, "card "+p+"\n")
		fx.many = append(fx.many, p)
	}
	g("add", "-A")
	// The huge files are one blob under many paths: over MaxTotalBytes in all, one
	// object to write.
	var oid string
	if err == nil {
		oid, err = gitInput(dir, strings.Repeat("h", MaxCardBytes), "hash-object", "-w", "--stdin")
	}
	var idx strings.Builder
	for i := 0; i < MaxTotalBytes/MaxCardBytes+1; i++ {
		p := fmt.Sprintf("huge/f%02d.md", i)
		fmt.Fprintf(&idx, "100644 %s\t%s\n", oid, p)
		fx.huge = append(fx.huge, p)
	}
	if err == nil {
		_, err = gitInput(dir, idx.String(), "update-index", "--add", "--index-info")
	}
	g("commit", "-q", "-m", "c1")
	fx.c1 = g("rev-parse", "HEAD")
	if err == nil {
		err = os.Symlink("a.md", filepath.Join(dir, "link.md"))
	}
	if err == nil {
		err = os.Symlink("dir", filepath.Join(dir, "dirlink"))
	}
	g("add", "link.md", "dirlink")
	g("update-index", "--add", "--cacheinfo", "160000,"+fx.c1+",sub")
	g("commit", "-q", "-m", "c2")
	fx.c2 = g("rev-parse", "HEAD")
	g("mv", "a.md", "renamed.md")
	g("rm", "-q", "dir/b.md")
	g("commit", "-q", "-m", "c3")
	fx.c3 = g("rev-parse", "HEAD")
	fx.tree = g("rev-parse", fx.c1+"^{tree}")
	g("tag", "-a", "-m", "tag", "v1")
	fx.tag = g("rev-parse", "v1")
	fx.dir = dir
	return err
}

// pin pins paths at commit with the identity supplied, which skips the origin
// lookup; the tests of the identity itself call Pin without it.
func (f *fixture) pin(paths []string, commit string) ([]pinned, []Refusal) {
	return pinL(bg, f.dir, commit, paths, WithIdentity("example.com/Owner/Repo"))
}
