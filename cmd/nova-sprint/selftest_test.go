package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selftestChildEnv marks this test binary run as one case's child: the gate
// resolves go and the lander resolves git from the process's own PATH, so a
// case that needs one of them absent is this binary once more under a PATH of
// its own (the pattern internal/testredis's again is).
const selftestChildEnv = "NOVA_SELFTEST_CHILD"

// TestSelftestLandsOneCardThroughTheTreeGate pins the install gate a hand step
// of an install of 2026-10-04 becomes (docs/SPEC-SPRINT.md section 11,
// selftest): the verb lands one card on a twin through the lander's tree gate
// — the base of its clone holds a go.mod and a main.go, so `go build ./...`
// and `go vet ./...` really run, the defect that install hid while its unit
// tests were green — and, when go cannot run at all, names the land step and
// the lander's own reason for the red gate.
func TestSelftestLandsOneCardThroughTheTreeGate(t *testing.T) {
	t.Parallel()
	t.Run("lands one card and prints one SELFTEST line", func(t *testing.T) {
		t.Parallel()
		if os.Getenv(selftestChildEnv) != "land" {
			// the case runs as a child of this binary: git's own directory ahead
			// of the caller's PATH, so a PATH wrapper that intercepts pushes
			// cannot redirect the flow's landings away from the bare
			// repositories the selftest makes, and go still resolves from the
			// caller's PATH
			out := selftestAgain(t, selftestChildPath(t, false), "land", "lands_one_card")
			assert.Contains(t, out, "SELFTEST OK landed=1 ", "the child's line: %s", out)
			return
		}
		dir := t.TempDir()
		ta := newTestApp(t)
		code, out, errs := ta.do("selftest --dir " + dir)
		require.Equal(t, 0, code, "selftest: exit %d\n%s%s", code, out, errs)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		require.Len(t, lines, 1, "the selftest prints one line: %s", out)
		t.Logf("the selftest line: %s", lines[0])
		for _, want := range []string{"SELFTEST OK landed=1 ", " land=", " gate=", " go=go", " dir="} {
			assert.Contains(t, lines[0], want, "the SELFTEST line: %s", lines[0])
		}
		left, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, left, "the selftest removes its fresh directory on success")
	})
	t.Run("names the step and the lander's reason when go cannot run", func(t *testing.T) {
		t.Parallel()
		if os.Getenv(selftestChildEnv) != "go-missing" {
			// the case runs as a child of this binary whose PATH holds only
			// git's own directory: git able, go not, so the tree gate cannot
			// run and the landing is refused
			out := selftestAgain(t, selftestChildPath(t, true), "go-missing", "names_the_step")
			assert.Contains(t, out, "SELFTEST FAILED step=land why=", "the child's line: %s", out)
			assert.Contains(t, out, "go build ./...", "the gate's run is in the reason: %s", out)
			return
		}
		dir := t.TempDir()
		ta := newTestApp(t)
		code, out, errs := ta.do("selftest --dir " + dir)
		require.Equal(t, 1, code, "selftest with go unavailable: exit %d\n%s%s", code, out, errs)
		t.Logf("the selftest line: %s", strings.TrimSpace(errs))
		assert.Contains(t, errs, "SELFTEST FAILED step=land why=", "the failure names the land step: %s", errs)
		assert.Contains(t, errs, "fails the tree gate", "the lander's own reason for the red gate: %s", errs)
		assert.Contains(t, errs, "go build ./...", "the gate's run is in the reason: %s", errs)
		assert.Contains(t, errs, " dir=", "the failure names the fresh directory it keeps: %s", errs)
		left, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, left, 1, "a failure keeps the fresh directory, named in the line")
	})
}

// selftestChildPath is the PATH a case's child runs under: the directory git
// names by --exec-path, which holds the git binary itself — alone, when only
// is the case that makes go unavailable, else ahead of the caller's PATH, so
// the flow's git is git's own wherever a PATH wrapper stands in front of it
// and the gate's go is still the caller's.
func selftestChildPath(t *testing.T, only bool) string {
	t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{}, "--exec-path")
	require.NoError(t, err, "git --exec-path: %s", res.Stderr)
	gitDir := strings.TrimSpace(string(res.Stdout))
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	require.FileExists(t, filepath.Join(gitDir, "git"+suffix), "git's exec path %s holds no git binary, so a PATH holding only it leaves git not able either", gitDir)
	require.NoFileExists(t, filepath.Join(gitDir, "go"+suffix), "git's exec path %s holds a go binary, so the case could not make go unavailable", gitDir)
	if only {
		return gitDir
	}
	return gitDir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// selftestAgain is this test binary once more as one case's child: the PATH
// and the marker that run the case, and its combined output. A child that
// does not pass its own case fails, and this run fails with the child's words.
func selftestAgain(t *testing.T, path, marker, subtest string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "TestSelftestLandsOneCardThroughTheTreeGate/"+subtest, "-test.count=1", "-test.v")
	cmd.Env = []string{selftestChildEnv + "=" + marker, "PATH=" + path}
	for _, k := range []string{"HOME", "TMPDIR", "TMP", "TEMP", "GOCACHE", "NOVA_TEST_NO_HOST"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the %s case as a child of this binary:\n%s", marker, out)
	return string(out)
}
