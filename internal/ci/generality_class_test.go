package ci

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generality_class_test.go enforces Glenn's standing instruction (Rule 1):
// "everything must be general. Not tied to the specifics of our fleet, or friends,
// just the general concepts behind. No host, machine, tailnet name, friend or person
// name in code, contracts, defaults or refusals; the concepts (machine, bench, coordinator,
// friend, seat, store, route, pool, card, stream, repo, issue, entry) are what the code
// knows; our fleet is one nova-config configuration; our names only in our config,
// receipts and docs examples marked as examples." (see docs/SPEC-CI.md#generality).
//
// Token lists are a curated inventory.
// 1. Machines / hosts / tailnet nodes (curated; no source file):
//    - space, hetzner, hulk, vision, batman, superman, studio, mini, captainamerica, antman, macbook
// 2. Friends / persons (AGENTS.md, message-bus sender lanes):
//    - alex, emma, freddy, glenn, johnny, rowan, stella
// 3. GitHub accounts and organizations (curated; no source file):
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
// - Raw string literals (e.g. `...`) are executable code data and are scanned,
//   even when their content contains example labels or import blocks.
//
// Existing occurrences are tracked in internal/ci/testdata/generality/ by
// source directory, with rows "<path/to/file.go>:<token> <count>".
// The allowlist only shrinks (shrink-only ceiling):
// - An unlisted occurrence or file:token fails the test.
// - An allowed file:token that increases its count fails the test.
// - A count lower than the listed ceiling fails the test, requiring the allowlist to shrink.
// - A stale entry with 0 occurrences fails the test, requiring row deletion.
// - Allowlist entries must remain sorted alphabetically.

const generalityAllowlistPath = "testdata/generality"

// forbiddenTokens is the curated inventory of friend/person names, hostnames,
// tailnet nodes, and GitHub accounts.
var forbiddenTokens = map[string]bool{
	"alex": true, "antman": true, "batman": true, "captainamerica": true,
	"emma": true, "freddy": true, "glenn": true, "hetzner": true,
	"hulk": true, "johnny": true, "macbook": true, "mas-bandwidth": true,
	"mini": true, "rowan": true, "space": true, "spacegame": true,
	"stella": true, "studio": true, "superman": true, "vision": true,
}

// ignoredCompoundWords contains standard Go library identifiers whose camelCase
// components should not be matched as forbidden tokens.
var ignoredCompoundWords = map[string]bool{
	"trimspace":        true,
	"trimleadingspace": true,
	"isspace":          true,
}

var reWord = regexp.MustCompile(`[a-zA-Z0-9]+`)
var reAccount = regexp.MustCompile(`(?i)mas-bandwidth`)

var candidateTokens = []string{
	"mas-bandwidth", "alex", "antman", "batman", "captainamerica",
	"emma", "freddy", "glenn", "hetzner", "hulk", "johnny",
	"macbook", "mini", "rowan", "space", "spacegame", "stella",
	"studio", "superman", "vision",
}

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

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

func isBoundaryBefore(text string, start int) bool {
	if start <= 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:start])
	return !isWordRune(r)
}

func isBoundaryAfter(text string, end int) bool {
	if end >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[end:])
	return !isWordRune(r)
}

func fileMayContainGenerality(cleanSrc []byte) bool {
	lower := bytes.ToLower(cleanSrc)
	for _, tok := range candidateTokens {
		if bytes.Contains(lower, []byte(tok)) {
			return true
		}
	}
	return false
}

