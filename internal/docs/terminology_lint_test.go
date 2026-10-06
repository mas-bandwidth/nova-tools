package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// terminology_lint_test.go holds docs/TERMINOLOGY.md's retired words as a rule:
// a word the owner retired does not come back in the live text. The words and
// what replaced each are in testdata/retired-words.txt; the tracked .go, .md,
// .yml and .sh files are read from `git ls-files` at the repository root, the
// way scratch_tracked_test.go reaches it.

const retiredWordsPath = "testdata/retired-words.txt"

// ledgerTag marks a ledger row in retired-words.txt: a file that still uses a
// retired word and awaits its card. The ledger only shrinks.
const ledgerTag = "LEDGER"

// lintedExts are the tracked file kinds the lint reads: docs, help text and
// comments (Go), the workflow and the scripts.
var lintedExts = map[string]bool{".go": true, ".md": true, ".yml": true, ".sh": true}

// recordPaths are the dated records, where a retired word is history and stays:
// the changelog, the resolutions, the release notes and the ratings.
var recordPaths = []string{"CHANGELOG.md", "RESOLUTIONS.md", "docs/RELEASE-NOTES-", "docs/ratings/"}

// namingPaths state each retirement and its replacement, so they name the word;
// the lint's own test names it to pin the matcher.
var namingPaths = []string{"docs/TERMINOLOGY.md", "docs/GLOSSARY.md", "docs/sprint/GLOSSARY.md", "internal/docs/terminology_lint_test.go"}

type retiredWord struct {
	word, replacement string
	pat               *regexp.Regexp
}

// TestRetiredWordsAppearOnlyInRecords fails once for every tracked file that
// uses a retired word outside the records and the ledger, naming the file, the
// line and the replacement; and once for every ledger row that no longer holds,
// so the ledger shrinks as the files are fixed.
func TestRetiredWordsAppearOnlyInRecords(t *testing.T) {
	t.Parallel()

	words, ledger := readRetiredWords(t)
	require.NotEmpty(t, words, "%s lists no retired word", retiredWordsPath)

	root := filepath.Join("..", "..")
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	require.NoError(t, err, "git ls-files from the repository root: %v", err)

	seen := map[string]bool{}
	var problems []string
	for _, path := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if !lintedPath(path) {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			continue
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, w := range words {
				if !w.pat.MatchString(line) {
					continue
				}
				key := path + "\t" + w.word
				seen[key] = true
				if !ledger[key] {
					problems = append(problems, path+":"+strconv.Itoa(i+1)+": uses the retired word \""+w.word+"\"; write \""+w.replacement+"\" (docs/TERMINOLOGY.md)")
				}
			}
		}
	}
	for key := range ledger {
		if !seen[key] {
			problems = append(problems, retiredWordsPath+": ledger row "+strings.ReplaceAll(key, "\t", " / ")+" no longer holds; delete the row")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// TestRetiredWordMatchingIsWholeWordAndAnyCase pins the matcher on both sides:
// the word alone matches, in any case, and a longer word that contains it does
// not.
func TestRetiredWordMatchingIsWholeWordAndAnyCase(t *testing.T) {
	t.Parallel()

	w := newRetiredWord("git bus", "nova-bus")
	for line, want := range map[string]bool{
		"the git bus is gone":     true,
		"The Git Bus":             true,
		"digit bus":               false,
		"git business":            false,
		"nova-bus replaced it":    false,
		"a (git bus) in brackets": true,
	} {
		if got := w.pat.MatchString(line); got != want {
			t.Errorf("%q: matched %v, want %v", line, got, want)
		}
	}
}

// TestLintedPathSkipsRecordsAndFixtures pins which tracked files the lint reads.
func TestLintedPathSkipsRecordsAndFixtures(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]bool{
		"docs/SPEC-SPRINT.md":             true,
		"cmd/nova-sprint/verbs.go":        true,
		"CHANGELOG.md":                    false,
		"RESOLUTIONS.md":                  false,
		"docs/RELEASE-NOTES-1.1.0.md":     false,
		"docs/ratings/1.1.0/a/b.md":       false,
		"internal/ci/testdata/ledger.txt": false,
		"internal/docs/testdata/x.md":     false,
		"docs/TERMINOLOGY.md":             false,
		"docs/sprint/GLOSSARY.md":         false,
		"internal/sprint/TABLES.lock":     false,
		"internal/sprintdash/page/app.js": false,
	} {
		if got := lintedPath(path); got != want {
			t.Errorf("lintedPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func lintedPath(path string) bool {
	if !lintedExts[filepath.Ext(path)] || strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/") {
		return false
	}
	for _, p := range recordPaths {
		if path == p || strings.HasPrefix(path, p) {
			return false
		}
	}
	for _, p := range namingPaths {
		if path == p {
			return false
		}
	}
	return true
}

// newRetiredWord compiles the whole-word, any-case matcher once; it is never
// built per line.
func newRetiredWord(word, replacement string) retiredWord {
	pat := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(word) + `($|[^A-Za-z0-9_])`)
	return retiredWord{word: word, replacement: replacement, pat: pat}
}

// readRetiredWords parses retired-words.txt: word rows and ledger rows.
func readRetiredWords(t *testing.T) ([]retiredWord, map[string]bool) {
	t.Helper()
	raw, err := os.ReadFile(retiredWordsPath)
	require.NoError(t, err, "%s: %v", retiredWordsPath, err)

	var words []retiredWord
	ledger := map[string]bool{}
	for n, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		switch {
		case cols[0] == ledgerTag && len(cols) == 3:
			ledger[cols[1]+"\t"+cols[2]] = true
		case cols[0] != ledgerTag && len(cols) == 2 && cols[1] != "":
			words = append(words, newRetiredWord(cols[0], cols[1]))
		default:
			t.Errorf("%s:%d: want <word><TAB><replacement> or LEDGER<TAB><path><TAB><word>, got %q", retiredWordsPath, n+1, line)
		}
	}
	return words, ledger
}
