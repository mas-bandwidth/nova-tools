//go:build functional

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// A directory generated from a ledger in a fixture checkout is admitted whole by
// `nova-sprint add --brief-dir` into a throwaway store, and the one-wave plan holds
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
	write("cmd/a/a_test.go", "package main\nimport \"testing\"\nfunc TestA(t *testing.T) { t.Setenv(\"A\", \"a\") }\n")
	write("cmd/b/b_test.go", "package main\nimport \"testing\"\nfunc TestB(t *testing.T) { t.Chdir(\".\") }\n")
	write("internal/c/c_test.go", "package c\nimport (\"os\"; \"testing\")\nfunc TestC(t *testing.T) { _ = os.Setenv(\"C\", \"c\") }\n")
	write("internal/ci/parallel_class_test.go", "package ci\n")
	write("internal/ci/testdata/serial-tests_allowlist.txt", "# ceiling: 3\ncmd/a/a_test.go:TestA serial: t.Setenv\ncmd/b/b_test.go:TestB serial: t.Chdir\ninternal/c/c_test.go:TestC serial: os.Setenv\n")
	git("init", "-q", "-b", "sprint/s")
	origin := filepath.Join(t.TempDir(), "example", "repo.git")
	require.NoError(t, os.MkdirAll(filepath.Dir(origin), 0o755))
	bare := exec.Command("git", "init", "--bare", "-q", origin)
	bareOut, err := bare.CombinedOutput()
	require.NoError(t, err, "git init --bare: %s", bareOut)
	git("remote", "add", "origin", origin)
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	git("push", "-q", "origin", "sprint/s")

	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "ledger", "--ledger", "serial-tests", "--repo-dir", repo, "--repo", origin, "--out", out)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "CARDS OK dir="+out+" cards=3 waves=1 tier=flash shared-paths=yes")
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.tsv"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "serial-tests-cmd-b-b\tcmd/b/b_test.go\tinternal/ci TestEveryTestOpensWithTParallel\t1\t-\n")
	brief, err := os.ReadFile(filepath.Join(out, "serial-tests-cmd-a-a.md"))
	require.NoError(t, err)
	assert.Contains(t, string(brief), "REPO: "+origin+"\nBASE: sprint/s\n")

	sprint := filepath.Join(t.TempDir(), "nova-sprint")
	if runtime.GOOS == "windows" {
		sprint += ".exe"
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", sprint, "../nova-sprint")
	build.Env = goenv.Clean(os.Environ())
	o, err := build.CombinedOutput()
	require.NoError(t, err, "building nova-sprint: %s", o)
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	require.NoError(t, fn.Load(context.Background(), client))
	const actor = "fixture-seat"
	home := t.TempDir()
	pushDir := filepath.Join(home, actor+"-working", "inbox", "sprint-judgments")
	require.NoError(t, os.MkdirAll(pushDir, 0o755))
	var env []string
	for _, entry := range goenv.Clean(os.Environ()) {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "HOME", "NOVA_SPRINT_SERVER", "NOVA_SPRINT_REDIS", "NOVA_REDIS_ADDR", "NOVA_SPRINT_ACTOR":
			continue
		}
		env = append(env, entry)
	}
	// The poisoned server proves every subprocess stays on the fixture Redis.
	env = append(env, "HOME="+home, "NOVA_SPRINT_REDIS="+addr, "NOVA_SPRINT_ACTOR="+actor, "NOVA_SPRINT_SERVER=127.0.0.1:0")
	sprintRun := func(args ...string) (int, string) {
		t.Helper()
		cmd := exec.Command(sprint, append(args, "--redis", addr)...)
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
	// A coordinator may add only after its session answers a check delivered by
	// the push loop. Use a throwaway folder as this test's session.
	code, text = sprintRun("seat", "push", "--harness", "claude", "--target", pushDir)
	require.Equal(t, 1, code, text) // recorded, but no check has been answered yet
	loop := exec.Command(sprint, "inbox", "--wait", "--push", "seat", "--timeout", "100ms", "--redis", addr)
	loop.Env = env
	require.NoError(t, loop.Start())
	defer func() {
		_ = loop.Process.Kill()
		_ = loop.Wait()
	}()
	var proofs []string
	require.Eventually(t, func() bool {
		var err error
		proofs, err = filepath.Glob(filepath.Join(pushDir, "PROOF-*"))
		return err == nil && len(proofs) == 1
	}, 10*time.Second, 20*time.Millisecond, "the push loop did not deliver a check")
	nonce := strings.TrimPrefix(filepath.Base(proofs[0]), "PROOF-")
	code, text = sprintRun("seat", "pong", nonce)
	require.Equal(t, 0, code, text)
	code, text = sprintRun("seat", "push")
	require.Equal(t, 0, code, text)
	code, text = sprintRun("add", "--stream", "s", "--brief-dir", out)
	assert.Equal(t, 2, code, "without --allow-shared-paths the cards' shared ledger is refused: %s", text)
	code, text = sprintRun("add", "--stream", "s", "--brief-dir", out, "--allow-shared-paths")
	require.Equal(t, 0, code, text)
	assert.Contains(t, text, "ADD OK stream=s cards=3 before=- moved=3 refused=0")
	_, text = sprintRun("card", "serial-tests-cmd-b-b")
	assert.Contains(t, text, "ready")
	assert.NotContains(t, text, "  needs serial")
	_, text = sprintRun("card", "serial-tests-cmd-a-a")
	assert.Contains(t, text, "ready")
	assert.NotContains(t, text, "  needs serial")
}