func lineMayContainGenerality(line string) bool {
	lower := strings.ToLower(line)
	for _, tok := range candidateTokens {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func mayContainDocExample(src []byte) bool {
	lower := bytes.ToLower(src)
	return bytes.Contains(lower, []byte("example")) || bytes.Contains(lower, []byte("e.g."))
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

	hasDocExample := mayContainDocExample(src)
	mode := parser.ImportsOnly
	if hasDocExample {
		mode = parser.ParseComments
	}
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, rel, parseSrc, mode)
	if file == nil {
		return clean
	}

	blank := func(pos, end token.Pos) {
		start := fset.Position(pos).Offset - offsetShift
		stop := fset.Position(end).Offset - offsetShift
		if start < 0 {
			start = 0
		}
		if stop > len(clean) {
			stop = len(clean)
		}
		for i := start; i < stop; i++ {
			if clean[i] != '\n' {
				clean[i] = ' '
			}
		}
	}

	for _, imp := range file.Imports {
		blank(imp.Pos(), imp.End())
	}
	if hasDocExample {
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				if isMarkedDocExample(c.Text) {
					blank(c.Pos(), c.End())
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

	textBuf := []byte(text)
	for _, m := range reAccount.FindAllStringIndex(text, -1) {
		if isBoundaryBefore(text, m[0]) && isBoundaryAfter(text, m[1]) {
			tokens = append(tokens, "mas-bandwidth")
			for i := m[0]; i < m[1]; i++ {
				textBuf[i] = ' '
			}
		}
	}
	text = string(textBuf)

	for _, w := range reWord.FindAllString(text, -1) {
		low := strings.ToLower(w)
		if ignoredCompoundWords[low] {
			continue
		}
		if forbiddenTokens[low] {
			tokens = append(tokens, low)
			continue
		}

		var buf []rune
		var prev rune
		flush := func() {
			if len(buf) > 0 {
				if chunk := strings.ToLower(string(buf)); forbiddenTokens[chunk] {
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
	tokens := extractTokensFromText(string(cleanSourceForGenerality("", []byte(line))))
	sort.Strings(tokens)
	return tokens
}

// GeneralitySourceFile represents a Go source file to scan for generality violations.
type GeneralitySourceFile struct {
	Rel string
	Src []byte
}

func generalityFixtureLedger(t *testing.T, shard, content string) *allowlist.Packages {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, shard)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	ledger, err := allowlist.LoadPackages(dir, allowlist.Options{Ceiling: true, Counted: true})
	require.NoError(t, err)
	return ledger
}

type generalityMessageReporter struct{ messages []string }

func (*generalityMessageReporter) Helper() {}
func (r *generalityMessageReporter) Errorf(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

// measureGeneralityCounts scans files and returns per-key occurrence counts.
func measureGeneralityCounts(files []GeneralitySourceFile) map[string]int {
	measuredCounts := make(map[string]int)
	for _, f := range files {
		cleanSrc := cleanSourceForGenerality(f.Rel, f.Src)
		if !fileMayContainGenerality(cleanSrc) {
			continue
		}
		for _, line := range strings.Split(string(cleanSrc), "\n") {
			if !lineMayContainGenerality(line) {
				continue
			}
			for _, tok := range extractTokensFromText(line) {
				measuredCounts[f.Rel+":"+tok]++
			}
		}
	}
	return measuredCounts
}

// generalityCountedLedger is the counted debt surface shared by a flat fixture
// and the living package-sharded ledger (docs/SPEC-CI.md#generality).
type generalityCountedLedger interface {
	Has(string) bool
	Count(string) int
	Rows() []allowlist.Row
	Ceiling() (int, bool)
}

func generalitySortedViolations(allow generalityCountedLedger) []string {
	var lists []*allowlist.List
	switch ledger := allow.(type) {
	case *allowlist.List:
		lists = []*allowlist.List{ledger}
	case *allowlist.Packages:
		lists = ledger.Lists()
	}
	var violations []string
	for _, list := range lists {
		rows := list.Rows()
		for i := 1; i < len(rows); i++ {
			if rows[i].Key < rows[i-1].Key {
				violations = append(violations, fmt.Sprintf("%s:%d: allowlist not sorted: %q should appear before %q", list.Path, rows[i].Line, rows[i].Key, rows[i-1].Key))
			}
		}
	}
	return violations
}

// checkGenerality scans the given source files against the counted ledger.
func checkGenerality(files []GeneralitySourceFile, allow generalityCountedLedger) []string {
	violations := generalitySortedViolations(allow)
	measuredCounts := measureGeneralityCounts(files)
	allowedCounts := make(map[string]int)
	for _, row := range allow.Rows() {
		allowedCounts[row.Key] = allow.Count(row.Key)
	}

	var ledgerPath string
	var result allowlist.Result
	reporter := &generalityMessageReporter{}
	switch ledger := allow.(type) {
	case *allowlist.List:
		ledgerPath = ledger.Path
		result = allowlist.CheckCountedMode(reporter, ledger, measuredCounts, false)
	case *allowlist.Packages:
		ledgerPath = ledger.Path
		result = allowlist.CheckPackagesCountedMode(reporter, ledger, measuredCounts, false)
	}
	violations = append(violations, reporter.messages...)

	// A new token still names its exact source line, not just the ledger key.
	for _, f := range files {
		cleanSrc := cleanSourceForGenerality(f.Rel, f.Src)
		if !fileMayContainGenerality(cleanSrc) {
			continue
		}
		for lineNum, line := range strings.Split(string(cleanSrc), "\n") {
			if !lineMayContainGenerality(line) {
				continue
			}
			for _, tok := range extractTokensFromText(line) {
				if !allow.Has(f.Rel + ":" + tok) {
					violations = append(violations, fmt.Sprintf(
						"%s:%d: forbidden reference to %q (Rule 1: generality guardrail; see docs/SPEC-CI.md#generality); no host, machine, tailnet, friend or person name in living code",
						f.Rel, lineNum+1, tok))
				}
			}
		}
	}

	for _, c := range result.Over {
		rel, tok, _ := strings.Cut(c.Key, ":")
		violations = append(violations, fmt.Sprintf(
			"%s: %d occurrences of forbidden token %q exceeds allowed count %d (Rule 1: generality guardrail; see docs/SPEC-CI.md#generality)",
			rel, c.Measured, tok, c.Listed))
	}
	for _, c := range result.Lowered {
		rel, tok, _ := strings.Cut(c.Key, ":")
		violations = append(violations, fmt.Sprintf(
			"%s: %d occurrences of forbidden token %q is below allowed count %d; shrink the count in %s (Rule 1: generality guardrail; the list only shrinks)",
			rel, c.Measured, tok, c.Listed, ledgerPath))
	}
	for _, row := range result.Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but no reference is in the living tree any more; delete the stale entry (the list only shrinks)",
			ledgerPath, row.Key))
	}

	// The shared checker holds each shard's row ceiling; the occurrence ceiling
	// remains aggregate across packages.
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

// TestGeneralityGuardrail holds living Go code in cmd/, internal/ and tools/ to Glenn's
// generality instruction (Rule 1): no hostnames or friend/person names in code,
// contracts, defaults or refusals.
func TestGeneralityGuardrail(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)

	var files []GeneralitySourceFile
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal", "tools") {
		if !f.HasDirNamed("testdata") && !f.HasDirNamed("vendor") {
			files = append(files, GeneralitySourceFile{Rel: f.Rel, Src: f.Src})
		}
	}

	allowPath := filepath.Join(tree.Root, "internal/ci", generalityAllowlistPath)
	allow, err := allowlist.LoadPackages(allowPath, allowlist.Options{Ceiling: true, Counted: true})
	require.NoError(t, err)
	if allowlist.Updating() {
		for _, v := range generalitySortedViolations(allow) {
			assert.Fail(t, v)
		}
		if t.Failed() {
			return
		}
		allowlist.CheckPackagesCounted(t, allow, measureGeneralityCounts(files))
		return
	}

	for _, v := range checkGenerality(files, allow) {
		assert.Fail(t, v)
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
		{"const owner = \"mas-bandwidth mas-bandwidth\"", []string{"mas-bandwidth", "mas-bandwidth"}},
		{"const owner = \"mas-bandwidth/mas-bandwidth\"", []string{"mas-bandwidth", "mas-bandwidth"}},
		{"mas-bandwidth  mas-bandwidth", []string{"mas-bandwidth", "mas-bandwidth"}},
		{"mas-bandwidth/mas-bandwidth/mas-bandwidth", []string{"mas-bandwidth", "mas-bandwidth", "mas-bandwidth"}},
		{"batman/superman/hulk", []string{"batman", "hulk", "superman"}},
		{"glenn  glenn", []string{"glenn", "glenn"}},
		{"glenn/glenn", []string{"glenn", "glenn"}},
		{"const label = \"K mas-bandwidth\"", []string{"mas-bandwidth"}},
		{"const label = \"K mas-bandwidth\"", []string{"mas-bandwidth"}},
		{"const label = \"İ mas-bandwidth\"", []string{"mas-bandwidth"}},
	}

	for _, tc := range cases {
		got := extractGeneralityTokens(tc.input)
		want := tc.want
		sort.Strings(want)
		assert.Equal(t, want, got, "extractGeneralityTokens(%q)", tc.input)
	}
}

// TestGeneralitySpaceHasNoSyntaxException: the machine name counts wherever
// it appears in Go syntax, as an identifier, a struct tag or a string.
func TestGeneralitySpaceHasNoSyntaxException(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, rel, src string
		want           int
	}{
		{"identifiers", "internal/client/use.go", "package client\nfunc use(space string) { _ = space }\n", 2},
		{"json-tag", "internal/client/types.go", "package client\ntype R struct { Name string `json:\"space\"` }\n", 1},
		{"literal", "internal/client/host.go", "package client\nconst host = \"space\"\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			counts := measureGeneralityCounts([]GeneralitySourceFile{{Rel: tc.rel, Src: []byte(tc.src)}})
			assert.Equal(t, tc.want, counts[tc.rel+":space"], "space count; cleaned source = %q", cleanSourceForGenerality(tc.rel, []byte(tc.src)))
		})
	}
}

// TestGeneralityOccurrenceWitness proves the reversed witness: adding a second
// occurrence of an allowed token to an already-allowed file fails the check,
// adjacent occurrences sharing delimiters undercount without boundary preservation,
// and raw string literals containing comments or imports are executable code data
// and are never exempted.
func TestGeneralityOccurrenceWitness(t *testing.T) {
	t.Parallel()

	t.Run("one-shard-over-ceiling-with-another-spare", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		for name, content := range map[string]string{
			"a.txt": "# ceiling: 1\na/file.go:glenn 1\na/file.go:hulk 1\n",
			"b.txt": "# ceiling: 1\n",
		} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
		}
		ledger, err := allowlist.LoadPackages(dir, allowlist.Options{Ceiling: true, Counted: true})
		require.NoError(t, err)
		files := []GeneralitySourceFile{{Rel: "a/file.go", Src: []byte("package a\nconst label = \"glenn hulk\"\n")}}
		violations := checkGenerality(files, ledger)
		require.Contains(t, strings.Join(violations, "\n"), "a.txt has 2 rows, over its ceiling of 1")
	})

	t.Run("second-occurrence-fails", func(t *testing.T) {
		allow := generalityFixtureLedger(t, "test.txt", "# Format: path/to/file.go:token count\n# ceiling: 1\ntest/file.go:glenn 1\n")

		// 1 occurrence: must pass with zero violations.
		oneOccurrence := []GeneralitySourceFile{{Rel: "test/file.go", Src: []byte("package test\nfunc foo() {\n\tmsg := \"hello glenn\"\n}\n")}}
		assert.Empty(t, checkGenerality(oneOccurrence, allow), "expected 1 occurrence to pass")

		// 2 occurrences: adding a second occurrence to the allowed file must fail!
		twoOccurrences := []GeneralitySourceFile{{Rel: "test/file.go", Src: []byte("package test\nfunc foo() {\n\tmsg := \"hello glenn\"\n\tsecond := \"glenn\"\n}\n")}}
		violationsTwo := checkGenerality(twoOccurrences, allow)
		require.NotEmpty(t, violationsTwo, "expected adding a second occurrence of an allowed token to fail, but it passed!")
		assert.Contains(t, strings.Join(violationsTwo, "\n"), "exceeds allowed count 1", "expected violation to mention 'exceeds allowed count 1'")
	})

	t.Run("adjacent-account-count", func(t *testing.T) {
		a := generalityFixtureLedger(t, "@root.txt", "# ceiling: 1\nfixture.go:mas-bandwidth 1\n")
		one := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\nconst owner = \"mas-bandwidth\"\n")}}
		assert.Empty(t, checkGenerality(one, a), "control")
		two := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\nconst owner = \"mas-bandwidth mas-bandwidth\"\n")}}
		assert.NotEmpty(t, checkGenerality(two, a), "second account occurrence escaped existing ceiling")
	})

	for _, tc := range []struct {
		name, src string
	}{
		{"raw-string-comment", "package fixture\nconst refusal = `\n// example: hulk\n`\n"},
		{"raw-string-import", "package fixture\nconst refusal = `\nimport (\nhulk\n)\n`\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := generalityFixtureLedger(t, "@root.txt", "# ceiling: 0\n")
			assert.NotEmpty(t, checkGenerality([]GeneralitySourceFile{{Rel: "fixture.go", Src: []byte(tc.src)}}, a), "runtime string mistaken for exempt comment or import")
		})
	}

	t.Run("legitimate-controls", func(t *testing.T) {
		a := generalityFixtureLedger(t, "@root.txt", "# ceiling: 0\n")
		// Real AST comment with marked example must be exempt
		commentPass := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\n// bench name (e.g. \"hulk\", \"space\")\nfunc foo() {}\n")}}
		assert.Empty(t, checkGenerality(commentPass, a), "legitimate marked comment failed")

		// Real AST import must be exempt
		importPass := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\nimport (\n\t\"github.com/mas-bandwidth/nova-tools/internal/ci\"\n)\n")}}
		assert.Empty(t, checkGenerality(importPass, a), "legitimate import failed")

		// Unmarked AST comment must NOT be exempt
		commentFail := []GeneralitySourceFile{{Rel: "fixture.go", Src: []byte("package fixture\n// glenn was here\nfunc foo() {}\n")}}
		assert.NotEmpty(t, checkGenerality(commentFail, a), "unmarked AST comment with forbidden token was unexpectedly exempted")
	})

	t.Run("unicode-before-account", func(t *testing.T) {
		for _, prefix := range []string{"K ", "K ", "İ "} {
			src := "package fixture\nconst label = \"" + prefix + "mas-bandwidth\"\n"
			counts := measureGeneralityCounts([]GeneralitySourceFile{{Rel: "fixture.go", Src: []byte(src)}})
			assert.Equal(t, 1, counts["fixture.go:mas-bandwidth"], "Unicode prefix %q hid account token", prefix)
		}
	})
}

