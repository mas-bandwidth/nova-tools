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
	"github.com/mas-bandwidth/nova-tools/internal/post"
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

// docs/TESTS.md's `## nova-post` is the one transcript of the eight this lane
// took that is NOT made to pass. It says something false about the tool, and
// the rule is that a false document is filed rather than bent: issue #1631.
//
// Two things are wrong with the section:
//
//  1. The section runs `--channel fake` six times. internal/post's
//     ParseChannel (post.go:111-118) used to refuse anything outside ghost,
//     bsky, email and discord at exit 2. The card fix3-nova-tools-1631 made
//     ParseChannel accept `fake` as a fifth channel, so the documented
//     command is no longer refused at the flag-parse layer. The tripwire
//     that pinned that defect (TestTESTSFirstRunSaysFakeIsAChannelUntil1631)
//     has been deleted: its specific assertion could no longer hold once
//     ParseChannel stopped refusing `fake`.
//  2. The block's first three commands write `<sha256>` where a hash goes.
//     docs/TESTS.md has no placeholder convention -- every other block in it is
//     real output pasted whole -- so those lines cannot be run by anything.
//     The tripwire below pins this with the parsing layer.
//
// When #1631 is fully settled -- the section re-cut to use ghost or to drop
// `<sha256>` -- delete TestTESTSFirstRunIsNotRunnableUntil1631 and add the
// executing TestTESTSFirstRunIsWhatTheToolPrints this tool's siblings carry.

// TestTESTSFirstRunIsNotRunnableUntil1631 pins the surviving defect (2): the
// transcript does not even parse into commands, because `--draft <sha256>` is
// a placeholder and not something a reader can type.
func TestTESTSFirstRunIsNotRunnableUntil1631(t *testing.T) {
	_, err := onboarding.Steps("nova-post", firstRunLines(t))
	if err == nil {
		t.Fatal("the `### First run` block now parses into runnable commands.\n" +
			"That is defect (2) of issue #1631 being fixed: delete this test and\n" +
			"add the executing TestTESTSFirstRunIsWhatTheToolPrints this tool's\n" +
			"siblings carry.")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("the block is unrunnable for a reason other than the `<sha256>` placeholder #1631 names: %v", err)
	}
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

// TestIssue1631 pins the defect filed in nova-tools #1631: docs/TESTS.md's
// `## nova-post` quickstart runs `--channel fake` six times, but
// internal/post's ParseChannel used to return a `bad-channel` Refusal at
// exit 2 for any value outside ghost, bsky, email and discord. The fix is to
// accept `fake` as a fifth channel so the documented command runs.
//
// red on base (ParseChannel refuses), green after the production change,
// red again when the production change is reverted.
func TestIssue1631(t *testing.T) {
	t.Helper()
	if _, err := post.ParseChannel("fake"); err != nil {
		t.Fatalf("post.ParseChannel(\"fake\") = %v\n#1631, open: docs/TESTS.md's `## nova-post` quickstart runs `--channel fake` and the binary must accept it, instead of refusing at exit 2.", err)
	}
}
