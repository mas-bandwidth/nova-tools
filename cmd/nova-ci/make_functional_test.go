//go:build functional

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// make_functional_test.go runs the Makefile's real `test` recipe with this
// test binary standing in for `go` (GO=<this binary>, NOVA_CI_FAKE_GO=<a go
// test -json fixture>): `go test` prints the fixture and exits 0, and `go run
// ./cmd/nova-ci ...` is this package's own run(). So what is proved is the
// recipe's exit handling -- make, bash, the pipe, SLOWTESTS_ENFORCE -- around
// the real slowtests verb, the boundary the push leg's swallowed exit crossed
// (#4413: ci.yml's push branch passed SLOWTESTS_ENFORCE=0, and the recipe
// turned the CI-SLEEPS exit 2 into 0). It starts make and bash, so it is the
// functional tier.

// fakeGoEnv names the fixture a fake `go test` prints.
const fakeGoEnv = "NOVA_CI_FAKE_GO"

func TestMain(m *testing.M) {
	if fixture := os.Getenv(fakeGoEnv); fixture != "" {
		os.Exit(fakeGo(fixture, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeGo answers the two go commands the test recipe runs.
func fakeGo(fixture string, args []string) int {
	switch {
	case len(args) > 0 && args[0] == "test":
		b, err := os.ReadFile(fixture)
		if err != nil {
			os.Stderr.WriteString("fake go: " + err.Error() + "\n")
			return 1
		}
		os.Stdout.Write(b)
		return 0
	case len(args) > 1 && args[0] == "run" && args[1] == "./cmd/nova-ci":
		return run(args[2:], os.Stdin, os.Stdout, os.Stderr)
	}
	os.Stderr.WriteString("fake go: unexpected " + strings.Join(args, " ") + "\n")
	return 1
}

// makeTest runs `make test` at the checkout root with the fake go, the fixture
// and the given make variables, and returns make's exit and its output.
func makeTest(t *testing.T, fixture string, vars ...string) (int, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "events.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-s", "-C", repoRoot(t), "test", "PKGS=./cmd/nova-ci", "GO=" + self}, vars...)
	cmd := exec.Command("make", args...)
	cmd.Env = append(os.Environ(), fakeGoEnv+"="+path, "RUNNER_TEMP="+tmp)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return exit.ExitCode(), out.String()
	case err != nil:
		t.Fatalf("make: %v\n%s", err, out.String())
	}
	return 0, out.String()
}

// PROBE 5 of the #4413 ruling, through make: the push leg's own variables
// (ci.yml: SLOWTESTS_FLAGS="--budget 60 --sleeps <ledger>", and the old
// SLOWTESTS_ENFORCE=0 spelled out) exit 2 on a SLEEPS skip off the ledger and
// print its CI-SLEEPS line, while a package over the 60 s budget alone prints
// its CI-SLOW line and exits 0. And the nightly leg's SLOWTESTS_ENFORCE=1 at
// the Makefile's own flags makes a 1.4 s test red that the default leaves
// green (PROBE 1 at the recipe).
func TestMakeTestExitIsCISleepsOnEveryLegAndCISlowOnlyNightly(t *testing.T) {
	t.Parallel()

	sleeps := `{"Action":"output","Package":"example.com/m/cmd/a","Test":"TestSleepsOffTheLedger","Output":"SLEEPS: waits\n"}
{"Action":"skip","Package":"example.com/m/cmd/a","Test":"TestSleepsOffTheLedger","Elapsed":0}
{"Action":"pass","Package":"example.com/m/cmd/a","Elapsed":0.2}
`
	slowPackage := `{"Action":"pass","Package":"example.com/m/cmd/a","Test":"TestWait","Elapsed":0.9}
{"Action":"pass","Package":"example.com/m/cmd/a","Elapsed":61.5}
`
	slowTest := `{"Action":"pass","Package":"example.com/m/cmd/a","Test":"TestTakesOnePointFour","Elapsed":1.4}
{"Action":"pass","Package":"example.com/m/cmd/a","Elapsed":1.5}
`
	push := []string{"SLOWTESTS_FLAGS=--budget 60 --sleeps internal/ci/sleeps-skips_allowlist.txt", "SLOWTESTS_ENFORCE=0"}

	code, out := makeTest(t, sleeps, push...)
	if code != 2 || !strings.Contains(out, "CI-SLEEPS test=TestSleepsOffTheLedger package=example.com/m/cmd/a") {
		t.Errorf("push leg, a SLEEPS skip off the ledger: make exit %d, want 2 with its CI-SLEEPS line:\n%s", code, out)
	}
	code, out = makeTest(t, slowPackage, push...)
	if code != 0 || !strings.Contains(out, "CI-SLOW package=example.com/m/cmd/a seconds=61.5s budget=60s") || !strings.Contains(out, "CI-LOAD load=") {
		t.Errorf("push leg, a package over 60 s: make exit %d, want 0 with its CI-SLOW and CI-LOAD lines:\n%s", code, out)
	}
	for _, c := range []struct {
		enforce string
		code    int
	}{{"SLOWTESTS_ENFORCE=0", 0}, {"SLOWTESTS_ENFORCE=1", 2}} {
		code, out = makeTest(t, slowTest, c.enforce)
		if code != c.code || !strings.Contains(out, "CI-SLOW test=TestTakesOnePointFour package=example.com/m/cmd/a seconds=1.4s budget=1s") {
			t.Errorf("the Makefile's flags, %s, a 1.4 s test: make exit %d, want %d with its CI-SLOW line:\n%s", c.enforce, code, c.code, out)
		}
	}
}
