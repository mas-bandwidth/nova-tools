package main

// What the tool tells a reader about itself: the help banner, each verb's help, the door every
// refusal names, and the spec that promises what it prints.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/mas-bandwidth/nova-tools/internal/testverbhelp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHelpListsOneSumForm pins that the bare help banner carries the one `sum` form, the
// --out/--month report, on its own synopsis line (a bare continuation would make two calls
// read as one). The `usage:` block ends at the first blank line, so the pasteable
// example lines below it are not counted as synopsis lines.
func TestHelpListsOneSumForm(t *testing.T) {
	t.Parallel()

	r := novaTokens.Do(t, "help").Exit(0).Out("--out <dir> --month <YYYY-MM>")
	_, block, _ := strings.Cut("\n"+r.Stdout, "\nusage:\n")
	block, _, _ = strings.Cut("\n"+block, "\n\n")
	assert.Equal(t, 1, strings.Count(block, "\n  nova-tokens sum "), "the usage block's `nova-tokens sum` synopsis lines: %s", r)
}

// Every verb answers -h and --help with its own help on stdout at exit 0, and
// none of them reads a transcript or writes a day file (the CLI style's rule
// (b)).
func TestEveryVerbAnswersHelpAndTouchesNothing(t *testing.T) {
	t.Parallel()
	out := []string{"--out", "{dir}/out"}
	testverbhelp.Check(t, novaTokens.NoStdin(), []testverbhelp.Case{
		{Verb: "fold", Flags: out}, {Verb: "report"}, {Verb: "ledger"}, {Verb: "sum"}, {Verb: "check"},
		{Verb: "sources"}, {Verb: "profiles"}, {Verb: "session"}, {Verb: "version"},
	})
	testverbhelp.HelpVerb(t, novaTokens.NoStdin(), "nova-tokens", "fold", "sources", "version")
}

// Every registered flag has a help description that tells a cold reader what value it takes
// or what effect the switch has (ledger and report help are inspection modes), and the two
// report modes, whose inputs and outputs differ, are each named.
func TestEveryVerbFlagHasADescription(t *testing.T) {
	t.Parallel()

	for verb, wants := range map[string][]string{
		"fold": nil, "report": {"mode: local note body", "mode: Redis month summary"}, "ledger": nil, "sum": nil,
		"check": nil, "sources": nil, "profiles": nil, "session": nil,
	} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			r := novaTokens.Do(t, verb, "-h").Exit(0).Out(wants...)
			_, flags, _ := strings.Cut("\n"+r.Stdout, "\nflags:\n")
			flags, _, _ = strings.Cut(flags, "\nexit code")
			n := 0
			for _, line := range strings.Split(flags, "\n") {
				if strings.HasPrefix(line, "  --") {
					n++
					assert.Contains(t, line[2:], "  ", "%s flag has no help description", verb)
				}
			}
			assert.Positive(t, n, "%s help listed no flags; the test did not inspect anything", verb)
		})
	}
}

// A bare verb names what is wrong and the door (ONBOARDING point 1): an invocation the tool
// cannot run prints `<tool>[ <verb>]: <what was wrong>; run: <tool> help`. A missing-flag or
// bad-value refusal printed with no door leaves the reader no place to look.
// The verbs are read from the tool's own help banner (a line opening `  nova-tokens <verb>`
// with a flag; version, whose line has none, is no refusal when bare) and each is run with
// no flags. A bad refusal that said "refusing to guess" and dumped the whole banner (which
// names help) would pass a check for "help" anywhere, so the door is pinned as the literal
// end of every refusal line, once. `version <stray>` is a row too: a bare version prints the
// version, so its stray argument is the refusal, and it still says what was wrong.
func TestIssue1451EveryRefusalNamesTheDoor(t *testing.T) {
	t.Parallel()

	const door = "; run: nova-tokens help"
	rows := map[string][]string{"version stray argument": {"version", "extra"}}
	for _, line := range strings.Split(novaTokens.Do(t, "help").Exit(0).Stdout, "\n") {
		if f := strings.Fields(line); strings.HasPrefix(line, "  nova-tokens ") && len(f) >= 3 && strings.Contains(line, " --") {
			rows[f[1]] = f[1:2]
		}
	}
	require.Greater(t, len(rows), 1, "no flag-bearing verbs parsed from the help banner")
	for name, args := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := novaTokens.Do(t, args...).Exit(2)
			for _, line := range strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n") {
				assert.True(t, strings.HasSuffix(line, door), "a refusal line does not end in the door: %q", line)
				assert.Equal(t, 1, strings.Count(line, door), "a refusal line names the door more than once: %q", line)
			}
			if args[0] == "version" {
				r.Err(`takes no positional arguments, got "extra"`)
				assert.Equal(t, 1, strings.Count(r.Stderr, door), "the stray argument's refusal is one line: %s", r)
			}
		})
	}
}

// docs/SPEC-TOKENS.md is the one place the contract is written.
func TestTheSpecPromisesWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	spec := testkit.ReadFile(t, filepath.Join("..", "..", "docs", "SPEC-TOKENS.md"))

	// Spec-versus-code drift: every verb the binary accepts and every first-two-token line it
	// prints is promised by the verbs block and the output grammar.
	t.Run("363 every verb and token it prints", func(t *testing.T) {
		t.Parallel()
		for _, want := range []string{"nova-tokens profiles", "nova-tokens session", "PROFILES OK", "PROFILES MODEL", "SESSION turns="} {
			assert.Contains(t, spec, want, "the binary accepts or prints it but docs/SPEC-TOKENS.md does not promise it")
		}
	})

	// The efficiency section: what the tool pays once and what it pays
	// again. One walk of the sources per run behind `--all`, a bounded coordinator read whose
	// day line carries the shares, and a `--timeout` that bounds one source and not the run.
	// The section is prose, so whitespace is collapsed: a phrase is checked for its words, not
	// its column.
	t.Run("85 the efficiency card names its rules", func(t *testing.T) {
		t.Parallel()
		_, section, ok := strings.Cut(spec, "## The efficiency card (#85), nova-tokens")
		require.True(t, ok, "the spec has no #85 efficiency card section")
		section, _, _ = strings.Cut(section, "\n## ")
		section = strings.Join(strings.Fields(section), " ")
		for _, want := range []string{
			// the measurement the card published, on the bench.
			"1,397", "1,699 MB", "57,239", "87 %",
			// the walk: claude.go parses the tree, the fold folds every day.
			"internal/tokens/claude.go:98", "cmd/nova-tokens/main.go:587", "one walk of the sources per run", "3.27 s", "dup=49765", "messages=57239",
			// the coordinator read: one source, one day, one OK, `check` one line per finding under `--max`.
			"TOKENS SOURCE", "TOKENS DAY", "TOKENS OK", "`check` is one line per finding", "`--max`", "33",
			// what a run waits on: `--timeout` is one source, not the run.
			"120 s", "`--timeout`", "CHECK FAIL files=9 rows=0 first=2026-07-29 last=2026-09-11 bad=9 missing=36 stray=2", "`sum --month 2026-09`", "fold --day <d>",
			"Red tests",
		} {
			assert.Contains(t, section, want, "SPEC-TOKENS.md's nova-tokens efficiency card names it")
		}
	})
}
