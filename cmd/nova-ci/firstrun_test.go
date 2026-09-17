package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstrun_test.go pins the onboarding standard for this binary: the usage
// banner's examples are RUN rather than read, and a refusal says what the input
// WANTS rather than only what was wrong. The central onboarding test in
// internal/ci builds the binary and checks the door; this file executes the
// lines behind it so an example that drifts out of the flag set is red here.

func runCIIn(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// (a) Every line in the banner's example block runs, exits 0 or 1, and prints
// something on stdout. `slowtests` reads stdin, so an empty stream stands in
// for "no events yet" and must still be a green, not a hang.
func TestUsageBannerExamplesRun(t *testing.T) {
	exit, stdout, stderr := runCIIn(t, "", "help")
	if exit != 0 {
		t.Fatalf("`nova-ci help` exit = %d, want 0; stderr: %s", exit, stderr)
	}
	examples, err := onboarding.ExampleLines(stdout, "nova-ci")
	if err != nil {
		t.Fatalf("%v\n\n%s", err, stdout)
	}
	for _, ex := range examples {
		args := strings.Fields(ex)[1:]
		exit, out, errs := runCIIn(t, "", args...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, errs)
			continue
		}
		if exit != 0 {
			t.Errorf("the usage example %q ran but said NO (exit %d)\nstderr: %s", ex, exit, errs)
		}
		if out == "" {
			t.Errorf("the usage example %q printed nothing on stdout", ex)
		}
	}
}

// (b) The two refusals a first run hits name what the input wants: the whole
// seconds a budget must be, and the line of stdin that was not a TestEvent.
func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	exit, _, stderr := runCIIn(t, "", "slowtests", "--budget", "0")
	if exit != 2 {
		t.Errorf("--budget 0 exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "greater than zero") {
		t.Errorf("--budget 0 stderr = %q, want it to say the budget must be greater than zero", stderr)
	}

	exit, _, stderr = runCIIn(t, "not json\n", "slowtests")
	if exit != 2 {
		t.Errorf("a malformed stdin exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr, "line 1") {
		t.Errorf("a malformed stdin stderr = %q, want it to name line 1", stderr)
	}
	if !strings.Contains(stderr, "run: nova-ci help") {
		t.Errorf("a refusal stderr = %q, want it to name the door", stderr)
	}
}
