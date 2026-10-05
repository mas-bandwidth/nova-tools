package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCLILab(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com",
			"GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	gitCmd("init", "-q", "-b", "main")
	signFile := filepath.Join(dir, "sign", "sign.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(signFile), 0o755))
	require.NoError(t, os.WriteFile(signFile, []byte("package sign\n"), 0o644))
	gitCmd("add", "-A")
	gitCmd("commit", "-q", "-m", "base")
	gitCmd("checkout", "-q", "-b", "card")
	require.NoError(t, os.WriteFile(signFile, []byte("package sign\n\nfunc Clean() {}\n"), 0o644))
	gitCmd("add", "-A")
	gitCmd("commit", "-q", "-m", "clean fix")
	return dir
}

func addCLILabViolations(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "elsewhere.go"), []byte("package elsewhere\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "elsewhere"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "elsewhere", "x.go"), []byte("package elsewhere\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sign", "RESULT.md"), []byte("the worker's own report\n"), 0o644))

	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git add: %v\n%s", err, out)

	cmd = exec.Command("git", "commit", "-q", "-m", "foreign work")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Someone", "GIT_AUTHOR_EMAIL=someone@elsewhere.example",
		"GIT_COMMITTER_NAME=Someone", "GIT_COMMITTER_EMAIL=someone@elsewhere.example",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "git commit: %v\n%s", err, out)
}

