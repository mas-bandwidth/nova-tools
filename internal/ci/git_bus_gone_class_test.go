package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// git_bus_gone_class_test.go holds one rule of the live tree (docs/SPEC-BUS.md):
// nova-bus is the Redis message bus. Spellings of the bus it replaced stay in
// dated records only: RESOLUTIONS.md, CHANGELOG.md, docs/RELEASE-NOTES-*,
// docs/ratings/, the ledgers and fixtures under testdata/, and the terminology
// lint that reads them. A glossary names what a word replaced after `Replaces:`,
// the one place a retired word may stand (docs/TERMINOLOGY.md), so that clause
// is not a mention. Any other live doc, workflow or comment that uses one
// fails this test.

// removedBusHeading is the card-id heading a shared doc carries. That line is
// the marker, not a sentence that names the removed bus.
var removedBusHeading = "### simp-" + "git" + "-bus-remnants-wb-bb.w6"

// removedBusGlossaries may name a retired word after `Replaces:`, as
// internal/docs/terminology_lint_test.go's scanText holds.
var removedBusGlossaries = map[string]bool{
	"docs/GLOSSARY.md": true, "docs/sprint/GLOSSARY.md": true, "docs/TERMINOLOGY.md": true,
}

func TestTheGitBusIsNamedOnlyInRecords(t *testing.T) {
	t.Parallel()

	spaced, hyphen, closed := removedBusSpellings()
	heading := removedBusHeading
	assert.NotEmpty(t, removedBusLines("docs/x.md", "see "+spaced+" here", spaced, hyphen, closed))
	assert.NotEmpty(t, removedBusLines("docs/x.md", "see "+hyphen+" here", spaced, hyphen, closed))
	assert.NotEmpty(t, removedBusLines("docs/x.md", "see "+closed+" here", spaced, hyphen, closed))
	assert.NotEmpty(t, removedBusLines("docs/x.md", heading+" names it", spaced, hyphen, closed))
	assert.Empty(t, removedBusLines("docs/x.md", heading, spaced, hyphen, closed))
	assert.Empty(t, removedBusLines("docs/x.md", "nova-bus keeps its key prefix", spaced, hyphen, closed))
	assert.Empty(t, removedBusLines("docs/x.md", "TestTheGitBusIsNamedOnlyInRecords", spaced, hyphen, closed))
	assert.Empty(t, removedBusLines("docs/GLOSSARY.md", "- **bus** — the bus. Replaces: "+spaced+".", spaced, hyphen, closed))
	assert.NotEmpty(t, removedBusLines("docs/SPEC-BUS.md", "Replaces: "+spaced, spaced, hyphen, closed))

	for _, rel := range []string{
		"RESOLUTIONS.md",
		"CHANGELOG.md",
		"docs/RELEASE-NOTES-1.0.0.md",
		"docs/ratings/1.1.0/x.md",
		"internal/ci/testdata/deleted-tests.txt",
		"internal/ci/testdata/namedpaths_allowlist.txt",
		"internal/docs/terminology_lint_test.go",
		"internal/docs/testdata/retired-words.txt",
	} {
		assert.True(t, removedBusMentionIsRecord(rel), rel)
	}
	for _, rel := range []string{
		"docs/CLI.md",
		"docs/SPEC-UPDATE.md",
		"docs/SPEC-BUS.md",
		".github/workflows/ci.yml",
		"internal/ci/hosted_shards_class_test.go",
		"internal/ci/git_bus_gone_class_test.go",
		"docs/GLOSSARY.md",
	} {
		assert.False(t, removedBusMentionIsRecord(rel), rel)
	}

	root := repoRoot(t)
	var live []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if removedBusMentionIsRecord(rel) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, hit := range removedBusLines(rel, string(body), spaced, hyphen, closed) {
			live = append(live, rel+":"+hit)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, live)
}

func removedBusSpellings() (spaced, hyphen, closed string) {
	return "git" + " bus", "git" + "-bus", "git" + "bus"
}

// removedBusMentionIsRecord reports whether rel is a dated record: the
// changelog, the resolutions, the release notes, the ratings, the ledgers and
// fixtures under testdata, and the terminology lint that reads them. It mirrors
// internal/docs/terminology_lint_test.go's isDatedRecord, so a record for that
// lint's rule is a record for this one.
func removedBusMentionIsRecord(rel string) bool {
	rel = filepath.ToSlash(rel)
	switch rel {
	case "RESOLUTIONS.md", "CHANGELOG.md",
		"internal/ci/testdata/deleted-tests.txt",
		"internal/ci/testdata/namedpaths_allowlist.txt",
		"internal/docs/terminology_lint_test.go":
		return true
	}
	if strings.HasPrefix(rel, "docs/RELEASE-NOTES-") {
		return true
	}
	if rel == "docs/ratings" || strings.HasPrefix(rel, "docs/ratings/") {
		return true
	}
	return strings.HasPrefix(rel, "testdata/") || strings.Contains(rel, "/testdata/")
}

func removedBusLines(rel, body, spaced, hyphen, closed string) []string {
	lines := strings.Split(body, "\n")
	// In a glossary, the text after `Replaces:` on a line is not read; it is
	// the one place a retired word may stand (docs/TERMINOLOGY.md).
	if removedBusGlossaries[rel] {
		for i, line := range lines {
			if cut := strings.Index(line, "Replaces:"); cut >= 0 {
				lines[i] = line[:cut]
			}
		}
	}
	// The shared-doc subsection heading is the card id, not a sentence that
	// names the removed bus. Any other line that uses a spelling, including one
	// that merely contains the id, is a hit.
	var hits []string
	for i, line := range lines {
		if strings.TrimSpace(line) == removedBusHeading {
			continue
		}
		if lineNamesRemovedBus(line, spaced, hyphen, closed) {
			hits = append(hits, fmt.Sprintf("%d:%s", i+1, strings.TrimRight(line, "\r")))
		}
	}
	return hits
}

func lineNamesRemovedBus(line, spaced, hyphen, closed string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, spaced) || strings.Contains(lower, hyphen) {
		return true
	}
	return containsClosedSpelling(lower, closed)
}

// containsClosedSpelling reports the closed spelling as its own word. An
// identifier that merely contains those letters is not that spelling.
func containsClosedSpelling(lower, closed string) bool {
	from := 0
	for {
		i := strings.Index(lower[from:], closed)
		if i < 0 {
			return false
		}
		i += from
		before := i == 0 || !isRemovedBusIdent(lower[i-1])
		end := i + len(closed)
		after := end == len(lower) || !isRemovedBusIdent(lower[end])
		if before && after {
			return true
		}
		from = i + 1
	}
}

func isRemovedBusIdent(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