// TestGeneralityAllowlistUpdate verifies that NOVA_CI_UPDATE=1 strictly adheres to
// the shrink-only invariant: new keys and increased counts are refused with an error,
// existing ledger files are unmodified upon refusal, and count decreases and row
// removals are cleanly written.
func TestGeneralityAllowlistUpdate(t *testing.T) {
	t.Parallel()

	// Exercise the shared sharded updater directly, recording its refusal instead
	// of mutating a global list through a separate writer.
	update := func(path string, measured map[string]int) error {
		ledger, err := allowlist.LoadPackages(filepath.Dir(path), allowlist.Options{Ceiling: true, Counted: true})
		if err != nil {
			return err
		}
		reporter := &generalityMessageReporter{}
		allowlist.CheckPackagesCountedMode(reporter, ledger, measured, true)
		var refusals []string
		for _, message := range reporter.messages {
			if !strings.Contains(message, allowlist.UpdatedRerun) {
				refusals = append(refusals, message)
			}
		}
		if len(refusals) > 0 {
			return fmt.Errorf("%s", strings.Join(refusals, "\n"))
		}
		return nil
	}

	t.Run("original-row-ceiling", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			measured map[string]int
			want     string
		}{
			{name: "unchanged-overfull", measured: map[string]int{"fixture.go:glenn": 2, "fixture.go:hulk": 2}},
			{name: "lower-counts-still-overfull", measured: map[string]int{"fixture.go:glenn": 1, "fixture.go:hulk": 1}},
			{name: "remove-row-to-fit", measured: map[string]int{"fixture.go:glenn": 1}, want: "# ceiling: 1\nfixture.go:glenn 1\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				p := filepath.Join(t.TempDir(), "@root.txt")
				old := "# ceiling: 1\nfixture.go:glenn 2\nfixture.go:hulk 2\n"
				require.NoError(t, os.WriteFile(p, []byte(old), 0600))
				err := update(p, tc.measured)
				if tc.want == "" {
					require.ErrorContains(t, err, "ceiling")
					raw, readErr := os.ReadFile(p)
					require.NoError(t, readErr)
					assert.Equal(t, old, string(raw))
				} else {
					require.NoError(t, err, "removing enough rows should repair the list")
					raw, readErr := os.ReadFile(p)
					require.NoError(t, readErr)
					assert.Equal(t, tc.want, string(raw))
				}
			})
		}
	})

	for _, tc := range []struct {
		name     string
		measured map[string]int
		contains []string
	}{
		{
			name:     "refuses-growth-new-key",
			measured: map[string]int{"fixture.go:glenn": 1, "fixture.go:hulk": 1},
			contains: []string{"refuses to grow", "fixture.go:hulk"},
		},
		{
			name:     "refuses-growth-increased-count",
			measured: map[string]int{"fixture.go:glenn": 2},
			contains: []string{"refuses to raise a count", "measured at 2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "@root.txt")
			old := "# ceiling: 1\nfixture.go:glenn 1\n"
			require.NoError(t, os.WriteFile(p, []byte(old), 0600))
			err := update(p, tc.measured)
			require.Error(t, err)
			for _, substr := range tc.contains {
				assert.Contains(t, err.Error(), substr)
			}
			raw, readErr := os.ReadFile(p)
			require.NoError(t, readErr)
			assert.Equal(t, old, string(raw), "ledger was modified despite refusal")
		})
	}

	t.Run("clean-write-on-shrinking", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "@root.txt")
		old := "# comment\n# ceiling: 3\nfixture.go:emma 2\nfixture.go:glenn 3\nfixture.go:rowan 1\n"
		require.NoError(t, os.WriteFile(p, []byte(old), 0600))
		// Shrink emma from 2 to 1, keep glenn at 3, drop rowan (absent / 0)
		measured := map[string]int{
			"fixture.go:emma":  1,
			"fixture.go:glenn": 3,
			"fixture.go:rowan": 0,
		}
		require.NoError(t, update(p, measured), "clean shrinking should succeed")
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		parsed, err := allowlist.Parse(p, string(raw), allowlist.Options{Ceiling: true, Counted: true})
		require.NoError(t, err, "failed to parse updated allowlist")
		ceil, ok := parsed.Ceiling()
		assert.True(t, ok)
		assert.Equal(t, 2, ceil)
		assert.Len(t, parsed.Rows(), 2)
		assert.True(t, parsed.Has("fixture.go:emma"))
		assert.True(t, parsed.Has("fixture.go:glenn"))
		assert.False(t, parsed.Has("fixture.go:rowan"), "expected rowan to be dropped from rows")
		expected := "# comment\n# ceiling: 2\nfixture.go:emma 1\nfixture.go:glenn 3\n"
		assert.Equal(t, expected, string(raw))
	})

	t.Run("reason-and-comments-survive-count-lowering", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name, separator string
		}{
			{"tab", "\t"},
			{"unicode-space", "\u2003"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				p := filepath.Join(dir, "@root.txt")
				neighbor := filepath.Join(dir, "other.txt")
				prefix := "# ledger heading\n# ceiling: 1\nfixture.go:glenn" + tc.separator
				suffix := tc.separator + "reason with words # keep this text\n# adjacent comment\n"
				other := "# ceiling: 1\nother/file.go:hulk 1 unrelated reason\n"
				require.NoError(t, os.WriteFile(p, []byte(prefix+"3"+suffix), 0600))
				require.NoError(t, os.WriteFile(neighbor, []byte(other), 0600))
				beforeNeighbor, err := os.Stat(neighbor)
				require.NoError(t, err)

				require.NoError(t, update(p, map[string]int{
					"fixture.go:glenn":   2,
					"other/file.go:hulk": 1,
				}))
				got, err := os.ReadFile(p)
				require.NoError(t, err)
				assert.Equal(t, prefix+"2"+suffix, string(got))
				gotOther, err := os.ReadFile(neighbor)
				require.NoError(t, err)
				assert.Equal(t, other, string(gotOther))
				afterNeighbor, err := os.Stat(neighbor)
				require.NoError(t, err)
				assert.True(t, os.SameFile(beforeNeighbor, afterNeighbor), "unrelated shard was rewritten")
			})
		}
	})
}
