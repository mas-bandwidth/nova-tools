// First-run tests for nova-post: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` section are RUN rather than read, because an
// example that has drifted out of the flag set teaches the wrong invocation to
// exactly the reader who cannot tell.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

func cli(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	code, banner, stderr := cli("help")
	if code != 0 {
		t.Fatalf("`nova-post help` exit=%d, want 0; stderr: %s", code, stderr)
	}
	examples, err := onboarding.ExampleLines(banner, "nova-post")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 {
		t.Fatal("the `example:` block holds no command")
	}
	for _, ex := range examples {
		fields := strings.Fields(ex)
		code, stdout, stderr := cli(fields[1:]...)
		if code != 0 {
			t.Fatalf("the usage example %q exits %d: %s", ex, code, stderr)
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

func TestBareCommandRefusesInOneLine(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := cli()
	if code != 2 {
		t.Fatalf("a bare `nova-post` exits %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("a bare `nova-post` wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "run: nova-post help") {
		t.Fatalf("the refusal names no door: %q", stderr)
	}
	if n := len(strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")); n != 1 {
		t.Fatalf("the refusal is %d lines, want 1: %q", n, stderr)
	}
}

// TestTESTSFirstRunIsWhatTheToolPrints runs docs/TESTS.md's `## nova-post`
// `### First run` block as written, in a fresh directory holding exactly the
// fixture the section names, and compares every line. Issue #1631 was this block
// running `--channel fake` (not one of the spec's four channels, refused at exit
// 2) and writing `<sha256>` where a hash goes; the block was re-cut on `ghost`
// with the real hash pasted, and this test is what keeps it runnable.
//
// Every documented command must exit 0 as well as print what the page shows:
// the block is the quickstart, and a quickstart step that fails is not one.
//
// TWO normalisations are declared, as the siblings declare them: the
// `<goos>/<goarch> go<version>` tail of `version` is the machine the line was
// recorded on, and the version word is what the build stamped itself with.
// Nothing else is normalised, so the hash is compared byte for byte.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	steps, err := onboarding.Steps("nova-post", firstRunLines(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-post command; this test would pass by running nothing")
	}
	t.Chdir(t.TempDir())
	write(t, "body.md", "A first post for the ghost channel.\n")
	write(t, "allowlist", "ghost\texample.com\n")
	if err := os.Mkdir("drafts", 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range onboarding.Execute(steps, runDocumented, onboarding.Version(), onboarding.GoBuild()) {
		t.Error(p)
	}
}

// runDocumented calls this binary's entry point with the documented arguments
// and refuses a non-zero exit: Compare carries no exit code, and the quickstart
// promises every step runs.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, fmt.Errorf("nova-post reads no stdin; a `< %s` in its transcript is the document's bug", s.Stdin)
	}
	code, stdout, stderr := cli(s.Args...)
	if code != 0 {
		return onboarding.Result{}, fmt.Errorf("the documented command\n  %s\nexits %d, and every quickstart step must exit 0:\n%s%s", s.Line, code, stdout, stderr)
	}
	return onboarding.Result{Code: code, Stdout: stdout, Stderr: stderr}, nil
}

// firstRunLines is the `### First run` transcript of this tool, as written.
func firstRunLines(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-post")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("the `### First run` section holds no transcript")
	}
	return lines
}

func write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
