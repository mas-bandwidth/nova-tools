package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// local_test.go is the contract of `nova-ci local` (nova-tools#4336). Every
// child process the verb would start is answered by a fake runner from a table:
// no test here starts git, bash, make or go, and none reads the clock. The
// checkout is a t.TempDir holding the two files the verb looks for.

// localReply is the fake's answer to one command, matched by its argv's
// leading words.
type localReply struct {
	prefix string
	stdout string
	stderr string
	code   int
}

// localFake answers commands from replies and records every command it saw.
type localFake struct {
	root    string
	replies []localReply
	calls   []localCmd
}

func (f *localFake) answer(c localCmd) (int, error) {
	f.calls = append(f.calls, c)
	line := strings.Join(c.Argv, " ")
	if line == "git rev-parse --show-toplevel" {
		_, err := io.WriteString(c.Stdout, f.root+"\n")
		return 0, err
	}
	for _, r := range f.replies {
		if strings.HasPrefix(line, r.prefix) {
			if _, err := io.WriteString(c.Stdout, r.stdout); err != nil {
				return -1, err
			}
			if _, err := io.WriteString(c.Stderr, r.stderr); err != nil {
				return -1, err
			}
			return r.code, nil
		}
	}
	return 0, nil
}

// call returns the recorded command whose argv starts with prefix, or nil.
func (f *localFake) call(prefix string) *localCmd {
	for i := range f.calls {
		if strings.HasPrefix(strings.Join(f.calls[i].Argv, " "), prefix) {
			return &f.calls[i]
		}
	}
	return nil
}

const localMergeBase = "0123456789abcdef0123456789abcdef01234567"

