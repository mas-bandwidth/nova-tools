//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// The fixture is built at test time and is a valid key for nothing. It is the string the
// assertions search the output for: a shim that printed it would have failed.
const shimFixtureValue = "sk-" + "notarealkey" + "0123456789abcdef"

// TestTheCardsShellNeverSeesASecret is the red team's probe, in Go: a shell started
// through the wrapper reports a length of 0 for a secret-named variable and a count of 0
// for every secret name, where the same shell started directly reports 35 and 1
// (nova-tools #1814). The control is one edit: drop pathWithDirFirst/SHELL from
// nativeChildEnv, or exec the real shell instead of the wrapper, and this goes red.
func TestTheCardsShellNeverSeesASecret(t *testing.T) {
	t.Parallel()

	slot := t.TempDir()
	dir, shell, err := writeNativeShellShims(slot)
	require.NoError(t, err, "the shims could not be written")
	require.Equal(t, nativeShellShimDir(slot), dir, "shim dir = %q, want %q", dir, nativeShellShimDir(slot))
	require.Contains(t, []string{"bash", "sh"}, filepath.Base(shell), "SHELL would be pinned at %q, which names no wrapper", shell)

	probe := `echo envlen=${#DEEPSEEK_API_KEY}; echo envnames=$(printenv | grep -c -E "KEY|TOKEN|SECRET")`
	env := append(os.Environ(),
		"DEEPSEEK_API_KEY="+shimFixtureValue,
		"SOME_TOKEN="+shimFixtureValue,
		"lower_case_secret="+shimFixtureValue,
		"MiXeD_KeY_NaMe="+shimFixtureValue,
	)

	// Red: the real shell, the environment as the harness holds it.
	real, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}
	before := runProbe(t, real, probe, env)
	require.Contains(t, before, "envlen="+itoa(len(shimFixtureValue)), "the control did not see the key: %q", before)
	require.NotContains(t, before, "envnames=0", "the control counted no secret names, so this test proves nothing: %q", before)

	// Green: the same probe through the wrapper.
	after := runProbe(t, filepath.Join(dir, "sh"), probe, env)
	require.Contains(t, after, "envlen=0", "the wrapped shell still holds the key's length: %q", after)
	require.Contains(t, after, "envnames=0", "the wrapped shell still carries a secret name: %q", after)
	require.NotContains(t, after, shimFixtureValue, "the wrapper printed the fixture value")
}

// TestTheShimNeverPrintsAValue reads the wrapper's own text: no value, of the fixture or
// of anything else, may appear in it, and the awk it runs prints names only.
func TestTheShimNeverPrintsAValue(t *testing.T) {
	t.Parallel()

	slot := t.TempDir()
	dir, _, err := writeNativeShellShims(slot)
	require.NoError(t, err, "the shims could not be written")
	raw, err := os.ReadFile(filepath.Join(dir, "sh"))
	require.NoError(t, err, "the wrapper could not be read")
	text := string(raw)
	require.NotContains(t, text, "$2", "the wrapper's awk prints more than a name:\n%s", text)
	require.NotContains(t, text, "print $0", "the wrapper's awk prints more than a name:\n%s", text)
	require.Contains(t, text, "command -v awk", "the wrapper does not fail closed when awk is absent:\n%s", text)
	require.Contains(t, text, "exit 127", "the wrapper does not refuse when it cannot list the names:\n%s", text)
}

// TestTheShimIsInTheWallsReadSetAndNotItsWriteSet pins where the wrappers live: under the
// slot, which nativeSandboxArgv passes as --read, and never under the job directory or the
// data home, which it passes as --write and the card can rewrite.
func TestTheShimIsInTheWallsReadSetAndNotItsWriteSet(t *testing.T) {
	t.Parallel()

	cfg := nativeRunConfig{slotDir: filepath.Join("root", "slot"), root: "root", label: "card"}
	dir := nativeShellShimDir(cfg.slotDir)
	jobDir := filepath.Join(cfg.slotDir, "jobs", cfg.label)
	dataHome := filepath.Join(cfg.slotDir, "data")
	require.True(t, within(cfg.slotDir, dir), "the shim %q is outside the slot %q, which is the wall's read set", dir, cfg.slotDir)
	require.False(t, within(jobDir, dir), "the shim %q sits inside a directory the card may write", dir)
	require.False(t, within(dataHome, dir), "the shim %q sits inside a directory the card may write", dir)
}

