package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// terminology_lint_test.go holds the retired words of docs/TERMINOLOGY.md: a
// tracked text file (docs, help text in cmd/, comments) uses none of them,
// outside the dated records. The words and what replaced each are
// testdata/retired-words.txt, tab separated: `retired<TAB>word<TAB>replacement`.
// A place that still uses one is a row `allow<TAB>path<TAB>word<TAB>count` of the
// same file: a ledger that only shrinks, so a new use is a refusal and a cleaned
// file makes its row stale. The glossaries name what a word replaced after
// `Replaces:`, the one place the word may stand there.

// retiredWord is one retired word and the word that replaced it.
type retiredWord struct {
	word, replacement string
}

// retiredLedger is the parsed retired-words.txt.
type retiredLedger struct {
	words []retiredWord
	allow map[string]int // path + "\t" + word -> uses still allowed
}

// lintExts are the extensions of the text files the lint reads.
var lintExts = map[string]bool{
	".go": true, ".md": true, ".txt": true, ".yml": true, ".yaml": true, ".js": true,
	".tla": true, ".lock": true, ".json": true, ".tsv": true, ".sh": true, ".html": true,
	".css": true, ".tmpl": true, ".j2": true, ".sql": true, ".lua": true, ".cfg": true,
}

// glossaryFiles may name a retired word after "Replaces:".
var glossaryFiles = map[string]bool{
	"docs/GLOSSARY.md": true, "docs/sprint/GLOSSARY.md": true, "docs/TERMINOLOGY.md": true,
}

// isDatedRecord reports whether a repository-relative path is a dated record or
// the lint's own data: the changelog, the resolutions, the release notes, the
// ratings, the ledgers under testdata/, and this lint.
func isDatedRecord(rel string) bool {
	switch {
	case rel == "CHANGELOG.md", rel == "RESOLUTIONS.md",
		rel == "internal/docs/terminology_lint_test.go",
		strings.HasPrefix(rel, "docs/RELEASE-NOTES-"),
		strings.HasPrefix(rel, "docs/ratings/"),
		strings.HasPrefix(rel, "testdata/"),
		strings.Contains(rel, "/testdata/"):
		return true
	}
	return false
}

// isWordByte reports whether b is a letter, so a retired word inside a longer
// word is not a use.
func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// countWord counts the uses of word in text, case-insensitively, as a whole word.
func countWord(text, word string) int {
	text, word = strings.ToLower(text), strings.ToLower(word)
	n := 0
	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return n
		}
		start, end := from+i, from+i+len(word)
		if (start == 0 || !isWordByte(text[start-1])) && (end == len(text) || !isWordByte(text[end])) {
			n++
		}
		from = end
	}
}

// scanText counts each retired word's uses in one file's text; in a glossary
// file the text after "Replaces:" on a line is not read.
func scanText(rel, text string, words []retiredWord) map[string]int {
	if rel == "docs/CLI.md" {
		text = withoutGeneratedCLIBlocks(text)
	}
	if glossaryFiles[rel] {
		lines := strings.Split(text, "\n")
		for i, l := range lines {
			if cut := strings.Index(l, "Replaces:"); cut >= 0 {
				lines[i] = l[:cut]
			}
		}
		text = strings.Join(lines, "\n")
	}
	found := map[string]int{}
	for _, w := range words {
		if n := countWord(text, w.word); n > 0 {
			found[w.word] = n
		}
	}
	return found
}

// Generated CLI blocks quote the binaries' help verbatim. The terminology
// ledger governs the hand-written documentation around them; changing a quote
// here would make the command reference untrue to the binary.
func withoutGeneratedCLIBlocks(text string) string {
	var hand strings.Builder
	for {
		start := clidocBegin.FindStringSubmatchIndex(text)
		if start == nil {
			hand.WriteString(text)
			return hand.String()
		}
		tool := text[start[2]:start[3]]
		endMarker := "<!-- clidoc:end " + tool + " -->"
		end := strings.Index(text[start[1]:], endMarker)
		if end < 0 {
			hand.WriteString(text)
			return hand.String()
		}
		hand.WriteString(text[:start[0]])
		text = text[start[1]+end+len(endMarker):]
	}
}

