package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

func runFlakeWith(args []string, runner ci.FlakeRunner) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cmdFlake(args, &stdout, &stderr, runner)
	return code, stdout.String(), stderr.String()
}

func TestFlakeFlagRefusals(t *testing.T) {
	t.Parallel()

	dummyRunner := func(ctx context.Context, pkg, testPattern string) (ci.RunOutcome, error) {
		return ci.RunOutcome{Passed: true}, nil
	}

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing_package",
			args:    []string{"--test", "TestFoo"},
			wantErr: "--package is required; refusing to guess",
		},
		{
			name:    "missing_test",
			args:    []string{"--package", "./internal/ci"},
			wantErr: "--test is required; refusing to guess",
		},
		{
			name:    "zero_runs",
			args:    []string{"--package", "./internal/ci", "--test", "TestFoo", "--runs", "0"},
			wantErr: "--runs must be greater than zero",
		},
		{
			name:    "negative_runs",
			args:    []string{"--package", "./internal/ci", "--test", "TestFoo", "--runs", "-5"},
			wantErr: "--runs must be greater than zero",
		},
		{
			name:    "zero_timeout",
			args:    []string{"--package", "./internal/ci", "--test", "TestFoo", "--timeout", "0s"},
			wantErr: "--timeout must be greater than zero",
		},
		{
			name:    "negative_timeout",
			args:    []string{"--package", "./internal/ci", "--test", "TestFoo", "--timeout", "-1s"},
			wantErr: "--timeout must be greater than zero",
		},
		{
			name:    "positional_arg",
			args:    []string{"--package", "./internal/ci", "--test", "TestFoo", "extra"},
			wantErr: "unexpected argument \"extra\"",
		},
		{
			name:    "unknown_flag",
			args:    []string{"--package", "./internal/ci", "--test", "TestFoo", "--bogus"},
			wantErr: "flag provided but not defined: -bogus",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runFlakeWith(tc.args, dummyRunner)
			if code != 2 {
				t.Errorf("exit code = %d, want 2; stderr: %q", code, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, tc.wantErr)
			}
			if !strings.Contains(stderr, "run: nova-ci help") {
				t.Errorf("stderr = %q, want it to contain 'run: nova-ci help'", stderr)
			}
		})
	}
}

func TestFlakeStableRun(t *testing.T) {
	t.Parallel()

	calls := 0
	runner := func(ctx context.Context, pkg, testPattern string) (ci.RunOutcome, error) {
		calls++
		if pkg != "./internal/ci" || testPattern != "TestStable" {
			t.Errorf("runner called with pkg=%q test=%q", pkg, testPattern)
		}
		return ci.RunOutcome{Passed: true, Output: "PASS"}, nil
	}

	code, stdout, stderr := runFlakeWith([]string{
		"--package", "./internal/ci",
		"--test", "TestStable",
		"--runs", "5",
		"--timeout", "200ms",
	}, runner)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if calls != 5 {
		t.Errorf("runner called %d times, want 5", calls)
	}
	wantLine := "STABLE package=./internal/ci test=TestStable runs=5 passed=5 failed=0\n"
	if stdout != wantLine {
		t.Errorf("stdout = %q, want %q", stdout, wantLine)
	}
}

func TestFlakeDetectedRun(t *testing.T) {
	t.Parallel()

	calls := 0
	runner := func(ctx context.Context, pkg, testPattern string) (ci.RunOutcome, error) {
		calls++
		if calls == 2 || calls == 4 {
			return ci.RunOutcome{Passed: false, Output: "FAIL"}, nil
		}
		return ci.RunOutcome{Passed: true, Output: "PASS"}, nil
	}

	code, stdout, stderr := runFlakeWith([]string{
		"--package", "./internal/ci",
		"--test", "TestFlaky",
		"--runs", "5",
	}, runner)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr: %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if calls != 5 {
		t.Errorf("runner called %d times, want 5", calls)
	}
	wantLine := "FLAKE package=./internal/ci test=TestFlaky runs=5 failed=2 passed=3\n"
	if stdout != wantLine {
		t.Errorf("stdout = %q, want %q", stdout, wantLine)
	}
}

func TestFlakeCannotRunRefusals(t *testing.T) {
	t.Parallel()

	t.Run("no_tests_matched", func(t *testing.T) {
		t.Parallel()
		runner := func(ctx context.Context, pkg, testPattern string) (ci.RunOutcome, error) {
			return ci.RunOutcome{NoTests: true, Output: "[no tests to run]"}, nil
		}

		code, stdout, stderr := runFlakeWith([]string{
			"--package", "./internal/ci",
			"--test", "TestDoesNotExist",
			"--runs", "3",
		}, runner)

		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, "no tests matched pattern") {
			t.Errorf("stderr = %q, want 'no tests matched pattern'", stderr)
		}
		if !strings.Contains(stderr, "run: nova-ci help") {
			t.Errorf("stderr = %q, want 'run: nova-ci help'", stderr)
		}
	})

	t.Run("package_setup_failed", func(t *testing.T) {
		t.Parallel()
		runner := func(ctx context.Context, pkg, testPattern string) (ci.RunOutcome, error) {
			return ci.RunOutcome{SetupFailed: true, Output: "cannot find package"}, nil
		}

		code, stdout, stderr := runFlakeWith([]string{
			"--package", "./nonexistent",
			"--test", "TestFoo",
			"--runs", "3",
		}, runner)

		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, "setup failed") {
			t.Errorf("stderr = %q, want 'setup failed'", stderr)
		}
	})
}

func TestFlakeDefaultRuns(t *testing.T) {
	t.Parallel()

	calls := 0
	runner := func(ctx context.Context, pkg, testPattern string) (ci.RunOutcome, error) {
		calls++
		return ci.RunOutcome{Passed: true, Output: "PASS"}, nil
	}

	code, stdout, stderr := runFlakeWith([]string{
		"--package", "./internal/ci",
		"--test", "TestDefault",
	}, runner)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if calls != 10 {
		t.Errorf("runner called %d times, want default 10", calls)
	}
	wantLine := "STABLE package=./internal/ci test=TestDefault runs=10 passed=10 failed=0\n"
	if stdout != wantLine {
		t.Errorf("stdout = %q, want %q", stdout, wantLine)
	}
}

func TestFlakeEndToEndHelpAndRefusalThroughRun(t *testing.T) {
	t.Parallel()

	// Bare flake through run()
	var stdout, stderr bytes.Buffer
	code := run([]string{"flake"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Errorf("bare flake exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--package is required") {
		t.Errorf("stderr = %q, want '--package is required'", stderr.String())
	}

	// flake -h through run()
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"flake", "-h"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Errorf("flake -h exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("flake -h stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "usage: nova-ci flake [flags]") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}

	// help flake through run()
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"help", "flake"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Errorf("help flake exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("help flake stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "usage: nova-ci flake [flags]") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
}
