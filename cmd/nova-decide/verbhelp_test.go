package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads its inputs, asks a backend or writes the record it names
// (the CLI style's rule (b)). The banner behind them is held to what a cold
// reader pastes (ONBOARDING points 1 and 6, SPEC-TOOLWORK.md rule 6): the
// setup lines write the files the example block reads, so every example line
// runs as printed from an empty directory; the how text shows the lines a
// recorded ask prints, byte for byte through the one comparator; the exit
// codes say exit 0 means the decision was recorded; and no usage line runs
// past 100 columns.
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	record := []string{"--record", "{dir}/decisions.jsonl"}
	testverbhelp.Check(t, cli.NoStdin(), []testverbhelp.Case{
		{Verb: "ask", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "read", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "score", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "gate", Flags: append([]string{"--backend", "jev"}, record...)},
		{Verb: "brief", Flags: append([]string{"--backend", "jev", "--card", "{dir}"}, record...)},
		{Verb: "outcome", Flags: record},
		{Verb: "calibrate", Flags: record},
		{Verb: "findings", Flags: record},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, cli.NoStdin(), "nova-decide", "ask", "read", "score", "gate", "brief", "outcome", "calibrate", "findings", "version")

	help := cli.OK(t, "help").Stdout
	examples, err := onboarding.ExampleLines(help, "nova-decide")
	require.NoError(t, err)

	t.Run("example block runs as printed from an empty directory", func(t *testing.T) {
		t.Parallel()
		assert.NotContains(t, help, "cmd/nova-decide/testdata", "the example block reads a source checkout, which a stranger pasting the lines has not")
		require.Equal(t, []string{
			"nova-decide ask --schema ./schema.json --state ./state.txt --backend fixed --answers ./answers.json --record ./decisions.jsonl --op card-1",
			`nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok --note "the review found nothing"`,
			"nova-decide findings --record ./decisions.jsonl --since 2026-10-01",
		}, examples, "the example block is ask, outcome and findings over the files the setup writes")

		bin := buildHelpBinary(t)
		dir := t.TempDir()
		for _, line := range setupLines(help) {
			exit, out, errs := runHelpLine(t, dir, bin, line)
			require.Zero(t, exit, "the setup line %q exits %d; its output: %s%s", line, exit, out, errs)
		}
		var ask, askOut string
		for _, line := range examples {
			exit, out, errs := runHelpLine(t, dir, bin, line)
			assert.Zero(t, exit, "the example %q exits %d, want 0 -- a line a stranger pastes must run as printed; its output: %s%s", line, exit, out, errs)
			if strings.HasPrefix(line, "nova-decide ask ") {
				ask, askOut = line, out
			}
		}
		require.NotEmpty(t, ask, "the example block holds no ask line")
		// The how text shows the lines the recorded ask printed, byte for
		// byte, compared by the one comparator (SPEC-TOOLWORK.md documents
		// rule 2; rule 6 holds the example lines to it).
		shown := shownResultLines(help)
		require.Len(t, shown, 2, "the how text shows the two lines a recorded ask prints")
		doc := []onboarding.Step{{Line: ask, Want: shown}}
		assert.Empty(t, onboarding.CompareTranscript(doc, []onboarding.Result{{Code: 0, Stdout: askOut}}, nil),
			"the lines the how text shows are not the lines a recorded ask prints")
	})

	t.Run("exit codes say 0 means the decision was recorded", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, help, "exit codes: 0 done, the decision was recorded, never that the answer was yes;")
		// The behaviour the sentence describes: an ask whose answer is no is
		// still exit 0, because the decision was recorded.
		schema, state, answers := writeSetupFiles(t, t.TempDir())
		cli.Do(t, "ask", "--schema", schema, "--state", state, "--backend", "fixed", "--answers", answers,
			"--record", filepath.Join(t.TempDir(), "decisions.jsonl"), "--op", "no-1").
			Exit(0).Out("ASK ANSWER question=ok type=noul value=no p=yes:0.1", "recorded=new")
	})

	t.Run("every usage line wraps at 100 columns", func(t *testing.T) {
		t.Parallel()
		var wrapped int
		inUsage := false
		for _, line := range strings.Split(help, "\n") {
			switch {
			case line == "usage:":
				inUsage = true
			case inUsage && line == "":
				inUsage = false
			case inUsage:
				wrapped++
				assert.LessOrEqual(t, utf8.RuneCountInString(line), 100, "the usage line is over 100 columns: %s", line)
			}
		}
		require.Positive(t, wrapped, "the banner holds no usage block; this subtest would pass by checking nothing")
	})

	t.Run("the other verbs' examples answer from -h and run", func(t *testing.T) {
		t.Parallel()
		run := sitting(t)
		for _, verb := range []string{"read", "score", "attempt", "grade", "gate", "brief", "calibrate"} {
			t.Run(verb, func(t *testing.T) {
				t.Parallel()
				var example string
				for _, line := range strings.Split(cli.OK(t, verb, "-h").Stdout, "\n") {
					if strings.Contains(line, "nova-decide "+verb+" ") && strings.Contains(line, "testdata") && !strings.Contains(line, "<") {
						example = strings.TrimSpace(line)
					}
				}
				require.NotEmpty(t, example, "the -h shows no runnable example line; the banner's example moved here")
				fields, err := onboarding.SplitShell(strings.TrimPrefix(example, "Example, from a checkout root: "))
				require.NoError(t, err, "the example line %q does not split as one command", example)
				res := run(fields[1:])
				assert.Equal(t, 0, res.Code, "the example %q exits %d; its output: %s", example, res.Code, res.Stderr)
			})
		}
	})
}

