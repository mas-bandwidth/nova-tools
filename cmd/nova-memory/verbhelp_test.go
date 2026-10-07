package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them indexes or writes the root it was pointed at
// (the CLI style's rule (b), #4505).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	root := []string{"--root", "{dir}"}
	testverbhelp.Check(t, memoryRun, []testverbhelp.Case{
		{Verb: "quickstart", Flags: append(root, "--draft", "{dir}/draft.md")},
		{Verb: "stats", Flags: root},
		{Verb: "search", Flags: root},
		{Verb: "check", Flags: root},
		{Verb: "verify", Flags: root},
		{Verb: "eval", Flags: root},
		{Verb: "boot", Flags: root},
		{Verb: "version"},
	})
	testverbhelp.HelpVerb(t, memoryRun, "nova-memory", "search", "version")

	// The exit codes line names each verb's answer, and every claim in it is
	// checked against the behaviour by running it: the help is changed to the
	// behaviour, never the reverse (docs/CLI-STYLE.md (d)).
	t.Run("the exit codes line says what each verb answers, and each ran here", func(t *testing.T) {
		_, help, _ := runCLI(t, "", "help")
		said := strings.Join(strings.Fields(help), " ")
		for _, want := range []string{
			"exit codes: by verb (each ran here),",
			"search, stats, boot: 0 ran; a search that finds nothing is still 0, and says so on its MISS line.",
			"check: 0 even when the draft repeats a note (the example's check does: it hands you receipts, and the verdict stays yours).",
			"verify: 0 clean, 1 a finding (a wikilink finding gates only under --links gate).",
			"eval: 0 at or above --floor, 1 recall@k under --floor.",
			"2 could not run (bad invocation).",
		} {
			assert.Containsf(t, said, want, "the exit codes line does not say %q:\n%s", want, help)
		}

		code, stdout, _ := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3", "zzqq", "xxvv")
		require.Equalf(t, 0, code, "a search that finds nothing answered %d, want 0: %s", code, stdout)
		assert.Containsf(t, stdout, "SEARCH MISS", "the empty search did not say so: %q", stdout)

		sit := exampleSitting(t)
		examples := usageExamples(t)
		require.Lenf(t, examples, 4, "want the quickstart, search, check and verify examples, got %q", examples)
		code, stdout, stderr := runExampleLine(t, sit, examples[2])
		require.Equalf(t, 0, code, "the example's check, whose draft repeats a note, answered %d, want 0: %s%s", code, stdout, stderr)
		code, stdout, stderr = runExampleLine(t, sit, examples[3])
		require.Equalf(t, 0, code, "the example's verify answered %d, want a clean 0: %s%s", code, stdout, stderr)
		code, _, stderr = runCLI(t, "", "verify", "--root", corpus, "--links", "gate", "--coverage", "notes/*.md:notes/index-*.md")
		require.Equalf(t, 1, code, "a finding under --links gate answered %d, want 1: %s", code, stderr)
		code, _, stderr = runCLI(t, "", "eval", "--root", corpus, "--channels", "bm25", "--k", "1", "--floor", "1", exampleGold)
		require.Equalf(t, 1, code, "recall@k under --floor answered %d, want 1: %s", code, stderr)
		code, _, _ = runCLI(t, "", "stats")
		require.Equalf(t, 2, code, "a bad invocation answered %d, want 2", code)
	})

	// The CAL sentence names the line a reader can find, pasted byte for byte
	// as the example's search prints it, and in the same sentence says which
	// score= compares with it.
	t.Run("the CAL line the help shows is the line the tool prints", func(t *testing.T) {
		sit := exampleSitting(t)
		code, stdout, stderr := runExampleLine(t, sit, usageExamples(t)[1])
		require.Equalf(t, 0, code, "the example's search answered %d: %s%s", code, stdout, stderr)
		assert.Equalf(t, theLine(t, stdout, "SEARCH CAL "), helpLine(t, "SEARCH CAL "),
			"the CAL line the help shows is not the line the example's search prints")
		assert.Containsf(t, flat(t, usage), "the score a fixed unrelated probe gets here, and a hit's score= at or below it is no better than noise when the hit's score-channel= names the same channel",
			"the CAL sentence no longer names what the line is or which score= compares with it:\n%s", usage)
	})

	// The receipt is pasted once, as the example's search prints it, and the
	// class sentence sits beside the line that prints class= — no longer
	// alone under first run.
	t.Run("the receipt the help shows is the line the example's search prints", func(t *testing.T) {
		sit := exampleSitting(t)
		code, stdout, stderr := runExampleLine(t, sit, usageExamples(t)[1])
		require.Equalf(t, 0, code, "the example's search answered %d: %s%s", code, stdout, stderr)
		printed := theLine(t, stdout, "SEARCH HIT rank=1 ")
		// The one value the paste cannot pin: the root= field names the
		// directory the run was pointed at, and the help shows the example's
		// own ./corpus. The sitting's temp root is mapped to it before the
		// compare; every other byte is compared.
		printed = strings.Replace(printed, "root="+filepath.Join(sit, "corpus")+": ", "root=./corpus: ", 1)
		shown := helpLine(t, "SEARCH HIT rank=1 ")
		assert.Equalf(t, printed, shown, "the receipt the help shows is not the line the example's search prints:\n help shows: %s\n tool prints: %s", shown, printed)
		hitAt, classAt := -1, -1
		for i, line := range strings.Split(usage, "\n") {
			switch {
			case line == shown:
				hitAt = i
			case strings.HasPrefix(line, "class is the top-level directory"):
				classAt = i
			}
		}
		require.GreaterOrEqualf(t, classAt, 0, "the class sentence is gone from the banner")
		assert.Greaterf(t, classAt, hitAt, "the class sentence does not sit beside the receipt that prints class= (receipt at line %d, sentence at line %d)", hitAt, classAt)
		assert.Equalf(t, 1, strings.Count(usage, "class is the top-level directory"),
			"the class sentence was moved but not dropped from where it stood:\n%s", usage)
	})
}

