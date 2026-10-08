// The nova-sprint lander's tree gate.
package main

import (
	"context"
	"io"
	"os/exec"
)

// TreeGateResult is the result of running the tree gate.
type TreeGateResult struct {
	Passed bool
	Failed []string
}

// runTreeGate runs the tree gate on the given packages.
// It runs go build, go vet, and go test on all packages.
func runTreeGate(ctx context.Context, packages []string, out, errOut io.Writer) TreeGateResult {
	result := TreeGateResult{Passed: true}

	// Run go build ./...
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		result.Passed = false
		result.Failed = append(result.Failed, "go build ./...")
	}

	// Run go vet ./...
	cmd = exec.CommandContext(ctx, "go", "vet", "./...")
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		result.Passed = false
		result.Failed = append(result.Failed, "go vet ./...")
	}

	// Run go test on packages.
	for _, pkg := range packages {
		cmd = exec.CommandContext(ctx, "go", "test", pkg)
		cmd.Stdout = out
		cmd.Stderr = errOut
		if err := cmd.Run(); err != nil {
			result.Passed = false
			result.Failed = append(result.Failed, pkg)
		}
	}

	return result
}

// runFunctionalChecks runs the four whole-tree functional checks.
func runFunctionalChecks(ctx context.Context, out, errOut io.Writer) TreeGateResult {
	result := TreeGateResult{Passed: true}
	cmd := exec.CommandContext(ctx, "go", "test", "-tags", "functional",
		"-run", "^(TestUncheckedErrors|TestStaticcheckFindings|TestDeadCode|TestEveryCommandMeetsTheOnboardingStandard)$",
		"./internal/ci/")
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		result.Passed = false
		result.Failed = append(result.Failed, "functional checks")
	}
	return result
}

// runInternalCI runs tests on internal/docs and internal/ci.
func runInternalCI(ctx context.Context, out, errOut io.Writer) TreeGateResult {
	result := TreeGateResult{Passed: true}
	cmd := exec.CommandContext(ctx, "go", "test", "./internal/docs/", "./internal/ci/")
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		result.Passed = false
		result.Failed = append(result.Failed, "internal/docs internal/ci")
	}
	return result
}
