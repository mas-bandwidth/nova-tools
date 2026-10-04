package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unionRemovals over three byte slices: two sides that remove adjacent rows give the
// base less both; the same row removed by both is removed once and counted on each;
// a lowered ceiling line is honoured at the lower of the two; a side that adds a line,
// one that raises the ceiling and a base that is not a list of lines at all are refused;
// a side that changed nothing removes nothing; the trailing newline follows the base.
func TestUnionRemovals(t *testing.T) {
	t.Parallel()
	const base = "# the ledger\n# ceiling: 4\na\nb\nc\nd\n"
	for _, tc := range []struct {
		name, base, left, right, want string
		nLeft, nRight                 int
		why                           string
	}{
		{"adjacent rows", base, "# the ledger\n# ceiling: 3\na\nc\nd\n", "# the ledger\n# ceiling: 3\na\nb\nd\n", "# the ledger\n# ceiling: 3\na\nd\n", 1, 1, ""},
		{"the same row on both sides", base, "# the ledger\n# ceiling: 3\na\nb\nd\n", "# the ledger\n# ceiling: 3\na\nb\nd\n", "# the ledger\n# ceiling: 3\na\nb\nd\n", 1, 1, ""},
		{"one side unchanged", base, base, "# the ledger\n# ceiling: 3\nb\nc\nd\n", "# the ledger\n# ceiling: 3\nb\nc\nd\n", 0, 1, ""},
		{"the ceiling left alone", base, "# the ledger\n# ceiling: 4\na\nc\nd\n", "# the ledger\n# ceiling: 4\nb\nc\nd\n", "# the ledger\n# ceiling: 4\nc\nd\n", 1, 1, ""},
		{"ceilings lowered unevenly", base, "# the ledger\n# ceiling: 2\nc\nd\n", "# the ledger\n# ceiling: 3\na\nc\nd\n", "# the ledger\n# ceiling: 2\nc\nd\n", 2, 1, ""},
		{"no ceiling, no trailing newline", "a\nb\nc", "a\nc", "b\nc", "c", 1, 1, ""},
		{"everything removed", "a\nb\n", "b\n", "a\n", "", 1, 1, ""},
		{"an added line is refused", base, "# the ledger\n# ceiling: 3\na\nc\nd\n", "# the ledger\n# ceiling: 5\na\nb\nc\nd\ne\n", "", 0, 0, "the right side raises the ceiling from 4 to 5"},
		{"an added row is refused", base, "# the ledger\n# ceiling: 4\na\nb\nc\nd\ne\n", base, "", 0, 0, `the left side adds a line, which is no removal: "e"`},
		{"a changed row is refused", base, base, "# the ledger\n# ceiling: 4\na\nb 2\nc\nd\n", "", 0, 0, `the right side adds a line, which is no removal: "b 2"`},
		{"a reordered side is refused", base, "# the ledger\n# ceiling: 4\nb\na\nc\nd\n", base, "", 0, 0, `the left side adds a line, which is no removal: "a"`},
		{"a bad ceiling is refused", "# ceiling: x\na\n", "a\n", "a\n", "", 0, 0, "the base's ceiling line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, nLeft, nRight, err := unionRemovals([]byte(tc.base), []byte(tc.left), []byte(tc.right))
			if tc.why != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.why)
				assert.Nil(t, out)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))
			assert.Equal(t, [2]int{tc.nLeft, tc.nRight}, [2]int{nLeft, nRight})
		})
	}
}

// unionPaths sends the shrink-only ledgers to the union and leaves the rest: a
// generated ledger a family owns (the generality fixtures allowlist is shrink-only by
// name and the family's by ownership), a counted-ledger shard, a prose file and a Go
// file are not resolved as a union.
func TestUnionPaths(t *testing.T) {
	t.Parallel()
	union, rest := unionPaths([]string{
		"internal/ci/testdata/dead_code_allowlist.txt",
		"internal/ci/testdata/generality_text_fixtures_allowlist.txt",
		"internal/ci/testdata/generality/rows.txt",
		"internal/ci/testdata/discarded/cmd.txt",
		"internal/ci/sleeps-skips_allowlist.txt",
		"notes.tsv",
		"internal/ci/testdata/x.go",
	}, landLedgers)
	assert.Equal(t, []string{"internal/ci/testdata/dead_code_allowlist.txt", "internal/ci/sleeps-skips_allowlist.txt"}, union)
	assert.Equal(t, []string{"internal/ci/testdata/generality_text_fixtures_allowlist.txt", "internal/ci/testdata/generality/rows.txt",
		"internal/ci/testdata/discarded/cmd.txt", "notes.tsv", "internal/ci/testdata/x.go"}, rest)
	assert.Equal(t, "ledger a.txt: resolved as the union of removals (-1 left, -2 right)", unionLine("a.txt", 1, 2))
}
