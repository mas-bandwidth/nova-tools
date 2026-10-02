package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	root     string
	replies  []localReply
	calls    []localCmd
	selected []string
	selErr   error
	selAsked []string
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

// localCheckout makes a checkout with a go.mod and a Makefile holding
// the given targets; with more than one, the test target takes GOTEST_TAGS.
func localCheckout(t *testing.T, targets ...string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644))
	var mk strings.Builder
	mk.WriteString("GO ?= go\n")
	if len(targets) > 1 {
		mk.WriteString("GOTEST_TAGS ?=\n")
	}
	for _, target := range targets {
		mk.WriteString(target + ": PKGS := ./cmd/...\n" + target + ":\n\t@true\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "Makefile"), []byte(mk.String()), 0o644))
	return root
}

// localFixture is a fake whose base and status answer green and whose selection
// is the packages in selected (one per line); the caller adds the make replies.
// The selection itself is pkgselect's, tested in its own package; the fixture
// stands in for it (localSelected records what it was asked).
func localFixture(t *testing.T, selected string, replies ...localReply) *localFake {
	t.Helper()
	f := &localFake{root: localCheckout(t, "test", "test-functional"), selected: strings.Fields(selected)}
	f.replies = append(f.replies,
		localReply{prefix: "git merge-base", stdout: localMergeBase + "\n"},
		localReply{prefix: "git status"},
	)
	f.replies = append(f.replies, replies...)
	return f
}

func runLocal(t *testing.T, f *localFake, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := cmdLocal(args, &out, &errb, f.answer, f.selector)
	return code, out.String(), errb.String()
}

// selector is the fake's package selection: the packages the fixture names, or
// its error.
func (f *localFake) selector(root, base string) ([]string, error) {
	f.selAsked = append(f.selAsked, root+" "+base)
	return f.selected, f.selErr
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
	require.Equal(t, 0, code, "exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	mb := f.call("git merge-base")
	require.NotNil(t, mb, "merge-base call = %+v, want `git merge-base origin/dev HEAD` in the checkout", mb)
	require.Equal(t, "git merge-base origin/dev HEAD", strings.Join(mb.Argv, " "), "merge-base call = %+v, want `git merge-base origin/dev HEAD` in the checkout", mb)
	require.Equal(t, f.root, mb.Dir, "merge-base call = %+v, want `git merge-base origin/dev HEAD` in the checkout", mb)
	require.Len(t, f.selAsked, 1, "selection asked = %v, want the checkout against the merge base", f.selAsked)
	require.Equal(t, f.root+" "+localMergeBase, f.selAsked[0], "selection asked = %v, want the checkout against the merge base", f.selAsked)
	mk := f.call("nice -n 15 make test ")
	require.NotNil(t, mk, "make test was not run; calls: %+v", f.calls)
	got, want := strings.Join(mk.Argv, "|"), "nice|-n|15|make|test|PKGS=./cmd/a ./internal/ci|GOTEST_P=2|GOTEST_COUNT_FLAG=-count=1|GOTEST_TAGS="
	assert.Equal(t, want, got, "make argv = %s, want %s", got, want)
	env := strings.Join(mk.Env, " ")
	assert.Contains(t, env, "GOMAXPROCS=2", "make env = %q, want GOMAXPROCS=2 and a private RUNNER_TEMP", env)
	assert.Contains(t, env, "RUNNER_TEMP=", "make env = %q, want GOMAXPROCS=2 and a private RUNNER_TEMP", env)
	for _, want := range []string{
		"packages=2 ./cmd/a ./internal/ci",
		"PKG ok      0.4s example.com/m/cmd/a",
		"PKG ok      1.6s example.com/m/internal/ci",
		"CI-SLOW OK packages=2",
		"nova-ci local: packages=2 seconds=2.0s red=0 make-exit=0",
		"nova-ci local: exit=0",
	} {
		assert.Contains(t, stdout, want, "stdout lacks %q:\n%s", want, stdout)
	}
	assert.NotContains(t, stdout, `"Action"`, "stdout carries raw TestEvent JSON:\n%s", stdout)
}

// --dry-run prints the selection and the make line the real run would start,
// from the same path, and starts no make.
func TestLocalDryRunShowsTheSelectionAndRunsNothing(t *testing.T) {
	t.Parallel()
	f := localFixture(t, "./cmd/a\n./internal/ci\n")
	code, stdout, stderr := runLocal(t, f, "--dry-run")
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	assert.Nil(t, f.call("nice -n 15 make"), "make ran under --dry-run")
	assert.Contains(t, stdout, "packages=2 ./cmd/a ./internal/ci")
	assert.Contains(t, stdout, `nova-ci local: would run nice -n 15 make test "PKGS=./cmd/a ./internal/ci" GOTEST_P=2`)
	assert.Contains(t, stdout, "nova-ci local: NOTE --dry-run ran no test")
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
	require.Equal(t, 1, code, "exit = %d, want 1\n%s", code, stdout)
	for _, want := range []string{
		"PKG FAIL    0.2s example.com/m/cmd/a",
		"RED package=example.com/m/cmd/a test=TestB",
		"    b_test.go:9: got 1, want 2",
		"red=1 make-exit=2",
	} {
		assert.Contains(t, stdout, want, "stdout lacks %q:\n%s", want, stdout)
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
	require.Equal(t, 1, code, "exit = %d, want 1\n%s", code, stdout)
	for _, want := range []string{"RED package=example.com/m/cmd/a test=-", "RED build:", "cmd/a/a.go:3:1: syntax error"} {
		assert.Contains(t, stdout, want, "stdout lacks %q:\n%s", want, stdout)
	}
}

// make test failing with no red test is one of two things. A SLEEPS skip off
// the ledger is a CI-SLEEPS line: the check said no, exit 1, as slowtests says
// it on every leg. With no CI-SLEEPS line a step could not run: exit 2. (A
// CI-SLOW line alone exits make 0: it is a measurement.)
func TestLocalMakeFailureWithNoRedTest(t *testing.T) {
	t.Parallel()
	sleeps := `{"Action":"output","Package":"example.com/m/cmd/a","Test":"TestSleeps","Output":"SLEEPS: waits\n"}
{"Action":"skip","Package":"example.com/m/cmd/a","Test":"TestSleeps","Elapsed":0}
{"Action":"pass","Package":"example.com/m/cmd/a","Elapsed":0.2}
CI-SLEEPS test=TestSleeps package=example.com/m/cmd/a: skipped for a wall-clock wait and not on internal/ci/sleeps-skips_allowlist.txt; inject a clock or tag it //go:build functional
`
	for _, c := range []struct {
		name, stream string
		want         int
		says         []string
	}{
		{"a CI-SLEEPS line is a no", sleeps, 1, []string{"CI-SLEEPS test=TestSleeps", "red=0 make-exit=2", "failed on 1 CI-SLEEPS line(s) above: a SLEEPS skip off the ledger"}},
		{"no CI-SLEEPS line is a step that could not run", "make: *** [test] Error 2\n", 2, []string{"red=0 make-exit=2", "no CI-SLEEPS line: a step could not run"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: c.stream, code: 2})
			code, stdout, _ := runLocal(t, f)
			assert.Equal(t, c.want, code, "exit = %d, want %d\n%s", code, c.want, stdout)
			for _, want := range c.says {
				assert.Contains(t, stdout, want, "stdout lacks %q:\n%s", want, stdout)
			}
		})
	}
}

// A change that selects nothing prints CI's words and runs nothing.
func TestLocalNothingSelectedRunsNothing(t *testing.T) {
	t.Parallel()
	f := localFixture(t, "")
	code, stdout, _ := runLocal(t, f, "--base", "origin/main")
	require.Equal(t, 0, code, "exit = %d, want 0\n%s", code, stdout)
	assert.Contains(t, stdout, "nothing to test for this change", "stdout lacks CI's words:\n%s", stdout)
	mb := f.call("git merge-base")
	if assert.NotNil(t, mb, "--base was not used: %+v", mb) {
		assert.Equal(t, "git merge-base origin/main HEAD", strings.Join(mb.Argv, " "), "--base was not used: %+v", mb)
	}
	assert.Nil(t, f.call("nice -n 15 make"), "make ran for an empty selection")
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
	require.Equal(t, 1, code, "exit = %d, want 1\n%s", code, stdout)
	mk := f.call("nice -n 15 make test ")
	require.NotNil(t, mk, "make call = %+v, want the test target with GOTEST_TAGS=functional", mk)
	require.Equal(t, "nice|-n|15|make|test|PKGS=./cmd/a|GOTEST_P=2|GOTEST_COUNT_FLAG=-count=1|GOTEST_TAGS=functional", strings.Join(mk.Argv, "|"), "make call = %+v, want the test target with GOTEST_TAGS=functional", mk)
	for _, want := range []string{"RED package=example.com/m/cmd/a test=TestStore", "redis: connection refused"} {
		assert.Contains(t, stdout, want, "stdout lacks %q:\n%s", want, stdout)
	}
}

// Uncommitted Go files are tested but not selected; the verb says so.
func TestLocalNamesUncommittedGoFiles(t *testing.T) {
	t.Parallel()
	f := localFixture(t, "./cmd/a\n", localReply{prefix: "nice -n 15 make test ", stdout: localGreenStream})
	f.replies = append([]localReply{{prefix: "git status", stdout: " M cmd/a/a.go\n?? cmd/b/b.go\n"}}, f.replies...)
	code, _, stderr := runLocal(t, f)
	require.Equal(t, 0, code, "exit = %d, want 0", code)
	assert.Contains(t, stderr, "NOTE 2 uncommitted Go file(s)", "stderr lacks the uncommitted note:\n%s", stderr)
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
		}, nil, `no merge base between "origin/dev" and HEAD`},
		{"selection fails", func(t *testing.T) *localFake {
			f := plain(t)
			f.selErr = errors.New("ERROR select-packages: go list failed\ngo: go.mod not found")
			return f
		}, nil, "the package selection against " + localMergeBase + " failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := tc.fake(t)
			code, stdout, stderr := runLocal(t, f, tc.args...)
			require.Equal(t, 2, code, "exit = %d, want 2\nstdout: %s\nstderr: %s", code, stdout, stderr)
			assert.Contains(t, stderr, tc.want, "stderr = %q, want it to say %q and name the door", stderr, tc.want)
			assert.Contains(t, stderr, "run: nova-ci local -h", "stderr = %q, want it to say %q and name the door", stderr, tc.want)
			assert.Nil(t, f.call("nice -n 15 make"), "make ran after a refusal")
		})
	}
}

