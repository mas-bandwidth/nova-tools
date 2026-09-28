package ci

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

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
// Token list derivation (curated inventory derived from fleet configuration evidence):
// 1. Machines / hosts / tailnet nodes:
//    - space, hetzner, hulk, vision, batman, superman, studio, mini, captainamerica, antman, macbook
// 2. Friends / persons (AGENTS.md, message-bus sender lanes):
//    - alex, emma, freddy, glenn, johnny, rowan, stella
// 3. GitHub accounts & organizations:
//    - mas-bandwidth, spacegame
//
// Boundary controls & exclusions:
// - Word boundary checks: substring occurrences inside legitimate English words
//   do not match (e.g. miniredis, deterministic, revision, minimum, studios, whitespace,
//   namespace, workspace).
// - Token matching scans without consuming delimiter/boundary characters so adjacent
//   occurrences (e.g. "mas-bandwidth mas-bandwidth" or "mas-bandwidth/mas-bandwidth")
//   and repeated names on one line are individually counted.
// - Compound standard library names (e.g. TrimSpace, TrimLeadingSpace, IsSpace) are excluded.
// - Go package import declarations (including "github.com/mas-bandwidth/nova-tools/...")
//   are identified via real AST import specs and excluded as language-level imports.
// - Explicitly marked documentation examples in real AST comments (e.g. lines with "e.g.",
//   "example:", "for example", "example.com") are excluded; code contracts, defaults,
//   and refusals are always scanned.
// - Raw string literals (e.g. `...`) are executable code data and are ALWAYS scanned,
//   even when their content contains example labels or import blocks.
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

// forbiddenTokens is the curated inventory of friend/person names, hostnames,
// tailnet nodes, and GitHub accounts derived from fleet configuration evidence.
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

func isBoundaryBefore(text string, start int) bool {
	if start <= 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:start])
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
		return false
	}
	return true
}

func isBoundaryAfter(text string, end int) bool {
	if end >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[end:])
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
		return false
	}
	return true
}

// cleanSourceForGenerality returns a copy of src where real AST import specs and
// real AST comments with marked documentation examples have been replaced with spaces.
// Newline characters are preserved to ensure 1-based line numbers remain exact.
// Raw string literals and other executable code are never blanked out.
func cleanSourceForGenerality(rel string, src []byte) []byte {
	clean := make([]byte, len(src))
	copy(clean, src)

	parseSrc := src
	offsetShift := 0
	if !strings.Contains(string(src), "package ") {
		parseSrc = append([]byte("package fixture\n"), src...)
		offsetShift = len("package fixture\n")
	}

	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, rel, parseSrc, parser.ParseComments)
	if file == nil {
		return clean
	}

	// 1. Blank out real AST import specs
	for _, imp := range file.Imports {
		start := fset.Position(imp.Pos()).Offset - offsetShift
		end := fset.Position(imp.End()).Offset - offsetShift
		if start < 0 {
			start = 0
		}
		if end > len(clean) {
			end = len(clean)
		}
		if start < end {
			for i := start; i < end; i++ {
				if clean[i] != '\n' {
					clean[i] = ' '
				}
			}
		}
	}

	// 2. Blank out real AST comments containing marked documentation examples
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if isMarkedDocExample(c.Text) {
				start := fset.Position(c.Pos()).Offset - offsetShift
				end := fset.Position(c.End()).Offset - offsetShift
				if start < 0 {
					start = 0
				}
				if end > len(clean) {
					end = len(clean)
				}
				if start < end {
					for i := start; i < end; i++ {
						if clean[i] != '\n' {
							clean[i] = ' '
						}
					}
				}
			}
		}
	}

	return clean
}

