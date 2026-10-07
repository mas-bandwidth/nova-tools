//go:build functional

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nova-tools/internal/goenv"
)

// A directory generated from a ledger in a fixture checkout is admitted whole by
// `nova-sprint add --brief-dir` into a twin store, and the waves' dependencies
// hold there: a wave 1 card is ready, a wave 2 card waits on its neighbours. A
// build is the functional tier's (nova-tools#4328). The checkout is on the sprint
// branch its stream lands on, since add refuses a card cut on dev outside the
// promotion stream (docs/SPEC-SPRINT.md section 7, the sprint branch).
func TestAGeneratedDirectoryIsAdmittedWholeAndItsWavesHold(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(goenv.Clean(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	write("cmd/a/a_test.go", "package main\n")
	write("cmd/b/b_test.go", "package main\n")
	write("internal/c/c_test.go", "package c\n")
	write("internal/ci/testdata/serial-tests_allowlist.txt", "# ceiling: 3\ncmd/a/a_test.go:TestA serial: t.Setenv\ncmd/b/b_test.go:TestB serial: t.Chdir\ninternal/c/c_test.go:TestC serial: os.Setenv\n")
	git("init", "-q", "-b", "sprint/s")
	git("remote", "add", "origin", filepath.Join(t.TempDir(), "example", "repo.git"))
	git("add", ".")
	git("commit", "-q", "-m", "fixture")

	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "ledger", "--ledger", "serial-tests", "--repo-dir", repo, "--out", out)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "CARDS OK dir="+out+" cards=3 waves=2 tier=flash shared-paths=yes")
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.tsv"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "serial-tests-cmd-b-b\tcmd/b/b_test.go\tinternal/ci TestEveryTestOpensWithTParallel\t2\tserial-tests-cmd-a-a,serial-tests-internal-c-c\n")
	brief, err := os.ReadFile(filepath.Join(out, "serial-tests-cmd-a-a.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief), "REPO: example/repo\nBASE: sprint/s\n")

	sprint := filepath.Join(t.TempDir(), "nova-sprint")
	if runtime.GOOS == "windows" {
		sprint += ".exe"
	}
	build := exec.Command("go", "build", "-o", sprint, "../nova-sprint")
	build.Env = goenv.Clean(os.Environ())
	o, err := build.CombinedOutput()
	require.NoError(t, err, "building nova-sprint: %s", o)
	env := append(goenv.Clean(os.Environ()), "NOVA_SPRINT_REDIS=mem:"+filepath.Join(t.TempDir(), "twin"), "NOVA_SPRINT_ACTOR=t")
	sprintRun := func(args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command(sprint, args...)
		cmd.Env = env
		var b bytes.Buffer
		cmd.Stdout, cmd.Stderr = &b, &b
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), b.String()
		}
		require.NoError(t, err, "nova-sprint %s", strings.Join(args, " "))
		return 0, b.String()
	}
	code, text := sprintRun("init", "--members", "m1", "--readers", "r1")
	require.Equal(t, 0, code, text)
	code, text = sprintRun("add", "--stream", "s", "--brief-dir", out)
	assert.Equal(t, 2, code, "without --allow-shared-paths the wave 1 cards' shared ledger is refused: %s", text)
	code, text = sprintRun("add", "--stream", "s", "--brief-dir", out, "--allow-shared-paths")
	require.Equal(t, 0, code, text)
	assert.Contains(t, text, "ADD OK stream=s cards=3 before=- moved=3 refused=0")
	_, text = sprintRun("card", "serial-tests-cmd-b-b")
	assert.Contains(t, text, "needs serial-tests-cmd-a-a")
	assert.Contains(t, text, "needs serial-tests-internal-c-c")
	assert.Contains(t, text, "waiting")
	_, text = sprintRun("card", "serial-tests-cmd-a-a")
	assert.Contains(t, text, "ready")
	assert.NotContains(t, text, "  needs serial")
}
