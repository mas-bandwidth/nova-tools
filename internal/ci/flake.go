package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// NoTestsError indicates that no tests in the package matched the specified pattern.
type NoTestsError struct {
	Package string
	Test    string
}

func (e *NoTestsError) Error() string {
	return fmt.Sprintf("no tests matched pattern %q in package %q", e.Test, e.Package)
}

// SetupFailedError indicates that the package failed to build or set up.
type SetupFailedError struct {
	Package string
	Output  string
}

func (e *SetupFailedError) Error() string {
	return fmt.Sprintf("package %q failed to build", e.Package)
}

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

// IsFlake reports whether a flake was detected (both pass and fail observed).
func (r FlakeResult) IsFlake() bool {
	return r.Passed > 0 && r.Failed > 0
}

// IsFail reports whether steady failure was detected (all runs failed).
func (r FlakeResult) IsFail() bool {
	return r.Passed == 0 && r.Failed > 0
}

// Line returns the one-line receipt for the flake detection run.
func (r FlakeResult) Line() string {
	if r.IsFlake() {
		return fmt.Sprintf("FLAKE package=%s test=%s runs=%d failed=%d passed=%d",
			oneline.Field(r.Package), oneline.Field(r.Test), r.Runs, r.Failed, r.Passed)
	}
	if r.IsFail() {
		return fmt.Sprintf("FAIL package=%s test=%s runs=%d failed=%d passed=0",
			oneline.Field(r.Package), oneline.Field(r.Test), r.Runs, r.Failed)
	}
	return fmt.Sprintf("STABLE package=%s test=%s runs=%d passed=%d failed=0",
		oneline.Field(r.Package), oneline.Field(r.Test), r.Runs, r.Passed)
}

type flakeTestEvent struct {
	Action      string `json:"Action"`
	Package     string `json:"Package"`
	Test        string `json:"Test"`
	Output      string `json:"Output"`
	FailedBuild string `json:"FailedBuild"`
}

// ExecTestRunner is the default production runner executing `go test -json -count=1 -run <pattern> <pkg>`.
func ExecTestRunner(ctx context.Context, pkg, testPattern string) (RunOutcome, error) {
	return ExecTestRunnerEnv(ctx, pkg, testPattern, nil)
}

// ExecTestRunnerEnv executes `go test -json -count=1 -run <pattern> <pkg>` with additional environment variables.
func ExecTestRunnerEnv(ctx context.Context, pkg, testPattern string, extraEnv []string) (RunOutcome, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-run", testPattern, pkg)
	configureFlakeProcess(cmd)
	cmd.Env = append(goenv.Clean(os.Environ()), extraEnv...)
	cmd.WaitDelay = 3 * time.Second
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return RunOutcome{SetupFailed: true, Output: "setup timed out: " + err.Error()}, nil
		}
		return RunOutcome{}, err
	}
	defer cleanupFlakeProcess(cmd)
	err := cmd.Wait()
	cleanupFlakeProcess(cmd)

	var (
		ranTests    int
		failedTests int
		buildFailed bool
	)

	for _, line := range bytes.Split(stdoutBuf.Bytes(), []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev flakeTestEvent
		if jsonErr := json.Unmarshal(line, &ev); jsonErr == nil {
			if ev.Action == "run" && ev.Test != "" {
				ranTests++
			}
			if ev.Action == "fail" && ev.Test != "" {
				failedTests++
			}
			if ev.Action == "build-fail" || ev.FailedBuild != "" {
				buildFailed = true
			}
		}
	}

	rawOutput := stdoutBuf.String()
	if stderrBuf.Len() > 0 {
		if len(rawOutput) > 0 {
			rawOutput += "\n"
		}
		rawOutput += stderrBuf.String()
	}

	if buildFailed {
		return RunOutcome{SetupFailed: true, Output: rawOutput}, nil
	}

	if ranTests == 0 {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return RunOutcome{SetupFailed: true, Output: "setup timed out: " + rawOutput}, nil
		}
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return RunOutcome{SetupFailed: true, Output: rawOutput}, nil
			}
			return RunOutcome{}, err
		}
		return RunOutcome{NoTests: true, Output: rawOutput}, nil
	}

	if err == nil && failedTests == 0 {
		return RunOutcome{Passed: true, Output: rawOutput}, nil
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
		return RunOutcome{Passed: false, Output: "test execution timed out: " + rawOutput}, nil
	}

	return RunOutcome{Passed: false, Output: rawOutput}, nil
}

// DetectFlake runs the configured test up to cfg.Runs times in isolated processes.
func DetectFlake(ctx context.Context, cfg FlakeConfig) (FlakeResult, error) {
	var missing []string
	if strings.TrimSpace(cfg.Package) == "" {
		missing = append(missing, "--package")
	}
	if strings.TrimSpace(cfg.Test) == "" {
		missing = append(missing, "--test")
	}
	if len(missing) == 1 {
		return FlakeResult{}, fmt.Errorf("%s is required; refusing to guess", missing[0])
	}
	if len(missing) > 1 {
		return FlakeResult{}, fmt.Errorf("%s are required; refusing to guess", strings.Join(missing, " and "))
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
			return FlakeResult{}, &NoTestsError{Package: cfg.Package, Test: cfg.Test}
		}
		if outcome.SetupFailed {
			return FlakeResult{}, &SetupFailedError{Package: cfg.Package, Output: outcome.Output}
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