// TestTheChildEnvPutsTheShimFirstAndPinsShell asserts the two names the harness resolves
// its tool shell through.
func TestTheChildEnvPutsTheShimFirstAndPinsShell(t *testing.T) {
	t.Parallel()

	shim := filepath.Join("slot", "shim")
	shell := filepath.Join(shim, "bash")
	env := nativeChildEnv("data", "job", "tmp", "", "", shim, shell, nil)
	path, ok := lookup(env, "PATH")
	require.True(t, ok, "the child was handed no PATH")
	first := strings.Split(path, string(os.PathListSeparator))[0]
	require.Equal(t, shim, first, "PATH starts with %q, want the shim %q", first, shim)
	got, ok := lookup(env, "SHELL")
	require.True(t, ok, "SHELL = %q (set=%v), want %q", got, ok, shell)
	require.Equal(t, shell, got, "SHELL = %q (set=%v), want %q", got, ok, shell)
	// Exactly once: a second SHELL would let the harness read either.
	n := 0
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == "SHELL" {
			n++
		}
	}
	require.Equal(t, 1, n, "SHELL appears %d times, want 1", n)
}

// TestTheChildEnvIsUnchangedWithoutAShim keeps the windows path and the argv-builder unit
// tests exactly as they were: no shim, no PATH edit, no SHELL.
func TestTheChildEnvIsUnchangedWithoutAShim(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	env := nativeChildEnv("data", "job", "tmp", "", "", "", "", nil)
	path, _ := lookup(env, "PATH")
	require.Equal(t, "/usr/bin", path, "PATH = %q, want the caller's own", path)
	_, ok := lookup(env, "SHELL")
	require.False(t, ok, "SHELL was set with no shim to point it at")
}

func lookup(env []string, want string) (string, bool) {
	for _, kv := range env {
		if name, val, _ := strings.Cut(kv, "="); name == want {
			return val, true
		}
	}
	return "", false
}

func runProbe(t *testing.T, shell, script string, env []string) string {
	t.Helper()
	cmd := exec.Command(shell, "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s: %s", shell, out)
	return string(out)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestTheCardsShellReachesNoGh is #3600's card half: a shell the harness starts with the
// shim first on PATH resolves `gh` to the refusing one, exit 2 naming REFUSED, and a
// counting fake gh later on PATH (the token's call counter, standing in) is never run.
// The control is one edit: drop the gh from writeNativeShellShims and the fake answers.
func TestTheCardsShellReachesNoGh(t *testing.T) {
	t.Parallel()

	fake := t.TempDir()
	counter := filepath.Join(fake, "calls")
	require.NoError(t, testbin.WriteExecutable(filepath.Join(fake, "gh"), []byte("#!/bin/sh\necho call >> '"+counter+"'\nexit 0\n"), 0o755))
	dir, _, err := writeNativeShellShims(t.TempDir())
	require.NoError(t, err, "the shims could not be written: %q, %v", dir, err)
	require.NotEmpty(t, dir, "the shims could not be written: %q, %v", dir, err)
	env := pathWithDirsFirst([]string{"PATH=" + fake + string(os.PathListSeparator) + os.Getenv("PATH")}, dir)
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}
	cmd := exec.Command(sh, "-c", "gh api user")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee, "gh through the card's PATH: err=%v out=%q, want exit 2 naming REFUSED", err, out)
	require.Equal(t, 2, ee.ExitCode(), "gh through the card's PATH: err=%v out=%q, want exit 2 naming REFUSED", err, out)
	require.Contains(t, string(out), "REFUSED", "gh through the card's PATH: err=%v out=%q, want exit 2 naming REFUSED", err, out)
	_, err = os.Stat(counter)
	require.Error(t, err, "the fake gh was called: the card's shell reached a real gh")
}

