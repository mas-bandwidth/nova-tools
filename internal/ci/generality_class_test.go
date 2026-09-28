package ci

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// generality_class_test.go is Glenn's standing instruction (note rowan-524f2007dbb4):
// "everything must be general. Not tied to the specifics of our fleet, or friends,
// just the general concepts behind. No host, machine, tailnet name, friend or person
// name in code, contracts, defaults or refusals; the concepts (machine, bench, coordinator,
// friend, seat, store, route, pool, card, stream, repo, issue, entry) are what the code
// knows; our fleet is one nova-config configuration; our names only in our config,
// receipts and docs examples marked as examples. Emma: a class test that greps the living
// tree for our hosts and names, the same shape as the parked-name grep, is a small PR
// for the queue."
//
// The rule sweeps cmd/ and internal/ (only living packages, skipping deprecated/, vendor/,
// testdata/, and _test.go files) for references to friend/person names:
// "rowan", "stella", "johnny", "freddy", "emma", "glenn"
// and hostnames:
// "batman", "superman", "hulk", "vision", "mini", "studio"
//
// Existing occurrences are held to internal/ci/testdata/generality_allowlist.txt,
// which is shrink-only in both directions: any new occurrence fails the test, and any
// listed occurrence that leaves the tree is stale and also fails the test.

const generalityAllowlistPath = "testdata/generality_allowlist.txt"

// forbiddenTokens is the set of friend/person names and hostnames to guard against.
var forbiddenTokens = map[string]bool{
	"batman":   true,
	"emma":     true,
	"freddy":   true,
	"glenn":    true,
	"hulk":     true,
	"johnny":   true,
	"mini":     true,
	"rowan":    true,
	"stella":   true,
	"studio":   true,
	"superman": true,
	"vision":   true,
}

// extractGeneralityTokens returns all forbidden tokens found in line.
// It matches words on word boundaries, non-alphanumeric separators (like underscores),
// and camelCase boundaries, in a case-insensitive manner.
func extractGeneralityTokens(line string) []string {
	var tokens []string
	var buf []rune
	flush := func() {
		if len(buf) > 0 {
			word := strings.ToLower(string(buf))
			if forbiddenTokens[word] {
				tokens = append(tokens, word)
			}
			buf = buf[:0]
		}
	}
	var prev rune
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if unicode.IsUpper(r) && unicode.IsLower(prev) {
				flush()
			}
			buf = append(buf, r)
		} else {
			flush()
		}
		prev = r
	}
	flush()
	return tokens
}

// TestGeneralityGuardrail holds living Go code in cmd/ and internal/ to Glenn's
// generality instruction (Rule 1): no hostnames or friend/person names in code,
// contracts, defaults or refusals.
func TestGeneralityGuardrail(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := loadAllowlist(t, generalityAllowlistPath, shrinkOnly)
	seen := map[string]bool{}
	var violations []string

	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") || f.HasDirNamed("vendor") || f.HasDirNamed("deprecated") {
			continue
		}
		rel := f.Rel
		lines := strings.Split(string(f.Src), "\n")
		for lineNum, line := range lines {
			tokens := extractGeneralityTokens(line)
			for _, tok := range tokens {
				key := rel + ":" + tok
				seen[key] = true
				if !allow.Has(key) {
					violations = append(violations, fmt.Sprintf(
						"%s:%d: forbidden reference to %q (Rule 1: generality guardrail; see rowan-524f2007dbb4); no host, machine, tailnet, friend or person name in living code",
						rel, lineNum+1, tok))
				}
			}
		}
	}

	for _, row := range allowlist.Check(t, allow, seen).Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but no reference is in the living tree any more; delete the stale entry (the list only shrinks)",
			generalityAllowlistPath, row.Key))
	}

	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestGeneralityTokenExtraction verifies that the extraction logic matches
// forbidden tokens at boundaries and ignores unrelated words containing substrings.
func TestGeneralityTokenExtraction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  []string
	}{
		{"glenn,rowan", []string{"glenn", "rowan"}},
		{"RowanPick", []string{"rowan"}},
		{"rowan_pick", []string{"rowan"}},
		{"HelpAskGlenn", []string{"glenn"}},
		{"studio:cards", []string{"studio"}},
		{"bench=batman", []string{"batman"}},
		{"revision", nil},
		{"deterministic", nil},
		{"minimum", nil},
		{"miniredis", nil},
		{"studios", nil},
	}

	for _, tc := range cases {
		got := extractGeneralityTokens(tc.input)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("extractGeneralityTokens(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
