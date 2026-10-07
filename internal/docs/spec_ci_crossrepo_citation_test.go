package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spec_ci_crossrepo_citation_test.go holds docs/SPEC-CI.md's pit-stop ledger
// citation to the thing a reader needs from a citation: WHERE the ledger is.
//
// PR #1765 was right that the link there resolved nowhere — there is no
// reports/ directory in nova-tools, so a destination one level up out of docs/
// was broken and had to go. But the target was not missing; it lives in
// ANOTHER repository. The prose that replaced the link kept the item and the
// date and dropped the repository and the path, leaving a citation a reader
// cannot follow, which is a citation to nothing. The fix names the repository
// and the path in words, and is deliberately not a link: a markdown link with
// no target in THIS repository is exactly what #1765 removed, so re-adding one
// would undo the change this test is committed beside.
//
// It reads the document as text and runs nothing.

// specCICrossRepoPath is the cited document, relative to this package.
const specCICrossRepoPath = "../../docs/SPEC-CI.md"

// crossRepoCitation is the literal that finds the paragraph: the ledger item
// whose home moved out of this repository.
const crossRepoCitation = "pit-stop ledger item 20"

// crossRepoLinkRe matches an inline-link destination that carries the report's
// filename: `](`, any run of non-`)`, the filename, any run of non-`)`, `)`.
var crossRepoLinkRe = regexp.MustCompile(`\]\([^)]*pitstop-tests-2026-09-17[^)]*\)`)

// TestTheCrossRepoCitationNamesItsRepository is the citation's contract.
func TestTheCrossRepoCitationNamesItsRepository(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(specCICrossRepoPath)
	require.NoError(t, err, "%s: %v", specCICrossRepoPath, err)

	paragraphs := crossRepoParagraphs(string(data))
	require.Len(t, paragraphs, 1, "%s: found %d paragraphs carrying %q, want exactly one; a second citation would split the contract and this test could not say which one it read",
		specCICrossRepoPath, len(paragraphs), crossRepoCitation)
	paragraph := paragraphs[0]

	assert.Contains(t, paragraph, "rowan-new", "%s: the pit-stop citation does not name the rowan-new repository; the ledger it cites lives there and nowhere in this repository, so a reader cannot follow it — name the repository in the paragraph",
		specCICrossRepoPath)

	assert.Contains(t, paragraph, "reports/pitstop-tests-2026-09-17.md", "%s: the pit-stop citation does not name reports/pitstop-tests-2026-09-17.md; that path is the only thing a reader can follow to the ledger — name it in the paragraph",
		specCICrossRepoPath)

	match := crossRepoLinkRe.FindString(paragraph)
	assert.Empty(t, match, "%s: the pit-stop citation is a link (%q); a markdown link whose destination has no target in THIS repository is what #1765 removed, so the citation must stay prose — do not re-add the link",
		specCICrossRepoPath, match)

	assert.Contains(t, paragraph, "2026-09-17", "%s: the sentence lost what it said; it must still carry the date 2026-09-17 and the words \"a rule lands with its sweep of the tree\"",
		specCICrossRepoPath)
	assert.Contains(t, paragraph, "a rule lands with its sweep of the tree", "%s: the sentence lost what it said; it must still carry the date 2026-09-17 and the words \"a rule lands with its sweep of the tree\"",
		specCICrossRepoPath)
}

// crossRepoParagraphs returns every blank-line-delimited paragraph of content
// that carries crossRepoCitation. A paragraph runs from the blank line before
// it to the blank line after it.
func crossRepoParagraphs(content string) []string {
	var paragraphs []string
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		paragraph := strings.Join(current, "\n")
		if strings.Contains(paragraph, crossRepoCitation) {
			paragraphs = append(paragraphs, paragraph)
		}
		current = nil
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return paragraphs
}
