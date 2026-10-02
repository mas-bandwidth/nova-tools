package ci

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceCleanupRemovesOnlyStaleLocks: a run cancelled mid-checkout left
// .git/index.lock in a runner's workspace (2026-10-02 04:34Z), the in-place reset
// failed on it, and the cleanup step emptied the tree; the re-seeded workspace had
// lost every ref it held (refs/remotes/origin/dev, which internal/ci's
// deprecated-imports ratchet reads). Every copy of the step now removes
// index.lock and HEAD.lock (the two locks that fail a reset of a detached
// checkout) when no process of the user has its cwd in the workspace, and keeps
// them, with today's fallback, when one does. The step is run here as it is
// written in ci.yml, over a temporary repository.
func TestWorkspaceCleanupRemovesOnlyStaleLocks(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	ci := readFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	cert := readFile(t, filepath.Join(root, ".github", "workflows", "certification.yml"))
	_, ciBodies := workspaceCleanupStepBodies(ci)
	_, certBodies := workspaceCleanupStepBodies(cert)
	bodies := append(ciBodies, certBodies...)
	require.NotEmpty(t, ciBodies, "no `%s` step in ci.yml", workspaceCleanupStepName)

	want := staleLockCode(stepScript(t, ciBodies[0]))
	require.Contains(t, want, `rm -f -- "$w/.git/index.lock" "$w/.git/HEAD.lock"`)
	for i, body := range bodies {
		assert.Equal(t, want, staleLockCode(stepScript(t, body)), "cleanup step %d of %d differs from ci.yml's first in its stale-lock lines", i+1, len(bodies))
	}

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("the step's liveness scan exists for linux and darwin only; %s keeps the fallback", runtime.GOOS)
	}
	script := stepScript(t, ciBodies[0])

	t.Run("stale locks are removed and the tree is cleaned in place", func(t *testing.T) {
		t.Parallel()
		ws := lockedWorkspace(t)
		out := runCleanupStep(t, script, ws)
		assert.Contains(t, out, "stale git locks removed")
		assert.Contains(t, out, "workspace cleaned in place")
		assert.NoFileExists(t, filepath.Join(ws, ".git", "index.lock"))
		assert.NoFileExists(t, filepath.Join(ws, ".git", "HEAD.lock"))
		assert.NoFileExists(t, filepath.Join(ws, "untracked"))
		tracked, err := os.ReadFile(filepath.Join(ws, "tracked"))
		require.NoError(t, err)
		assert.Equal(t, "base\n", string(tracked), "the reset restores the tracked file")
		_, err = gitOut(ws, "rev-parse", "--verify", "-q", "refs/remotes/origin/dev")
		assert.NoError(t, err, "the workspace keeps its refs")
	})

	t.Run("a live process in the workspace keeps the locks and today's fallback", func(t *testing.T) {
		t.Parallel()
		ws := lockedWorkspace(t)
		holder := exec.Command("sh", "-c", "read -r line")
		holder.Dir = ws
		stdin, err := holder.StdinPipe()
		require.NoError(t, err)
		require.NoError(t, holder.Start())
		defer func() {
			_ = stdin.Close()
			_ = holder.Wait()
		}()
		out := runCleanupStep(t, script, ws)
		assert.Contains(t, out, "git locks kept, held by: "+strconv.Itoa(holder.Process.Pid))
		assert.NotContains(t, out, "workspace cleaned in place")
		entries, err := os.ReadDir(ws)
		require.NoError(t, err)
		assert.Empty(t, entries, "the reset failed on the held lock, so the tree is emptied as before")
	})
}

// lockedWorkspace is a checkout as a runner leaves one after a cancel: detached
// HEAD, a remote-tracking ref, a modified tracked file, an untracked file, and
// index.lock and HEAD.lock left behind.
func lockedWorkspace(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "nova-tools")
	require.NoError(t, os.MkdirAll(ws, 0o755))
	git := func(args ...string) {
		t.Helper()
		_, err := gitOut(ws, append([]string{"-c", "user.name=ci", "-c", "user.email=ci@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		require.NoError(t, err, "git %v", args)
	}
	git("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(ws, "tracked"), []byte("base\n"), 0o644))
	git("add", "tracked")
	git("commit", "-q", "-m", "base")
	git("update-ref", "refs/remotes/origin/dev", "HEAD")
	git("checkout", "-q", "--detach")
	require.NoError(t, os.WriteFile(filepath.Join(ws, "tracked"), []byte("changed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "untracked"), []byte("x\n"), 0o644))
	for _, l := range []string{"index.lock", "HEAD.lock"} {
		require.NoError(t, os.WriteFile(filepath.Join(ws, ".git", l), nil, 0o644))
	}
	return ws
}

// runCleanupStep runs the step's script as the runner does (bash -eo pipefail,
// in the workspace) and returns its output; the step never fails a job.
func runCleanupStep(t *testing.T, script, ws string) string {
	t.Helper()
	cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "GITHUB_WORKSPACE="+ws)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	require.NoError(t, cmd.Run(), "the cleanup step failed:\n%s", out.String())
	return out.String()
}

// stepScript returns the dedented `run: |` block of a step body.
func stepScript(t *testing.T, body string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "run: |" {
			continue
		}
		indent := -1
		var script []string
		for _, s := range lines[i+1:] {
			if strings.TrimSpace(s) == "" {
				script = append(script, "")
				continue
			}
			n := len(s) - len(strings.TrimLeft(s, " "))
			if indent < 0 {
				indent = n
			}
			if n < indent {
				break
			}
			script = append(script, s[indent:])
		}
		joined := strings.Join(script, "\n")
		require.NotContains(t, joined, "${{", "the cleanup step's script carries an expression this test cannot run")
		return joined
	}
	t.Fatalf("no `run: |` in the cleanup step:\n%s", body)
	return ""
}

// staleLockCode is the step's stale-lock lines without comments: from the
// workspace resolution to the line before the in-place reset.
func staleLockCode(script string) string {
	start := strings.Index(script, `w=$(cd "${GITHUB_WORKSPACE}" && pwd -P)`)
	end := strings.Index(script, `if git -C "${GITHUB_WORKSPACE}" reset -q --hard`)
	if start < 0 || end < start {
		return ""
	}
	var code []string
	for _, l := range strings.Split(script[start:end], "\n") {
		if s := strings.TrimSpace(l); s != "" && !strings.HasPrefix(s, "#") {
			code = append(code, l)
		}
	}
	return strings.Join(code, "\n")
}
