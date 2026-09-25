package card

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// CheckBound returns true unless kind is script or report (case-insensitive).
func CheckBound(kind string) bool {
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		return true
	}
	return k != "script" && k != "report"
}

// Receipt represents check.receipt content.
type Receipt struct {
	Identity     string
	Head         string
	BaseSha      string
	CheckSha256  string
	ExpectSha256 string
	HeadExit     string
	HeadMatch    string
	BaseExit     string
	BaseMatch    string
	OutputSha256 string
}

func (r Receipt) Format() string {
	return fmt.Sprintf("identity %s\nhead %s\nbase_sha %s\ncheck_sha256 %s\nexpect_sha256 %s\nhead_exit %s\nhead_match %s\nbase_exit %s\nbase_match %s\noutput_sha256 %s\n",
		r.Identity, r.Head, r.BaseSha, r.CheckSha256, r.ExpectSha256, r.HeadExit, r.HeadMatch, r.BaseExit, r.BaseMatch, r.OutputSha256)
}

// RunChecks runs CHECK at head and base according to spec.
func RunChecks(repoDir, branch, baseSha, checkCmd, expectStr, identity string, resultsDir string, timeout time.Duration) (outcome, reason string, receipt Receipt, err error) {
	// 1. Verify head branch
	cmd := exec.Command("git", "-C", repoDir, "rev-parse", "--verify", fmt.Sprintf("refs/heads/%s", branch))
	headOut, err := cmd.CombinedOutput()
	if err != nil {
		return "FAILED", "other", Receipt{}, fmt.Errorf("head branch not found: %s", string(headOut))
	}
	headSha := strings.TrimSpace(string(headOut))

	// 2. Verify baseSha exists in repo
	cmd = exec.Command("git", "-C", repoDir, "cat-file", "-e", baseSha)
	if err := cmd.Run(); err != nil {
		return "BLOCKED", "env", Receipt{}, fmt.Errorf("base-sha %s not in repo", baseSha)
	}

	// 3. Verify head descends from base-sha
	cmd = exec.Command("git", "-C", repoDir, "merge-base", "--is-ancestor", baseSha, headSha)
	if err := cmd.Run(); err != nil {
		return "BLOCKED", "base-moved", Receipt{}, fmt.Errorf("head does not descend from base-sha")
	}

	// Setup out dir
	outDir := filepath.Join(filepath.Dir(resultsDir), "out")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "BLOCKED", "env", Receipt{}, err
	}
	headLogPath := filepath.Join(outDir, "check-head.log")
	baseLogPath := filepath.Join(outDir, "check-base.log")

	// Head run
	headWorktree := filepath.Join(os.TempDir(), "nsprint-head-"+identity)
	_ = safepath.RemoveUnder(os.TempDir(), headWorktree)
	defer func() { _ = safepath.RemoveUnder(os.TempDir(), headWorktree) }()

	cmd = exec.Command("git", "-C", repoDir, "worktree", "add", "--detach", headWorktree, headSha)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "BLOCKED", "env", Receipt{}, fmt.Errorf("failed to create head worktree: %s", string(out))
	}

	headExit, headOutput, err := runCheckCommand(headWorktree, checkCmd, headLogPath, timeout, nil)
	if err != nil && headExit == 0 {
		headExit = -1 // timeout
	}

	re, err := regexp.Compile(expectStr)
	if err != nil {
		return "BLOCKED", "spec", Receipt{}, fmt.Errorf("invalid expect regexp: %w", err)
	}

	headMatch := re.Match(headOutput)

	// Output sha256
	h := sha256.New()
	h.Write(headOutput)
	outputSha := hex.EncodeToString(h.Sum(nil))

	checkSha := fmt.Sprintf("%x", sha256.Sum256([]byte(checkCmd)))
	expectSha := fmt.Sprintf("%x", sha256.Sum256([]byte(expectStr)))

	receipt = Receipt{
		Identity:     identity,
		Head:         headSha,
		BaseSha:      baseSha,
		CheckSha256:  checkSha,
		ExpectSha256: expectSha,
		HeadExit:     fmt.Sprintf("%d", headExit),
		HeadMatch:    fmt.Sprintf("%t", headMatch),
		BaseExit:     "-",
		BaseMatch:    "-",
		OutputSha256: outputSha,
	}

	if headExit == -1 {
		writeReceipt(resultsDir, receipt)
		return "FAILED", "timeout", receipt, nil
	}
	if headExit != 0 || !headMatch {
		writeReceipt(resultsDir, receipt)
		return "FAILED", "tests-red", receipt, nil
	}

	// Test-only patch: git diff baseSha headSha -- '*_test.go'
	cmd = exec.Command("git", "-C", repoDir, "diff", baseSha, headSha, "--", "*_test.go")
	patchBytes, err := cmd.Output()
	if err != nil || len(patchBytes) == 0 {
		writeReceipt(resultsDir, receipt)
		return "BLOCKED", "spec", receipt, nil
	}

	// Base run
	baseWorktree := filepath.Join(os.TempDir(), "nsprint-base-"+identity)
	_ = safepath.RemoveUnder(os.TempDir(), baseWorktree)
	defer func() { _ = safepath.RemoveUnder(os.TempDir(), baseWorktree) }()

	cmd = exec.Command("git", "-C", repoDir, "worktree", "add", "--detach", baseWorktree, baseSha)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "BLOCKED", "env", receipt, fmt.Errorf("failed to create base worktree: %s", string(out))
	}

	// Apply patch
	patchFile := filepath.Join(os.TempDir(), "test.patch")
	os.WriteFile(patchFile, patchBytes, 0644)
	defer os.Remove(patchFile)

	cmd = exec.Command("git", "-C", baseWorktree, "apply", patchFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		writeReceipt(resultsDir, receipt)
		return "BLOCKED", "spec", receipt, fmt.Errorf("failed to apply test-only patch: %s", string(out))
	}

	baseExit, baseOutput, err := runCheckCommand(baseWorktree, checkCmd, baseLogPath, timeout, []string{"BASE_RUN=1"})
	if err != nil && baseExit == 0 {
		baseExit = -1
	}

	baseMatch := re.Match(baseOutput)

	receipt.BaseExit = fmt.Sprintf("%d", baseExit)
	receipt.BaseMatch = fmt.Sprintf("%t", baseMatch)
	writeReceipt(resultsDir, receipt)

	if baseExit == -1 {
		return "FAILED", "timeout", receipt, nil
	}
	if baseExit != 0 && baseExit != 1 {
		return "BLOCKED", "env", receipt, nil
	}
	if baseExit == 0 || (baseExit == 1 && baseMatch) {
		return "BLOCKED", "spec", receipt, nil
	}

	return "DONE", "done", receipt, nil
}

func runCheckCommand(dir, checkCmd, logPath string, timeout time.Duration, envExtra []string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if timeout <= 0 {
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Minute)
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", checkCmd)
	cmd.Dir = dir
	if len(envExtra) > 0 {
		cmd.Env = append(os.Environ(), envExtra...)
	}
	output, err := cmd.CombinedOutput()
	_ = os.WriteFile(logPath, output, 0644)

	if ctx.Err() == context.DeadlineExceeded {
		return -1, output, fmt.Errorf("timeout")
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), output, nil
		}
		return 1, output, err
	}
	return 0, output, nil
}

func writeReceipt(resultsDir string, r Receipt) {
	if resultsDir == "" {
		return
	}
	_ = os.MkdirAll(resultsDir, 0755)
	_ = os.WriteFile(filepath.Join(resultsDir, "check.receipt"), []byte(r.Format()), 0644)
}