func TestRetiredWordScanSkipsOnlyGeneratedCLIBlocks(t *testing.T) {
	t.Parallel()
	words := []retiredWord{{word: "asleep", replacement: "down"}}
	doc := "asleep in prose\n<!-- clidoc:begin nova-friend -->\nasleep in help\n<!-- clidoc:end nova-friend -->\nasleep in examples"
	assert.Equal(t, map[string]int{"asleep": 2}, scanText("docs/CLI.md", doc, words))
	assert.Equal(t, map[string]int{"asleep": 3}, scanText("docs/OTHER.md", doc, words))
	assert.Equal(t, map[string]int{"asleep": 2}, scanText("docs/CLI.md", "asleep <!-- clidoc:begin nova-friend --> asleep", words))
}

// loadRetired parses testdata/retired-words.txt.
func loadRetired(t *testing.T) retiredLedger {
	t.Helper()
	data, err := os.ReadFile("testdata/retired-words.txt")
	require.NoError(t, err, "reading the retired words: %v", err)
	led := retiredLedger{allow: map[string]int{}}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		switch {
		case f[0] == "retired" && len(f) == 3:
			led.words = append(led.words, retiredWord{word: f[1], replacement: f[2]})
		case f[0] == "allow" && len(f) == 4:
			n, convErr := strconv.Atoi(f[3])
			require.NoError(t, convErr, "retired-words.txt line %d: count %q is not a number", i+1, f[3])
			led.allow[f[1]+"\t"+f[2]] = n
		default:
			require.Failf(t, "retired-words.txt", "line %d is neither `retired<TAB>word<TAB>replacement` nor `allow<TAB>path<TAB>word<TAB>count`: %q", i+1, line)
		}
	}
	return led
}

// scanTree counts the retired words in every text file outside the dated
// records, keyed path + "\t" + word.
func scanTree(t *testing.T, words []retiredWord) map[string]int {
	t.Helper()
	root := "../.."
	out := map[string]int{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == "jobs" {
				return filepath.SkipDir
			}
			return nil
		}
		if !lintExts[filepath.Ext(rel)] || isDatedRecord(rel) {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		for word, n := range scanText(rel, string(data), words) {
			out[rel+"\t"+word] = n
		}
		return nil
	})
	require.NoError(t, err, "walking the repository: %v", err)
	return out
}

// TestRetiredWordsAppearOnlyInRecords fails when a retired word is used outside
// a dated record beyond the ledger's rows, and when a row is stale (the ledger
// only shrinks). docs/TERMINOLOGY.md, the rules of naming.
func TestRetiredWordsAppearOnlyInRecords(t *testing.T) {
	t.Parallel()

	led := loadRetired(t)
	require.GreaterOrEqual(t, len(led.words), 3, "retired-words.txt names %d words, want at least three (a git bus word, an asleep word, an untiered word)", len(led.words))
	replacement := map[string]string{}
	for _, w := range led.words {
		replacement[w.word] = w.replacement
	}

	found := scanTree(t, led.words)
	var keys []string
	for k := range found {
		keys = append(keys, k)
	}
	for k := range led.allow {
		if _, ok := found[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		path, word, _ := strings.Cut(k, "\t")
		got, allowed := found[k], led.allow[k]
		switch {
		case got > allowed:
			assert.Failf(t, "retired word", "%s uses %q %d times, %d allowed: say %q instead (docs/TERMINOLOGY.md), or the file is a dated record", path, word, got, allowed, replacement[word])
		case got < allowed:
			assert.Failf(t, "stale ledger row", "%s uses %q %d times and the ledger allows %d: set the row in testdata/retired-words.txt to `allow\t%s\t%s\t%d`, or delete it at 0", path, word, got, allowed, path, word, got)
		}
	}
}

// TestRetiredWordScanCatchesAUse pins the scan: a use is found whole-word and
// case-insensitively, a longer word is not a use, and a glossary's Replaces
// clause is the one place a glossary may name a retired word.
func TestRetiredWordScanCatchesAUse(t *testing.T) {
	t.Parallel()

	words := []retiredWord{{"asleep", "down"}, {"git bus", "Redis bus"}}
	cases := []struct {
		name, rel, text string
		want            map[string]int
	}{
		{"plain use", "docs/SPEC.md", "the friend is asleep", map[string]int{"asleep": 1}},
		{"case and count", "cmd/x/main.go", "Asleep, asleep.", map[string]int{"asleep": 2}},
		{"two words", "docs/SPEC.md", "the git bus is gone", map[string]int{"git bus": 1}},
		{"inside a longer word", "docs/SPEC.md", "fallasleepy asleeps", map[string]int{}},
		{"glossary replaces clause", "docs/GLOSSARY.md", "- **down** — x. Replaces: asleep.", map[string]int{}},
		{"glossary elsewhere", "docs/GLOSSARY.md", "- **down** — not asleep. Replaces: asleep.", map[string]int{"asleep": 1}},
		{"replaces clause outside a glossary", "docs/SPEC.md", "Replaces: asleep", map[string]int{"asleep": 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, scanText(c.rel, c.text, words))
		})
	}
	for _, rel := range []string{"CHANGELOG.md", "RESOLUTIONS.md", "docs/RELEASE-NOTES-1.1.0.md", "docs/ratings/a.md", "internal/ci/testdata/x.txt", "testdata/y.txt"} {
		assert.True(t, isDatedRecord(rel), "%s is a dated record", rel)
	}
	assert.False(t, isDatedRecord("docs/SPEC-SPRINT.md"), "a spec is not a dated record")
}