// TestCLIExamplesMatchWhatTheToolPrints reads the rewritten examples in docs/CLI.md
// through onboarding.Transcript and onboarding.Compare, following the pattern
// in hygiene_test.go. All four rewritten CLI examples are executed and compared.
func TestCLIExamplesMatchWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	require.NoError(t, err)

	linesHygiene, err := onboarding.Transcript(string(raw), "nova-check", "hygiene")
	require.NoError(t, err)

	linesDogfood, err := onboarding.Transcript(string(raw), "nova-check", "The dogfood ledger")
	require.NoError(t, err)

	// Cut the steps the way hygiene_test.go does: every hygiene command carries
	// <email> quoted in --identity, and the shared parser does not run a line
	// holding > even quoted.
	var steps []onboarding.Step
	for _, lines := range [][]string{linesHygiene, linesDogfood} {
		var curStep *onboarding.Step
		for _, line := range lines {
			if strings.HasPrefix(line, "$ ") {
				cmd := strings.TrimPrefix(line, "$ ")
				if strings.HasSuffix(cmd, "\\") {
					cmd = strings.TrimSuffix(cmd, "\\")
				}
				args, err := onboarding.SplitShell(cmd)
				require.NoError(t, err, "cannot split transcript line %q: %v", line, err)
				require.True(t, len(args) > 0 && args[0] == "nova-check", "not a nova-check command: %q", line)
				steps = append(steps, onboarding.Step{Line: line, Args: args[1:]})
				curStep = &steps[len(steps)-1]
				continue
			}
			if curStep != nil && strings.HasPrefix(line, "    ") && strings.HasSuffix(curStep.Line, "\\") {
				// Continuation line of a command
				lineClean := strings.TrimSpace(line)
				if strings.HasSuffix(lineClean, "\\") {
					lineClean = strings.TrimSuffix(lineClean, "\\")
				}
				extraArgs, err := onboarding.SplitShell(lineClean)
				require.NoError(t, err, "cannot split continuation line %q: %v", line, err)
				curStep.Args = append(curStep.Args, extraArgs...)
				continue
			}
			if curStep != nil {
				curStep.Want = append(curStep.Want, line)
			}
		}
	}

	for i := range steps {
		for len(steps[i].Want) > 0 && strings.TrimSpace(steps[i].Want[len(steps[i].Want)-1]) == "" {
			steps[i].Want = steps[i].Want[:len(steps[i].Want)-1]
		}
	}

	var stepDogfood, stepRefusal, stepHygieneOK, stepHygieneMax *onboarding.Step
	for i := range steps {
		s := &steps[i]
		switch {
		case strings.Contains(s.Line, "dogfood record"):
			stepDogfood = s
		case strings.Contains(s.Line, "fix-with-red-test"):
			stepRefusal = s
		case strings.Contains(s.Line, "sign/**") && !strings.Contains(s.Line, "--max"):
			stepHygieneOK = s
		case strings.Contains(s.Line, "sign/**") && strings.Contains(s.Line, "--max"):
			stepHygieneMax = s
		}
	}
	require.NotNil(t, stepDogfood, "dogfood record step missing from transcript")
	require.NotNil(t, stepRefusal, "hygiene refusal step missing from transcript")
	require.NotNil(t, stepHygieneOK, "hygiene OK step missing from transcript")
	require.NotNil(t, stepHygieneMax, "hygiene max step missing from transcript")

	t.Run("dogfood record", func(t *testing.T) {
		seams := dogfoodSeams{clock: func() time.Time { return time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC) }}

		t.Cleanup(func() { _ = os.RemoveAll("./dogfood-receipts") })
		_ = os.RemoveAll("./dogfood-receipts")

		args := append([]string(nil), stepDogfood.Args...)
		hasSource := false
		for _, a := range args {
			if a == "--cli" || a == "--tools" {
				hasSource = true
				break
			}
		}
		if !hasSource {
			args = append(args, "--cli", filepath.Join("..", "..", "docs", "CLI.md"))
		}

		code, stdout, stderr := dogfoodRunWith(seams, args[1:]...)
		require.Equal(t, 0, code, "exit %d, stderr: %s", code, stderr)
		res := onboarding.Result{Code: code, Stdout: stdout, Stderr: stderr}
		normFilePrefix, err := onboarding.Elide("receipt path prefix", `file=\./`, "file=")
		require.NoError(t, err)
		normReceiptSum, err := onboarding.Elide("receipt hash", `ada-[0-9a-f]{8}\.json`, "ada-<hash>.json")
		require.NoError(t, err)

		for _, p := range onboarding.Compare(*stepDogfood, res, []onboarding.Norm{normFilePrefix, normReceiptSum}) {
			assert.Fail(t, "check failed", p.Message)
		}
	})

	t.Run("hygiene refusal", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := run(stepRefusal.Args, &out, &errb)
		require.Equal(t, 2, code)
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		for _, p := range onboarding.Compare(*stepRefusal, res, nil) {
			assert.Fail(t, "check failed", p.Message)
		}
	})

	t.Run("hygiene ok", func(t *testing.T) {
		labDir := setupCLILab(t)
		args := append([]string(nil), stepHygieneOK.Args...)
		for i := range args {
			if args[i] == "." && i > 0 && args[i-1] == "--repo" {
				args[i] = labDir
			}
		}
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		require.Equal(t, 0, code, "exit %d, stderr: %s", code, errb.String())
		res := onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}
		for _, p := range onboarding.Compare(*stepHygieneOK, res, nil) {
			assert.Fail(t, "check failed", p.Message)
		}
	})

	t.Run("hygiene max", func(t *testing.T) {
		labDir := setupCLILab(t)
		addCLILabViolations(t, labDir)

		args := append([]string(nil), stepHygieneMax.Args...)
		for i := range args {
			if args[i] == "." && i > 0 && args[i-1] == "--repo" {
				args[i] = labDir
			}
		}
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		require.Equal(t, 1, code, "exit %d, stderr: %s", code, errb.String())
		res := onboarding.Result{Code: code, Stdout: out.String() + errb.String(), Stderr: ""}

		normRepo, err := onboarding.Elide("lab dir", regexp.QuoteMeta(labDir), ".")
		require.NoError(t, err)
		normSha, err := onboarding.Elide("commit sha", `at=[0-9a-f]{12}`, "at=0a19082d2973")
		require.NoError(t, err)

		for _, p := range onboarding.Compare(*stepHygieneMax, res, []onboarding.Norm{normRepo, normSha}) {
			assert.Fail(t, "check failed", p.Message)
		}
	})
}
