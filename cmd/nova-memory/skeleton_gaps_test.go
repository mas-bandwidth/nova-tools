package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryProbe declares nova-memory's verbs the way the move onto internal/tool
// would (docs/STANDARD.md section 2, "one shape across the set"), with
// Tool.Default on search because search is the verb a bare word most nearly
// means. The tests below run it and the real tool against the skeleton as it
// stands and record where the skeleton stops the move; the card that lands the
// move deletes this file together with the private dispatcher in main.go.
func memoryProbe() *tool.Tool {
	return &tool.Tool{
		Name:      "nova-memory",
		What:      "search your own markdown notes, and check a draft against what they already say",
		How:       "each run reads the --root trees and builds its index in memory; nothing is written",
		ExitTable: "0 ran and passed, 1 ran and failed, 2 could not run",
		Default:   "search",
		Verbs: []tool.Verb{
			{
				Name:   "search",
				Usage:  "search --root <dir>... --channels <list> --k <n> <words>...",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Required("root", "the corpus root directory, repeatable")
					f.Required("channels", "the retrieval channels: bm25, trigram")
					f.Int("k", 0, "receipts per query, positive")
				},
				// The words never arrive: Call carries no Args, so the verb
				// that owns them cannot read them. The marker proves the cap
				// lifted and the run happened anyway.
				Run: func(*tool.Call) *tool.Out {
					return tool.Done().Fact("words", 0)
				},
			},
			{
				Name:   "check",
				Usage:  "check --root <dir>... --channels <list> --k <n> <file|->",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Required("root", "the corpus root directory, repeatable")
					f.Required("channels", "the retrieval channels: bm25, trigram")
					f.Int("k", 0, "receipts per candidate, positive")
				},
				Run: func(*tool.Call) *tool.Out { return tool.Done() },
			},
			{
				Name:   "eval",
				Usage:  "eval --root <dir>... --channels <list> --k <n> --floor <f> <gold.tsv>",
				Effect: tool.Inspection,
				Flags: func(f *tool.Flags) {
					f.Required("root", "the corpus root directory, repeatable")
					f.Required("channels", "the retrieval channels: bm25, trigram")
					f.Int("k", 0, "receipts per query, positive")
					f.Float64("floor", 0, "minimum recall@k in (0,1]")
				},
				Run: func(*tool.Call) *tool.Out { return tool.Done() },
			},
		},
	}
}

// TestTheSkeletonRefusesThePositionalArgumentsThreeVerbsTake pins the first
// gap. nova-memory's search takes its query as free words, check its candidate
// as one path or -, and eval its known-answer rows as one gold path
// (docs/CLI.md, the tool's usage block), and package flag stops at the first
// non-flag word, so all three reach the verb as positional arguments. The
// skeleton's call refuses every positional argument on every verb but
// Tool.Default, and Default is one name while three verbs here need it; and
// even where the cap lifts — the one default verb, named explicitly — Call
// exposes no Args, so the words never reach the code that runs them. The
// unblock is what the fuse card named for the same gap: restore
// Flags.Positional and Call.Args, and a way for more than one verb to take
// positionals; until then a move of this tool would strand search, check and
// eval behind a second dispatcher, which this card forbids.
func TestTheSkeletonRefusesThePositionalArgumentsThreeVerbsTake(t *testing.T) {
	t.Parallel()

	probe := memoryProbe()
	require.Empty(t, probe.Problems(), "the probe is a whole definition, so the refusals below are the gap and not a malformed verb")

	_, ok := reflect.TypeOf((*tool.Call)(nil)).Elem().MethodByName("Args")
	assert.Falsef(t, ok, "Call grew an Args method; this test and the gap it pins are stale — delete both with the move")

	cases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{
			name:       "check names its candidate as one positional",
			args:       []string{"check", "--root", corpus, "--channels", "bm25", "--k", "2", "-"},
			wantStderr: `CHECK REFUSED: takes no positional arguments, got "-" (flags come before arguments); run: nova-memory help` + "\n",
		},
		{
			name:       "eval names its gold file as one positional",
			args:       []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.8", exampleGold},
			wantStderr: `EVAL REFUSED: takes no positional arguments, got "` + exampleGold + `" (flags come before arguments); run: nova-memory help` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := memoryProbe().Run(tc.args, strings.NewReader(""), &stdout, &stderr)
			assert.Equal(t, 2, code, "the candidate is refused, so the verb never runs; stdout=%q", stdout.String())
			assert.Empty(t, stdout.String())
			assert.Equal(t, tc.wantStderr, stderr.String())
		})
	}

	// search is the one default verb, so its words pass the cap — and still
	// cannot be read: the verb runs and answers a count of zero words.
	t.Run("search is the default verb and still cannot read its words", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := probe.Run([]string{"search", "--root", corpus, "--channels", "bm25", "--k", "3", "lantern", "glazing"}, strings.NewReader(""), &stdout, &stderr)
		require.Equal(t, 0, code, "the words passed the cap; stderr: %s", stderr.String())
		assert.Equal(t, "SEARCH OK words=0\n", stdout.String(), "Call carries no Args, so the query never reached the verb")
	})
}

