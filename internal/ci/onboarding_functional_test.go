//go:build functional

package ci

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/goenv"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// onboarding_functional_test.go builds every command and runs it bare and with
// help: builds are the functional tier's (Glenn 2026-09-26, nova-tools#4328).

// notYetOnTheStandard names the commands whose onboarding work is in flight on
// another branch, with the branch named, so that a skip here is a dated pointer
// rather than a permanent exemption. Each entry earns its skip only while the
// docs/TESTS.md section is genuinely still missing: the moment that branch
// merges, the condition below stops firing and the entry can be deleted.
// It is EMPTY, and an empty list is the point: nova-bus was the last entry, and it
// sat here after the branch it named had merged -- the draft verb, the tolerant send
// and the refusals all landed, the `### First run` did not, and the one tool the
// README sends a stranger to first was the one tool excused from the standard. The
// transcript is now in docs/TESTS.md and cmd/nova-bus/firstrun_functional_test.go executes it.
// An entry added here has to name a branch that is genuinely open, and it stops
// firing the moment that branch's section lands.
var notYetOnTheStandard = map[string]string{}

func TestEveryCommandMeetsTheOnboardingStandard(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	transcripts := readFile(t, filepath.Join(root, "docs", "TESTS.md"))
	catalogue := readmeWhatItDoes(t, readFile(t, filepath.Join(root, "README.md")))

	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	require.NoError(t, err)
	found := 0
	// The tool-answers rule is measured on the same binaries and held to its
	// ledger once every subtest has finished (toolanswers_class_test.go).
	answers := newToolAnswers(t)
	t.Cleanup(func() { answers.check(t, found) })
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found++
		tool := e.Name()
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			answers.begin()
			_, firstRunErr := onboarding.FirstRun(transcripts, tool)
			if branch, inFlight := notYetOnTheStandard[tool]; inFlight && firstRunErr != nil {
				t.Skipf("%s is being brought up to this standard on branch %s; delete its entry from notYetOnTheStandard when that branch merges (%v)", tool, branch, firstRunErr)
			}

			// (c) The transcript section in docs/TESTS.md that a test executes.
			assert.NoError(t, firstRunErr, "%v\n(docs/STANDARD.md, onboarding point 5(c): every tool's docs/TESTS.md section opens with `%s`)", firstRunErr, onboarding.FirstRunHeading)

			// (a), first half: the bare command REFUSES in one line and names the
			// door. It used to be the banner itself, which cost between 1,900 and
			// 6,500 bytes to say that no arguments is not an invocation — and cost
			// the same on every flag typo, which is the common case.
			bin := builtTool(t, root, tool)
			exit, stdout, stderr := runBare(t, root, tool, bin, nil)
			assert.Equal(t, 2, exit, "a bare `%s` exits %d, want 2 (could not run — no arguments is not an invocation)", tool, exit)
			assert.Empty(t, stdout, "a bare `%s` wrote to stdout: %q; a refusal belongs on stderr", tool, stdout)
			// One line, or two: point 2 says a refusal must state what the input
			// WANTS, and where that guidance is a sentence of its own it follows on
			// one indented line. Two is the ceiling, and the second line has to be
			// the hint rather than more of the banner.
			lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
			switch {
			case len(lines) > 2:
				assert.Fail(t, fmt.Sprintf("a bare `%s` printed %d lines, want 1 (or 2 with its hint); the banner is behind `%s help`, not in front of every mistake:\n%s", tool, len(lines), tool, stderr))
			case len(lines) == 2 && !strings.HasPrefix(lines[1], "  "):
				assert.Fail(t, fmt.Sprintf("a bare `%s` printed a second line that is not an indented hint:\n%s", tool, stderr))
			}
			want := "run: " + tool + " help"
			assert.Contains(t, stderr, want, "a bare `%s` names no door; it must contain %q:\n%s", tool, want, stderr)

			// (a), second half: the door opens, on stdout, at exit 0, and what is
			// behind it ends in runnable lines.
			exit, banner, helpErr := runBare(t, root, tool, bin, []string{"help"})
			assert.Equal(t, 0, exit, "`%s help` exits %d, want 0; stderr: %s", tool, exit, helpErr)
			// Every verb of it answers -h with that verb's help (verbhelp_functional_test.go),
			// and every mistake with the way forward (toolanswers_functional_test.go).
			if helps, living := everyVerbAnswersHelp(t, root, tool, bin, banner); living {
				measureToolAnswers(t, answers, tool, bin, banner, stderr, helps)
			}
			answers.settle()

			// (d) The banner answers the three questions a stranger brings, before
			// the usage lines (docs/ONBOARDING.md point 6): line 1 says in one
			// sentence what the tool does, a `how it works:` paragraph near the top
			// names its nouns and where its state lives, and the `example:` block
			// is a first run of at least three command lines. No exemptions.
			sentence, err := onboarding.OpeningSentence(banner, tool)
			assert.NoError(t, err, "%v\n(docs/ONBOARDING.md point 6: what does it do?)", err)
			if assert.NotZero(t, onboarding.HowItWorksLine(banner), "`%s help` has no %q paragraph in its first %d lines; it names the tool's nouns and where its state lives (docs/ONBOARDING.md point 6: how does it work?)", tool, onboarding.HowItWorksLabel, onboarding.HowItWorksWithin) {
				n := onboarding.HowItWorksLength(banner)
				assert.LessOrEqual(t, n, onboarding.HowItWorksMaxLines, "`%s help`'s %q paragraph takes %d lines, over %d; it names the nouns and where the state lives, and the usage says the rest (docs/ONBOARDING.md point 6)", tool, onboarding.HowItWorksLabel, n, onboarding.HowItWorksMaxLines)
			}
			got := onboarding.ExampleCommands(banner, tool)
			assert.GreaterOrEqual(t, len(got), onboarding.MinExampleCommands, "`%s help`'s example: block runs the tool %d time(s): %q; a first run is at least %d command lines a stranger runs in order (docs/ONBOARDING.md point 6: how do I use it?)", tool, len(got), got, onboarding.MinExampleCommands)
			// The README's catalogue says the same sentence, so a reader choosing a
			// tool there and a reader opening its help meet one answer.
			if cell, listed := catalogue[tool]; listed && err == nil {
				assert.Equal(t, cell, sentence, "README.md's \"What it does\" for %s is %q, and line 1 of `%s help` says %q; they are one sentence", tool, cell, tool, sentence)
			}

			examples, err := onboarding.ExampleLines(banner, tool)
			require.NoError(t, err, "%v\n(docs/STANDARD.md, onboarding point 1)\n\nwhat it printed:\n%s", err, banner)
			for _, ex := range examples {
				ok := !strings.Contains(ex, "<") && !strings.Contains(ex, ">")
				assert.True(t, ok, "the example %q still carries a placeholder; the `example:` block is for lines a stranger can paste, and the usage block above it is where <dir> and <file> belong", ex)
			}
		})
	}
	require.NotZero(t, found, "no command directories found under cmd/; this test was looking in the wrong place and would have passed by checking nothing")
}