func memoryRun(args []string, stdout, stderr io.Writer) int {
	return run(args, strings.NewReader(""), stdout, stderr)
}

// exampleSitting runs the banner's setup: block in a fresh directory and
// returns it: the corpus the example: lines are typed against. The block is
// parsed from the banner, so a setup line that stops building what the pasted
// lines show fails here too.
func exampleSitting(t *testing.T) string {
	t.Helper()
	sit := t.TempDir()
	_, block, found := strings.Cut(usage, "\nsetup:\n")
	require.Truef(t, found, "the banner has no setup: block")
	for _, raw := range strings.Split(block, "\n") {
		if !strings.HasPrefix(raw, "  ") {
			break
		}
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "mkdir -p "):
			dir := filepath.Join(sit, filepath.FromSlash(strings.TrimPrefix(line, "mkdir -p ")))
			require.NoErrorf(t, os.MkdirAll(dir, 0o755), "setup line %q", line)
		case strings.HasPrefix(line, "printf "):
			text, target, found := strings.Cut(line, " > ")
			require.Truef(t, found, "setup line %q does not redirect", line)
			payload, ok := strings.CutPrefix(strings.TrimPrefix(text, "printf "), "'")
			require.Truef(t, ok, "setup line %q does not quote its text", line)
			payload, ok = strings.CutSuffix(payload, "'")
			require.Truef(t, ok, "setup line %q does not close its quote", line)
			path := filepath.Join(sit, filepath.FromSlash(target))
			require.NoErrorf(t, os.MkdirAll(filepath.Dir(path), 0o755), "setup line %q", line)
			require.NoErrorf(t, os.WriteFile(path, []byte(strings.ReplaceAll(payload, `\n`, "\n")), 0o644), "setup line %q", line)
		case strings.HasPrefix(line, "cp "):
			src, dst, found := strings.Cut(strings.TrimPrefix(line, "cp "), " ")
			require.Truef(t, found, "setup line %q copies nothing", line)
			b, err := os.ReadFile(filepath.Join(sit, filepath.FromSlash(src)))
			require.NoErrorf(t, err, "setup line %q", line)
			require.NoErrorf(t, os.WriteFile(filepath.Join(sit, filepath.FromSlash(dst)), b, 0o644), "setup line %q", line)
		default:
			require.Failf(t, "a setup line this helper does not run", "%q", line)
		}
	}
	return sit
}

// runExampleLine runs one example: line against a sitting, with --root and
// the draft file pointed at it, so the line's shape is what is under test and
// not the reader's directory layout.
func runExampleLine(t *testing.T, sit, line string) (int, string, string) {
	t.Helper()
	args := strings.Fields(line)[1:]
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--root" {
			args[i+1] = filepath.Join(sit, "corpus")
		}
	}
	if n := len(args); n > 0 && strings.HasSuffix(args[n-1], "draft.md") {
		args[n-1] = filepath.Join(sit, "draft.md")
	}
	return runCLI(t, "", args...)
}

// helpLine returns the one banner line that opens with prefix.
func helpLine(t *testing.T, prefix string) string {
	t.Helper()
	return theLine(t, usage, prefix)
}

// theLine returns the one line of a stream that opens with prefix, and fails
// when there is none or more than one: the paste is one line, as printed.
func theLine(t *testing.T, stream, prefix string) string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(stream, "\n") {
		if strings.HasPrefix(line, prefix) {
			got = append(got, line)
		}
	}
	require.Lenf(t, got, 1, "the stream holds %d lines opening with %q, want 1:\n%s", len(got), prefix, stream)
	return got[0]
}

// flat collapses a banner's line wraps, so a sentence can be asserted across
// the wrap without pinning where it breaks.
func flat(t *testing.T, text string) string {
	t.Helper()
	return strings.Join(strings.Fields(text), " ")
}

// TestEvalHelpShowsGoldFileFormatAndTwoLineExample pins that eval's help names
// the gold file format and shows a two-line example, so a caller does not have
// to learn the format from a parse refusal.
func TestEvalHelpShowsGoldFileFormatAndTwoLineExample(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "eval", "-h")
	require.Equalf(t, 0, exit, "eval -h must exit 0: stderr = %q", stderr)
	assert.Contains(t, stdout, "gold file format: query<TAB>expected[,expected]")
	assert.Contains(t, stdout, "how often should the lantern glazing be washed\tnotes/lantern.md")
	assert.Contains(t, stdout, "washing the glazing before an onshore gale\tnotes/lantern.md,log/1974-03-11.md")

	exit, stdoutHelp, stderr := runCLI(t, "", "help", "eval")
	require.Equalf(t, 0, exit, "help eval must exit 0: stderr = %q", stderr)
	assert.Equal(t, stdout, stdoutHelp, "`help eval` and `eval -h` must match")
}

// TestBootHelpStatesItChecksThePin pins that boot's help says what the verb
// does: it checks the pin — every named file present, readable, and its size —
// rather than loading the pinned memories into the process.
func TestBootHelpStatesItChecksThePin(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "boot", "-h")
	require.Equalf(t, 0, exit, "boot -h must exit 0: stderr = %q", stderr)
	assert.Contains(t, stdout, "checks the pin: every file present and readable, and their size")

	exit, stdoutHelp, stderr := runCLI(t, "", "help", "boot")
	require.Equalf(t, 0, exit, "help boot must exit 0: stderr = %q", stderr)
	assert.Equal(t, stdout, stdoutHelp, "`help boot` and `boot -h` must match")
}
