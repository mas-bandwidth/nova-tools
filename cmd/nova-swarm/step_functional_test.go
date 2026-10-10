//go:build functional

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goStep is a Go script step that upper-cases a.txt, and, when the network reaches it, says so
// in the file instead (docs/SPEC-SPRINT.md, a card is a tree of steps:
// a script step runs in its own wall, the network denied and no credential in its
// environment).
const goStep = `package main

import (
	"bytes"
	"net"
	"os"
	"time"
)

func main() {
	b, err := os.ReadFile("a.txt")
	if err != nil {
		panic(err)
	}
	out := bytes.ToUpper(b)
	if c, err := net.DialTimeout("tcp", "1.1.1.1:53", 3*time.Second); err == nil {
		c.Close()
		out = []byte("the network reached the step\n")
	}
	if err := os.WriteFile("a.txt", out, 0o644); err != nil {
		panic(err)
	}
}
`

// TestAGoScriptStepIsBuiltAndRunInItsOwnWall runs a Go script step through the real
// toolchain and the real wall (TestMain builds nova-sandbox): the program is built, run in the
// checkout with the network denied, its hash checked and its commit made.
func TestAGoScriptStepIsBuiltAndRunInItsOwnWall(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("the repository builds real wall backends only on Darwin and Linux, not %s", runtime.GOOS)
	}
	realWallBackend(t)
	repo := stepRepo(t)
	var prog strings.Builder
	for _, l := range strings.Split(strings.TrimRight(goStep, "\n"), "\n") {
		prog.WriteString("  " + l + "\n")
	}
	want := sha256.Sum256([]byte("FOO AND FOO\n"))
	card := "RESULT: s1-7 sha=0123456789ab\nPATHS: a.txt\n\nSTEP 1. Enter with cd repo.\n" +
		"STEP 2. Upper-case.\n  PATHS: a.txt\n  COMMIT: upper-case a\n  VERDICT: the hash holds\n  SCRIPT: go\n  ```go\n" + prog.String() + "  ```\n" +
		"  POST: sha256 a.txt " + hex.EncodeToString(want[:]) + "\nSTEP 3. End as JOB.md says.\n"
	work, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	exit, stdout, stderr := runSwarm(t, "step", "--card", writeLintCard(t, "s1-7.md", card), "--dir", repo, "--work", work, "--sandbox", builtSandbox)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "STEP OK step 2: ok ")
	b, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "FOO AND FOO\n", string(b), "the program ran, with no network")
	assert.Equal(t, "upper-case a\n", runGit(t, repo, "log", "-1", "--format=%s"))
}

// TestAGoVetPostRunsInTheStepsWall is a regex step whose POST is `exit0 go vet ./...`, run in
// the real wall with GOCACHE naming a cache outside the wall (this process's own): the step's
// go gets a private build cache in its temp and reads no shared one (docs/SPEC-SPRINT.md, a
// card is a tree of steps).
func TestAGoVetPostRunsInTheStepsWall(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("the repository builds real wall backends only on Darwin and Linux, not %s", runtime.GOOS)
	}
	realWallBackend(t)
	repo := stepRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/step\n\ngo 1.22\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.go"), []byte("package step\n\nfunc Foo() int { return 1 }\n"), 0o644))
	runGit(t, repo, "add", "go.mod", "a.go")
	runGit(t, repo, "commit", "-q", "-m", "module")
	card := "RESULT: s1-6 sha=0123456789ab\nPATHS: a.go\n\nSTEP 1. Enter with cd repo.\n" +
		"STEP 2. Rename.\n  PATHS: a.go\n  COMMIT: rename Foo to Bar\n  VERDICT: the vet is clean\n  SCRIPT: regex\n  ```\n  s/Foo/Bar/\n  ```\n  POST: exit0 go vet ./...\nSTEP 3. End as JOB.md says.\n"
	work, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NotEmpty(t, os.Getenv("GOCACHE")+os.Getenv("HOME"), "a build cache outside the wall is what this test is about")
	exit, stdout, stderr := runSwarm(t, "step", "--card", writeLintCard(t, "s1-6.md", card), "--dir", repo, "--work", work, "--sandbox", builtSandbox)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stdout, "STEP OK step 2: ok ")
	assert.Equal(t, "rename Foo to Bar\n", runGit(t, repo, "log", "-1", "--format=%s"))
	_, err = os.Stat(filepath.Join(work, "tmp", "go-build"))
	assert.NoError(t, err, "go vet's build cache is the step's own, in its temp")
}

// realWallBackend is the real wall backend the built nova-sandbox reports on
// this kernel (`nova-sandbox check`), skipping where it reports none.
func realWallBackend(t *testing.T) string {
	t.Helper()
	require.NotEmpty(t, builtSandbox, "TestMain builds nova-sandbox for the real wall run")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, builtSandbox, "check")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	require.NoError(t, err, "nova-sandbox check failed; stdout=%q", string(out))
	fields := strings.Fields(string(out))
	require.GreaterOrEqual(t, len(fields), 2, "malformed nova-sandbox check output: %q", string(out))
	require.Equal(t, "CHECK", fields[0], "malformed nova-sandbox check output: %q", string(out))
	require.Equal(t, "OK", fields[1], "malformed nova-sandbox check output: %q", string(out))
	backend := ""
	for _, field := range fields[2:] {
		if strings.HasPrefix(field, "backend=") {
			backend = strings.TrimPrefix(field, "backend=")
			break
		}
	}
	require.NotEmpty(t, backend, "CHECK OK did not name a backend: %q", string(out))
	if backend == "none" {
		t.Skipf("the built nova-sandbox reports no supported backend on this kernel: %s", strings.TrimSpace(string(out)))
	}
	want := "sandbox-exec"
	if runtime.GOOS == "linux" {
		want = "landlock"
	}
	require.Equal(t, want, backend, "unexpected real wall backend: %q", string(out))
	return backend
}