// readmeWhatItDoes returns the "What it does" cell of every row of README.md's
// catalogue, keyed by the tool the row links, HTML entities decoded. A catalogue
// without that column, or a row without the cell, fails here rather than
// passing by comparing nothing.
func readmeWhatItDoes(t *testing.T, readme string) map[string]string {
	t.Helper()
	header := regexp.MustCompile(`<thead><tr>(.*?)</tr></thead>`).FindStringSubmatch(readme)
	require.NotNil(t, header, "README.md has no catalogue table header")
	col := -1
	for i, th := range regexp.MustCompile(`<th>(.*?)</th>`).FindAllStringSubmatch(header[1], -1) {
		if th[1] == "What it does" {
			col = i
		}
	}
	require.GreaterOrEqual(t, col, 0, "README.md's catalogue has no \"What it does\" column: %s", header[0])
	cells := regexp.MustCompile(`<td(?: nowrap)?>(.*?)</td>`)
	link := regexp.MustCompile(`<a href="[^"]+">(nova-[a-z-]+)</a>`)
	out := map[string]string{}
	for _, row := range regexp.MustCompile(`<tr><td>.*?</tr>`).FindAllString(readme, -1) {
		tds := cells.FindAllStringSubmatch(row, -1)
		var tool string
		for _, td := range tds {
			if m := link.FindStringSubmatch(td[1]); m != nil && tool == "" {
				tool = m[1]
			}
		}
		ok := tool != "" && col < len(tds)
		require.True(t, ok, "README.md catalogue row names no tool or has no \"What it does\" cell: %s", row)
		out[tool] = html.UnescapeString(tds[col][1])
	}
	require.NotEmpty(t, out, "README.md's catalogue has no rows")
	return out
}

// builtTool returns the path of one command from the package's one build of
// every command (builtTools). It is BUILT rather than called as a package,
// because what this test is about is what a stranger meets at a shell prompt.
// The binary is shared: the onboarding walk, the refusal-grammar walk and the
// version walk all run the same build, which is one `go build ./cmd/...` per
// package run instead of a link per tool per walk (#516; the per-walk links
// took the functional internal/ci package past its 100 s bound at GOMAXPROCS=2).
func builtTool(t *testing.T, root, tool string) string {
	t.Helper()
	bin := filepath.Join(builtTools(t, root), exeName(tool))
	_, err := os.Stat(bin)
	require.NoError(t, err, "cmd/%s is a directory under cmd/ but `go build ./cmd/...` made no %s; every directory under cmd/ is a command", tool, filepath.Base(bin))
	return bin
}

// builtTools builds every command ONCE per package run, in one `go build -o
// <dir>/ ./cmd/...`, and returns the directory the binaries are in. The first
// caller builds while the others wait; every caller reads the one result, and
// a failed build fails every caller with its output. TestMain removes the
// directory (classtests_class_test.go).
func builtTools(t *testing.T, root string) string {
	t.Helper()
	toolsShared.once.Do(func() {
		dir, err := os.MkdirTemp("", "ci-built-tools-")
		if err != nil {
			toolsShared.err = err
			return
		}
		toolsShared.dir = dir
		build := exec.Command("go", "build", "-o", dir+string(os.PathSeparator), "./cmd/...")
		build.Env = goenv.Clean(os.Environ())
		build.Dir = root
		toolsShared.out, toolsShared.err = build.CombinedOutput()
	})
	require.NoErrorf(t, toolsShared.err, "building ./cmd/...: %v\n%s", toolsShared.err, toolsShared.out)
	return toolsShared.dir
}

// runBare runs the built command with the arguments given (none, or `help`).
func runBare(t *testing.T, root, tool, bin string, args []string) (exit int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = root
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	default:
		require.Fail(t, fmt.Sprintf("running %s: %v", tool, err))
	}
	return exit, out.String(), errb.String()
}
