package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A first run by someone who has never seen this tool hits three refusals in a
// row — a directory name passed as --channels, then a missing --k, then a
// missing --root — and the no-guessing law says every one of them must stay a
// refusal. What it does not say is that a refusal may only name what was
// wrong. These tests pin the sentence each one now ends with, because guidance
// nothing checks rots into a claim about a message that has since moved.

const firstRunDraft = "The lantern glazing is cleaned with two cloths, one for the brass and one for the glass, before the fog signal is tested.\n"

func TestARefusalSaysWhatTheFlagWants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, want string
		args       []string
	}{
		{
			name: "a directory name passed as a channel",
			args: []string{"search", "--root", corpus, "--channels", "journal", "--k", "3", "x"},
			want: `unknown channel "journal": --channels names a retrieval method, not a directory; the channels are bm25 and trigram, and bm25 alone is the usual start`,
		},
		{
			name: "search without root",
			args: []string{"search", "--channels", "bm25", "--k", "3", "x"},
			want: "--root is required; it wants your corpus directory, the tree to index; it is never guessed from the working directory or the environment, so write it out every run",
		},
		// The hint belongs to the flag, not to the verb that happened to want
		// it: a first run of check must not be told less than a first run of
		// search.
		{
			name: "check without root",
			args: []string{"check", "-"},
			want: "--root is required; it wants " + rootWants,
		},
		{
			name: "an empty channel list",
			args: []string{"search", "--root", corpus, "--channels", "", "--k", "3", "x"},
			want: "--channels names a retrieval method, not a directory",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tc.args...)
			require.Equalf(t, 2, exit, "exit = %d, want 2 — guidance must not soften the refusal; stderr: %s", exit, stderr)
			assert.Containsf(t, stderr, tc.want, "stderr = %q,\nwant it to contain %q", stderr, tc.want)
			assert.Equalf(t, "", stdout, "a refusal must print nothing on stdout, got %q", stdout)
		})
	}
}

// --channels and --k have defaults, stated in their flag text: every channel and k=10,
// named on the OK line. A value given behaves as it always did. (A rater: two required
// flags a first run had to look up before it could search at all.)
func TestChannelsAndKHaveDefaults(t *testing.T) {
	t.Parallel()

	draft := writeDraft(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"search with neither", []string{"search", "--root", corpus, "glazing"}, "SEARCH OK hits=6 k=10 channels=bm25,trigram "},
		{"search with both given", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "3", "lantern"}, "SEARCH OK hits=3 k=3 channels=bm25 "},
		{"search with k alone", []string{"search", "--root", corpus, "--k", "2", "lantern"}, "SEARCH OK hits=2 k=2 channels=bm25,trigram "},
		{"search with channels alone", []string{"search", "--root", corpus, "--channels", "trigram", "glazing"}, "SEARCH OK hits=6 k=10 channels=trigram "},
		{"check with neither", []string{"check", "--root", corpus, draft}, "MEMORY OK candidates=1 source=" + draft + " k=10 channels=bm25,trigram "},
		{"eval with neither", []string{"eval", "--root", corpus, "--floor", "0.5", exampleGold}, "EVAL OK recall@10="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tc.args...)
			require.Equalf(t, 0, exit, "exit = %d; stderr: %s", exit, stderr)
			line, _, _ := strings.Cut(stdout, "\n")
			assert.Truef(t, strings.HasPrefix(line, tc.want), "stdout opens %q, want %q", line, tc.want)
		})
	}
	for _, verb := range []string{"search", "check", "eval"} {
		_, help, _ := runCLI(t, "", verb, "-h")
		assert.Contains(t, help, "retrieval channels, bm25 and trigram (default: both)")
		assert.Contains(t, help, "positive (default 10)")
	}
}

// The usage banner ends in the quickstart line and one example per retrieval
// verb, and the examples are RUN here rather than read: an example that has
// drifted out of the flag set teaches the wrong invocation to exactly the
// reader who cannot tell. The quickstart line comes first because it is the
// one a reader with nothing but a directory can type.
func TestUsageBannerExamplesRun(t *testing.T) {
	t.Parallel()

	examples := usageExamples(t)
	require.Lenf(t, examples, 4, "want quickstart, search, check and verify examples under `example:`, got %d: %q", len(examples), examples)
	assert.Truef(t, strings.HasPrefix(examples[0], "nova-memory quickstart "), "the first example is not the quickstart: %q", examples[0])
	assert.Truef(t, strings.HasPrefix(examples[1], "nova-memory search "), "the second example is not a search: %q", examples[1])
	assert.Truef(t, strings.HasPrefix(examples[2], "nova-memory check "), "the third example is not a check: %q", examples[2])
	assert.Truef(t, strings.HasPrefix(examples[3], "nova-memory verify "), "the fourth example is not a verify: %q", examples[3])
	draft := writeDraft(t)
	for _, ex := range examples {
		exit, stdout, stderr := runCLI(t, "", localize(strings.Fields(ex)[1:], draft)...)
		require.Equalf(t, 0, exit, "the usage example %q does not run: exit %d, stderr: %s", ex, exit, stderr)
		assert.NotEqualf(t, "", stdout, "the usage example %q printed nothing", ex)
	}
}