var glossaryEntryRe = regexp.MustCompile(`^- \*\*[^*]+\*\* — .+`)

// githubSlug is the anchor GitHub makes of a heading.
func githubSlug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		switch {
		case r == ' ' || r == '-':
			b.WriteByte('-')
		case r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestGlossariesDefineEveryTermWithItsSection reads both glossaries: every entry
// is one definition naming the section that defines it, each link resolves to a
// file and a heading, TERMINOLOGY.md links both, and the entries are in order.
func TestGlossariesDefineEveryTermWithItsSection(t *testing.T) {
	t.Parallel()

	term, err := os.ReadFile("../../docs/TERMINOLOGY.md")
	require.NoError(t, err, "reading TERMINOLOGY.md: %v", err)
	for _, link := range []string{"](GLOSSARY.md)", "](sprint/GLOSSARY.md)"} {
		assert.Contains(t, string(term), link, "docs/TERMINOLOGY.md links the glossary %s", link)
	}

	for _, rel := range []string{"docs/GLOSSARY.md", "docs/sprint/GLOSSARY.md"} {
		data, readErr := os.ReadFile(filepath.Join("../..", rel))
		require.NoError(t, readErr, "reading %s: %v", rel, readErr)
		// An entry may wrap; join each bullet to one line.
		var entries []string
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, "- "):
				entries = append(entries, line)
			case strings.HasPrefix(line, "  ") && len(entries) > 0:
				entries[len(entries)-1] += " " + strings.TrimSpace(line)
			}
		}
		require.GreaterOrEqual(t, len(entries), 15, "%s has %d entries, want at least 15", rel, len(entries))
		prev := ""
		for _, e := range entries {
			if !assert.Regexp(t, glossaryEntryRe, e, "%s: entry is `- **term** — definition.`: %q", rel, e) {
				continue
			}
			name := strings.ToLower(e[4 : strings.Index(e[4:], "**")+4])
			assert.Less(t, prev, name, "%s: entries are in alphabetical order, %q follows %q", rel, name, prev)
			prev = name
			m := regexp.MustCompile(`Defined: \[[^\]]+\]\(([^)#]+)(?:#([^)]+))?\)`).FindStringSubmatch(e)
			if !assert.NotNil(t, m, "%s: %q names no `Defined: [section](link)`", rel, name) {
				continue
			}
			target := filepath.Join("../..", filepath.Dir(rel), m[1])
			doc, docErr := os.ReadFile(target)
			if !assert.NoError(t, docErr, "%s: %q links %s, which does not exist", rel, name, m[1]) || m[2] == "" {
				continue
			}
			slugs := map[string]bool{}
			for _, l := range strings.Split(string(doc), "\n") {
				if strings.HasPrefix(l, "#") {
					slugs[githubSlug(strings.TrimLeft(l, "# "))] = true
				}
			}
			assert.True(t, slugs[m[2]], "%s: %q links %s#%s, which is no heading there", rel, name, m[1], m[2])
		}
	}
}
