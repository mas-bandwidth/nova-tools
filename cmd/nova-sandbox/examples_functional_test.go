//go:build functional

package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// Run both HOME-prefixed examples, including their setup, in an owned fixture.
func TestSandboxCLISetupAndCommandsMatchOutput(t *testing.T) {
	t.Parallel()
	needDarwin(t)
	if _, err := os.Stat("/opt/homebrew/bin/git"); err != nil {
		t.Skip("example requires /opt/homebrew/bin/git")
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	j := newJob(t)
	owned := func(p string) string {
		t.Helper()
		p = strings.ReplaceAll(p, "/path/to", j.base)
		if !strings.HasPrefix(p, j.base+string(filepath.Separator)) {
			t.Fatalf("example path escapes fixture: %s", p)
		}
		return p
	}
	secret := owned("/path/to/.config/anthropic/env")
	if err := os.MkdirAll(filepath.Dir(secret), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, nil, 0600); err != nil {
		t.Fatal(err)
	}
	repo := owned("/path/to/pool/jobs/j1/repo")
	cmd := exec.Command("/opt/homebrew/bin/git", "init", "--quiet", "-b", "main", repo)
	cmd.Env = j.env("GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v: %s", err, out)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := onboarding.Elide("probe process id", `\.nova-sandbox-probe-[0-9]+`, ".nova-sandbox-probe-PID")
	if err != nil {
		t.Fatal(err)
	}
	ancestors, err := onboarding.Elide("number of fixture ancestors", `ancestors=[0-9]+`, "ancestors=N")
	if err != nil {
		t.Fatal(err)
	}
	cwd := owned("/path/to/pool/jobs/j1")
	norms := []onboarding.Norm{onboarding.Path("/path/to/.local/bin/nova-sandbox", exe), onboarding.Path("/path/to", j.base), pid, ancestors, onboarding.Path(base64.RawURLEncoding.EncodeToString([]byte("/path/to/pool/jobs/j1")), base64.RawURLEncoding.EncodeToString([]byte(cwd)))}
	// Compare the complete help invocation with the command executed below.
	const jobExamples = "macOS job examples (replace /path/to with your own paths):\n"
	bannerStart := strings.Index(usage, jobExamples)
	if bannerStart < 0 {
		t.Fatal("missing help example")
	}
	bannerBlock := strings.SplitN(usage[bannerStart+len(jobExamples):], "\n\n", 2)[0]
	bannerCommands := exampleCommands(t, bannerBlock, j.base)
	if len(bannerCommands) != 1 {
		t.Fatal("expected one help wrap command")
	}
	count := 0
	for i, block := range strings.Split(string(doc), "```") {
		if i%2 == 0 || (!strings.Contains(block, "$ mkdir -p /path/to/pool/jobs/j1/home") && !strings.Contains(block, "$ HOME=/path/to/pool/jobs/j1/home \\")) {
			continue
		}
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		for n := 0; n < len(lines); {
			if !strings.HasPrefix(lines[n], "$ ") {
				t.Fatalf("unexpected example line %q", lines[n])
			}
			step := onboarding.Step{Line: lines[n], StderrWhole: true}
			command := strings.TrimPrefix(lines[n], "$ ")
			n++
			for strings.HasSuffix(command, "\\") {
				if n == len(lines) {
					t.Fatal("unterminated continuation")
				}
				command = strings.TrimSuffix(command, "\\") + strings.TrimSpace(lines[n])
				n++
			}
			for n < len(lines) && !strings.HasPrefix(lines[n], "$ ") {
				step.Want = append(step.Want, lines[n])
				n++
			}
			argv, err := onboarding.SplitShell(command)
			if err != nil {
				t.Fatal(err)
			}
			for k, a := range argv {
				if strings.Contains(a, "/path/to") {
					argv[k] = strings.ReplaceAll(a, "/path/to", j.base)
				}
			}
			if len(argv) > 2 && argv[2] == "--read" {
				bannerArgs, err := onboarding.SplitShell(bannerCommands[0])
				if err != nil || !reflect.DeepEqual(argv, bannerArgs) {
					t.Fatal("help and CLI wrap invocations differ")
				}
			}
			var result onboarding.Result
			if argv[0] == "mkdir" {
				if len(argv) != 3 || argv[1] != "-p" {
					t.Fatalf("unsupported setup: %s", command)
				}
				target := owned(argv[2])
				mkdir := exec.Command("/bin/mkdir", "-p", target)
				out, err := mkdir.CombinedOutput()
				if err != nil {
					t.Fatalf("mkdir: %v: %s", err, out)
				}
				result.Stdout = string(out)
			} else {
				if len(argv) < 3 || !strings.HasPrefix(argv[0], "HOME=") || argv[1] != "nova-sandbox" {
					t.Fatalf("unsupported command: %s", command)
				}
				home := owned(strings.TrimPrefix(argv[0], "HOME="))
				result.Code, result.Stdout, result.Stderr = j.tool(t, []string{"HOME=" + home, "PATH=/opt/homebrew/bin:/usr/bin:/bin"}, argv[2:]...)
			}
			if result.Code != 0 {
				t.Errorf("%s exited %d: %s", step.Line, result.Code, result.Stderr)
			}
			for _, problem := range onboarding.Compare(step, result, norms) {
				t.Error(problem)
			}
			count++
		}
	}
	if count != 3 {
		t.Fatalf("compared %d commands, want setup, probe and git", count)
	}
}

// The portable first help command uses the recorded macOS transcript here;
// the built-binary onboarding gate checks its invocation on every platform.
func TestHelpCheckExampleMatchesTranscript(t *testing.T) {
	t.Parallel()
	needDarwin(t)
	examples, err := onboarding.ExampleLines(usage, "nova-sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 1 || examples[0] != "nova-sandbox check" {
		t.Fatalf("help examples = %v, want one check command", examples)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(doc), "nova-sandbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || lines[0] != "$ "+examples[0] {
		t.Fatalf("first transcript command does not match help example %q", examples[0])
	}
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "$ ") {
			lines = lines[:i]
			break
		}
	}
	steps, err := onboarding.Steps("nova-sandbox", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("check transcript has %d steps, want one", len(steps))
	}
	step := steps[0]
	step.StderrWhole = true
	var out, errb bytes.Buffer
	code := run(step.Args, strings.NewReader(""), &out, &errb, os.Environ())
	for _, problem := range onboarding.Compare(step, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil) {
		t.Error(problem)
	}
}
