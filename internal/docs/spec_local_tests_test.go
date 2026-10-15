package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSpecLocalTestsAreRealOrOwed pins that every test the spec names exists
// or names the card that owes it. It reads docs/SPEC-LOCAL.md's Tests table
// and the Go _test.go sources with only stdlib (os, regexp, path/filepath).
// A `TestX` named in the table is OK if:
// - a func TestX(t *testing.T) { ... } (or t.Parallel first) exists in any *_test.go, or
// - the name in the table text is followed by (owed: <card>) , or
// - followed by (in mas-bandwidth/nova-sprint)
func TestSpecLocalTestsAreRealOrOwed(t *testing.T) {
	t.Parallel()

	specBytes, err := os.ReadFile("../../docs/SPEC-LOCAL.md")
	require.NoError(t, err, "docs/SPEC-LOCAL.md: %v", err)
	spec := string(specBytes)

	declared := declaredTestNames(t)

	// Find the Tests table: from | rule | test | to the end of table (lines starting with | )
	table := extractTestsTable(spec)

	// Now, the table content has cells with `TestName` optionally followed by (owed:..) or note
	// Collect each backticked TestName and the text immediately after it until , ; or end of cell.
	bad := []string{}
	reTest := regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")
	matches := reTest.FindAllStringSubmatchIndex(table, -1)
	for _, m := range matches {
		name := table[m[2]:m[3]]
		after := table[m[1]:]
		// take up to next ` or , or ; or | or newline
		cut := len(after)
		for i, c := range after {
			if c == '`' || c == ',' || c == ';' || c == '|' || c == '\n' {
				cut = i
				break
			}
		}
		trailer := strings.TrimSpace(after[:cut])
		hasOwed := strings.HasPrefix(trailer, "(owed: ") || strings.Contains(trailer, "(owed: ")
		hasSprint := strings.Contains(trailer, "(in mas-bandwidth/nova-sprint)")
		if hasOwed || hasSprint {
			continue
		}
		if !declared[name] {
			bad = append(bad, name)
		}
	}
	require.Empty(t, bad, "docs/SPEC-LOCAL.md Tests table names tests with no declaration and no owing note: %v", bad)
}

// extractTestsTable returns the content of the | rule | test | markdown table.
func extractTestsTable(s string) string {
	lines := strings.Split(s, "\n")
	inTable := false
	var buf []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "| rule | test |") {
			inTable = true
		}
		if inTable {
			buf = append(buf, line)
			if trim != "" && !strings.HasPrefix(trim, "|") {
				break
			}
		}
		if inTable && trim == "" && len(buf) > 2 {
			break
		}
	}
	return strings.Join(buf, "\n")
}

// declaredTestNames walks the tree for *_test.go and greps ^func TestName\(t \*testing\.T\)
func declaredTestNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "testdata" || name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil // ignore unreadable
		}
		content := string(b)
		// match func TestX(  with t *testing.T  or any
		re := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\([^*)]*\*testing\.T[^)]*\)`)
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			names[m[1]] = true
		}
		return nil
	})
	require.NoError(t, err)
	return names
}