// usageExamples returns the command lines under the banner's `example:` heading.
//
// It asks for the banner, because a bare invocation no longer IS one: a refusal now costs
// one line and names the door (`run: nova-memory help`), and this reads what is behind
// that door -- which is also a test that the door opens.
func usageExamples(t *testing.T) []string {
	t.Helper()
	exit, stdout, stderr := runCLI(t, "", "help")
	require.Equalf(t, 0, exit, "`nova-memory help` must be exit 0, got %d; stderr: %s", exit, stderr)
	_, tail, found := strings.Cut(stdout, "\nexample:\n")
	require.Truef(t, found, "the usage banner has no `example:` section:\n%s", stdout)
	var out []string
	for _, line := range strings.Split(tail, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "nova-memory ") {
			out = append(out, strings.Join(strings.Fields(line), " "))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The First run section of docs/TESTS.md, executed line for line below

// readmeFirstRun returns the fenced transcripts under `### First run`, one
// slice of lines per block: the quickstart block first, the two-verb block
// last. They are checked separately because they are two different promises —
// one run that printed everything, and two runs a reader types themselves.
func readmeFirstRun(t *testing.T) [][]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	require.NoError(t, err)
	// Scoped to THIS tool's section first: every command in this repo now carries
	// a `### First run` (ONBOARDING.md), so cutting on the first one in the file
	// would hand this test another binary's transcript to run.
	_, section, found := strings.Cut(string(raw), "\n## nova-memory\n")
	require.True(t, found, "docs/TESTS.md has no `## nova-memory` section")
	_, tail, found := strings.Cut(section, "\n### First run\n")
	require.True(t, found, "docs/TESTS.md `## nova-memory` has no `### First run` section; it is what a stranger reads before anything else here")
	body, _, found := strings.Cut(tail, "\n## ")
	if !found {
		body = tail
	}
	var blocks [][]string
	var lines []string
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "```") {
			if fenced && len(lines) > 0 {
				blocks = append(blocks, lines)
				lines = nil
			}
			fenced = !fenced
			continue
		}
		if fenced {
			lines = append(lines, line)
		}
	}
	require.GreaterOrEqualf(t, len(blocks), 2, "`### First run` must hold a quickstart transcript and the two-verb transcript, got %d fenced blocks", len(blocks))
	return blocks
}

// localize points an example or README command at the fixture corpus and at a
// real candidate file, so the command's SHAPE is what is under test and not
// the reader's directory layout.
func localize(args []string, draft string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "--root" {
			out[i+1] = corpus
		}
	}
	if len(out) > 0 && strings.HasSuffix(out[len(out)-1], "draft.md") {
		out[len(out)-1] = draft
	}
	return out
}

func writeDraft(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "draft.md")
	require.NoError(t, os.WriteFile(path, []byte(firstRunDraft), 0o644))
	return path
}

// ---------------------------------------------------------------------------
// quickstart — the first run, and what it promises in print

