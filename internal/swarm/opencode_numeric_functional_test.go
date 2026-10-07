//go:build functional

package swarm

// This file's tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). They run under -tags functional.

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeInvalidNumericUsageIsAReadFailure(t *testing.T) {
	t.Parallel()

	r := fakeReader(fakeSQLite3(t))
	max := strconv.Itoa(int(^uint(0) >> 1))
	for _, tc := range []struct{ name, rows string }{
		{"negative", "private-provider\tm\t-1\t0\t\t\t\t\n"},
		{"malformed", "private-provider\tm\tprivate-cell\t0\t\t\t\t\n"},
		{"out_of_range", "private-provider\tm\t9223372036854775808\t0\t\t\t\t\n"},
		{"column_overflow", "private-provider\tm\t" + max + "\t0\t\t\t\t\nprivate-provider\tm\t1\t0\t\t\t\t\n"},
		{"total_overflow", "private-provider\tm\t" + max + "\t1\t\t\t\t\n"},
		{"truncated_row", "private-provider\tm\t1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeDB(t, home, tc.rows)
			u, err := r.read(home)
			require.Error(t, err, "corrupt source must fail instead of producing observed usage: %+v", u)
			require.False(t, u.Observed, "a failed source must return no observed partial map: %+v", u)
			require.Empty(t, u.Values, "a failed source must return no observed partial map: %+v", u)
			assert.Contains(t, err.Error(), "row ", "the refusal identifies its row: %v", err)
			assert.Contains(t, err.Error(), "column", "the refusal identifies its column: %v", err)
			assert.NotContains(t, err.Error(), "private-provider", "the refusal must not echo source data: %v", err)
			assert.NotContains(t, err.Error(), "private-cell", "the refusal must not echo source data: %v", err)
		})
	}
}

func TestOpenCodeNumericBoundsPreserveZeroAndAbsence(t *testing.T) {
	t.Parallel()

	r := fakeReader(fakeSQLite3(t))
	max := strconv.Itoa(int(^uint(0) >> 1))
	home := t.TempDir()
	writeDB(t, home, "p\tm\t"+max+"\t0\t\t-\t\t\n")

	u, err := r.read(home)
	require.NoError(t, err, "host integer boundary is valid: %v", err)
	require.True(t, u.Observed, "a numeric row is observed")
	total, _, _ := u.Sum()
	require.Equal(t, max, strconv.Itoa(total), "downstream total at the host boundary = %d, want %s", total, max)
	budget, _, _ := u.Budget()
	require.Equal(t, max, strconv.Itoa(budget), "downstream budget at the host boundary = %d, want %s", budget, max)
	for _, tc := range []struct{ column, want string }{
		{"tokens_in", max},
		{"tokens_out", "0"},
		{"cache_write", Dash},
		{"cache_read", Dash},
		{"reasoning", Dash},
	} {
		got := u.Values[tc.column]
		assert.Equal(t, tc.want, got, "%s = %q, want %q", tc.column, got, tc.want)
	}
}