// extractTokensFromText returns all forbidden tokens found in a text fragment.
// Boundaries are scanned without consuming boundary characters so adjacent occurrences
// (e.g. "mas-bandwidth mas-bandwidth" or "mas-bandwidth/mas-bandwidth") and repeated
// names on one line are individually counted.
func extractTokensFromText(text string) []string {
	var tokens []string

	// 1. Check for hyphenated account name: mas-bandwidth without consuming boundary delimiters
	target := "mas-bandwidth"
	targetLen := len(target)
	lower := strings.ToLower(text)
	textBuf := []byte(text)

	pos := 0
	for pos < len(lower) {
		idx := strings.Index(lower[pos:], target)
		if idx < 0 {
			break
		}
		start := pos + idx
		end := start + targetLen

		if isBoundaryBefore(text, start) && isBoundaryAfter(text, end) {
			tokens = append(tokens, target)
			for i := start; i < end; i++ {
				textBuf[i] = ' '
			}
		}
		pos = end
	}
	text = string(textBuf)

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
	cleaned := cleanSourceForGenerality("", []byte(line))
	tokens := extractTokensFromText(string(cleaned))
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

// measureGeneralityCounts scans files and returns per-key occurrence counts and keys.
func measureGeneralityCounts(files []GeneralitySourceFile) (map[string]int, map[string]bool, []string) {
	measuredCounts := make(map[string]int)
	measuredKeys := make(map[string]bool)
	var rawViolations []string

	for _, f := range files {
		rel := f.Rel
		cleanSrc := cleanSourceForGenerality(rel, f.Src)
		lines := strings.Split(string(cleanSrc), "\n")
		for lineNum, line := range lines {
			tokens := extractTokensFromText(line)
			for _, tok := range tokens {
				key := rel + ":" + tok
				measuredKeys[key] = true
				measuredCounts[key]++
				rawViolations = append(rawViolations, fmt.Sprintf("%s:%d:%s", rel, lineNum+1, tok))
			}
		}
	}
	return measuredCounts, measuredKeys, rawViolations
}

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
		cleanSrc := cleanSourceForGenerality(rel, f.Src)
		lines := strings.Split(string(cleanSrc), "\n")
		for lineNum, line := range lines {
			tokens := extractTokensFromText(line)
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

func writeGeneralityAllowlist(path string, measuredCounts map[string]int) error {
	var keys []string
	for k := range measuredCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(`# generality_allowlist.txt -- existing occurrences of friend/person names,
# hostnames, and fleet identifiers in living Go files (Rule 1: generality guardrail).
#
# "everything must be general. Not tied to the specifics of our fleet, or friends,
# just the general concepts behind. No host, machine, tailnet name, friend or person
# name in code, contracts, defaults or refusals; the concepts (machine, bench, coordinator,
# friend, seat, store, route, pool, card, stream, repo, issue, entry) are what the code
# knows; our fleet is one nova-config configuration; our names only in our config, receipts
# and docs examples marked as examples."
#
# Format: path/to/file.go:token count
#
# The list only shrinks. Checked in both directions with per-key counts:
# - An entry no longer present on the tree is stale and fails the test.
# - Any new occurrence not listed fails the test.
# - Adding a second occurrence to an already-allowed file exceeds the ceiling and fails.
# - A count lower than the allowed count requires shrinking the allowlist entry.
#
`)
	sb.WriteString(fmt.Sprintf("# ceiling: %d\n", len(keys)))
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("%s %d\n", k, measuredCounts[k]))
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// TestGeneralityGuardrail holds living Go code in cmd/ and internal/ to Glenn's
// generality instruction (Rule 1): no hostnames or friend/person names in code,
// contracts, defaults or refusals.
func TestGeneralityGuardrail(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)

	var files []GeneralitySourceFile
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		if f.HasDirNamed("testdata") || f.HasDirNamed("vendor") || f.HasDirNamed("deprecated") {
			continue
		}
		files = append(files, GeneralitySourceFile{Rel: f.Rel, Src: f.Src})
	}

	if allowlist.Updating() {
		measuredCounts, _, _ := measureGeneralityCounts(files)
		allowPath := filepath.Join(tree.Root, "internal/ci", generalityAllowlistPath)
		if err := writeGeneralityAllowlist(allowPath, measuredCounts); err != nil {
			t.Fatalf("failed to rewrite generality allowlist: %v", err)
		}
		t.Fatal(allowlist.UpdatedRerun)
	}

	allow := loadAllowlist(t, generalityAllowlistPath, shrinkOnly)
	violations := checkGenerality(files, allow)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestGeneralityTokenExtraction verifies that the extraction logic matches
// forbidden tokens at boundaries, counts without consuming boundary delimiters,
// and ignores unrelated words containing substrings.
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
		// Adjacent spaces and slashes, repeated occurrences on one line
		{"const owner = \"mas-bandwidth mas-bandwidth\"", []string{"mas-bandwidth", "mas-bandwidth"}},
		{"const owner = \"mas-bandwidth/mas-bandwidth\"", []string{"mas-bandwidth", "mas-bandwidth"}},
		{"mas-bandwidth  mas-bandwidth", []string{"mas-bandwidth", "mas-bandwidth"}},
		{"mas-bandwidth/mas-bandwidth/mas-bandwidth", []string{"mas-bandwidth", "mas-bandwidth", "mas-bandwidth"}},
		{"batman/superman/hulk", []string{"batman", "hulk", "superman"}},
		{"glenn  glenn", []string{"glenn", "glenn"}},
		{"glenn/glenn", []string{"glenn", "glenn"}},
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
// occurrence of an allowed token to an already-allowed file fails the check,
// adjacent occurrences sharing delimiters undercount without boundary preservation,
// and raw string literals containing comments or imports are executable code data
// and are never exempted.
func TestGeneralityOccurrenceWitness(t *testing.T) {
	t.Parallel()

	t.Run("second-occurrence-fails", func(t *testing.T) {
		allowContent := "# Format: path/to/file.go:token count\n# ceiling: 1\ntest/file.go:glenn 1\n"
		allow, err := allowlist.Parse("testdata/generality_allowlist.txt", allowContent, allowlist.Options{Ceiling: true})
		if err != nil {
			t.Fatal(err)
		}

		// 1 occurrence: must pass with zero violations.
		oneOccurrence := []GeneralitySourceFile{
			{Rel: "test/file.go", Src: []byte("package test\nfunc foo() {\n\tmsg := \"hello glenn\"\n}\n")},
		}
		violationsOne := checkGenerality(oneOccurrence, allow)
		if len(violationsOne) != 0 {
			t.Fatalf("expected 1 occurrence to pass, got violations: %v", violationsOne)
		}

		// 2 occurrences: adding a second occurrence to the allowed file must fail!
		twoOccurrences := []GeneralitySourceFile{
			{Rel: "test/file.go", Src: []byte("package test\nfunc foo() {\n\tmsg := \"hello glenn\"\n\tsecond := \"glenn\"\n}\n")},
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
	})

	t.Run("adjacent-account-count", func(t *testing.T) {
		a, err := allowlist.Parse("fixture", "# ceiling: 1\nfixture.go:mas-bandwidth 1\n", allowlist.Options{Ceiling: true})
		if err != nil {
			t.Fatal(err)
		}
		one := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\nconst owner = \"mas-bandwidth\"\n")}}
		if v := checkGenerality(one, a); len(v) != 0 {
			t.Fatalf("control: %v", v)
		}
		two := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\nconst owner = \"mas-bandwidth mas-bandwidth\"\n")}}
		v := checkGenerality(two, a)
		t.Logf("two occurrences: %v", v)
		if len(v) == 0 {
			t.Error("second account occurrence escaped existing ceiling")
		}
	})

	for _, tc := range []struct {
		name, src string
	}{
		{"raw-string-comment", "package fixture\nconst refusal = `\n// example: hulk\n`\n"},
		{"raw-string-import", "package fixture\nconst refusal = `\nimport (\nhulk\n)\n`\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := allowlist.Parse("fixture", "# ceiling: 0\n", allowlist.Options{Ceiling: true})
			if err != nil {
				t.Fatal(err)
			}
			v := checkGenerality([]GeneralitySourceFile{{Rel: "fixture.go", Src: []byte(tc.src)}}, a)
			t.Logf("runtime string violations: %v", v)
			if len(v) == 0 {
				t.Error("runtime string mistaken for exempt comment or import")
			}
		})
	}

	t.Run("legitimate-controls", func(t *testing.T) {
		a, err := allowlist.Parse("fixture", "# ceiling: 0\n", allowlist.Options{Ceiling: true})
		if err != nil {
			t.Fatal(err)
		}
		// Real AST comment with marked example must be exempt
		commentPass := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\n// bench name (e.g. \"hulk\", \"space\")\nfunc foo() {}\n")}}
		if v := checkGenerality(commentPass, a); len(v) != 0 {
			t.Errorf("legitimate marked comment failed: %v", v)
		}

		// Real AST import must be exempt
		importPass := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\nimport (\n\t\"github.com/mas-bandwidth/nova-tools/internal/ci\"\n)\n")}}
		if v := checkGenerality(importPass, a); len(v) != 0 {
			t.Errorf("legitimate import failed: %v", v)
		}

		// Unmarked AST comment must NOT be exempt
		commentFail := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\n// glenn was here\nfunc foo() {}\n")}}
		if v := checkGenerality(commentFail, a); len(v) == 0 {
			t.Error("unmarked AST comment with forbidden token was unexpectedly exempted")
		}
	})
}