// The GOTEST_P=2 the verb passes is a knob the Makefile's test target reads:
// a variable nothing consumes would leave -p 2 resting on GOMAXPROCS alone
// while the help and the printed command claimed it.
func TestMakefileTestTargetTakesGOTEST_P(t *testing.T) {
	t.Parallel()
	mk, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	require.NoError(t, err)
	var recipe string
	lines := strings.Split(string(mk), "\n")
	for i, line := range lines {
		if line == "test:" && i+1 < len(lines) {
			recipe = lines[i+1]
		}
	}
	require.NotEqual(t, "", recipe, "the Makefile has no `test:` rule followed by its recipe")
	assert.Contains(t, recipe, "-p $(GOTEST_P)", "make test does not pass -p $(GOTEST_P); nova-ci local's GOTEST_P=%s would be a phantom:\n%s", localCores, recipe)
}

// The real selection starts pkgselect's git and go commands through the verb's
// own runner: niced at 15, GOMAXPROCS=2, in the checkout. A go.mod change puts
// the whole tree in scope, so the diff and one go list answer it.
func TestLocalSelectionRunsThroughTheNicedRunner(t *testing.T) {
	t.Parallel()
	f := &localFake{root: localCheckout(t, "test"), replies: []localReply{
		{prefix: "nice -n 15 git cat-file -e " + localMergeBase + "^{commit}"},
		{prefix: "nice -n 15 git diff --name-only " + localMergeBase + " HEAD", stdout: "go.mod\n"},
		{prefix: "nice -n 15 go list ./cmd/... ./internal/... ./tools/...", stdout: "example.com/m/cmd/a\nexample.com/m/internal/ci\n"},
	}}
	pkgs, err := localSelectThrough(f.answer)(f.root, localMergeBase)
	require.NoError(t, err)
	assert.Equal(t, []string{"./cmd/a", "./internal/ci"}, pkgs)
	for _, c := range f.calls {
		assert.Equal(t, f.root, c.Dir, "call %v", c.Argv)
		assert.Contains(t, strings.Join(c.Env, " "), "GOMAXPROCS=2", "call %v", c.Argv)
		assert.Equal(t, "nice -n 15", strings.Join(c.Argv[:3], " "), "call %v", c.Argv)
	}
}

// A selection that fails is refused by name, never an empty run.
func TestLocalSelectionFailureIsAnError(t *testing.T) {
	t.Parallel()
	f := &localFake{root: localCheckout(t, "test"), replies: []localReply{
		{prefix: "nice -n 15 git diff", stdout: "go.mod\n"},
		{prefix: "nice -n 15 go list", stderr: "go: cannot find main module", code: 1},
	}}
	_, err := localSelectThrough(f.answer)(f.root, localMergeBase)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot find main module")
}
