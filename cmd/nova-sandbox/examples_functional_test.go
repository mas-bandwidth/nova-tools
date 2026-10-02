//go:build functional

package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run both HOME-prefixed examples, including their setup, in an owned fixture.
func TestSandboxCLISetupAndCommandsMatchOutput(t *testing.T) {
	t.Parallel()
	needDarwin(t)
	if _, err := os.Stat("/opt/homebrew/bin/git"); err != nil {
		t.Skip("example requires /opt/homebrew/bin/git")
	}
	doc := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "CLI.md"))
	j := newJob(t)
	owned := func(p string) string {
		t.Helper()
		p = strings.ReplaceAll(p, "/path/to", j.base)
		require.True(t, strings.HasPrefix(p, j.base+string(filepath.Separator)), "example path escapes fixture: %s", p)
		return p
	}
	secret := owned("/path/to/.config/anthropic/env")
	require.NoError(t, os.MkdirAll(filepath.Dir(secret), 0700))
	require.NoError(t, os.WriteFile(secret, nil, 0600))
	repo := owned("/path/to/pool/jobs/j1/repo")
	cmd := exec.Command("/opt/homebrew/bin/git", "init", "--quiet", "-b", "main", repo)
	cmd.Env = j.env("GIT_CONFIG_NOSYSTEM=1")
	initOut, err := cmd.CombinedOutput()
	require.NoError(t, err, "init: %v: %s", err, initOut)
	exe, err := os.Executable()
	require.NoError(t, err)
	exe, err = filepath.EvalSymlinks(exe)
	require.NoError(t, err)
	pid, err := onboarding.Elide("probe process id", `\.nova-sandbox-probe-[0-9]+`, ".nova-sandbox-probe-PID")
	require.NoError(t, err)
	ancestors, err := onboarding.Elide("number of fixture ancestors", `ancestors=[0-9]+`, "ancestors=N")
	require.NoError(t, err)
	cwd := owned("/path/to/pool/jobs/j1")
	norms := []onboarding.Norm{onboarding.Path("/path/to/.local/bin/nova-sandbox", exe), onboarding.Path("/path/to", j.base), pid, ancestors, onboarding.Path(base64.RawURLEncoding.EncodeToString([]byte("/path/to/pool/jobs/j1")), base64.RawURLEncoding.EncodeToString([]byte(cwd)))}
	// Compare the complete help invocation with the command executed below.
	const jobExamples = "macOS job examples (replace /path/to with your own paths):\n"
	bannerStart := strings.Index(usage, jobExamples)
	require.GreaterOrEqual(t, bannerStart, 0, "missing help example")
	bannerBlock := strings.SplitN(usage[bannerStart+len(jobExamples):], "\n\n", 2)[0]
	bannerCommands := exampleCommands(t, bannerBlock, j.base)
	require.Len(t, bannerCommands, 1, "expected one help wrap command")
	count := 0
	for i, block := range strings.Split(doc, "```") {
		if i%2 == 0 || (!strings.Contains(block, "$ mkdir -p /path/to/pool/jobs/j1/home") && !strings.Contains(block, "$ HOME=/path/to/pool/jobs/j1/home \\")) {
			continue
		}
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		for n := 0; n < len(lines); {
			require.True(t, strings.HasPrefix(lines[n], "$ "), "unexpected example line %q", lines[n])
			step := onboarding.Step{Line: lines[n], StderrWhole: true}
			command := strings.TrimPrefix(lines[n], "$ ")
			n++
			for strings.HasSuffix(command, "\\") {
				require.NotEqual(t, len(lines), n, "unterminated continuation")
				command = strings.TrimSuffix(command, "\\") + strings.TrimSpace(lines[n])
				n++
			}
			for n < len(lines) && !strings.HasPrefix(lines[n], "$ ") {
				step.Want = append(step.Want, lines[n])
				n++
			}
			argv, err := onboarding.SplitShell(command)
			require.NoError(t, err)
			for k, a := range argv {
				if strings.Contains(a, "/path/to") {
					argv[k] = strings.ReplaceAll(a, "/path/to", j.base)
				}
			}
			if len(argv) > 2 && argv[2] == "--read" {
				bannerArgs, err := onboarding.SplitShell(bannerCommands[0])
				require.NoError(t, err, "help and CLI wrap invocations differ")
				require.Equal(t, bannerArgs, argv, "help and CLI wrap invocations differ")
			}
			var result onboarding.Result
			if argv[0] == "mkdir" {
				require.True(t, len(argv) == 3 && argv[1] == "-p", "unsupported setup: %s", command)
				target := owned(argv[2])
				mkdir := exec.Command("/bin/mkdir", "-p", target)
				out, err := mkdir.CombinedOutput()
				require.NoError(t, err, "mkdir: %v: %s", err, out)
				result.Stdout = string(out)
			} else {
				require.True(t, len(argv) >= 3 && strings.HasPrefix(argv[0], "HOME=") && argv[1] == "nova-sandbox", "unsupported command: %s", command)
				home := owned(strings.TrimPrefix(argv[0], "HOME="))
				result = saw(t, []string{"HOME=" + home, "PATH=/opt/homebrew/bin:/usr/bin:/bin"}, argv[2:]...)
			}
			assert.Equal(t, 0, result.Code, "%s exited %d: %s", step.Line, result.Code, result.Stderr)
			for _, problem := range onboarding.Compare(step, result, norms) {
				t.Error(problem)
			}
			count++
		}
	}
	require.Equal(t, 3, count, "compared %d commands, want setup, probe and git", count)
}

// The portable first help command uses the recorded macOS transcript here;
// the built-binary onboarding gate checks its invocation on every platform.
func TestHelpCheckExampleMatchesTranscript(t *testing.T) {
	t.Parallel()
	needDarwin(t)
	examples, err := onboarding.ExampleLines(usage, "nova-sandbox")
	require.NoError(t, err)
	require.Equal(t, []string{"nova-sandbox check"}, examples, "help examples = %v, want one check command", examples)
	lines, err := onboarding.FirstRun(testkit.ReadFile(t, filepath.Join("..", "..", "docs", "TESTS.md")), "nova-sandbox")
	require.NoError(t, err)
	require.NotEmpty(t, lines, "first transcript command does not match help example %q", examples[0])
	require.Equal(t, "$ "+examples[0], lines[0], "first transcript command does not match help example %q", examples[0])
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "$ ") {
			lines = lines[:i]
			break
		}
	}
	steps, err := onboarding.Steps("nova-sandbox", lines)
	require.NoError(t, err)
	require.Len(t, steps, 1, "check transcript has %d steps, want one", len(steps))
	step := steps[0]
	step.StderrWhole = true
	for _, problem := range onboarding.Compare(step, saw(t, os.Environ(), step.Args...), nil) {
		t.Error(problem)
	}
}
