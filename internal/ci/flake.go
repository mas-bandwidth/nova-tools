package ci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// RunOutcome represents the result of a single isolated test run.
type RunOutcome struct {
	Passed      bool
	NoTests     bool
	SetupFailed bool
	Output      string
}

// FlakeRunner executes one test run in an isolated process.
type FlakeRunner func(ctx context.Context, pkg, testPattern string) (RunOutcome, error)

// FlakeConfig configures flake detection.
type FlakeConfig struct {
	Package string
	Test    string
	Runs    int
	Timeout time.Duration
	Runner  FlakeRunner
}

// FlakeResult is the result of running flake detection.
type FlakeResult struct {
	Package string
	Test    string
	Runs    int
	Passed  int
	Failed  int
}

// IsFlake reports whether a flake was detected (at least one failure).
func (r FlakeResult) IsFlake() bool {
	return r.Failed > 0
}

// Line returns the one-line receipt for the flake detection run.
func (r FlakeResult) Line() string {
	if r.IsFlake() {
		return fmt.Sprintf("FLAKE package=%s test=%s runs=%d failed=%d passed=%d",
			oneline.Field(r.Package), oneline.Field(r.Test), r.Runs, r.Failed, r.Passed)
	}
	return fmt.Sprintf("STABLE package=%s test=%s runs=%d passed=%d failed=0",
		oneline.Field(r.Package), oneline.Field(r.Test), r.Runs, r.Passed)
}

// ExecTestRunner is the default production runner executing `go test -count=1 -run <pattern> <pkg>`.
func ExecTestRunner(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-run", testPattern, pkg)
	cmd.Env = goenv.Clean(os.Environ())
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	output := buf.String()

	if strings.Contains(output, "[no tests to run]") {
		return RunOutcome{NoTests: true, Output: output}, nil
	}
	if strings.Contains(output, "[setup failed]") ||
		strings.Contains(output, "[build failed]") ||
		strings.Contains(output, "directory not found") ||
		strings.Contains(output, "cannot find package") ||
		strings.Contains(output, "no Go files in") {
		return RunOutcome{SetupFailed: true, Output: output}, nil
	}

	if err == nil {
		return RunOutcome{Passed: true, Output: output}, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return RunOutcome{Passed: false, Output: output}, nil
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
		return RunOutcome{Passed: false, Output: "test execution timed out: " + output}, nil
	}

	return RunOutcome{}, err
}

// DetectFlake runs the configured test up to cfg.Runs times in isolated processes.
func DetectFlake(ctx context.Context, cfg FlakeConfig) (FlakeResult, error) {
	if strings.TrimSpace(cfg.Package) == "" {
		return FlakeResult{}, errors.New("--package is required; refusing to guess")
	}
	if strings.TrimSpace(cfg.Test) == "" {
		return FlakeResult{}, errors.New("--test is required; refusing to guess")
	}
	if cfg.Runs <= 0 {
		return FlakeResult{}, fmt.Errorf("--runs must be greater than zero (got %d)", cfg.Runs)
	}
	runner := cfg.Runner
	if runner == nil {
		runner = ExecTestRunner
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	passed := 0
	failed := 0

	for i := 0; i < cfg.Runs; i++ {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		outcome, err := runner(runCtx, cfg.Package, cfg.Test)
		cancel()

		if err != nil {
			return FlakeResult{}, fmt.Errorf("run %d failed: %w", i+1, err)
		}
		if outcome.NoTests {
			return FlakeResult{}, fmt.Errorf("no tests matched pattern %q in package %q", cfg.Test, cfg.Package)
		}
		if outcome.SetupFailed {
			return FlakeResult{}, fmt.Errorf("package %q setup failed: %s", cfg.Package, oneline.Cap(outcome.Output, oneline.TailBytes))
		}

		if outcome.Passed {
			passed++
		} else {
			failed++
		}
	}

	return FlakeResult{
		Package: cfg.Package,
		Test:    cfg.Test,
		Runs:    cfg.Runs,
		Passed:  passed,
		Failed:  failed,
	}, nil
}
