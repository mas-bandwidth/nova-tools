package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The native budget helpers a functional file and a slow file both call, kept
// here without a build tag so `-tags slow`, `-tags functional` and their join
// all see the one definition each (SPEC-WORKER rule 13d, issue #1545).

// usageRows reads a card's usage.tsv into a header and its rows, split on tabs.
//
//lint:ignore U1000 used by the functional and slow tagged budget tests, which staticcheck reads without build tags
func usageRows(t *testing.T, jobDir string) ([]string, [][]string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
	require.NoError(t, err, "the card wrote no usage.tsv under %s: %v", jobDir, err)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2, "usage.tsv holds a header and at least one row:\n%s", raw)
	var rows [][]string
	for _, l := range lines[1:] {
		rows = append(rows, strings.Split(l, "\t"))
	}
	return strings.Split(lines[0], "\t"), rows
}

// cell is one named column of one usage row.
//
//lint:ignore U1000 used by the functional and slow tagged budget tests, which staticcheck reads without build tags
func cell(t *testing.T, head []string, row []string, name string) string {
	t.Helper()
	for i, h := range head {
		if h == name && i < len(row) {
			return row[i]
		}
	}
	t.Fatalf("usage.tsv has no column %q in %v", name, head)
	return ""
}