// localCheckout makes a checkout with the select script and a Makefile holding
// the given targets; with more than one, the test target takes GOTEST_TAGS.
func localCheckout(t *testing.T, targets ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "scripts", "select-packages.sh"), []byte("#!/usr/bin/env bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var mk strings.Builder
	mk.WriteString("GO ?= go\n")
	if len(targets) > 1 {
		mk.WriteString("GOTEST_TAGS ?=\n")
	}
	for _, target := range targets {
		mk.WriteString(target + ": PKGS := ./cmd/...\n" + target + ":\n\t@true\n")
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(mk.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// localFixture is a fake whose base, status and selection answer green; the
// caller adds the make replies.
func localFixture(t *testing.T, selected string, replies ...localReply) *localFake {
	t.Helper()
	f := &localFake{root: localCheckout(t, "test", "test-functional")}
	f.replies = append(f.replies,
		localReply{prefix: "git merge-base", stdout: localMergeBase + "\n"},
		localReply{prefix: "git status"},
		localReply{prefix: "nice -n 15 bash .github/scripts/select-packages.sh", stdout: selected},
	)
	f.replies = append(f.replies, replies...)
	return f
}

func runLocal(t *testing.T, f *localFake, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cmdLocal(args, &out, &errb, f.answer)
	return code, out.String(), errb.String()
}

const localGreenStream = `{"Action":"start","Package":"example.com/m/cmd/a"}
{"Action":"run","Package":"example.com/m/cmd/a","Test":"TestA"}
{"Action":"output","Package":"example.com/m/cmd/a","Test":"TestA","Output":"=== RUN   TestA\n"}
{"Action":"pass","Package":"example.com/m/cmd/a","Test":"TestA","Elapsed":0.3}
{"Action":"pass","Package":"example.com/m/cmd/a","Elapsed":0.4}
{"Action":"pass","Package":"example.com/m/internal/ci","Elapsed":1.6}
CI-SLOW OK packages=2 slowest=example.com/m/internal/ci:1.6s
`

// A green run selects against the merge base, runs the Makefile's test target
// niced at two cores with -count=1 and a private RUNNER_TEMP, prints one PKG
// line per package with its seconds, passes slowtests' verdict through, and
// exits 0.
func TestLocalGreenRunsTheUnitTierAsCIDoes(t *testing.T) {
	t.Parallel()
	f := localFixture(t, "./cmd/a\n./internal/ci\n", localReply{prefix: "nice -n 15 make test ", stdout: localGreenStream})
	code, stdout, stderr := runLocal(t, f)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if mb := f.call("git merge-base"); mb == nil || strings.Join(mb.Argv, " ") != "git merge-base origin/dev HEAD" || mb.Dir != f.root {
		t.Fatalf("merge-base call = %+v, want `git merge-base origin/dev HEAD` in the checkout", mb)
	}
	sel := f.call("nice -n 15 bash")
	if sel == nil || strings.Join(sel.Argv, " ") != "nice -n 15 bash .github/scripts/select-packages.sh "+localMergeBase {
		t.Fatalf("selection call = %+v, want select-packages.sh against the merge base", sel)
	}
	mk := f.call("nice -n 15 make test ")
	if mk == nil {
		t.Fatalf("make test was not run; calls: %+v", f.calls)
	}
	if got, want := strings.Join(mk.Argv, "|"), "nice|-n|15|make|test|PKGS=./cmd/a ./internal/ci|GOTEST_P=2|GOTEST_COUNT_FLAG=-count=1|GOTEST_TAGS="; got != want {
		t.Errorf("make argv = %s, want %s", got, want)
	}
	env := strings.Join(mk.Env, " ")
	if !strings.Contains(env, "GOMAXPROCS=2") || !strings.Contains(env, "RUNNER_TEMP=") {
		t.Errorf("make env = %q, want GOMAXPROCS=2 and a private RUNNER_TEMP", env)
	}
	for _, want := range []string{
		"packages=2 ./cmd/a ./internal/ci",
		"PKG ok      0.4s example.com/m/cmd/a",
		"PKG ok      1.6s example.com/m/internal/ci",
		"CI-SLOW OK packages=2",
		"nova-ci local: packages=2 seconds=2.0s red=0 make-exit=0",
		"nova-ci local: exit=0",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, `"Action"`) {
		t.Errorf("stdout carries raw TestEvent JSON:\n%s", stdout)
	}
}

// A red test is named with the tail of its own output, and the verb exits 1.
func TestLocalRedNamesTheTestWithItsOutput(t *testing.T) {
	t.Parallel()
	stream := `{"Action":"run","Package":"example.com/m/cmd/a","Test":"TestB"}
{"Action":"output","Package":"example.com/m/cmd/a","Test":"TestB","Output":"    b_test.go:9: got 1, want 2\n"}
{"Action":"fail","Package":"example.com/m/cmd/a","Test":"TestB","Elapsed":0.1}
{"Action":"fail","Package":"example.com/m/cmd/a","Elapsed":0.2}
`
	f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: stream, code: 2})
	code, stdout, _ := runLocal(t, f)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, stdout)
	}
	for _, want := range []string{
		"PKG FAIL    0.2s example.com/m/cmd/a",
		"RED package=example.com/m/cmd/a test=TestB",
		"    b_test.go:9: got 1, want 2",
		"red=1 make-exit=2",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// A package that does not build is red with the compiler's words.
func TestLocalBuildFailureIsRed(t *testing.T) {
	t.Parallel()
	stream := `{"ImportPath":"example.com/m/cmd/a [example.com/m/cmd/a.test]","Action":"build-output","Output":"# example.com/m/cmd/a\n"}
{"ImportPath":"example.com/m/cmd/a [example.com/m/cmd/a.test]","Action":"build-output","Output":"cmd/a/a.go:3:1: syntax error\n"}
{"Action":"fail","Package":"example.com/m/cmd/a","Elapsed":0,"FailedBuild":"example.com/m/cmd/a [example.com/m/cmd/a.test]"}
`
	f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: stream, code: 2})
	code, stdout, _ := runLocal(t, f)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, stdout)
	}
	for _, want := range []string{"RED package=example.com/m/cmd/a test=-", "RED build:", "cmd/a/a.go:3:1: syntax error"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// Green tests over the unit budgets fail make test with no red test: the
// CI-SLOW line is printed and the verb exits 2, as slowtests does.
func TestLocalOverBudgetExitsTwo(t *testing.T) {
	t.Parallel()
	stream := `{"Action":"pass","Package":"example.com/m/cmd/a","Test":"TestSlow","Elapsed":3.1}
{"Action":"pass","Package":"example.com/m/cmd/a","Elapsed":3.2}
CI-SLOW test=TestSlow package=example.com/m/cmd/a seconds=3.1s budget=1s
`
	f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: stream, code: 2})
	code, stdout, _ := runLocal(t, f)
	if code != 2 {
		t.Fatalf("exit = %d, want 2\n%s", code, stdout)
	}
	for _, want := range []string{"CI-SLOW test=TestSlow", "red=0 make-exit=2", "over the unit budgets"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// A change that selects nothing prints CI's words and runs nothing.
func TestLocalNothingSelectedRunsNothing(t *testing.T) {
	t.Parallel()
	f := localFixture(t, "")
	code, stdout, _ := runLocal(t, f, "--base", "origin/main")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "nothing to test for this change") {
		t.Errorf("stdout lacks CI's words:\n%s", stdout)
	}
	if mb := f.call("git merge-base"); mb == nil || strings.Join(mb.Argv, " ") != "git merge-base origin/main HEAD" {
		t.Errorf("--base was not used: %+v", mb)
	}
	if f.call("nice -n 15 make") != nil {
		t.Error("make ran for an empty selection")
	}
}

