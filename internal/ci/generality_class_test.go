package ci

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// generality_class_test.go enforces Glenn's standing instruction (Rule 1):
// "everything must be general. Not tied to the specifics of our fleet, or friends,
// just the general concepts behind. No host, machine, tailnet name, friend or person
// name in code, contracts, defaults or refusals; the concepts (machine, bench, coordinator,
// friend, seat, store, route, pool, card, stream, repo, issue, entry) are what the code
// knows; our fleet is one nova-config configuration; our names only in our config,
// receipts and docs examples marked as examples." (see docs/SPEC-CI.md#generality).
//
// Token list derivation (derived from fleet inventory, not from memory):
// 1. Machines / hosts / tailnet nodes:
//    - fleet/machines.tsv & fleet/inventory.py:
//        space, hetzner, hulk, vision, batman, superman, studio, mini, captainamerica
//    - ~/.ssh/config Host lines:
//        space, batman, superman, hulk, vision, captainamerica, antman, hetzner, macbook
// 2. Friends / persons (AGENTS.md, message-bus sender lanes):
//    - alex, emma, freddy, glenn, johnny, rowan, stella
// 3. GitHub accounts & organizations:
//    - mas-bandwidth, spacegame
//
// Boundary controls & exclusions:
// - Word boundary checks: substring occurrences inside legitimate English words
//   do not match (e.g. miniredis, deterministic, revision, minimum, studios, whitespace,
//   namespace, workspace).
// - Compound standard library names (e.g. TrimSpace, TrimLeadingSpace, IsSpace) are excluded.
// - Go package import declarations (including "github.com/mas-bandwidth/nova-tools/...")
//   are excluded as language-level imports.
// - Explicitly marked documentation examples in comments (e.g. lines with "e.g.",
//   "example:", "for example", "example.com") are excluded; code contracts, defaults,
//   and refusals are always scanned.
//
// Existing occurrences are tracked in internal/ci/testdata/generality_allowlist.txt,
// formatted as "<path/to/file.go>:<token> <count>".
// The allowlist only shrinks (shrink-only ceiling):
// - An unlisted occurrence or file:token fails the test.
// - An allowed file:token that increases its count fails the test.
// - A count lower than the listed ceiling fails the test, requiring the allowlist to shrink.
// - A stale entry with 0 occurrences fails the test, requiring row deletion.
// - Allowlist entries must remain sorted alphabetically.

const generalityAllowlistPath = "testdata/generality_allowlist.txt"

// forbiddenTokens is the set of friend/person names, hostnames, tailnet nodes,
// and GitHub accounts derived from the fleet inventory.
var forbiddenTokens = map[string]bool{
	"alex":           true,
	"antman":         true,
	"batman":         true,
	"captainamerica": true,
	"emma":           true,
	"freddy":         true,
	"glenn":          true,
	"hetzner":        true,
	"hulk":           true,
	"johnny":         true,
	"macbook":        true,
	"mas-bandwidth":  true,
	"mini":           true,
	"rowan":          true,
	"space":          true,
	"spacegame":      true,
	"stella":         true,
	"studio":         true,
	"superman":       true,
	"vision":         true,
}

// ignoredCompoundWords contains standard Go library identifiers whose camelCase
// components should not be matched as forbidden tokens.
var ignoredCompoundWords = map[string]bool{
	"trimspace":        true,
	"trimleadingspace": true,
	"isspace":          true,
}

// reMasBandwidth matches mas-bandwidth when not preceded or followed by an alphanumeric character, hyphen, or underscore.
var reMasBandwidth = regexp.MustCompile(`(?i)(?:^|[^a-zA-Z0-9_-])(mas-bandwidth)(?:$|[^a-zA-Z0-9_-])`)
var reWord = regexp.MustCompile(`[a-zA-Z0-9]+`)

// isMarkedDocExample reports whether a comment line is an explicit documentation example.
func isMarkedDocExample(comment string) bool {
	lower := strings.ToLower(comment)
	return strings.Contains(lower, "e.g.") ||
		strings.Contains(lower, "example:") ||
		strings.Contains(lower, "for example") ||
		strings.Contains(lower, "example.com") ||
		strings.Contains(lower, "(example") ||
		strings.Contains(lower, "as an example")
}

// splitCodeAndComment splits a line into code and comment portions, respecting string literals.
func splitCodeAndComment(line string) (string, string) {
	inQuote := rune(0)
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inQuote != 0 {
			if r == inQuote && (i == 0 || runes[i-1] != '\\') {
				inQuote = 0
			}
			continue
		}
		if r == '"' || r == '`' || r == '\'' {
			inQuote = r
			continue
		}
		if r == '/' && i+1 < len(runes) && runes[i+1] == '/' {
			return string(runes[:i]), string(runes[i+2:])
		}
	}
	return line, ""
}

