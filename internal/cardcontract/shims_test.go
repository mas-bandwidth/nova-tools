package cardcontract

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShimPushToALocalPathRemoteReachesGit pins the local-path push
// (docs/SPEC-CARD-CONTRACT.md, the push): a push whose remote is a local path
// goes through to the real git unchanged, so the bare repository the push names
// holds the commit after, and records nothing in the job, while a push to
// origin is still answered by the shim (pushed.tsv written, origin untouched):
// a test or tool inside a card that pushes to a bare repository it made under
// its own temp directory needs the push to land.
func TestShimPushToALocalPathRemoteReachesGit(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the shims are POSIX sh; a windows bench writes none (docs/SPEC-WORKER.md)")
	}
	for _, family := range []string{"claude", "plain"} {
		root := t.TempDir()
		bare, job, work := filepath.Join(root, "origin.git"), filepath.Join(root, "job"), filepath.Join(root, "job", "repo")
		shimGit(t, root, "init", "-q", "--bare", bare)
		shimGit(t, root, "clone", "-q", bare, work)
		require.NoError(t, os.MkdirAll(job, 0o755))
		shimGit(t, work, "commit", "-q", "--allow-empty", "-m", "one")
		head := shimGit(t, work, "rev-parse", "HEAD")
		f := Frame{Kind: "work", Card: "c1", Attempt: 1, Model: "test/model", Repo: cardURL, BaseRef: "main", Branch: "sprint/c1", ReviewBase: "main"}
		s := Staged{Job: job, Repo: work, Head: head, Git: realGit(t)}
		shims := filepath.Join(root, "shim")
		require.NoError(t, Install(For(family), f, s, shims), family)

		// a push to the bare repository by its local path reaches the real git
		code, _, errb := shimSh(t, shims, work, "git push "+shq(bare)+" HEAD:refs/heads/x")
		require.Equal(t, 0, code, "%s: %s", family, errb)
		assert.NotContains(t, errb, "pushed by the sprint", "%s: the push is not answered by the shim", family)
		assert.Equal(t, head, shimGit(t, bare, "rev-parse", "refs/heads/x"), "%s: the bare repository holds the commit", family)
		_, err := os.Stat(filepath.Join(job, PushedName))
		assert.True(t, os.IsNotExist(err), "%s: a push to a local-path remote records nothing", family)

		// a push to origin is still answered by the shim: pushed.tsv written, origin untouched
		code, _, errb = shimSh(t, shims, work, "git push origin HEAD")
		require.Equal(t, 0, code, "%s: %s", family, errb)
		assert.Contains(t, errb, "pushed by the sprint", family)
		branch, got := LastPushed(job)
		assert.Equal(t, "main", branch, family)
		assert.Equal(t, head, got, family)
		assert.Equal(t, "refs/heads/x", strings.TrimSpace(shimGit(t, bare, "for-each-ref", "--format=%(refname)", "refs/heads")), "%s: origin is untouched", family)
	}
}

// realGit finds the real git binary: the first git on PATH that is not a script
// (docs/SPEC-CARD-CONTRACT.md section 5, native fills Staged.Git with it). The
// wall installs the shims — scripts — first on the child's PATH, and a push
// handed through to a shim there is answered by it, never pushed, so the shims
// the tests render hand through to the real git.
func realGit(t *testing.T) string {
	t.Helper()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, "git")
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var head [2]byte
		n, _ := f.Read(head[:])
		require.NoError(t, f.Close())
		if n == 2 && head[0] == '#' && head[1] == '!' {
			continue // a script that answers for git: a shim, not the real git
		}
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	t.Fatal("no real git on PATH: every git there is a script")
	return ""
}

// shimGit runs the real git in dir, hermetic, and returns its trimmed output:
// never the git of the surrounding PATH, whose shims answer for commands the
// test sets up with.
func shimGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(realGit(t), append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// shimSh runs one command line in dir with the rendered shims first on PATH; its
// exit, stdout and stderr.
func shimSh(t *testing.T, shims, dir, line string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+shims+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return code, out.String(), errb.String()
}
