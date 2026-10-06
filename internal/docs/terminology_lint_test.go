package docs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// terminology_lint_test.go holds docs/TERMINOLOGY.md's retirements as a rule. A word the
// family retired comes back by habit, in a help line, a comment or a spec, and a reader who
// meets two words for one thing stops trusting both. testdata/retired-words.txt lists each
// retired word and what replaced it; the lint reads `git ls-files` from the repository root
// and fails for any tracked text file that uses one outside the dated records.
//
// Records keep the words of their day: CHANGELOG.md, RESOLUTIONS.md, docs/RELEASE-NOTES-*,
// docs/ratings/ and every testdata/ directory (the ledgers). The three terminology pages
// name a retired word on purpose, to say what replaced it. Every other hit is either fixed
// or carried as a row of the shrink-only allowlist in retired-words.txt, one row per file
// and word with its count; a count that rises is a new use, and a count that falls fails
// until the row is lowered, so the ledger only ever shrinks. It reads text and runs nothing.

const retiredWordsPath = "testdata/retired-words.txt"

// retiredWordRow is one `word <w> => <replacement>` row.
type retiredWordRow struct {
	word        string
	replacement string
	re          *regexp.Regexp
}

// retiredAllowKey names one allowlist row.
type retiredAllowKey struct{ word, path string }

// retiredWordsFile parses retired-words.txt: words, and the allowlist of counts.
func retiredWordsFile(t *testing.T) ([]retiredWordRow, map[retiredAllowKey]int) {
	t.Helper()
	raw, err := os.ReadFile(retiredWordsPath)
	require.NoError(t, err, "%s: %v", retiredWordsPath, err)

	var words []retiredWordRow
	allow := map[retiredAllowKey]int{}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "word "):
			word, replacement, ok := strings.Cut(strings.TrimPrefix(line, "word "), " => ")
			require.True(t, ok && strings.TrimSpace(word) != "" && strings.TrimSpace(replacement) != "",
				"%s:%d: a word row is `word <retired word> => <what replaced it>`, got %q", retiredWordsPath, i+1, line)
			word = strings.TrimSpace(word)
			pattern := `(?i)\b` + strings.Join(strings.Fields(regexp.QuoteMeta(word)), `\s+`) + `\b`
			words = append(words, retiredWordRow{word: word, replacement: strings.TrimSpace(replacement), re: regexp.MustCompile(pattern)})
		case strings.HasPrefix(line, "allow "):
			f := strings.Fields(strings.TrimPrefix(line, "allow "))
			require.GreaterOrEqual(t, len(f), 3, "%s:%d: an allow row is `allow <word> <path> <count>`, got %q", retiredWordsPath, i+1, line)
			n, convErr := strconv.Atoi(f[len(f)-1])
			require.NoError(t, convErr, "%s:%d: the count of an allow row is a number: %v", retiredWordsPath, i+1, convErr)
			key := retiredAllowKey{word: strings.Join(f[:len(f)-2], " "), path: f[len(f)-2]}
			_, dup := allow[key]
			require.False(t, dup, "%s:%d: a second allow row for %s in %s", retiredWordsPath, i+1, key.word, key.path)
			allow[key] = n
		default:
			require.Failf(t, "unknown row", "%s:%d: a row starts `word ` or `allow `, got %q", retiredWordsPath, i+1, line)
		}
	}
	require.NotEmpty(t, words, "%s names no retired word", retiredWordsPath)
	return words, allow
}

// retiredWordsExempt says a path is a dated record or a terminology page, where the
// words of the day or the retirement itself belong.
func retiredWordsExempt(path string) bool {
	switch path {
	case "CHANGELOG.md", "RESOLUTIONS.md", "docs/TERMINOLOGY.md", "docs/GLOSSARY.md", "docs/sprint/GLOSSARY.md":
		return true
	}
	return strings.HasPrefix(path, "docs/RELEASE-NOTES-") ||
		strings.HasPrefix(path, "docs/ratings/") ||
		strings.HasPrefix(path, "testdata/") ||
		strings.Contains(path, "/testdata/")
}

// retiredWordCounts counts each retired word's uses in each tracked text file outside the records.
func retiredWordCounts(t *testing.T, words []retiredWordRow) map[retiredAllowKey]int {
	t.Helper()
	root := filepath.Join("..", "..")
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	require.NoError(t, err, "git ls-files from the repository root: %v", err)

	counts := map[retiredAllowKey]int{}
	for _, path := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if path == "" || retiredWordsExempt(path) {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil || bytes.IndexByte(body, 0) >= 0 {
			continue
		}
		for _, w := range words {
			if n := len(w.re.FindAllIndex(body, -1)); n > 0 {
				counts[retiredAllowKey{word: w.word, path: path}] = n
			}
		}
	}
	return counts
}

// TestRetiredWordsAppearOnlyInRecords is the lint. It fails once for every file and word
// whose count differs from the allowlist, naming the replacement.
func TestRetiredWordsAppearOnlyInRecords(t *testing.T) {
	t.Parallel()

	words, allow := retiredWordsFile(t)
	replacement := map[string]string{}
	for _, w := range words {
		replacement[w.word] = w.replacement
	}
	for key := range allow {
		_, known := replacement[key.word]
		require.True(t, known, "%s: an allow row names %q, which is no retired word", retiredWordsPath, key.word)
	}

	counts := retiredWordCounts(t, words)
	var problems []string
	for key, n := range counts {
		switch have := allow[key]; {
		case n > have:
			problems = append(problems, fmt.Sprintf("%s uses the retired word %q %d times (allowed %d); write %s", key.path, key.word, n, have, replacement[key.word]))
		case n < have:
			problems = append(problems, fmt.Sprintf("%s uses %q %d times, below its allow row of %d; lower the row in %s (allow %s %s %d)", key.path, key.word, n, have, retiredWordsPath, key.word, key.path, n))
		}
	}
	for key, have := range allow {
		if _, still := counts[key]; !still {
			problems = append(problems, fmt.Sprintf("%s no longer uses %q; delete its allow row (allow %s %s %d) from %s", key.path, key.word, key.word, key.path, have, retiredWordsPath))
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// TestRetiredWordsLintSeesAWord pins the matcher on the file's own rows: the word whole, in
// any case, a phrase across a line break, and nothing inside a longer word.
func TestRetiredWordsLintSeesAWord(t *testing.T) {
	t.Parallel()

	words, _ := retiredWordsFile(t)
	for _, w := range words {
		assert.True(t, w.re.MatchString("a "+w.word+" here"), "%q: the matcher misses the word", w.word)
		assert.True(t, w.re.MatchString(strings.ToUpper(w.word)), "%q: the matcher is case-sensitive", w.word)
		assert.True(t, w.re.MatchString(strings.Replace(w.word, " ", "\n", 1)), "%q: the matcher misses a line break inside the phrase", w.word)
		assert.False(t, w.re.MatchString("x"+w.word), "%q: the matcher fires inside a longer word", w.word)
		assert.False(t, w.re.MatchString(w.word+"s"), "%q: the matcher fires inside a longer word", w.word)
	}
}