// TestTheChildEnvResolvesTheBenchGo is the mechanical sprint's hurt of 2026-10-02: a card's
// gate calls bare `go` and `gofmt`, the loop unit's PATH names no Go, and the child was
// handed that PATH. The child's PATH now carries the bench's GOROOT/bin right after the
// shim, so both names resolve to the bench's sdk Go whatever PATH the member started with.
func TestTheChildEnvResolvesTheBenchGo(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sdkBin := filepath.Join(home, "sdk", "go1.26.6", "bin")
	require.NoError(t, os.MkdirAll(sdkBin, 0o755))
	for _, tool := range []string{"go", "gofmt"} {
		require.NoError(t, testbin.WriteExecutable(filepath.Join(sdkBin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, "go", "bin"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(sdkBin, "go"), filepath.Join(home, "go", "bin", "go")))

	shim := filepath.Join("slot", "shim")
	env := nativeChildEnv("data", "job", "tmp", "", "", shim, filepath.Join(shim, "bash"),
		swarm.BenchPath(runtime.GOOS, home, "/usr/bin:/bin"))
	path, ok := lookup(env, "PATH")
	require.True(t, ok, "the child was handed no PATH")
	dirs := filepath.SplitList(path)
	require.GreaterOrEqual(t, len(dirs), 2, "PATH = %q", path)
	require.Equal(t, shim, dirs[0], "the shim is not first on PATH %q", path)
	want, err := filepath.EvalSymlinks(sdkBin)
	require.NoError(t, err)
	for _, tool := range []string{"go", "gofmt"} {
		found := ""
		for _, d := range dirs {
			if fi, err := os.Stat(filepath.Join(d, tool)); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
				found = d
				break
			}
		}
		assert.Equal(t, want, found, "the child's %s resolves in %q, want the bench's sdk Go; PATH %q", tool, found, path)
	}
}

// TestTheChildEnvResolvesEverySdkTool is the fleet tooling probe of 2026-10-04: the child's
// PATH carried GOROOT/bin and nothing else of ~/sdk, so a card's `dotnet`, `cargo`, `java`
// and the rest were "command not found" inside a wall that executes them. The child's PATH
// now carries ~/sdk/bin right after the shim and before the bench's GOROOT/bin.
func TestTheChildEnvResolvesEverySdkTool(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	goroot := filepath.Join(home, "sdk", "go1.26.6", "bin")
	sdkBin := filepath.Join(home, "sdk", "bin")
	require.NoError(t, os.MkdirAll(goroot, 0o755))
	require.NoError(t, os.MkdirAll(sdkBin, 0o755))
	exe := []byte("#!/bin/sh\nexit 0\n")
	require.NoError(t, testbin.WriteExecutable(filepath.Join(goroot, "go"), exe, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(goroot, "go"), filepath.Join(sdkBin, "go")))
	require.NoError(t, testbin.WriteExecutable(filepath.Join(sdkBin, "dotnet"), exe, 0o755))
	realGoroot, err := filepath.EvalSymlinks(goroot)
	require.NoError(t, err)

	shim := filepath.Join("slot", "shim")
	env := nativeChildEnv("data", "job", "tmp", "", "", shim, filepath.Join(shim, "bash"),
		swarm.BenchPath(runtime.GOOS, home, "/usr/bin:/bin"))
	path, ok := lookup(env, "PATH")
	require.True(t, ok, "the child was handed no PATH")
	dirs := filepath.SplitList(path)
	require.GreaterOrEqual(t, len(dirs), 3, "PATH = %q", path)
	assert.Equal(t, []string{shim, sdkBin, realGoroot}, dirs[:3], "the shim, then ~/sdk/bin, then GOROOT/bin; PATH %q", path)
	found := ""
	for _, d := range dirs {
		if fi, err := os.Stat(filepath.Join(d, "dotnet")); err == nil && fi.Mode().Perm()&0o111 != 0 {
			found = d
			break
		}
	}
	assert.Equal(t, sdkBin, found, "the child's dotnet resolves in %q, want the bench's ~/sdk/bin; PATH %q", found, path)
}

// TestAPipeIntoTheShimReachesTheCommand pins the wrapper's stdin carry: the real shell is
// started in the background, and a non-interactive /bin/sh sends a background command's
// stdin to /dev/null, so the wrapper holds stdin open on fd 3 and hands it to the command;
// what is piped into the shim reaches the command and comes back on stdout
// (nova-tools #1814, the shell wrapper).
func TestAPipeIntoTheShimReachesTheCommand(t *testing.T) {
	t.Parallel()
	dir, _, err := writeNativeShellShims(t.TempDir())
	require.NoError(t, err)
	shim := filepath.Join(dir, "sh")
	cmd := exec.Command(shim, "-c", "cat")
	cmd.Stdin = strings.NewReader("into the shim, through the wrapper, to the command\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the shim did not run the command: %q", out)
	require.Equal(t, "into the shim, through the wrapper, to the command\n", string(out),
		"what was piped into the shim did not reach the command: the backgrounded shell lost its stdin")
}
