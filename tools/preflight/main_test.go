package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
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

// preflight runs the tool with the fake toolchain named by GO, GOFMT and MAKE
// and returns its exit code and both streams.
func preflight(t *testing.T, f *fakeRunner, extraEnv []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, env{
		stdout:  &out,
		stderr:  &errb,
		environ: append([]string{"GO=fake-go", "GOFMT=fake-gofmt", "MAKE=fake-make"}, extraEnv...),
		dir:     "/repo",
		runner:  f,
	})
	return code, out.String(), errb.String()
}

func TestHelpPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	code, out, _ := preflight(t, f, nil, "--help")
	if code != 0 || !strings.Contains(out, "Usage:") {
		t.Fatalf("--help: code=%d out=%q", code, out)
	}
	if len(f.calls) != 0 {
		t.Errorf("--help ran %d commands, want none", len(f.calls))
	}
	if code, out, _ := preflight(t, &fakeRunner{}, nil, "-h"); code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("-h: code=%d out=%q", code, out)
	}
}

func TestUnformattedFilesFailTheRunBeforeVetAndTests(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{fmtOut: "internal/x/unformatted.go\ndeprecated/old/old.go\n"}
	code, _, errOut := preflight(t, f, nil, "./internal/x")
	if code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	if !strings.Contains(errOut, "unformatted.go") || !strings.Contains(errOut, "gofmt check FAILED") {
		t.Errorf("stderr does not name the unformatted file:\n%s", errOut)
	}
	if strings.Contains(errOut, "deprecated/old") {
		t.Errorf("a file under deprecated/ was reported:\n%s", errOut)
	}
	if len(f.named("fake-go"))+len(f.named("fake-make")) != 0 {
		t.Errorf("vet or tests ran after a gofmt finding: %v", f.calls)
	}
}

func TestDeprecatedFilesAloneDoNotFailGofmt(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{fmtOut: "deprecated/old/old.go\n"}
	code, out, _ := preflight(t, f, nil, "./internal/x")
	if code != 0 || !strings.Contains(out, "gofmt: OK") {
		t.Fatalf("code=%d out=%s", code, out)
	}
}

func TestGofmtIsAskedToListTheWholeTreeFromTheRoot(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(t, f, nil, "./internal/x")
	got := f.named("fake-gofmt")
	if len(got) != 1 || !reflect.DeepEqual(got[0].args, []string{"-l", "."}) || got[0].dir != "/repo" {
		t.Errorf("gofmt calls = %+v, want one `-l .` in /repo", got)
	}
}

func TestVetFailureFailsTheRunBeforeTests(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{codes: map[string]int{"fake-go": 1}}
	code, out, _ := preflight(t, f, nil, "./internal/x")
	if code != 1 {
		t.Fatalf("code=%d, want vet's 1", code)
	}
	if len(f.named("fake-make")) != 0 {
		t.Errorf("tests ran after a vet failure")
	}
	if strings.Contains(out, "ALL CHECKS PASSED") {
		t.Errorf("a failed run printed ALL CHECKS PASSED:\n%s", out)
	}
}

func TestAFailingMakeFailsTheRunWithItsExitCode(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{codes: map[string]int{"fake-make": 2}}
	code, out, _ := preflight(t, f, nil, "./internal/x")
	if code != 2 {
		t.Fatalf("code=%d, want make's 2", code)
	}
	if strings.Contains(out, "ALL CHECKS PASSED") || strings.Contains(out, "unit tests: OK") {
		t.Errorf("a failed make printed success:\n%s", out)
	}
}

func TestPackageArgumentsReachVetAndMake(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	code, out, _ := preflight(t, f, nil, "./internal/swarm", "./cmd/nova-swarm")
	if code != 0 || !strings.Contains(out, "ALL CHECKS PASSED") {
		t.Fatalf("code=%d out=%s", code, out)
	}
	vet := f.named("fake-go")
	if len(vet) != 1 || !reflect.DeepEqual(vet[0].args, []string{"vet", "./internal/swarm", "./cmd/nova-swarm"}) {
		t.Errorf("vet calls = %+v", vet)
	}
	mk := f.named("fake-make")
	want := []string{"test-full", "GO=fake-go", "PKGS=./internal/swarm ./cmd/nova-swarm"}
	if len(mk) != 1 || !reflect.DeepEqual(mk[0].args, want) {
		t.Errorf("make calls = %+v, want args %v", mk, want)
	}
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeRunner{}
			if code, _, _ := preflight(t, f, tc.environ, tc.args...); code != 0 {
				t.Fatalf("code=%d", code)
			}
			mk := f.named("fake-make")
			if len(mk) != 1 || mk[0].args[len(mk[0].args)-1] != tc.want {
				t.Errorf("make args = %v, want last %q", mk, tc.want)
			}
		})
	}
}