// TestTheOKLineCarriesOneValueTwoWaysAndCheckAnswersUnderAnotherName pins the
// second and third gaps, against the real tool's own output (docs/TESTS.md,
// the search and check transcripts).
//
// The search first line ends `: query="..."`: the query is a fact of the JSON
// answer and a prose tail of the head line at once — one value, two renderings
// (docs/STANDARD.md section 2). Out's head prints facts and no tail; a Why
// prints after the colon but lands in the JSON's why, not its facts, so no
// Out holds the query in both places. check answers under the token MEMORY,
// and Out's token is the verb's own name upper-cased, so the move would print
// CHECK. The unblock is a prose-tail field on the head (a fact that renders
// behind the colon in text and as itself in JSON) and a token the verb may
// name.
func TestTheOKLineCarriesOneValueTwoWaysAndCheckAnswersUnderAnotherName(t *testing.T) {
	t.Parallel()

	t.Run("search ends its head with the query as prose", func(t *testing.T) {
		t.Parallel()
		exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3", "lantern", "glazing")
		require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
		assert.Containsf(t, stdout, `: query="`, "the head line carries the query after a colon, the place the skeleton has no field for:\n%s", stdout)
	})

	t.Run("check answers under the token MEMORY", func(t *testing.T) {
		t.Parallel()
		exit, stdout, stderr := runCLI(t, "The lantern glazing needs clean cloths for brass and glass.",
			"check", "--root", corpus, "--channels", "bm25", "--k", "2", "-")
		require.Equalf(t, 0, exit, "exit = %d, want 0; stderr: %s", exit, stderr)
		assert.Truef(t, strings.HasPrefix(stdout, "MEMORY "), "check answers under MEMORY, a token that is not the verb's name; Out's token is the verb's own:\n%s", stdout)
	})

	t.Run("the nearest Out holds the query in only one of the two places", func(t *testing.T) {
		t.Parallel()
		const query = `query="lantern glazing"`
		asFact := tool.Done()
		asFact.Verb = "search"
		asFact.Fact("hits", 1).Fact("query", tool.Text(`lantern glazing`))
		var text bytes.Buffer
		asFact.Render(&text, false)
		assert.NotContainsf(t, text.String(), ": "+query, "a fact renders in the head without the tail: %s", text.String())

		asWhy := tool.Done()
		asWhy.Verb = "search"
		asWhy.Why = []string{query}
		var lines, asJSON bytes.Buffer
		asWhy.Render(&lines, false)
		assert.Containsf(t, lines.String(), ": "+query, "a why renders after the colon: %s", lines.String())
		asWhy.Render(&asJSON, true)
		assert.Containsf(t, asJSON.String(), `"why":["query=`, "the same value moves out of the JSON's facts and into its why: %s", asJSON.String())
		assert.Containsf(t, asJSON.String(), `"facts":{}`, "the JSON's facts no longer hold the query: %s", asJSON.String())
	})
}
