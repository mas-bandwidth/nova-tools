package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/selftalk"
	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
)

// Every shape the banner names, by the words it names it with, is found by a scan of a sentence
// carrying those words, under the class or shape the banner gives it. The raters' sharpest point
// about this tool was the help naming shapes the scan missed; this holds the banner to the scan.
func TestEveryShapeTheHelpNamesIsFound(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ named, sentence, want string }{
		{"cannot check", "I cannot check my own work.", "STANDING"},
		{"bad at", "I am bad at estimating time.", "STANDING"},
		{"worst", "I am the worst at reviews.", "STANDING"},
		{"terrible at", "I am terrible at estimates.", "STANDING"},
		{"cannot ever", "I cannot ever get this right.", "STANDING"},
		{"fallible", "I am fallible.", "STANDING"},
		{"RANKING: I am the best", "I am the best reviewer here.", "RANKING"},
		{"my\n                     weakest instrument", "Recall, my weakest instrument, failed again.", "RANKING"},
		{"FORECLOSURE: I will never be\n                     a good planner", "I will never be a good planner.", "FORECLOSURE"},
		{"I have no recall", "I have no recall of yesterday.", "FORECLOSURE"},
		{"(VERDICT-IDIOM: dead as a practice)", "Known as a proposition, dead as a practice.", "VERDICT-IDIOM"},
		{"TRAIT: I always\n                     overpromise", "I always overpromise.", "TRAIT"},
		{"I tend to rush", "I tend to rush.", "TRAIT"},
	} {
		t.Run(tc.sentence, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, usage, tc.named, "the banner no longer names this shape; drop the row or restore the words")
			f := write(t, t.TempDir(), "page.md", tc.sentence+"\n")
			exit, _, stderr := runSelfTalk(t, f)
			assert.Equal(t, 1, exit)
			assert.Equal(t, 1, strings.Count(stderr, "SELFTALK FAIL "), "one finding, not two: %s", stderr)
			assert.Contains(t, stderr, ":1: "+tc.want+" match=")
		})
	}
}

// What the banner says is licensed is not reported: a dated claim, an instrument, an aspiration,
// an imperative and a quotation, each carrying a shape that is reported without the licence.
func TestWhatTheHelpLicensesIsNotFound(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"On 2026-09-30 I am bad at estimating time.",
		"RULE: my weakest instrument gets a second reader.",
		"I want my weakest instrument to get a second reader.",
		"Keep my weakest instrument in view.",
		`"My weakest instrument gets a second reader."`,
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			f := write(t, t.TempDir(), "page.md", in+"\n")
			exit, stdout, stderr := runSelfTalk(t, f)
			assert.Equal(t, 0, exit, "stderr: %s", stderr)
			assert.Contains(t, stdout, "SELFTALK OK files=1")
		})
	}
}

// A rule document with ANY finding carries its banner, a STANDING one as much as an INSTALLATION
// one: the banner says what a finding there is for, whichever class found it. Both renderings.
func TestRuleDocBannerCoversEveryClass(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, body string }{
		{"standing only", "I am bad at estimating time.\n"},
		{"installation only", "I have no associative recall to drag anything back later.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := write(t, t.TempDir(), "POLICY.md", tc.body)
			exit, stdout, _ := runSelfTalk(t, "--rule-doc", "POLICY.md", f)
			assert.Equal(t, 1, exit)
			assert.Contains(t, stdout, "SELFTALK RULEDOC "+f+": "+selftalk.RuleDocumentBanner)

			exit, stdout, _ = runSelfTalk(t, "--json", "--rule-doc", "POLICY.md", f)
			assert.Equal(t, 1, exit)
			var got struct {
				Items []struct {
					Kind   string
					Fields map[string]any
				}
			}
			require.NoError(t, json.Unmarshal([]byte(stdout), &got), stdout)
			require.NotEmpty(t, got.Items)
			assert.Equal(t, "ruledoc", got.Items[0].Kind)
			assert.Equal(t, f, got.Items[0].Fields["file"])
		})
	}
}

