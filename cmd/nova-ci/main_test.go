package main

import (
	"bytes"
	"strings"
	"testing"
)

// main_test.go is the red-test contract of the nova-ci command line: the
// onboarding door a bare run and `help` open, and the slowtests verb end to
// end through run(). Every fixture is a canned TestEvent string; nothing here
// runs `go test` or reads the clock.

func runCI(t *testing.T, args []string, stdin string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// A bare command refuses in one line that names the door.
func TestBareCommandNamesTheDoor(t *testing.T) {
	code, stdout, stderr := runCI(t, nil, "")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty; a refusal belongs on stderr", stdout)
	}
	if !strings.Contains(stderr, "run: nova-ci help") {
		t.Errorf("stderr = %q, want it to name `run: nova-ci help`", stderr)
	}
	if n := len(strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")); n > 2 {
		t.Errorf("stderr printed %d lines, want at most 2:\n%s", n, stderr)
	}
}

// help opens the door on stdout at exit 0, and ends in an example block whose
// lines are commands a stranger can paste.
func TestHelpOpensTheDoor(t *testing.T) {
	code, stdout, stderr := runCI(t, []string{"help"}, "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "\nexample:\n") {
		t.Errorf("help has no example: block:\n%s", stdout)
	}
}

// slowtests under budget prints the OK line on stdout and exits 0.
func TestSlowtestsUnderBudgetIsOK(t *testing.T) {
	stdin := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":3.2}
`
	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60"}, stdin)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	want := "CI-SLOW OK packages=1 slowest=example.com/pkg:3.2s\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// slowtests over budget prints one line per offending package and exits 2.
func TestSlowtestsOverBudgetExitsTwo(t *testing.T) {
	stdin := `{"Action":"pass","Package":"example.com/pkg","Test":"TestA","Elapsed":3.2}
{"Action":"pass","Package":"example.com/pkg","Elapsed":75.3}
`
	code, stdout, _ := runCI(t, []string{"slowtests", "--budget", "60"}, stdin)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	want := "CI-SLOW package=example.com/pkg seconds=75.3s budget=60s slowest=TestA:3.2s\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// A malformed line is a refusal on stderr at exit 2, and prints no OK line.
func TestSlowtestsMalformedLineRefuses(t *testing.T) {
	code, stdout, stderr := runCI(t, []string{"slowtests", "--budget", "60"}, "not json\n")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on a refusal", stdout)
	}
	if !strings.Contains(stderr, "nova-ci slowtests") {
		t.Errorf("stderr = %q, want the refusal to name the verb", stderr)
	}
	if !strings.Contains(stderr, "line 1") {
		t.Errorf("stderr = %q, want it to name the offending line", stderr)
	}
	if !strings.Contains(stderr, "run: nova-ci help") {
		t.Errorf("stderr = %q, want it to name the door", stderr)
	}
}

// A budget of zero or less is refused rather than read as unlimited.
func TestSlowtestsRefusesANonPositiveBudget(t *testing.T) {
	for _, budget := range []string{"0", "-1"} {
		code, _, stderr := runCI(t, []string{"slowtests", "--budget", budget}, "")
		if code != 2 {
			t.Errorf("--budget %s: exit = %d, want 2", budget, code)
		}
		if !strings.Contains(stderr, "budget") {
			t.Errorf("--budget %s: stderr = %q, want it to name the budget", budget, stderr)
		}
	}
}
