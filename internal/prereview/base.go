package prereview

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// BaseGate is the status of the pull request relative to its target base branch:
// ok, behind, or conflict.
type BaseGate string

const (
	BaseOK       BaseGate = "ok"
	BaseBehind   BaseGate = "behind"
	BaseConflict BaseGate = "conflict"
)

// BaseGateFromGH derives the base gate status from GitHub's mergeable and
// mergeStateStatus values.
//
// In GitHub's API:
//   mergeable: MERGEABLE, CONFLICTING, UNKNOWN
//   mergeStateStatus: CLEAN, BEHIND, DIRTY, BLOCKED, HAS_HOOKS, UNKNOWN, UNSTABLE
func BaseGateFromGH(mergeable, mergeStateStatus string) BaseGate {
	m := strings.ToUpper(strings.TrimSpace(mergeable))
	s := strings.ToUpper(strings.TrimSpace(mergeStateStatus))
	if m == "CONFLICTING" || s == "DIRTY" {
		return BaseConflict
	}
	if s == "BEHIND" {
		return BaseBehind
	}
	return BaseOK
}

// GitRunner is an interface for executing git commands, satisfied by exec or test doubles.
type GitRunner interface {
	RunGit(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// ExecGitRunner runs git using os/exec.
type ExecGitRunner struct{}

func (ExecGitRunner) RunGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// BaseGateFromGit computes the base gate (ok, behind, conflict) using git merge-tree
// and git merge-base against a local repository directory.
//
// If git merge-tree detects conflicts (exit code 1), it returns BaseConflict.
// If git merge-tree succeeds (clean merge), it checks whether base is an ancestor
// of head via git merge-base --is-ancestor base head. If base is not an ancestor
// (exit code 1), head is BaseBehind. Otherwise it is BaseOK.
func BaseGateFromGit(ctx context.Context, runner GitRunner, dir, base, head string) (BaseGate, error) {
	if runner == nil {
		runner = ExecGitRunner{}
	}
	// 1. Check for merge conflicts via git merge-tree --write-tree base head
	_, err := runner.RunGit(ctx, dir, "merge-tree", "--write-tree", base, head)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return BaseConflict, nil
		}
		if strings.Contains(err.Error(), "exit status 1") {
			return BaseConflict, nil
		}
		return BaseOK, fmt.Errorf("git merge-tree: %w", err)
	}

	// 2. Check if head is behind base via git merge-base --is-ancestor base head
	_, err = runner.RunGit(ctx, dir, "merge-base", "--is-ancestor", base, head)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return BaseBehind, nil
		}
		if strings.Contains(err.Error(), "exit status 1") {
			return BaseBehind, nil
		}
		return BaseOK, fmt.Errorf("git merge-base --is-ancestor: %w", err)
	}

	return BaseOK, nil
}
