package main

import (
	"errors"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/stretchr/testify/assert"
)

// TestHelpExampleLinesRunAsPrinted: every line of this tool's `example:` block runs, as printed,
// from the root of a checkout, and exits 0. nova-tools #1455 measured 28 of 61 pasted example lines
// exiting 2 because the line names an input the reader has not made. An example exiting 2 is a broken
// example (ONBOARDING point 1). SCOPE, said out loud: this covers nova-tokens's own block and nothing else.
//
// The usage banner is read AS SOURCE -- the `usage` constant beside this test -- rather than by
// running `help`, because the lines under test are the ones a reader pastes; the binary is built
// only so the lines can be RUN, with that binary on PATH and stdin closed, exactly as a stranger
// would. The block reads ./repos.tsv, ./transcripts, ./session.jsonl and ./out, so the setup
// block creates its inputs directly in an otherwise empty temporary directory.
//
// No example line here pushes, publishes, contacts a forge, acts on a machine or needs a key:
// fold, check, sum, sources, report and session read files and write one day file, so no line is
// skipped by name. A skip would be a hole, and this block has none.
func TestHelpExampleLinesRunAsPrinted(t *testing.T) {
	t.Parallel()

	lines := exampleBlockLines(usage)
	require.NotEmpty(t, lines, "the usage banner's `example:` blocks hold no line; this test would pass by running nothing")

	setupLines := fixtureSetupLines(usage)
	require.NotEmpty(t, setupLines, "the usage banner has no fixture setup block above the block, so a stranger pasting it names\n"+
		"inputs they have not made (nova-tools #1455: an example exiting 2 is a broken example).\n"+
		"The missing block opens with:\n  %s", wantFixtureSetup)
	assert.Equal(t, wantFixtureSetup, setupLines[0], "the setup block does not open with the line that makes the transcript directory")
	for _, setup := range setupLines {
		assert.NotContains(t, setup, "cmd/nova-tokens/testdata", "the setup still depends on a source checkout")
	}

	bin := buildExampleBinary(t)

	root := t.TempDir()
	for _, setup := range setupLines {
		exit, out := runExampleLine(t, root, filepath.Dir(bin), setup)
		require.Equal(t, 0, exit, "the fixture setup line exits %d, want 0:\n  %s\nits first output line: %s",
			exit, setup, exampleFirstLine(out))
	}

	for _, line := range lines {
		exit, out := runExampleLine(t, root, filepath.Dir(bin), line)
		assert.Equal(t, 0, exit, "the example `%s` exits %d, want 0 -- a line a stranger pastes must run as printed:\nfirst output line: %s",
			line, exit, exampleFirstLine(out))
		// The setup block writes ./repos.tsv and a transcript carrying a tool_use whose
		// file_path the block's own `schema` rule matches, so the first run shows a rules
		// file attributing: a fold that booked the day as unknown would mean the pasted
		// rule matches nothing (docs/SPEC-TOKENS.md: the first match on a session's path
		// wins, and what matched nothing is other=<pct>%).
		if strings.HasPrefix(line, "nova-tokens fold ") {
			assert.Contains(t, out, "unknown=0.0%", "the fold the block's own ./repos.tsv should attribute printed:\n%s", out)
		}
	}
}

// wantFixtureSetup is the first line of the standalone shell setup expected above the
// example block: the directories the pasted lines write into.
const wantFixtureSetup = "mkdir -p ./transcripts ./out"

// fixtureSetupLines returns the lines under the banner's `setup:` heading, in banner
// order, left-trimmed: the shell a stranger runs before the `example:` block, in the
// shape nova-memory's setup: uses (docs/STANDARD.md section 3, ONBOARDING point 6). The
// block ends at the first line the heading's own indent does not carry.
func fixtureSetupLines(usage string) []string {
	_, block, found := strings.Cut(usage, "\nsetup:\n")
	if !found {
		return nil
	}
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "  ") {
			break
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

// exampleBlockLines returns every command under an `example:` heading in a usage banner, in
// banner order, across every block. A line beginning with the tool's name under the heading is
// an example; a blank line closes the block, so the prose between two blocks is not swept up.
func exampleBlockLines(usage string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(usage, "\n") {
		if line == "example:" {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			inBlock = false
			continue
		}
		if strings.HasPrefix(trimmed, "nova-tokens ") {
			out = append(out, trimmed)
		}
	}
	return out
}

// buildExampleBinary builds this command into a temp dir and returns its path. It builds rather
// than calls the package because the question is what a stranger meets at a shell prompt; the
// environment is sanitized with goenv.Clean because the parent's GOFLAGS can reshape a go
// command's output under the parser that reads it (pkg/goenv, the `goenv` class test).
func buildExampleBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-tokens")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = goenv.Clean(os.Environ())
	cmd.Dir = "."
	{
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "building nova-tokens: %v\n%s", err, out)
	}
	return bin
}

// runExampleLine runs one line through `sh -c` with the built binary first on PATH, from dir,
// with stdin closed. It returns the exit code and the combined output.
func runExampleLine(t *testing.T, dir, binDir, line string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = nil
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
		return 0, out.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), out.String()
	default:
		require.FailNowf(t, "example command failed", "running %q: %v", line, err)
		return 0, ""
	}
}

// copyExampleTree copies src under dst, making directories as it goes. Every path it writes is
// inside the caller's t.TempDir() (AGENTS.md rule 10).
func copyExampleTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	require.NoError(t, err, "copying the fixture %s: %v", src, err)
}

// exampleFirstLine is the first line of an output, which is where a refusal says what was wrong.
func exampleFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
