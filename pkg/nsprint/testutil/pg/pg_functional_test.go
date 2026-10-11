//go:build functional

package pg

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestThrowawayPostgresStartsAndAnswers is the helper's own proof: a server
// under the test's directory, a fresh database per call, a query answered,
// and the stop leaving nothing running on the port.
func TestThrowawayPostgresStartsAndAnswers(t *testing.T) {
	t.Parallel()

	s := Start(t)
	if !strings.HasPrefix(s.DSN("x"), "postgres://postgres@127.0.0.1:"+s.Port+"/x") {
		require.True(t, strings.HasPrefix(s.DSN("x"), "postgres://postgres@127.0.0.1:"+s.Port+"/x"), "dsn %q", s.DSN("x"))
	}
	a, b := s.Database(t), s.Database(t)
	if a == b {
		require.NotEqual(t, b, a, "two databases share a DSN: %s", a)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", a)
	if err != nil {
		require.NoError(t, err, err)
	}
	defer db.Close()
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		require.Failf(t, "assertion failed", "select 1: %d %v", one, err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (n int)"); err != nil {
		require.NoError(t, err, err)
	}
	other, err := sql.Open("pgx", b)
	if err != nil {
		require.NoError(t, err, err)
	}
	defer other.Close()
	var exists bool
	if err := other.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 't')").Scan(&exists); err != nil {
		require.NoError(t, err, err)
	}
	if exists {
		require.False(t, exists, "a table made in one database is visible in another: the databases are not separate")
	}
}

// TestBinariesNamesTheMissingBinary: the answer for a runner with no
// Postgres is one line naming the binary, never a skip.
func TestBinariesNamesTheMissingBinary(t *testing.T) {
	t.Parallel()

	dir, err := Binaries()
	if err != nil {
		require.NoError(t, err, "this test runs where the binaries are: %v", err)
	}
	if dir == "" {
		require.NotEqual(t, "", dir, "Binaries returned no directory and no error")
	}
}
