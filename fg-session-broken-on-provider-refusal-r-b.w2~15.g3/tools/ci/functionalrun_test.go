package main

import (
	"bytes"
	"strings"
	"testing"
)

func runFunctional(sel string, selCode int, testCode int, args ...string) (int, string, string, *fakeCmdRunner) {
	var out, errb bytes.Buffer
	e := env{stdout: &out, stderr: &errb, getenv: func(string) string { return "" }}
	r := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) {
		if len(c.Args) > 1 && c.Args[0] == "run" && c.Args[1] == "./cmd/nova-ci" {
			return sel, selCode, nil
		}
		return "", testCode, nil
	}}
	code := functionalRun(e, r, args)
	return code, out.String(), errb.String(), r
}

func TestFunctionalRunNothingToRunPrintsTheSelectorsLineAndPasses(t *testing.T) {
	t.Parallel()
	code, out, _, r := runFunctional("CI FUNCTIONAL OK packages=0 reason=no functional file\n", 0, 0, "./a", "./b")
	if code != 0 || out != "functional: CI FUNCTIONAL OK packages=0 reason=no functional file\n" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
	if got := r.lines(); len(got) != 1 || got[0] != "go run ./cmd/nova-ci functional ./a ./b" {
		t.Fatalf("commands %q", got)
	}
}

func TestFunctionalRunRunsTheSelectedPackagesWithTheTierFlags(t *testing.T) {
	t.Parallel()
	code, out, _, r := runFunctional("./internal/x ./internal/y\n^(TestA|TestB)$\n", 0, 0, "--go", "go1", "--p", "3", "--timeout", "90s", "./internal/...")
	if code != 0 || out != "functional: ./internal/x ./internal/y\n" {
		t.Fatalf("exit %d stdout %q", code, out)
	}
	want := []string{
		"go1 run ./cmd/nova-ci functional ./internal/...",
		"go1 test -tags functional -p 3 -count=1 -timeout 90s -run ^(TestA|TestB)$ ./internal/x ./internal/y",
	}
	if got := r.lines(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands\n got %q\nwant %q", got, want)
	}
}

func TestFunctionalRunDefaultsArePTwoAndOneHundredSeconds(t *testing.T) {
	t.Parallel()
	_, _, _, r := runFunctional("./x\nTestX\n", 0, 0, "./x")
	if got := r.lines(); len(got) != 2 || got[1] != "go test -tags functional -p 2 -count=1 -timeout 100s -run TestX ./x" {
		t.Fatalf("commands %q", got)
	}
}

func TestFunctionalRunPassesTheTestsExitCodeThrough(t *testing.T) {
	t.Parallel()
	if code, _, _, _ := runFunctional("./x\nTestX\n", 0, 1, "./x"); code != 1 {
		t.Fatalf("exit %d, want the failing tests' 1", code)
	}
}

func TestFunctionalRunRefusesToRunNothingInSilence(t *testing.T) {
	t.Parallel()
	code, _, errb, r := runFunctional("", 0, 0, "./x")
	if code != 2 || !strings.Contains(errb, "nova-ci functional printed nothing; refusing to run nothing in silence") {
		t.Fatalf("exit %d stderr %q", code, errb)
	}
	if len(r.calls) != 1 {
		t.Fatalf("commands %q: go test ran after an empty selection", r.lines())
	}
}

func TestFunctionalRunASelectorFailureIsRefused(t *testing.T) {
	t.Parallel()
	code, out, _, r := runFunctional("CI FUNCTIONAL OK packages=0\n", 1, 0, "./x")
	if code != 2 || out != "" || len(r.calls) != 1 {
		t.Fatalf("exit %d stdout %q commands %q: a failed selector must not read as nothing to run", code, out, r.lines())
	}
}

func TestFunctionalRunASelectionWithNoPatternIsRefused(t *testing.T) {
	t.Parallel()
	code, _, errb, r := runFunctional("./x\n", 0, 0, "./x")
	if code != 2 || !strings.Contains(errb, "not a package list and a -run pattern") || len(r.calls) != 1 {
		t.Fatalf("exit %d stderr %q commands %q", code, errb, r.lines())
	}
}

func TestFunctionalRunUsageRefusals(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--nope"}, {"--p"}, {"--timeout"}, {"--go"}} {
		if code, _, _, _ := runFunctional("", 0, 0, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