// extractTokensFromText returns all forbidden tokens found in a text fragment.
func extractTokensFromText(text string) []string {
	var tokens []string

	// 1. Check for hyphenated account name: mas-bandwidth
	if strings.Contains(strings.ToLower(text), "mas-bandwidth") {
		matches := reMasBandwidth.FindAllStringSubmatchIndex(text, -1)
		for _, m := range matches {
			if len(m) >= 4 && m[2] >= 0 && m[3] >= 0 {
				tokens = append(tokens, "mas-bandwidth")
			}
		}
		text = reMasBandwidth.ReplaceAllStringFunc(text, func(s string) string {
			return strings.Repeat(" ", len(s))
		})
	}

	// 2. Scan individual words and handle camelCase transitions
	words := reWord.FindAllString(text, -1)
	for _, w := range words {
		low := strings.ToLower(w)
		if ignoredCompoundWords[low] {
			continue
		}
		if forbiddenTokens[low] {
			tokens = append(tokens, low)
			continue
		}

		// Split on camelCase boundaries
		var buf []rune
		var prev rune
		flush := func() {
			if len(buf) > 0 {
				chunk := strings.ToLower(string(buf))
				if forbiddenTokens[chunk] {
					tokens = append(tokens, chunk)
				}
				buf = buf[:0]
			}
		}
		for _, r := range w {
			if unicode.IsUpper(r) && unicode.IsLower(prev) {
				flush()
			}
			buf = append(buf, r)
			prev = r
		}
		flush()
	}

	return tokens
}

// extractGeneralityTokens returns all forbidden tokens found in line.
// Marked documentation examples in comments are excluded.
func extractGeneralityTokens(line string) []string {
	code, comment := splitCodeAndComment(line)
	var tokens []string
	tokens = append(tokens, extractTokensFromText(code)...)
	if comment != "" && !isMarkedDocExample(comment) {
		tokens = append(tokens, extractTokensFromText(comment)...)
	}
	sort.Strings(tokens)
	return tokens
}

// GeneralitySourceFile represents a Go source file to scan for generality violations.
type GeneralitySourceFile struct {
	Rel string
	Src []byte
}

type allowlistDiscardReporter struct{}

func (allowlistDiscardReporter) Helper()                           {}
func (allowlistDiscardReporter) Errorf(format string, args ...any) {}

// checkGenerality scans the given source files against the allowlist and returns all violations.
func checkGenerality(files []GeneralitySourceFile, allow *allowlist.List) []string {
	var violations []string

	// Parse allowed per-key counts from allowlist rows.
	allowedCounts := make(map[string]int)
	rows := allow.Rows()
	for i, row := range rows {
		fields := strings.Fields(row.Text)
		if len(fields) < 2 {
			violations = append(violations, fmt.Sprintf("%s:%d: malformed row %q: expected <file:token> <count>", allow.Path, row.Line, row.Text))
			continue
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil || count <= 0 {
			violations = append(violations, fmt.Sprintf("%s:%d: invalid count in row %q: expected positive integer", allow.Path, row.Line, row.Text))
			continue
		}
		allowedCounts[fields[0]] = count

		// Enforce that allowlist rows are sorted alphabetically by key.
		if i > 0 && rows[i].Key < rows[i-1].Key {
			violations = append(violations, fmt.Sprintf("%s:%d: allowlist not sorted: %q should appear before %q", allow.Path, row.Line, rows[i].Key, rows[i-1].Key))
		}
	}

	measuredCounts := make(map[string]int)
	measuredKeys := make(map[string]bool)

	for _, f := range files {
		rel := f.Rel
		lines := strings.Split(string(f.Src), "\n")
		inImport := false
		for lineNum, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import (") {
				inImport = true
				continue
			}
			if inImport {
				if trimmed == ")" || strings.HasPrefix(trimmed, ")") {
					inImport = false
				}
				continue
			}
			if strings.HasPrefix(trimmed, "import ") {
				continue
			}

			tokens := extractGeneralityTokens(line)
			for _, tok := range tokens {
				key := rel + ":" + tok
				measuredKeys[key] = true
				measuredCounts[key]++

				if !allow.Has(key) {
					violations = append(violations, fmt.Sprintf(
						"%s:%d: forbidden reference to %q (Rule 1: generality guardrail; see docs/SPEC-CI.md#generality); no host, machine, tailnet, friend or person name in living code",
						rel, lineNum+1, tok))
				}
			}
		}
	}

	// Per-key count checks: shrink-only ceiling per file:token key.
	for key, allowed := range allowedCounts {
		actual := measuredCounts[key]
		colon := strings.LastIndex(key, ":")
		tok := key
		rel := key
		if colon >= 0 {
			rel = key[:colon]
			tok = key[colon+1:]
		}

		if actual > allowed {
			violations = append(violations, fmt.Sprintf(
				"%s: %d occurrences of forbidden token %q exceeds allowed count %d (Rule 1: generality guardrail; see docs/SPEC-CI.md#generality)",
				rel, actual, tok, allowed))
		} else if actual < allowed && actual > 0 {
			violations = append(violations, fmt.Sprintf(
				"%s: %d occurrences of forbidden token %q is below allowed count %d; shrink the count in %s (Rule 1: generality guardrail; the list only shrinks)",
				rel, actual, tok, allowed, allow.Path))
		}
	}

	// Check for stale rows (keys in allowlist with 0 occurrences in living code).
	checkRes := allowlist.CheckMode(allowlistDiscardReporter{}, allow, measuredKeys, false)
	for _, row := range checkRes.Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but no reference is in the living tree any more; delete the stale entry (the list only shrinks)",
			allow.Path, row.Key))
	}

	// Enforce row count ceiling.
	if n, ok := allow.Ceiling(); ok && len(allow.Rows()) > n {
		violations = append(violations, fmt.Sprintf("%s has %d rows, over its ceiling of %d; the list only shrinks", allow.Path, len(allow.Rows()), n))
	}

	// Enforce total occurrence ceiling.
	totalMeasured := 0
	for _, c := range measuredCounts {
		totalMeasured += c
	}
	totalAllowed := 0
	for _, c := range allowedCounts {
		totalAllowed += c
	}
	if totalMeasured > totalAllowed {
		violations = append(violations, fmt.Sprintf(
			"total occurrences of forbidden tokens (%d) exceeds allowlist ceiling (%d); the list only shrinks",
			totalMeasured, totalAllowed))
	}

	sort.Strings(violations)
	return violations
}

