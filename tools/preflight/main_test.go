package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunner records every command and answers from a script: gofmt lists the
// files in fmtOut, and a command is given the exit code in codes under its
// name (0 when absent). Nothing is started.
type fakeRunner struct {
	calls  []command
	fmtOut string
	codes  map[string]int
}

func (f *fakeRunner) run(c command) (int, error) {
	f.calls = append(f.calls, c)
	if c.name == "fake-gofmt" && f.fmtOut != "" && c.out != nil {
		c.out.Write([]byte(f.fmtOut))
	}
	return f.codes[c.name], nil
}

// named is the calls whose command is name.
func (f *fakeRunner) named(name string) []command {
	var out []command
	for _, c := range f.calls {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

// preflight is the tool in process with the fake toolchain named by GO, GOFMT
// and MAKE, extraEnv after them, run by f in /repo.
func preflight(f *fakeRunner, extraEnv ...string) testkit.Main {
	return func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
		environ := append([]string{"GO=fake-go", "GOFMT=fake-gofmt", "MAKE=fake-make"}, extraEnv...)
		return run(args, env{stdout: stdout, stderr: stderr, environ: environ, dir: "/repo", runner: f})
	}
}

func TestHelpPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Do(t, "--help").Exit(0).Out("Usage:")
	assert.Empty(t, f.calls, "--help ran commands, want none")
	preflight(&fakeRunner{}).Do(t, "-h").Exit(0).Out("Usage:")
}

func TestUnformattedFilesFailTheRunBeforeVetAndTests(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{fmtOut: "internal/x/unformatted.go\n"}
	preflight(f).Do(t, "./internal/x").Exit(1).Err("unformatted.go", "gofmt check FAILED")
	assert.Empty(t, append(f.named("fake-go"), f.named("fake-make")...), "vet or tests ran after a gofmt finding: %v", f.calls)
}

func TestGofmtIsAskedToListTheWholeTreeFromTheRoot(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Run("./internal/x")
	got := f.named("fake-gofmt")
	require.Len(t, got, 1, "want one gofmt call: %+v", got)
	assert.Equal(t, []string{"-l", "."}, got[0].args)
	assert.Equal(t, "/repo", got[0].dir)
}

func TestVetFailureFailsTheRunBeforeTests(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{codes: map[string]int{"fake-go": 1}}
	preflight(f).Do(t, "./internal/x").Exit(1).NotOut("ALL CHECKS PASSED")
	assert.Empty(t, f.named("fake-make"), "tests ran after a vet failure")
}

func TestAFailingMakeFailsTheRunWithItsExitCode(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{codes: map[string]int{"fake-make": 2}}
	preflight(f).Do(t, "./internal/x").Exit(2).NotOut("ALL CHECKS PASSED", "unit tests: OK")
}

func TestPackageArgumentsReachVetAndMake(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Do(t, "./internal/swarm", "./cmd/nova-swarm").Exit(0).Out("ALL CHECKS PASSED")
	vet := f.named("fake-go")
	require.Len(t, vet, 1)
	assert.Equal(t, []string{"vet", "./internal/swarm", "./cmd/nova-swarm"}, vet[0].args)
	mk := f.named("fake-make")
	require.Len(t, mk, 1)
	assert.Equal(t, []string{"test-full", "GO=fake-go", "PKGS=./internal/swarm ./cmd/nova-swarm"}, mk[0].args)
}

func TestRunFlagAndRunEnvReachMakeAsRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		environ []string
		want    string
	}{
		{"-run", []string{"-run", "TestSpecific", "./internal/swarm"}, nil, "RUN=TestSpecific"},
		{"--run", []string{"--run", "TestA|TestB", "./internal/swarm"}, nil, "RUN=TestA|TestB"},
		{"RUN env", []string{"./internal/swarm"}, []string{"RUN=TestEnv"}, "RUN=TestEnv"},
		{"flag beats env", []string{"-run", "TestFlag", "./internal/swarm"}, []string{"RUN=TestEnv"}, "RUN=TestFlag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeRunner{}
			preflight(f, tc.environ...).Do(t, tc.args...).Exit(0)
			mk := f.named("fake-make")
			require.Len(t, mk, 1)
			assert.Equal(t, tc.want, mk[0].args[len(mk[0].args)-1])
		})
	}
}

func TestNoRunPatternPassesNoRunToMake(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Run("./internal/a")
	for _, a := range f.named("fake-make")[0].args {
		assert.False(t, strings.HasPrefix(a, "RUN="), "make was handed %q with no pattern given", a)
	}
}