func TestNoRunPatternPassesNoRunToMake(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(t, f, nil, "./internal/a")
	for _, a := range f.named("fake-make")[0].args {
		if strings.HasPrefix(a, "RUN=") {
			t.Errorf("make was handed %q with no pattern given", a)
		}
	}
}

func TestRunFlagWithoutAPatternIsRefused(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	code, _, errOut := preflight(t, f, nil, "-run")
	if code != 1 || !strings.Contains(errOut, "-run requires a regex pattern argument") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
	if len(f.calls) != 0 {
		t.Errorf("a refused flag ran commands: %v", f.calls)
	}
}

func TestPKGSIsThePackageSetWhenNoneAreGiven(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(t, f, []string{"PKGS=./internal/fromenv ./cmd/x"})
	vet := f.named("fake-go")[0].args
	if !reflect.DeepEqual(vet, []string{"vet", "./internal/fromenv", "./cmd/x"}) {
		t.Errorf("vet args = %v", vet)
	}
	mk := f.named("fake-make")[0].args
	if mk[2] != "PKGS=./internal/fromenv ./cmd/x" {
		t.Errorf("make args = %v", mk)
	}
}

func TestTheDefaultPackageSetIsCmdAndInternal(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(t, f, nil)
	vet := f.named("fake-go")[0].args
	if !reflect.DeepEqual(vet, []string{"vet", "./cmd/...", "./internal/..."}) {
		t.Errorf("vet args = %v", vet)
	}
}

func TestPackageArgumentsBeatPKGS(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(t, f, []string{"PKGS=./internal/fromenv"}, "./internal/given")
	if vet := f.named("fake-go")[0].args; !reflect.DeepEqual(vet, []string{"vet", "./internal/given"}) {
		t.Errorf("vet args = %v", vet)
	}
}

func TestTheHostGuardReachesEveryStep(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(t, f, nil, "./internal/x")
	for _, c := range f.calls {
		if lookup(c.env, "NOVA_TEST_NO_HOST") != "1" {
			t.Errorf("%s ran without NOVA_TEST_NO_HOST=1: %v", c.name, c.env)
		}
	}
	// A caller that chose another value keeps it.
	g := &fakeRunner{}
	preflight(t, g, []string{"NOVA_TEST_NO_HOST=0"}, "./internal/x")
	if got := lookup(g.calls[0].env, "NOVA_TEST_NO_HOST"); got != "0" {
		t.Errorf("a caller's NOVA_TEST_NO_HOST=0 became %q", got)
	}
}

func TestEveryStepRunsInTheRepositoryRoot(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	preflight(t, f, nil, "./internal/x")
	for _, c := range f.calls {
		if c.dir != "/repo" {
			t.Errorf("%s ran in %q, want /repo", c.name, c.dir)
		}
	}
}

func TestToolchainDefaultsAreGoGofmtMake(t *testing.T) {
	t.Parallel()
	f := &fakeRunner{}
	var out, errb bytes.Buffer
	run([]string{"./internal/x"}, env{stdout: &out, stderr: &errb, environ: nil, dir: "/repo", runner: f})
	var names []string
	for _, c := range f.calls {
		names = append(names, c.name)
	}
	if !reflect.DeepEqual(names, []string{"gofmt", "go", "make"}) {
		t.Errorf("commands = %v", names)
	}
	if got := f.calls[2].args[1]; got != "GO=go" {
		t.Errorf("make was handed %q, want GO=go", got)
	}
}

// TestTheMakefileTestFullRecipeTakesWhatPreflightHandsIt reads the real
// Makefile with `make -n`, which prints the recipe without running it: the
// handoff is `test-full GO= PKGS= RUN=`, and the recipe that comes out must
// carry each of them to the go test line.
func TestTheMakefileTestFullRecipeTakesWhatPreflightHandsIt(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("make is not a Windows tool")
	}
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("no make on PATH")
	}
	root, err := startRoot()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(makeBin, "-n", "test-full", "GO=fake-go", "PKGS=./internal/a ./internal/b", "RUN=TestX")
	cmd.Dir = root
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n test-full: %v\n%s", err, raw)
	}
	want := `fake-go test -count=1 -run "TestX" ./internal/a ./internal/b`
	if !strings.Contains(strings.Join(strings.Fields(string(raw)), " "), want) {
		t.Errorf("the test-full recipe is not %q:\n%s", want, raw)
	}
	cmd = exec.Command(makeBin, "-n", "test-full", "GO=fake-go", "PKGS=./internal/a")
	cmd.Dir = root
	raw, _ = cmd.CombinedOutput()
	if flat := strings.Join(strings.Fields(string(raw)), " "); strings.Contains(flat, "-run") || !strings.Contains(flat, "fake-go test -count=1 ./internal/a") {
		t.Errorf("without RUN the recipe carries -run or drops the packages:\n%s", raw)
	}
}

func TestRepoRootFindsGoModFromASubdirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := repoRoot(sub)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("repoRoot = %s, want %s", got, dir)
	}
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