// TestGeneralityGuardrail holds living Go code in cmd/ and internal/ to Glenn's
// generality instruction (Rule 1): no hostnames or friend/person names in code,
// contracts, defaults or refusals.
func TestGeneralityGuardrail(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := loadAllowlist(t, generalityAllowlistPath, shrinkOnly)

	var files []GeneralitySourceFile
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") || f.HasDirNamed("vendor") || f.HasDirNamed("deprecated") {
			continue
		}
		files = append(files, GeneralitySourceFile{Rel: f.Rel, Src: f.Src})
	}

	violations := checkGenerality(files, allow)
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
		{"TrimSpace", nil},
		{"unicode.IsSpace", nil},
		{"whitespace", nil},
		{"namespace", nil},
		{"workspace", nil},
		{"bench=space", []string{"space"}},
		{"mas-bandwidth/nova-tools", []string{"mas-bandwidth"}},
		{"rowan@mas-bandwidth.com", []string{"mas-bandwidth", "rowan"}},
		{"// bench name (e.g. \"hulk\", \"space\")", nil},
		{"return fmt.Errorf(\"for example --seat studio\")", []string{"studio"}},
	}

	for _, tc := range cases {
		got := extractGeneralityTokens(tc.input)
		sort.Strings(got)
		sort.Strings(tc.want)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("extractGeneralityTokens(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestGeneralityOccurrenceWitness proves the reversed witness: adding a second
// occurrence of an allowed token to an already-allowed file fails the check.
func TestGeneralityOccurrenceWitness(t *testing.T) {
	t.Parallel()

	allowContent := "# Format: path/to/file.go:token count\n# ceiling: 1\ntest/file.go:glenn 1\n"
	allow, err := allowlist.Parse("testdata/generality_allowlist.txt", allowContent, allowlist.Options{Ceiling: true})
	if err != nil {
		t.Fatal(err)
	}

	// 1 occurrence: must pass with zero violations.
	oneOccurrence := []GeneralitySourceFile{
		{Rel: "test/file.go", Src: []byte("func foo() {\n\tmsg := \"hello glenn\"\n}\n")},
	}
	violationsOne := checkGenerality(oneOccurrence, allow)
	if len(violationsOne) != 0 {
		t.Fatalf("expected 1 occurrence to pass, got violations: %v", violationsOne)
	}

	// 2 occurrences: adding a second occurrence to the allowed file must fail!
	twoOccurrences := []GeneralitySourceFile{
		{Rel: "test/file.go", Src: []byte("func foo() {\n\tmsg := \"hello glenn\"\n\tsecond := \"glenn\"\n}\n")},
	}
	violationsTwo := checkGenerality(twoOccurrences, allow)
	if len(violationsTwo) == 0 {
		t.Fatalf("expected adding a second occurrence of an allowed token to fail, but it passed!")
	}
	matched := false
	for _, v := range violationsTwo {
		if strings.Contains(v, "exceeds allowed count 1") {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("expected violation to mention 'exceeds allowed count 1', got: %v", violationsTwo)
	}
}
