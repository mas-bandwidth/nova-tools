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

// docs/TESTS.md's `## nova-post` is the one transcript of the eight this lane
// took that is NOT made to pass. It says something false about the tool, and
// the rule is that a false document is filed rather than bent: issue #1631.
//
// Two things are wrong with the section, and both are asserted below as
// EXPECTED FAILURES -- tests that are green exactly while the defect is there
// and go red the moment it is fixed, so the day #1631 is decided this file
// says so out loud instead of leaving a stale skip behind.
//
//  1. The prose says "Slice 1 carries only the in-process `fake` channel" and
//     the block runs `--channel fake` six times, while internal/post's
//     ParseChannel (post.go:111-118) accepts only ghost, bsky, email and
//     discord and refuses anything else at exit 2.
//  2. The block's first three commands write `<sha256>` where a hash goes.
//     docs/TESTS.md has no placeholder convention -- every other block in it is
//     real output pasted whole -- so those lines cannot be run by anything.
//
// When #1631 is settled, DELETE both tests below and give this package the same
// TestTESTSFirstRunIsWhatTheToolPrints its siblings carry: read the section,
// onboarding.Steps it, onboarding.Execute it, declaring the hash and the build
// triple. Nothing else here has to change; the harness is already in place.

// TestTESTSFirstRunIsNotRunnableUntil1631 pins defect (2): the transcript does
// not even parse into commands, because `--draft <sha256>` is a placeholder and
// not something a reader can type.
func TestTESTSFirstRunIsNotRunnableUntil1631(t *testing.T) {
	_, err := onboarding.Steps("nova-post", firstRunLines(t))
	if err == nil {
		t.Fatal("the `### First run` block now parses into runnable commands.\n" +
			"That is issue #1631 being fixed, and it is good news: delete this test and\n" +
			"TestTESTSFirstRunSaysFakeIsAChannelUntil1631, and add the executing\n" +
			"TestTESTSFirstRunIsWhatTheToolPrints this tool's siblings carry.")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Errorf("the block is unrunnable for a reason other than the `<sha256>` placeholder #1631 names: %v", err)
	}
}

// TestTESTSFirstRunSaysFakeIsAChannelUntil1631 pins defect (1) with a receipt
// rather than a claim: the documented command is run, in a temp directory
// holding the body and the allowlist the section's fixture line describes, so
// the refusal cannot be about a missing file. It is about the channel.
//
// BOTH halves of the receipt are READ FROM THE DOCUMENT -- the command and the
// block written under it. A test that keeps its own copy of the document's line
// cannot notice the document changing, which is the failure this whole harness
// exists to catch; it would go on printing a receipt for a line that had been
// rewritten or deleted. It is read by hand rather than with onboarding.Steps
// because Steps REFUSES this block until #1631 is decided -- that is the other
// tripwire, TestTESTSFirstRunIsNotRunnableUntil1631, and the two must not be
// the same test.
func TestTESTSFirstRunSaysFakeIsAChannelUntil1631(t *testing.T) {
	documented, shown := "", []string(nil)
	lines := firstRunLines(t)
	for i, line := range lines {
		if !strings.HasPrefix(line, "$ nova-post draft --channel fake") {
			continue
		}
		documented = strings.TrimPrefix(line, "$ ")
		for _, under := range lines[i+1:] {
			if strings.HasPrefix(under, "$ ") || strings.TrimSpace(under) == "" {
				break
			}
			shown = append(shown, under)
		}
		break
	}
	if documented == "" {
		t.Fatal("the `### First run` block no longer drafts on `--channel fake`.\n" +
			"That is issue #1631 being fixed: delete this test and\n" +
			"TestTESTSFirstRunIsNotRunnableUntil1631, and add the executing\n" +
			"TestTESTSFirstRunIsWhatTheToolPrints this tool's siblings carry.")
	}
	if len(shown) == 0 {
		t.Fatalf("the document writes nothing under\n  $ %s\nso there is no promise here to hold #1631 against", documented)
	}
	args, err := onboarding.SplitShell(documented)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	t.Chdir(dir)
	write(t, "body.md", "A first post for the friends channel.\n")
	write(t, "allowlist", "fake\tfriends\n")

	code, stdout, stderr := cli(args[1:]...)
	if code != 2 || !strings.Contains(stderr, "bad-channel") {
		t.Fatalf("the documented command\n  $ %s\nexits %d with\n  %s%s\nand issue #1631 says it refuses `fake` at exit 2.\nIf `fake` now ships, that issue is fixed: delete this test and its neighbour\nand add the executing TestTESTSFirstRunIsWhatTheToolPrints.",
			documented, code, stdout, stderr)
	}
	// The receipt, in the words the document is measured against. It is logged
	// rather than only asserted so that `go test -v ./cmd/nova-post/` prints
	// what #1631 was filed on -- and the document's side of it is the document's
	// own block, read above, not a copy of it kept here.
	t.Logf("#1631, still open: the documented command\n  $ %s\nprints\n  %sand the document shows\n%s", documented, stderr, onboarding.Block(shown))
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