// TestHelpShowsASchemaAndAResultLine holds the banner's how-it-works text to
// what a cold reader needs before a first run: the term noul defined where it
// first appears, the setup's inline schema, state and fixed-backend answers,
// the ANSWER line the run prints, and that exit 0 means the decision was
// recorded.
func TestHelpShowsASchemaAndAResultLine(t *testing.T) {
	t.Parallel()
	help := cli.OK(t, "help").Stdout

	first := ""
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "noul") {
			first = line
			break
		}
	}
	require.NotEmpty(t, first, "the help never uses the term noul")
	assert.Contains(t, first, "yes-or-no", "noul is not defined where it first appears: %q", first)

	assert.Contains(t, help, `{"name":`, "the help carries no inline schema example")
	assert.Contains(t, help, "ANSWER question=", "the help shows no result line")
	assert.Contains(t, help, "the decision was recorded, never that the answer was yes", "the help does not say exit 0 is recorded, never that the answer was yes")
}

// setupLines returns the command lines under the banner's `setup:` heading,
// which write the files the `example:` block reads, in banner order.
func setupLines(banner string) []string {
	_, tail, found := strings.Cut(banner, "\nsetup:\n")
	if !found {
		return nil
	}
	var out []string
	for _, line := range strings.Split(tail, "\n") {
		if strings.TrimSpace(line) == "" || line[0] != ' ' {
			break
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

// shownResultLines returns the how-it-works lines that begin as the lines an
// ask prints: the ASK OK line and the ASK ANSWER lines, in banner order.
func shownResultLines(banner string) []string {
	var out []string
	for _, line := range strings.Split(banner, "\n") {
		if strings.HasPrefix(line, "ASK OK ") || strings.HasPrefix(line, "ASK ANSWER ") {
			out = append(out, line)
		}
	}
	return out
}

// writeSetupFiles writes the three files the banner's setup block writes, into
// dir, and returns their paths: the same bytes a stranger pastes from the
// banner lands there.
func writeSetupFiles(t *testing.T, dir string) (schema, state, answers string) {
	t.Helper()
	schema, state, answers = filepath.Join(dir, "schema.json"), filepath.Join(dir, "state.txt"), filepath.Join(dir, "answers.json")
	require.NoError(t, os.WriteFile(schema, []byte(`{"name":"q","questions":{"ok":{"type":"noul","instructions":"It asks."}}}`), 0o644))
	require.NoError(t, os.WriteFile(state, []byte("R?"), 0o644))
	require.NoError(t, os.WriteFile(answers, []byte(`{"ok":{"noul":0.1}}`), 0o644))
	return schema, state, answers
}

// buildHelpBinary builds this command into a temp dir and returns its path. It
// builds rather than calls the package because the question is what a stranger
// meets at a shell prompt; the environment is sanitized with goenv.Clean
// because the parent's GOFLAGS can reshape a go command's output under the
// parser that reads it (internal/goenv, the `goenv` class test).
func buildHelpBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-decide")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = goenv.Clean(os.Environ())
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building nova-decide: %v\n%s", err, out)
	return bin
}

// runHelpLine runs one line of the banner through `sh -c` with the built
// binary first on PATH, from dir, with stdin closed: the line runs exactly as
// a stranger pastes it. It returns the exit code and both streams.
func runHelpLine(t *testing.T, dir, bin, line string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = nil
	var out, errs strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errs
	var exitErr *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
		return 0, out.String(), errs.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), out.String(), errs.String()
	default:
		require.FailNowf(t, "help line failed", "running %q: %v", line, err)
		return 0, "", ""
	}
}