// --functional runs the same make test with the functional build tag, as CI's
// merge-group and push legs do; its red is the verb's red.
func TestLocalFunctionalAddsTheTag(t *testing.T) {
	t.Parallel()
	stream := `{"Action":"output","Package":"example.com/m/cmd/a","Test":"TestStore","Output":"    store_test.go:12: redis: connection refused\n"}
{"Action":"fail","Package":"example.com/m/cmd/a","Test":"TestStore","Elapsed":0.5}
{"Action":"fail","Package":"example.com/m/cmd/a","Elapsed":0.6}
`
	f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: stream, code: 2})
	code, stdout, _ := runLocal(t, f, "--functional")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, stdout)
	}
	mk := f.call("nice -n 15 make test ")
	if mk == nil || strings.Join(mk.Argv, "|") != "nice|-n|15|make|test|PKGS=./cmd/a|GOTEST_P=2|GOTEST_COUNT_FLAG=-count=1|GOTEST_TAGS=functional" {
		t.Fatalf("make call = %+v, want the test target with GOTEST_TAGS=functional", mk)
	}
	for _, want := range []string{"RED package=example.com/m/cmd/a test=TestStore", "redis: connection refused"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// Uncommitted Go files are tested but not selected; the verb says so.
func TestLocalNamesUncommittedGoFiles(t *testing.T) {
	t.Parallel()
	f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: localGreenStream})
	f.replies = append([]localReply{{prefix: "git status", stdout: " M cmd/a/a.go\n?? cmd/b/b.go\n"}}, f.replies...)
	code, _, stderr := runLocal(t, f)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr, "NOTE 2 uncommitted Go file(s)") {
		t.Errorf("stderr lacks the uncommitted note:\n%s", stderr)
	}
}

// Every refusal prints one line naming the door and exits 2.
func TestLocalRefusalsPrint(t *testing.T) {
	t.Parallel()
	plain := func(t *testing.T) *localFake { return localFixture(t, "./cmd/a\n") }
	cases := []struct {
		name string
		fake func(t *testing.T) *localFake
		args []string
		want string
	}{
		{"argument", plain, []string{"./cmd/a"}, "unexpected argument"},
		{"flag", plain, []string{"--bogus"}, "bogus"},
		{"empty base", plain, []string{"--base", " "}, "--base wants the ref"},
		{"not a checkout", func(t *testing.T) *localFake {
			f := plain(t)
			f.root = t.TempDir()
			return f
		}, nil, "go test -p 2 -count=1 <packages>"},
		{"no functional tags", func(t *testing.T) *localFake {
			f := plain(t)
			f.root = localCheckout(t, "test")
			return f
		}, []string{"--functional"}, "takes no GOTEST_TAGS"},
		{"no merge base", func(t *testing.T) *localFake {
			f := plain(t)
			f.replies = append([]localReply{{prefix: "git merge-base", stderr: "fatal: Not a valid object name origin/dev", code: 128}}, f.replies...)
			return f
		}, nil, "no merge base between origin/dev and HEAD"},
		{"selection fails", func(t *testing.T) *localFake {
			f := plain(t)
			f.replies = append([]localReply{{prefix: "nice -n 15 bash", stderr: "go: go.mod not found", code: 1}}, f.replies...)
			return f
		}, nil, "select-packages.sh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := tc.fake(t)
			code, stdout, stderr := runLocal(t, f, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "run: nova-ci help") {
				t.Errorf("stderr = %q, want it to say %q and name the door", stderr, tc.want)
			}
			if f.call("nice -n 15 make") != nil {
				t.Error("make ran after a refusal")
			}
		})
	}
}

// The GOTEST_P=2 the verb passes is a knob the Makefile's test target reads:
// a variable nothing consumes would leave -p 2 resting on GOMAXPROCS alone
// while the help and the printed command claimed it.
func TestMakefileTestTargetTakesGOTEST_P(t *testing.T) {
	t.Parallel()
	mk, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	var recipe string
	lines := strings.Split(string(mk), "\n")
	for i, line := range lines {
		if line == "test:" && i+1 < len(lines) {
			recipe = lines[i+1]
		}
	}
	if recipe == "" {
		t.Fatal("the Makefile has no `test:` rule followed by its recipe")
	}
	if !strings.Contains(recipe, "-p $(GOTEST_P)") {
		t.Errorf("make test does not pass -p $(GOTEST_P); nova-ci local's GOTEST_P=%s would be a phantom:\n%s", localCores, recipe)
	}
}