// The three steps, in order, with the flags each one is echoed with. The
// promise quickstart makes is that the line above an output IS the line that
// produced it, so the echoes are checked as text and not as shapes: a
// demonstration whose printed command differs from the command it ran teaches
// an invocation that does not work.
func TestQuickstartEchoesEveryCommandItRuns(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus)
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")

	require.Truef(t, strings.HasPrefix(lines[0], "QUICKSTART RUN root="), "the first line does not say what this run chose: %q", lines[0])
	// The words are quoted free text at the end of the line, their spaces kept.
	i := strings.Index(lines[0], " words=")
	require.GreaterOrEqualf(t, i, 0, "the first line names no words: %q", lines[0])
	words, err := strconv.Unquote(lines[0][i+len(" words="):])
	require.NoErrorf(t, err, "the words are not one quoted value: %q", lines[0])
	{
		got := field(t, lines[0], "words-source")
		assert.Equalf(t, "corpus-top-terms", got, "words-source = %q, want corpus-top-terms when --words was not given", got)
	}
	for _, w := range strings.Split(words, " ") {
		assert.Falsef(t, quickstartFunctionWords[w], "the demonstration query offers %q, a function word, as one of this corpus's own terms", w)
	}
	assert.Equalf(t, "QUICKSTART OK done=3", lines[len(lines)-2], "the line before NOTE is\n  %s\nwant\n  %s", lines[len(lines)-2], "QUICKSTART OK done=3")
	{
		want := "QUICKSTART NOTE " + quickstartChoiceNote
		assert.Equalf(t, want, lines[len(lines)-1], "the last line is\n  %s\nwant\n  %s", lines[len(lines)-1], want)
	}

	// Each echo, and the token the output under it must open with. The k
	// values are the ones the closing note names, so a change to one that
	// forgets the other fails here.
	steps := []struct{ echo, token string }{
		{"$ nova-memory stats --root " + corpus, "STATS"},
		{"$ nova-memory search --root " + corpus + " --channels bm25 --k 3 " + words, "SEARCH"},
		{"$ nova-memory check --root " + corpus + " --channels bm25 --k 2 -", "MEMORY"},
	}
	at := 0
	for _, st := range steps {
		i := indexOf(lines, st.echo)
		require.GreaterOrEqualf(t, i, 0, "quickstart never printed the command line\n  %s\ngot:\n%s", st.echo, stdout)
		assert.GreaterOrEqualf(t, i, at, "the steps are out of order: %q came before the step above it", st.echo)
		if assert.Lessf(t, i+1, len(lines), "no output under %q: %q", st.echo, lines) {
			assert.Truef(t, strings.HasPrefix(lines[i+1], st.token+" "), "the line under %q is not that command's output: %q", st.echo, lines[i+1])
		}
		at = i
	}
	// The candidate is named before the check that reads it, because a
	// paragraph arriving on stdin is the one thing in the transcript a reader
	// cannot see the source of.
	demo := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "QUICKSTART DEMO ") {
			demo = i
		}
	}
	assert.GreaterOrEqualf(t, demo, 0, "no QUICKSTART DEMO line above the check step:\n%s", stdout)
	assert.Lessf(t, demo, indexOf(lines, steps[2].echo), "no QUICKSTART DEMO line above the check step:\n%s", stdout)
	assert.Contains(t, quickstartChoiceNote, "not defaults", "the closing note no longer says the choices are not defaults, which is the whole point of the verb")
}

// --words and --draft are the caller's, and they must reach the echoed line:
// a flag that changed the run without changing the printed command would make
// the transcript a decoration.
func TestQuickstartRunsTheWordsAndDraftItWasGiven(t *testing.T) {
	t.Parallel()

	draft := writeDraft(t)
	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus, "--words", "glazing", "--words", "brass", "--draft", draft)
	require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	{
		got := field(t, lines[0], "words-source")
		assert.Equalf(t, "given", got, "words-source = %q, want given", got)
	}
	want := "$ nova-memory search --root " + corpus + " --channels bm25 --k 3 glazing brass"
	assert.GreaterOrEqualf(t, indexOf(lines, want), 0, "quickstart never printed\n  %s\ngot:\n%s", want, stdout)
	{
		want := "$ nova-memory check --root " + corpus + " --channels bm25 --k 2 " + draft
		assert.GreaterOrEqualf(t, indexOf(lines, want), 0, "quickstart never printed\n  %s\ngot:\n%s", want, stdout)
	}
	assert.NotContains(t, stdout, "QUICKSTART DEMO", "a run given its own --draft must not announce the corpus's first paragraph")
}

// Exit 0 means all three ran. A step that could not run ends the demonstration
// there, and the closing note — the sentence a reader is meant to leave with —
// is not printed over a run that did not finish.
func TestQuickstartExitsTwoWhenAStepCouldNotRun(t *testing.T) {
	t.Parallel()

	empty := filepath.Join(t.TempDir(), "empty.md")
	require.NoError(t, os.WriteFile(empty, []byte("ok\n"), 0o644))
	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus, "--draft", empty)
	require.Equalf(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
	assert.Containsf(t, stderr, "the check step could not run", "stderr does not name the step that failed: %q", stderr)
	assert.NotContains(t, stdout, quickstartChoiceNote, "a quickstart that did not finish printed its closing note anyway")
	assert.Containsf(t, stdout, "SEARCH OK", "the steps that did run must still be on the page: %q", stdout)
	assert.NotContainsf(t, stdout, "QUICKSTART OK", "a quickstart that failed printed QUICKSTART OK:\n%s", stdout)
}

