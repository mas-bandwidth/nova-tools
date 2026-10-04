package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
// runs as printed from an empty directory; the exit-codes sentence carries
// the recorded ask's own lines, byte for byte through the one comparator,
// and says exit 0 means the decision was recorded, never that the answer was
// yes (SPEC-NOVA-DECIDE section 7); and every usage line is one synopsis
// with no required flag bracketed as optional, because a second line at the
// same column is a second synopsis and a stranger pasting it alone omits the
// other line's required flags (a wrap at 100 columns with a continuation
// indent is internal/tool's to print, as this card's report proposes).
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
		// The exit-codes sentence shows the lines the recorded ask printed,
		// byte for byte, compared by the one comparator (SPEC-TOOLWORK.md
		// documents rule 2; rule 6 holds the example lines to it).
		shown := shownResultLines(help)
		require.Len(t, shown, 2, "the banner shows the two lines a recorded ask prints")
		doc := []onboarding.Step{{Line: ask, Want: shown}}
		assert.Empty(t, onboarding.CompareTranscript(doc, []onboarding.Result{{Code: 0, Stdout: askOut}}, nil),
			"the lines the banner shows are not the lines a recorded ask prints")
	})

	t.Run("exit codes show the recorded ask and say 0 means recorded", func(t *testing.T) {
		t.Parallel()
		codes := section(help, "exit codes: ")
		assert.Contains(t, codes, "0 done, exit 0 means the decision was recorded, never that the answer was yes; a recorded ask prints",
			"the exit-codes sentence does not say what exit 0 means")
		for _, line := range shownResultLines(help) {
			assert.Contains(t, codes, line, "the exit-codes sentence does not carry the recorded ask's own line %q", line)
		}
		assert.NotContains(t, codes, "setup:", "the exit codes are the exit sentence only; the setup block is the banner's")
		assert.NotContains(t, codes, "printf", "the exit codes are the exit sentence only; the setup block is the banner's")
		// Every verb's -h quotes the exit table (STANDARD.md, section 3), so
		// it quotes the sentence and never the setup block.
		h := cli.OK(t, "findings", "-h").Stdout
		assert.Contains(t, h, "exit 0 means the decision was recorded", "findings -h does not quote the exit sentence")
		assert.NotContains(t, h, "printf", "findings -h quotes the setup block, which is not exit codes")
		// The behaviour the sentence describes: an ask whose answer is no is
		// still exit 0, because the decision was recorded.
		schema, state, answers := writeSetupFiles(t, t.TempDir())
		cli.Do(t, "ask", "--schema", schema, "--state", state, "--backend", "fixed", "--answers", answers,
			"--record", filepath.Join(t.TempDir(), "decisions.jsonl"), "--op", "no-1").
			Exit(0).Out("ASK ANSWER question=ok type=noul value=no p=yes:0.1", "recorded=new")
	})

	t.Run("every usage line is one synopsis, its required flags unbracketed", func(t *testing.T) {
		t.Parallel()
		usage := usageLines(help)
		require.NotEmpty(t, usage, "the banner holds no usage block; this subtest would pass by checking nothing")
		for _, verb := range []string{"ask", "read", "score", "attempt", "grade", "gate", "brief", "outcome", "calibrate", "findings", "version", "help"} {
			var lines []string
			for _, l := range usage {
				if words := strings.Fields(l); len(words) > 1 && words[0] == "nova-decide" && words[1] == verb {
					lines = append(lines, l)
				}
			}
			if !assert.Len(t, lines, 1,
				"%d usage lines name %s; a second line is a second synopsis at the same column, and a stranger pasting it alone omits the other line's required flags",
				len(lines), verb) {
				continue
			}
			for _, name := range requiredFlags(cli.OK(t, verb, "-h").Stdout) {
				assert.Contains(t, lines[0], "--"+name, "%s's usage line omits --%s, which its -h marks required", verb, name)
				assert.NotContains(t, lines[0], "[--"+name, "%s's usage line brackets --%s as optional, which its -h marks required", verb, name)
			}
		}
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

// section returns a banner's text after one opening marker (its `exit codes: `)
// up to the first blank line.
func section(banner, open string) string {
	_, tail, _ := strings.Cut(banner, open)
	head, _, _ := strings.Cut(tail, "\n\n")
	return head
}

// usageLines returns the banner's usage block: the lines between `usage:` and
// the first blank line under it, each trimmed.
func usageLines(banner string) []string {
	_, tail, found := strings.Cut(banner, "\nusage:\n")
	if !found {
		return nil
	}
	var out []string
	for _, line := range strings.Split(tail, "\n") {
		if line == "" {
			break
		}
		out = append(out, strings.TrimSpace(line))
	}
	return out
}

// requiredFlags returns the names of the flags a verb's -h marks (required):
// the one mark of a flag the verb cannot run without, read back from the help
// a stranger pastes beside.
func requiredFlags(verbHelp string) []string {
	var out []string
	for _, line := range strings.Split(verbHelp, "\n") {
		if !strings.HasPrefix(line, "  --") || !strings.HasSuffix(line, "(required)") {
			continue
		}
		out = append(out, strings.TrimPrefix(strings.Fields(line)[0], "--"))
	}
	return out
}

// shownResultLines returns the banner lines that begin as the lines an ask
// prints: the ASK OK line and the ASK ANSWER lines, in banner order.
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
// banner land there, with the fixed backend's answer at 0.1, so the ask's
// answer is no where the behaviour check wants a recorded no.
func writeSetupFiles(t *testing.T, dir string) (schema, state, answers string) {
	t.Helper()
	schema, state, answers = filepath.Join(dir, "schema.json"), filepath.Join(dir, "state.txt"), filepath.Join(dir, "answers.json")
	require.NoError(t, os.WriteFile(schema, []byte(`{"name":"q","questions":{"ok":{"type":"noul","instructions":"Yes?"}}}`), 0o644))
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