// Every flag a verb registers is named on that verb's usage line in the banner, so the usage a
// reader meets first and the flags the verb takes cannot disagree. The flags are read from each
// verb's own -h, which lists what its flag set defines.
func TestEveryVerbsUsageLineNamesItsFlags(t *testing.T) {
	t.Parallel()

	usageLine := func(prefix string) string {
		for _, l := range strings.Split(usage, "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, prefix) {
				return l
			}
		}
		return ""
	}
	for verb, prefix := range map[string]string{
		"scan":    "nova-self-talk [--", // the scan's flags stand on the plain usage line
		"shapes":  "nova-self-talk shapes ",
		"example": "nova-self-talk example ",
		"version": "nova-self-talk version ",
	} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			line := usageLine(prefix)
			require.NotEmpty(t, line, "the banner has no usage line for %s", verb)
			exit, help, _ := runSelfTalk(t, verb, "-h")
			require.Equal(t, 0, exit)
			_, flags, _ := strings.Cut(help, "flags:\n")
			for _, l := range strings.Split(flags, "\n") {
				if name, ok := strings.CutPrefix(l, "  --"); ok {
					name, _, _ = strings.Cut(name, " ")
					assert.Contains(t, line, "[--"+name, "%s registers --%s and its usage line does not name it: %s", verb, name, line)
				}
			}
		})
	}
}