// A quickstart given a missing draft file refuses and exits 2, and must NOT
// print QUICKSTART OK before or after the failure.
func TestQuickstartWithMissingDraftDoesNotPrintOK(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing-draft.md")
	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus, "--draft", missing)
	require.Equalf(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
	assert.NotContainsf(t, stdout, "QUICKSTART OK", "quickstart with missing draft printed QUICKSTART OK:\n%s", stdout)
	assert.Containsf(t, stderr, "the check step could not run", "stderr does not name the step that failed: %q", stderr)
}

func indexOf(lines []string, want string) int {
	for i, line := range lines {
		if line == want {
			return i
		}
	}
	return -1
}

// field reads one key=value field off an event line.
func field(t *testing.T, line, key string) string {
	t.Helper()
	for _, tok := range strings.Fields(line) {
		if k, v, ok := strings.Cut(tok, "="); ok && k == key {
			return v
		}
	}
	require.FailNowf(t, "missing expected field", "no %s= field on %q", key, line)
	return ""
}

// ---------------------------------------------------------------------------
// One run, every reason

// A first run is usually wrong about more than one thing, and the tool that
// answers one refusal at a time turns that into a guessing game played one
// round per invocation. Every reason a run cannot start is reported by the run
// that could not start.
func TestARefusalReportsEveryReasonAtOnce(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "search with a bad channel and a bad k, and no query",
			args: []string{"search", "--root", corpus, "--channels", "journal", "--k", "0"},
			want: []string{
				`unknown channel "journal"`,
				"--k must be a positive receipt budget",
				"takes <words>..., at least 1 argument, got 0",
			},
		},
		{
			name: "check missing everything but the verb",
			args: []string{"check"},
			want: []string{
				"--root is required", rootWants,
				"takes <file|->, exactly 1 argument, got 0",
			},
		},
		{
			name: "eval with a bad channel, a bad k and a bad floor",
			args: []string{"eval", "--root", corpus, "--channels", "journal", "--k", "-1", "--floor", "2", exampleGold},
			want: []string{
				`unknown channel "journal"`,
				"--k must be a positive receipt budget",
				"--floor must be in (0,1]",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tc.args...)
			require.Equalf(t, 2, exit, "exit = %d, want 2; stderr: %s", exit, stderr)
			assert.Equalf(t, "", stdout, "a refusal must print nothing on stdout, got %q", stdout)
			for _, w := range tc.want {
				assert.Containsf(t, stderr, w, "stderr does not report %q; one run must report them all, got:\n%s", w, stderr)
			}
		})
	}
}

// The echoed step is a line the reader is meant to PASTE, and the shell they
// paste it into is the one on their own machine. The specimen is the Windows
// path: rendered as a Go string literal every separator doubled, so the echo
// named C:\\Users\\... — a path that does not exist, printed by the one verb
// whose whole job is to be copied. Both platforms' rules are pinned here
// rather than on the platform that happens to be running.
func TestTheEchoedStepPastesBackIntoThatPlatformsShell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		windows bool
		argv    []string
		want    string
	}{
		{
			name:    "a windows path is echoed verbatim, separators and all",
			windows: true,
			argv:    []string{"check", "--root", "corpus", `C:\Users\RUNNER~1\AppData\Local\Temp\Test1\001\draft.md`},
			want:    `check --root corpus C:\Users\RUNNER~1\AppData\Local\Temp\Test1\001\draft.md`,
		},
		{
			name:    "a windows path holding a space is double-quoted, which that shell takes literally",
			windows: true,
			argv:    []string{"check", `C:\Program Files\notes\draft.md`},
			want:    `check "C:\Program Files\notes\draft.md"`,
		},
		{
			name: "a posix path is echoed verbatim",
			argv: []string{"check", "--root", "corpus", "/var/folders/t7/Test1/001/draft.md"},
			want: "check --root corpus /var/folders/t7/Test1/001/draft.md",
		},
		{
			name: "a posix argument holding a space is single-quoted, and keeps every character",
			argv: []string{"search", "salt haze"},
			want: `search 'salt haze'`,
		},
		{
			name: "a posix argument the shell would act on is quoted rather than handed over",
			argv: []string{"search", "$HOME", "a'b", `back\slash`},
			want: `search '$HOME' 'a'\''b' 'back\slash'`,
		},
		{
			name:    "an empty argument is still a word on both shells",
			windows: true,
			argv:    []string{"search", ""},
			want:    `search ""`,
		},
		{
			name: "a newline in an argument cannot break the echo in two",
			argv: []string{"search", "one\ntwo"},
			want: `search 'one\x0atwo'`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			{
				got := commandLineFor(tc.argv, tc.windows)
				assert.Equalf(t, tc.want, got, "commandLineFor(%q, windows=%v) =\n  %s\nwant\n  %s", tc.argv, tc.windows, got, tc.want)
			}
			assert.NotContains(t, commandLineFor(tc.argv, tc.windows), "\n", "the echoed line is more than one line")
		})
	}
}

