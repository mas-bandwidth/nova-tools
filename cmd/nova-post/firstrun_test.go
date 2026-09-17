// First-run tests for nova-post: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` section are RUN rather than read, because an
// example that has drifted out of the flag set teaches the wrong invocation to
// exactly the reader who cannot tell.
package main

import (
	"bytes"
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

func TestTESTSFirstRunSectionExists(t *testing.T) {
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
}
