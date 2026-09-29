package ci

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDetectFlakeValidations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Missing package
	_, err := DetectFlake(ctx, FlakeConfig{Test: "TestFoo", Runs: 5})
	if err == nil || !strings.Contains(err.Error(), "--package is required") {
		t.Errorf("missing package error: got %v, want --package is required", err)
	}

	// Missing test
	_, err = DetectFlake(ctx, FlakeConfig{Package: "./internal/ci", Runs: 5})
	if err == nil || !strings.Contains(err.Error(), "--test is required") {
		t.Errorf("missing test error: got %v, want --test is required", err)
	}

	// Invalid runs
	_, err = DetectFlake(ctx, FlakeConfig{Package: "./internal/ci", Test: "TestFoo", Runs: 0})
	if err == nil || !strings.Contains(err.Error(), "--runs must be greater than zero") {
		t.Errorf("zero runs error: got %v, want --runs must be greater than zero", err)
	}
}

func TestDetectFlakeStable(t *testing.T) {
	t.Parallel()

	callCount := 0
	fakeRunner := func(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
		callCount++
		return RunOutcome{Passed: true, Output: "PASS"}, nil
	}

	res, err := DetectFlake(context.Background(), FlakeConfig{
		Package: "./internal/ci",
		Test:    "TestStable",
		Runs:    5,
		Runner:  fakeRunner,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 5 {
		t.Errorf("callCount = %d, want 5", callCount)
	}
	if res.IsFlake() {
		t.Errorf("res.IsFlake() = true, want false")
	}
	if res.Passed != 5 || res.Failed != 0 {
		t.Errorf("res = %+v, want Passed=5, Failed=0", res)
	}
	wantLine := "STABLE package=./internal/ci test=TestStable runs=5 passed=5 failed=0"
	if res.Line() != wantLine {
		t.Errorf("res.Line() = %q, want %q", res.Line(), wantLine)
	}
}

func TestDetectFlakeIntermittent(t *testing.T) {
	t.Parallel()

	callCount := 0
	fakeRunner := func(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
		callCount++
		// Fail on run 2 and 4
		if callCount == 2 || callCount == 4 {
			return RunOutcome{Passed: false, Output: "FAIL"}, nil
		}
		return RunOutcome{Passed: true, Output: "PASS"}, nil
	}

	res, err := DetectFlake(context.Background(), FlakeConfig{
		Package: "./internal/ci",
		Test:    "TestFlaky",
		Runs:    5,
		Runner:  fakeRunner,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 5 {
		t.Errorf("callCount = %d, want 5", callCount)
	}
	if !res.IsFlake() {
		t.Errorf("res.IsFlake() = false, want true")
	}
	if res.Passed != 3 || res.Failed != 2 {
		t.Errorf("res = %+v, want Passed=3, Failed=2", res)
	}
	wantLine := "FLAKE package=./internal/ci test=TestFlaky runs=5 failed=2 passed=3"
	if res.Line() != wantLine {
		t.Errorf("res.Line() = %q, want %q", res.Line(), wantLine)
	}
}

func TestDetectFlakeAllFailed(t *testing.T) {
	t.Parallel()

	fakeRunner := func(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
		return RunOutcome{Passed: false, Output: "FAIL"}, nil
	}

	res, err := DetectFlake(context.Background(), FlakeConfig{
		Package: "./internal/ci",
		Test:    "TestBroken",
		Runs:    3,
		Runner:  fakeRunner,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsFlake() {
		t.Errorf("res.IsFlake() = false, want true")
	}
	if res.Passed != 0 || res.Failed != 3 {
		t.Errorf("res = %+v, want Passed=0, Failed=3", res)
	}
	wantLine := "FLAKE package=./internal/ci test=TestBroken runs=3 failed=3 passed=0"
	if res.Line() != wantLine {
		t.Errorf("res.Line() = %q, want %q", res.Line(), wantLine)
	}
}

func TestDetectFlakeNoTestsMatched(t *testing.T) {
	t.Parallel()

	fakeRunner := func(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
		return RunOutcome{NoTests: true, Output: "[no tests to run]"}, nil
	}

	_, err := DetectFlake(context.Background(), FlakeConfig{
		Package: "./internal/ci",
		Test:    "TestMissing",
		Runs:    3,
		Runner:  fakeRunner,
	})
	if err == nil || !strings.Contains(err.Error(), "no tests matched pattern") {
		t.Errorf("expected no tests error, got %v", err)
	}
}

func TestDetectFlakeSetupFailed(t *testing.T) {
	t.Parallel()

	fakeRunner := func(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
		return RunOutcome{SetupFailed: true, Output: "[build failed]"}, nil
	}

	_, err := DetectFlake(context.Background(), FlakeConfig{
		Package: "./internal/ci",
		Test:    "TestBuildError",
		Runs:    3,
		Runner:  fakeRunner,
	})
	if err == nil || !strings.Contains(err.Error(), "setup failed") {
		t.Errorf("expected setup failed error, got %v", err)
	}
}

func TestDetectFlakeRunnerError(t *testing.T) {
	t.Parallel()

	fakeRunner := func(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
		return RunOutcome{}, errors.New("exec error: binary not found")
	}

	_, err := DetectFlake(context.Background(), FlakeConfig{
		Package: "./internal/ci",
		Test:    "TestExecErr",
		Runs:    3,
		Runner:  fakeRunner,
	})
	if err == nil || !strings.Contains(err.Error(), "exec error: binary not found") {
		t.Errorf("expected runner error, got %v", err)
	}
}

func TestExecTestRunnerRealRun(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run an existing fast test in allowlist package
	outcome, err := ExecTestRunner(ctx, "github.com/mas-bandwidth/nova-tools/internal/ci/allowlist", "TestCheckReportsWithoutWriting")
	if err != nil {
		t.Fatalf("ExecTestRunner failed: %v", err)
	}
	if !outcome.Passed {
		t.Errorf("expected test to pass, output: %s", outcome.Output)
	}
}
