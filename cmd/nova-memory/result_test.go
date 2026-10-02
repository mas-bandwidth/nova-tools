package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonResult is the part of a --json result these tests read.
type jsonResult struct {
	Result struct {
		Verb   string   `json:"verb"`
		Status string   `json:"status"`
		Exit   int      `json:"exit"`
		Remedy string   `json:"remedy"`
		Why    []string `json:"why"`
	} `json:"result"`
	Facts map[string]any `json:"facts"`
	Items []struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"items"`
	Notes []string `json:"notes"`
}

// Every verb but version takes --json: one object on stdout, nothing on stderr, and the
// exit the lines end with (X1: two of seven verbs took it).
func TestEveryVerbAnswersAsOneJSONObject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pin := filepath.Join(dir, "pin.txt")
	require.NoError(t, os.WriteFile(pin, []byte("notes/lantern.md\n"), 0o644))
	draft := filepath.Join(dir, "draft.md")
	require.NoError(t, os.WriteFile(draft, []byte(firstRunDraft), 0o644))
	for _, tc := range []struct {
		name   string
		args   []string
		exit   int
		status string
		facts  map[string]any
		kinds  []string
	}{
		{"stats", []string{"stats", "--root", corpus}, 0, "ok", map[string]any{"schema": "nova-memory/2", "files": 6.0}, []string{"class"}},
		{"search", []string{"search", "--root", corpus, "--channels", "bm25", "--k", "3", "lantern"}, 0, "ok", map[string]any{"k": 3.0}, []string{"hit"}},
		{"check, --json after the file", []string{"check", "--root", corpus, "--channels", "bm25", "--k", "2", draft}, 0, "ok", map[string]any{"candidates": 1.0}, []string{"hit"}},
		{"verify", []string{"verify", "--root", corpus, "--links", "info", "--coverage", "notes/lantern.md:notes/index-notes.md"}, 0, "ok", map[string]any{"gating": 0.0, "links": "info"}, nil},
		{"eval", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "3", "--floor", "0.1", exampleGold}, 0, "ok", map[string]any{"floor": 0.1}, nil},
		{"eval below its floor", []string{"eval", "--root", corpus, "--channels", "bm25", "--k", "1", "--floor", "1", exampleGold}, 1, "failed", map[string]any{"floor": 1.0}, []string{"miss"}},
		{"boot", []string{"boot", "--root", corpus, "--pin", pin}, 0, "ok", map[string]any{"files": 1.0}, nil},
		{"quickstart", []string{"quickstart", "--root", corpus}, 0, "ok", map[string]any{"done": 3.0, "words-source": "corpus-top-terms"}, []string{"step"}},
		{"a refusal", []string{"stats"}, 2, "refused", nil, nil},
		{"an unknown flag", []string{"stats", "--root", corpus, "--bogus"}, 2, "refused", nil, nil},
		{"an unknown verb", []string{"frobnicate"}, 2, "refused", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			textExit, _, _ := runCLI(t, "", tc.args...)
			exit, stdout, stderr := runCLI(t, "", append(append([]string(nil), tc.args...), "--json")...)
			assert.Equal(t, tc.exit, exit)
			assert.Equal(t, textExit, exit, "--json and the lines end with different exits")
			assert.Empty(t, stderr, "--json wrote to stderr")
			var got jsonResult
			require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout is not one JSON object:\n%s", stdout)
			assert.Equal(t, tc.status, got.Result.Status)
			assert.Equal(t, tc.exit, got.Result.Exit)
			for k, v := range tc.facts {
				assert.Equal(t, v, got.Facts[k], "fact %s", k)
			}
			seen := map[string]bool{}
			for _, it := range got.Items {
				seen[it.Kind] = true
			}
			for _, k := range tc.kinds {
				assert.True(t, seen[k], "no item of kind %s", k)
			}
			if tc.status == "refused" {
				assert.NotEmpty(t, got.Result.Why)
			}
		})
	}
}

// verbNames is every verb the tool answers, version included.
func verbNames() []string {
	names := []string{"version"}
	for _, v := range memoryTool().Verbs {
		names = append(names, v.Name)
	}
	return names
}

// Every verb's -h states its effect: each is an inspection, and none takes --dry-run
// because none writes (the tool-answers ledger's dry-run row).
func TestEveryVerbsHelpStatesItsEffect(t *testing.T) {
	t.Parallel()

	for _, verb := range verbNames() {
		t.Run(verb, func(t *testing.T) {
			exit, stdout, _ := runCLI(t, "", verb, "-h")
			require.Equal(t, 0, exit)
			assert.Contains(t, stdout, "\neffect: inspection: reads, writes nothing")
		})
	}
}

// A mistake is one line in the refusal grammar every tool shares, naming the way forward:
// the verbs there are, or the verb's flags and the nearest one (X2, X3, X4, M-3).
func TestEveryMistakeIsOneRefusalNamingTheWayForward(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"bare", nil, "MEMORY REFUSED: no verb given; the verbs are quickstart, stats, search, check, verify, eval, boot, version; run: nova-memory help\n"},
		{"unknown verb", []string{"serch"}, `MEMORY REFUSED: unknown verb "serch"; did you mean search? the verbs are quickstart, stats, search, check, verify, eval, boot, version; run: nova-memory help` + "\n"},
		{"unknown flag", []string{"stats", "--rot", corpus}, "STATS REFUSED: unknown flag --rot; the flags of stats are --exclude, --json, --root; did you mean --root?; run: nova-memory stats -h\n"},
		{"a flag with no value", []string{"search", "--root", corpus, "--channels", "bm25", "x", "--k"}, "SEARCH REFUSED: --k needs a value: it wants a whole number (receipts per query, positive (default 10): k is the mind's budget, and zero is not unlimited); run: nova-memory search -h\n"},
		{"check's lines are MEMORY's", []string{"check", "--root", corpus, "--channels", "bm25", "--k", "2", "a.md", "b.md"}, "MEMORY REFUSED: takes <file|->, exactly 1 argument, got 2; run: nova-memory check -h\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := runCLI(t, "", tc.args...)
			assert.Equal(t, 2, exit)
			assert.Empty(t, stdout)
			assert.Equal(t, tc.want, stderr)
		})
	}
}

// Flags may follow the query words: `lantern --json` is the flag, not a word searched for,
// and -- still ends the flags for a word that starts with a dash.
func TestFlagsMayFollowTheQueryWords(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "lantern", "--channels", "bm25", "--k", "3", "--json")
	require.Equal(t, 0, exit, stderr)
	var got jsonResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, "lantern", got.Facts["query"])

	exit, stdout, stderr = runCLI(t, "", "search", "--root", corpus, "--channels", "bm25", "--k", "3", "--", "-glazing", "--json")
	require.Equal(t, 0, exit, stderr)
	assert.Contains(t, stdout, `SEARCH OK hits=`)
	assert.Contains(t, stdout, `query="-glazing --json"`)
}

// The quickstart's words are quoted free text, their spaces kept, never \x20 (M-2).
func TestQuickstartQuotesItsWords(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "quickstart", "--root", corpus, "--words", "lantern", "--words", "clean deploy")
	require.Equal(t, 0, exit, stderr)
	first := strings.SplitN(stdout, "\n", 2)[0]
	assert.NotContains(t, first, `\x20`)
	assert.True(t, strings.HasSuffix(first, ` words="lantern clean deploy"`), "the first line is %q", first)
	_, err := strconv.Unquote(first[strings.Index(first, " words=")+len(" words="):])
	assert.NoError(t, err)
}

// Under channel fusion the rank orders fused=, which never rises down the list, while
// score= is the native score of the channel named beside it and may (M-4: a rater read
// score= as the rank's order). The banner says which column the rank follows.
func TestTheRankFollowsFusedAndTheBannerSaysSo(t *testing.T) {
	t.Parallel()

	exit, stdout, stderr := runCLI(t, "", "search", "--root", corpus, "--channels", "bm25,trigram", "--k", "5", "glazing", "salt", "haze", "brass")
	require.Equal(t, 0, exit, stderr)
	prev, hits, channels := 1.0, 0, map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "SEARCH HIT ") {
			continue
		}
		hits++
		f, err := strconv.ParseFloat(field(t, line, "fused"), 64)
		require.NoError(t, err)
		assert.LessOrEqual(t, f, prev, "fused= rose down the list: %s", line)
		prev = f
		channels[field(t, line, "score-channel")] = true
	}
	assert.Equal(t, 5, hits)
	assert.Len(t, channels, 2, "the fixture query is meant to reach both channels")
	for _, verb := range []string{"search", "check"} {
		_, help, _ := runCLI(t, "", verb, "-h")
		assert.Contains(t, help, "a hit is ranked by fused=")
		assert.Contains(t, help, "so score= need not fall with rank")
	}
}

// The definition meets the standard the banner and help carry by construction only when
// it is complete: every verb's effect, the how text's size, the status words.
func TestMemoryToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, memoryTool().Problems())
}