func TestRunFlagWithoutAPatternIsRefused(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Do(t, "-run").Exit(1).Err("-run requires a regex pattern argument")
	assert.Empty(t, f.calls, "a refused flag ran commands")
}

func TestPKGSIsThePackageSetWhenNoneAreGiven(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f, "PKGS=./internal/fromenv ./cmd/x").Run()
	assert.Equal(t, []string{"vet", "./internal/fromenv", "./cmd/x"}, f.named("fake-go")[0].args)
	assert.Equal(t, "PKGS=./internal/fromenv ./cmd/x", f.named("fake-make")[0].args[2])
}

func TestTheDefaultPackageSetIsCmdAndInternal(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Run()
	assert.Equal(t, []string{"vet", "./cmd/...", "./internal/..."}, f.named("fake-go")[0].args)
}

func TestPackageArgumentsBeatPKGS(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f, "PKGS=./internal/fromenv").Run("./internal/given")
	assert.Equal(t, []string{"vet", "./internal/given"}, f.named("fake-go")[0].args)
}

func TestTheHostGuardReachesEveryStep(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Run("./internal/x")
	for _, c := range f.calls {
		assert.Equal(t, "1", lookup(c.env, "NOVA_TEST_NO_HOST"), "%s ran without NOVA_TEST_NO_HOST=1: %v", c.name, c.env)
	}
	// A caller that chose another value keeps it.
	g := &fakeRunner{}
	preflight(g, "NOVA_TEST_NO_HOST=0").Run("./internal/x")
	assert.Equal(t, "0", lookup(g.calls[0].env, "NOVA_TEST_NO_HOST"), "a caller's NOVA_TEST_NO_HOST=0 was changed")
}

func TestEveryStepRunsInTheRepositoryRoot(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(f).Run("./internal/x")
	for _, c := range f.calls {
		assert.Equal(t, "/repo", c.dir, "%s ran outside the root", c.name)
	}
}

func TestToolchainDefaultsAreGoGofmtMake(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	run([]string{"./internal/x"}, env{stdout: io.Discard, stderr: io.Discard, environ: nil, dir: "/repo", runner: f})
	var names []string
	for _, c := range f.calls {
		names = append(names, c.name)
	}
	assert.Equal(t, []string{"gofmt", "go", "make"}, names)
	assert.Equal(t, "GO=go", f.calls[2].args[1], "make was handed the wrong GO")
}

// TestTheMakefileTestFullRecipeTakesWhatPreflightHandsIt reads the real
// Makefile with `make -n`, which prints the recipe without running it: the
// handoff is `test-full GO= PKGS= RUN=`, and the recipe that comes out must
// carry each of them to the go test line.
func TestTheMakefileTestFullRecipeTakesWhatPreflightHandsIt(t *testing.T) {
	t.Parallel()
	testkit.SkipOn(t, "windows", "make is not a Windows tool")
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("no make on PATH")
	}
	root, err := startRoot()
	require.NoError(t, err)
	cmd := exec.Command(makeBin, "-n", "test-full", "GO=fake-go", "PKGS=./internal/a ./internal/b", "RUN=TestX")
	cmd.Dir = root
	raw, err := cmd.CombinedOutput()
	require.NoError(t, err, "make -n test-full: %s", raw)
	want := `fake-go test -count=1 -run "TestX" ./internal/a ./internal/b`
	assert.Contains(t, strings.Join(strings.Fields(string(raw)), " "), want, "the test-full recipe")
	cmd = exec.Command(makeBin, "-n", "test-full", "GO=fake-go", "PKGS=./internal/a")
	cmd.Dir = root
	raw, _ = cmd.CombinedOutput()
	flat := strings.Join(strings.Fields(string(raw)), " ")
	assert.NotContains(t, flat, "-run", "without RUN the recipe carries -run")
	assert.Contains(t, flat, "fake-go test -count=1 ./internal/a", "without RUN the recipe drops the packages")
}

func TestRepoRootFindsGoModFromASubdirectory(t *testing.T) {
	t.Parallel()
	dir := testkit.Tree(t, t.TempDir(), map[string]string{"go.mod": "module x\n"})
	sub := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	got, err := repoRoot(sub)
	require.NoError(t, err)
	assert.Equal(t, dir, got)
}

func TestRepoRootRefusesATreeWithNoGoMod(t *testing.T) {
	t.Parallel()
	// A temp dir has no go.mod of its own, and none above it on any machine
	// this runs on; if one does, the refusal is not reachable from here.
	dir := t.TempDir()
	if _, err := repoRoot(dir); err == nil {
		t.Skip("a go.mod sits above the temp directory on this machine")
	}
}