// `shapes` prints the detector table row for row (ledger T2), from the same rows the scan walks.
func TestShapesPrintsTheDetectorTable(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runSelfTalk(t, "shapes")
	require.Equal(t, 0, exit, stderr)
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	rules := selftalk.Rules()
	require.Len(t, lines, len(rules)+2, "an OK line, one ROW per rule, a NOTE")
	assert.True(t, strings.HasPrefix(lines[0], "SHAPES OK rows="), lines[0])
	for i, r := range rules {
		assert.True(t, strings.HasPrefix(lines[i+1], "SHAPES ROW class="+r.Class+" shape="+r.Name+" says="), lines[i+1])
		assert.Contains(t, lines[i+1], "finds="+quote(r.Finds))
	}

	exit, stdout, _ = runSelfTalk(t, "shapes", "--json")
	require.Equal(t, 0, exit)
	var got struct {
		Items []struct {
			Kind   string            `json:"kind"`
			Fields map[string]string `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got.Items, len(rules))
	assert.Equal(t, rules[0].Pattern, got.Items[0].Fields["pattern"])
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) } // ignored: a string always marshals

// A finding names the words that matched (ledger T5), on the line and in --json.
func TestAFindingNamesTheWordsThatMatched(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "page.md", "I am bad at estimating time.\n")
	_, _, stderr := runSelfTalk(t, f)
	assert.Contains(t, stderr, `:1: STANDING match="bad at": I am bad at estimating time.`)
}

// --json is the same run as one JSON object on stdout (ledger T7): the facts are the closing
// line's counts and there is one item per finding line.
func TestJSONIsTheSameRunAsTheLines(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "page.md",
		"I cannot check my own work.\n\nOn 2026-07-30 I cannot check my own work.\n\nI am the best reviewer here.\n")
	exit, stdout, stderr := runSelfTalk(t, "--json", f)
	require.Equal(t, 1, exit)
	assert.Empty(t, stderr)
	var got struct {
		Result struct {
			Verb, Status string
			Exit         int
		}
		Facts map[string]int
		Items []struct {
			Kind   string
			Fields map[string]any
		}
		Notes []string
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), stdout)
	assert.Equal(t, "failed", got.Result.Status)
	assert.Equal(t, 1, got.Result.Exit)
	assert.Equal(t, map[string]int{"files": 1, "skipped": 0, "claims": 2, "standing": 1, "installations": 1, "dated": 1, "shown": 2}, got.Facts)
	require.Len(t, got.Items, 2)
	assert.Equal(t, "standing", got.Items[0].Kind)
	assert.Equal(t, "cannot check", got.Items[0].Fields["match"])
	assert.Equal(t, "RANKING", got.Items[1].Fields["shape"])
	assert.EqualValues(t, 5, got.Items[1].Fields["line"])
	assert.Len(t, got.Notes, 1)

	_, lines, _ := runSelfTalk(t, f)
	assert.Contains(t, lines, "SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2")

	exit, stdout, stderr = runSelfTalk(t, "--json")
	assert.Equal(t, 2, exit)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, `"status":"refused"`)
	assert.Contains(t, stdout, `"no files named; refusing to guess"`)
}

// - is standard input, one file named - (ledger T8).
func TestDashReadsStandardInput(t *testing.T) {
	t.Parallel()

	var out, errb bytes.Buffer
	exit := runStdin("", []string{"-"}, strings.NewReader("# Journal\nI am bad at estimating time.\n"), &out, &errb)
	assert.Equal(t, 1, exit)
	assert.Contains(t, errb.String(), `SELFTALK FAIL -:2: STANDING match="bad at": I am bad at estimating time.`)
}

// The first word is a verb only when it is one; `help <anything>` is help, `scan` is the scan,
// and a bare word that is no file names the verbs (ledger T6, X3).
func TestAWordThatLooksLikeAVerb(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "page.md", "I am bad at estimating time.\n")
	exit, _, stderr := runSelfTalk(t, "scan", f)
	assert.Equal(t, 1, exit)
	assert.Contains(t, stderr, "STANDING")

	for _, args := range [][]string{{"help", "foo"}, {"help", "foo", "bar"}, {"help"}} {
		exit, stdout, _ := runSelfTalk(t, args...)
		assert.Equal(t, 0, exit, args)
		assert.True(t, strings.HasPrefix(stdout, "nova-self-talk: flags sentences"), args)
	}

	exit, stdout, _ := runSelfTalk(t, "help", "example")
	assert.Equal(t, 0, exit)
	assert.Contains(t, stdout, "--dry-run")
	assert.Contains(t, stdout, "effect: local write")

	exit, stdout, _ = runSelfTalk(t, "help", "scan")
	assert.Equal(t, 0, exit)
	assert.Contains(t, stdout, "usage: nova-self-talk scan [flags]")
	assert.Contains(t, stdout, "--rule-doc")
	assert.Contains(t, stdout, "effect: inspection")

	exit, _, stderr = runSelfTalk(t, "scna", f)
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, `nova-self-talk REFUSED: cannot read "scna": `)
	assert.Contains(t, stderr, "it is not a verb either (the verbs are scan, shapes, example, version, help; a file of that name is ./scna); run: nova-self-talk help")
}

// One run names every problem it can find, each on a REFUSED line with the door (X4), and an
// unknown flag is answered with the flags there are (X2).
func TestEveryProblemIsNamedAtOnceInTheRefusalGrammar(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runSelfTalk(t, "--max", "-1")
	assert.Equal(t, 2, exit)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "nova-self-talk REFUSED: --max must be a line ceiling of zero or more (got -1)")
	assert.Contains(t, stderr, "nova-self-talk REFUSED: no files named; refusing to guess; run: nova-self-talk help")

	_, _, stderr = runSelfTalk(t, "--zzz", "a.md")
	assert.Equal(t, "nova-self-talk REFUSED: unknown flag -zzz; the flags are --skip, --rule-doc, --max, --json; run: nova-self-talk help\n", stderr)
}

// `example` writes the pages built into the binary (X6): a first run needs no checkout. A page
// already there is kept, one with other bytes is never replaced, and --dry-run writes nothing.
func TestExampleWritesThePagesFromTheBinary(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "pages")
	exit, stdout, stderr := runSelfTalk(t, "example", "--dry-run", dir)
	require.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, "would-write=RULES.md,journal.md")
	assert.NoDirExists(t, dir)

	exit, stdout, stderr = runSelfTalk(t, "example", dir)
	require.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, "EXAMPLE OK dir=")
	assert.Contains(t, stdout, "wrote=RULES.md,journal.md kept=-; run: nova-self-talk "+filepath.Join(dir, "journal.md"))
	want, err := os.ReadFile(filepath.Join(examplePages, "journal.md"))
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(dir, "journal.md"))
	require.NoError(t, err)
	assert.Equal(t, want, got)

	exit, stdout, _ = runSelfTalk(t, "example", dir)
	assert.Equal(t, 0, exit)
	assert.Contains(t, stdout, "wrote=- kept=RULES.md,journal.md")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "RULES.md"), []byte("mine\n"), 0o644))
	exit, _, stderr = runSelfTalk(t, "example", dir)
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "nova-self-talk example REFUSED: ")
	assert.Contains(t, stderr, "exists with other content and is never replaced; name an empty directory; run: nova-self-talk help example")
	mine, err := os.ReadFile(filepath.Join(dir, "RULES.md"))
	require.NoError(t, err)
	assert.Equal(t, "mine\n", string(mine))

	exit, _, stderr = runSelfTalk(t, "example")
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "takes one directory to write the pages into, got 0 arguments: nova-self-talk example ./pages")
}

// Example publishes complete pages without replacing a target, including a dangling link.
func TestExampleWritesItsPagesWithoutReplacingAndLeavesNoPartialFile(t *testing.T) {
	t.Parallel()

	t.Run("complete pages without temporary files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		exit, _, stderr := runSelfTalk(t, "example", dir)
		require.Equal(t, 0, exit, stderr)
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		for _, entry := range entries {
			want, err := pages.ReadFile("testdata/example-pages/" + entry.Name())
			require.NoError(t, err)
			got, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	})
	t.Run("dangling symlink never writes outside directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.md")
		target := filepath.Join(dir, "journal.md")
		require.NoError(t, os.Symlink(outside, target))
		exit, _, stderr := runSelfTalk(t, "example", dir)
		assert.Equal(t, 2, exit, stderr)
		assert.Contains(t, stderr, "cannot write")
		assert.NoFileExists(t, outside)
		link, err := os.Readlink(target)
		require.NoError(t, err)
		assert.Equal(t, outside, link)
	})
}

// The command example points to must survive shell parsing as one literal path, including a
// leading dash that would otherwise be parsed as a scan flag.
func TestExampleNextQuotesPathAsOneShellArgument(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, verb, file string
		want             []string
	}{
		{"ordinary path", "", "pages/journal.md", []string{"nova-self-talk", "pages/journal.md"}},
		{"spaces", "", "pages/my journal.md", []string{"nova-self-talk", "pages/my journal.md"}},
		{"apostrophe", "", "pages/O'Brien.md", []string{"nova-self-talk", "pages/O'Brien.md"}},
		{"literal shell syntax", "example", "pages/$HOME;$(touch marker).md", []string{"nova-self-talk", "example", "pages/$HOME;$(touch marker).md"}},
		{"leading dash", "", "-pages/journal.md", []string{"nova-self-talk", "--", "-pages/journal.md"}},
		{"leading dash directory", "example", "-pages", []string{"nova-self-talk", "example", "--", "-pages"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := onboarding.SplitShell(exampleNext(tc.verb, tc.file))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The emitted line and JSON remedy both hold a path the existing shell parser reads as one word.
func TestExampleEmitsRunnableNextCommandInBothFormats(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "O'Brien $HOME;$(touch marker) pages")
	exit, text, stderr := runSelfTalk(t, "example", dir)
	require.Equal(t, 0, exit, stderr)
	_, next, found := strings.Cut(strings.TrimSpace(text), "; run: ")
	require.True(t, found, text)
	want := []string{"nova-self-talk", filepath.Join(dir, "journal.md")}
	got, err := onboarding.SplitShell(next)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	exit, raw, stderr := runSelfTalk(t, "example", "--json", dir)
	require.Equal(t, 0, exit, stderr)
	var result struct {
		Result struct {
			Remedy string `json:"remedy"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &result), raw)
	assert.Equal(t, next, result.Result.Remedy)
	got, err = onboarding.SplitShell(result.Result.Remedy)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// Paths whose characters the one-line renderer escapes cannot be replayed; refuse before writing
// the example directory, in both result formats.
func TestExampleRefusesControlCharactersBeforeWriting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		char rune
	}{
		{"tab", '\t'},
		{"newline", '\n'},
		{"delete", 0x7f},
		{"c1", '\u0085'},
		{"line separator", '\u2028'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, asJSON := range []bool{false, true} {
				t.Run(map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					dir := filepath.Join(root, "bad"+string(tc.char)+"name")
					args := []string{"example"}
					if asJSON {
						args = append(args, "--json")
					}
					args = append(args, dir)
					exit, stdout, stderr := runSelfTalk(t, args...)
					assert.Equal(t, 2, exit)
					if asJSON {
						assert.Empty(t, stderr)
						assert.Contains(t, stdout, `"status":"refused"`)
						assert.Contains(t, stdout, "characters the one-line output must escape")
						assert.Contains(t, stdout, "would not name the same path")
					} else {
						assert.Empty(t, stdout)
						assert.Contains(t, stderr, "characters the one-line output must escape")
						assert.Contains(t, stderr, "would not name the same path")
					}
					entries, err := os.ReadDir(root)
					require.NoError(t, err)
					assert.Empty(t, entries)
				})
			}
		})
	}
}