// Every flag a verb defines has an entry in that verb's -h (F5-7): the flags live with
// the verb that takes them, under `flags:`.
func TestEveryDefinedFlagAppearsInAVerbsHelp(t *testing.T) {
	t.Parallel()

	var help strings.Builder
	for _, verb := range verbNames() {
		exit, stdout, _ := runCLI(t, "", verb, "-h")
		require.Equalf(t, 0, exit, "%s -h failed: %d", verb, exit)
		help.WriteString(stdout)
	}
	for _, f := range []string{
		"root", "channels", "k", "exclude", "floor", "links",
		"coverage", "frontmatter", "exempt", "fail-max", "words", "draft", "pin", "json",
	} {
		assert.Containsf(t, help.String(), "\n  --"+f+" ", "flag --%s has no entry in any verb's -h:\n%s", f, help.String())
	}
}

func TestQuickstartRunsWithDashLeadingWords(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus, "--words", "-glazing")
	require.Equalf(t, 0, exit, "quickstart with dash-leading word failed: exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	assert.Containsf(t, stdout, "$ nova-memory search ", "quickstart did not complete search step with dash-leading word:\n%s", stdout)
	assert.Containsf(t, stdout, `query="-glazing"`, "quickstart did not complete search step with dash-leading word:\n%s", stdout)
}

// TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine runs the whole
// `## nova-memory` `### First run` section and compares every command's output
// with the block written under it: same number of lines, same lines, same
// order, through the one comparator (onboarding.CompareTranscript). It replaced
// two set-of-shapes tests that compared only event prefixes and field names.
//
// THE SECTION IS TWO BLOCKS, and they are two different promises:
//
//   - The first block is the whole stdout of `nova-memory quickstart`, which
//     ECHOES the three commands it runs as `$ nova-memory ...` lines and prints
//     each one's output beneath. onboarding.Steps cannot cut this block, because
//     it would read every echoed `$` line as a command of the sitting. So the
//     block is executed as the single command it is -- its first `$` line --
//     with every following line, echoes included, as that command's output.
//   - The second block is a genuine two-command sitting (a `search`, then a
//     `check` of a draft on disk) and is handed to onboarding.Steps whole.
//
// ONE RUN-OWNED VALUE IS DECLARED, from the shared table: `build`, the index build time
// the STATS line measures (the document was recorded at one duration and a fresh run
// prints another). Every other value -- the six files, the scores, the snippets -- is
// compared as written.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	// Resolve the document and the checkout root from the package directory,
	// before the sitting moves this test somewhere else: the document is a
	// fixed file, while the commands run where `./corpus` and `draft.md`
	// resolve as written.
	blocks := readmeFirstRun(t)
	root := repoRoot(t)

	// Prove this file and onboarding.FirstRun read the same section: the blocks
	// flattened are the lines FirstRun returns, in the same order. If they ever
	// disagree, the section is being read two ways and one reader drifts
	// unwatched.
	raw, err := os.ReadFile(filepath.Join(root, "docs", "TESTS.md"))
	require.NoError(t, err)
	firstRun, err := onboarding.FirstRun(string(raw), "nova-memory")
	require.NoError(t, err)
	var flat []string
	for _, b := range blocks {
		flat = append(flat, b...)
	}
	require.Equalf(t, strings.Join(firstRun, "\n"), strings.Join(flat, "\n"), "the two readers of `### First run` disagree:\nblocks:\n%s\nFirstRun:\n%s",
		strings.Join(flat, "\n"), strings.Join(firstRun, "\n"))

	// docs/CLI.md shows the same first run, and it is this one: the command reference's
	// transcript is the executed one, line for line, so it cannot drift from the tool.
	cli, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	require.NoError(t, err)
	cliRun, err := onboarding.FirstRun(string(cli), "nova-memory")
	require.NoError(t, err)
	assert.Equal(t, firstRun, cliRun, "CLI.md's `### First run` for nova-memory is not the transcript this test executes")
	// And the help's search and check examples are the sitting's two commands.
	helpExamples, err := onboarding.ExampleLines(memoryTool().Banner(), "nova-memory")
	require.NoError(t, err)
	var examples []string
	for _, ex := range helpExamples {
		examples = append(examples, strings.Join(strings.Fields(ex), " "))
	}
	for _, ex := range []string{
		"nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass",
		"nova-memory check --root ./corpus --channels bm25 --k 3 draft.md",
	} {
		assert.Contains(t, examples, ex, "the help's example block does not hold %q", ex)
		assert.Contains(t, flat, "$ "+ex, "the sitting does not run %q", ex)
	}

	require.Lenf(t, blocks, 2, "`### First run` holds %d fenced blocks, want 2: the quickstart transcript and the two-verb sitting", len(blocks))
	require.Truef(t, strings.HasPrefix(blocks[0][0], "$ nova-memory quickstart "), "the first `### First run` block does not open on the quickstart command: %q", blocks[0][0])

	// The transcript WRITES a draft beside the corpus, so it runs against a
	// copy of the fixture in t.TempDir(), never against what ships. Standing in
	// the copy is also what makes the documented paths (`--root ./corpus`,
	// `draft.md`) resolve as written, so no path norm is declared.
	sit := firstRunSitting(t)
	t.Chdir(sit)

	// The first block: one command, all of its output.
	steps, err := onboarding.Steps("nova-memory", blocks[0][:1])
	require.NoError(t, err)
	steps[0].Want = blocks[0][1:]

	// The second block: every `$` line is a command.
	sitting, err := onboarding.Steps("nova-memory", blocks[1])
	require.NoError(t, err)
	assert.Lenf(t, sitting, 2, "the second `### First run` block runs %d commands, want 2: a search and a check of the draft", len(sitting))
	steps = append(steps, sitting...)

	run := runDocumented(t)
	var results []onboarding.Result
	for _, s := range steps {
		r, err := run(s)
		require.NoError(t, err)
		results = append(results, r)
	}
	for _, p := range onboarding.CompareTranscript(steps, results, []onboarding.Field{{Name: "build"}}) {
		assert.Fail(t, p.String())
	}
}

// firstRunSitting materializes the directory the documented commands are typed
// in: `corpus/` is a writable copy of the fixture, so the sitting never touches
// what ships, and `draft.md` is the candidate paragraph the second block checks.
func firstRunSitting(t *testing.T) string {
	t.Helper()
	sit := t.TempDir()
	dst := filepath.Join(sit, "corpus")
	src := filepath.Join("..", "..", "cmd", "nova-memory", "testdata", "corpus")
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sit, "draft.md"), []byte(firstRunDraft), 0o644))
	return sit
}

func runDocumented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		stdin := io.Reader(strings.NewReader(""))
		if s.Stdin != "" {
			f, err := os.Open(s.Stdin)
			if err != nil {
				return onboarding.Result{}, err
			}
			defer f.Close()
			stdin = f
		}
		var out, errb bytes.Buffer
		code := run(s.Args, stdin, &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	{
		_, err := os.Stat(filepath.Join(root, "docs", "TESTS.md"))
		require.NoErrorf(t, err, "docs/TESTS.md is not under %s: %v", root, err)
	}
	return root
}

func TestVerifyHelpExampleMatchesWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	found := false
	for _, line := range usageExamples(t) {
		if !strings.HasPrefix(line, "nova-memory verify ") {
			continue
		}
		found = true
		code, out, errOut := runCLI(t, "", localize(strings.Fields(line)[1:], "")...)
		require.Equalf(t, 0, code, "example exit %d: %s", code, errOut)
		want := onboarding.Step{Line: line, Want: []string{
			"VERIFY INFO wikilink: [[storm-glass]] resolves to no file (e.g. from log/1974-03-11.md)",
			"VERIFY OK gating=0 info=1 shown=1 coverage=0 frontmatter=0 links=info",
		}}
		for _, problem := range onboarding.CompareTranscript([]onboarding.Step{want}, []onboarding.Result{{Code: code, Stdout: out, Stderr: errOut}}, nil) {
			assert.Fail(t, problem.String())
		}
	}
	require.True(t, found, "help has no nova-memory verify example")
}
