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

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// A directory generated from a ledger in a fixture checkout is admitted whole by
// `nova-sprint add --brief-dir` into a twin store, and the one-wave plan holds
// there: every card is ready and none needs another, while two cards that share
// the ledger's path still make the add want --allow-shared-paths. A build is the
// functional tier's (nova-tools#4328). The checkout is on the sprint branch its
// stream lands on, since add refuses a card cut on dev outside the promotion
// stream (docs/SPEC-SPRINT.md section 7, the sprint branch).
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
	// the ledger's own test, whose package every card's START and PATHS name
	// (internal/ci/*.go), is at the base as it is in the real tree
	write("internal/ci/serial_test.go", "package ci\n\nimport \"testing\"\n\nfunc TestEveryTestOpensWithTParallel(t *testing.T) { t.Parallel() }\n")
	write("internal/ci/testdata/serial-tests_allowlist.txt", "# ceiling: 3\ncmd/a/a_test.go:TestA serial: t.Setenv\ncmd/b/b_test.go:TestB serial: t.Chdir\ninternal/c/c_test.go:TestC serial: os.Setenv\n")
	// origin is a bare twin of example/repo holding the sprint branch: add reads every
	// brief at the tip of its BASE: in the lander's clone of its REPO:
	origin := filepath.Join(t.TempDir(), "example", "repo.git")
	git("init", "-q", "-b", "sprint/s")
	git("init", "-q", "--bare", "-b", "sprint/s", origin)
	git("remote", "add", "origin", origin)
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	git("push", "-q", "origin", "HEAD:refs/heads/sprint/s")

	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "ledger", "--ledger", "serial-tests", "--repo-dir", repo, "--out", out)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "CARDS OK dir="+out+" cards=3 waves=1 tier=flash shared-paths=yes")
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.tsv"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "serial-tests-cmd-b-b\tcmd/b/b_test.go\tinternal/ci TestEveryTestOpensWithTParallel\t1\t-\n")
	brief, err := os.ReadFile(filepath.Join(out, "serial-tests-cmd-a-a.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief), "REPO: example/repo\nBASE: sprint/s\nKIND: ledger\n")
	assert.Contains(t, string(brief), "\nSTOP: the ledger row for cmd/a/a_test.go in internal/ci/testdata/serial-tests_allowlist.txt shrinks from 1 to 0 and the class test TestEveryTestOpensWithTParallel stays green, and the STEP 4 gate passes\n")

	// the cards the twin store admits are the same cut with REPO: naming the twin, so
	// the lander clones it rather than example/repo over the network
	admit := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr = runCard("generate", "--from", "ledger", "--ledger", "serial-tests", "--repo-dir", repo, "--repo", origin, "--out", admit)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "CARDS OK dir="+admit+" cards=3 waves=1 tier=flash shared-paths=yes")

	sprint := filepath.Join(t.TempDir(), "nova-sprint")
	if runtime.GOOS == "windows" {
		sprint += ".exe"
	}
	build := exec.Command("go", "build", "-o", sprint, "../nova-sprint")
	build.Env = goenv.Clean(os.Environ())
	o, err := build.CombinedOutput()
	require.NoError(t, err, "building nova-sprint: %s", o)
	// the seat's name is the role word, as in cmd/nova-worker's member drive: add holds
	// every brief to name no coordinator by name (personal-name), and a one-letter
	// name is a word of any brief
	env := append(goenv.Clean(os.Environ()), "NOVA_SPRINT_REDIS=mem:"+filepath.Join(t.TempDir(), "twin"), "NOVA_SPRINT_ACTOR=coordinator",
		// the lander's clones go under the test's own directories (os.UserCacheDir)
		"HOME="+t.TempDir(), "XDG_CACHE_HOME="+t.TempDir())
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
	// the seat is proven before any coordinator verb runs, as the built binary
	// wants of every name (docs/SPEC-SPRINT.md, "The push proof"): a folder push
	// target recorded, a check reported delivered, and the session's pong
	const nonce = "0123456789abcdef"
	for _, step := range [][]string{
		{"seat", "push", "--harness", "claude", "--target", t.TempDir()},
		{"seat", "push", "--sent", nonce},
	} {
		code, text = sprintRun(step...)
		require.Contains(t, []int{0, 1}, code, "nova-sprint %s: %s", strings.Join(step, " "), text) // 1 is PUSH DOWN, not proven yet
	}
	code, text = sprintRun("seat", "pong", nonce)
	require.Equal(t, 0, code, text)
	code, text = sprintRun("seat", "push")
	require.Equal(t, 0, code, text)
	require.Contains(t, text, "PUSH OK name=coordinator")
	code, text = sprintRun("add", "--stream", "s", "--brief-dir", admit)
	assert.Equal(t, 2, code, "without --allow-shared-paths the cards' shared ledger is refused: %s", text)
	code, text = sprintRun("add", "--stream", "s", "--brief-dir", admit, "--allow-shared-paths")
	require.Equal(t, 0, code, text)
	assert.Contains(t, text, "ADD OK stream=s cards=3 before=- moved=3 refused=0")
	_, text = sprintRun("card", "serial-tests-cmd-b-b")
	assert.Contains(t, text, "ready")
	assert.NotContains(t, text, "  needs serial")
	_, text = sprintRun("card", "serial-tests-cmd-a-a")
	assert.Contains(t, text, "ready")
	assert.NotContains(t, text, "  needs serial")
}
