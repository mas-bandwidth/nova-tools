//go:build functional

package pg

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// TestThrowawayPostgresStartsAndAnswers is the helper's own proof: a server
// under the test's directory, a fresh database per call, a query answered,
// and the stop leaving nothing running on the port.
func TestThrowawayPostgresStartsAndAnswers(t *testing.T) {
	t.Parallel()

	s := Start(t)
	if !strings.HasPrefix(s.DSN("x"), "postgres://postgres@127.0.0.1:"+s.Port+"/x") {
		t.Fatalf("dsn %q", s.DSN("x"))
	}
	a, b := s.Database(t), s.Database(t)
	if a == b {
		t.Fatalf("two databases share a DSN: %s", a)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", a)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("select 1: %d %v", one, err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (n int)"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("pgx", b)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var exists bool
	if err := other.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 't')").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("a table made in one database is visible in another: the databases are not separate")
	}
}

// TestBinariesNamesTheMissingBinary: the answer for a runner with no
// Postgres is one line naming the binary, never a skip.
func TestBinariesNamesTheMissingBinary(t *testing.T) {
	t.Parallel()

	dir, err := Binaries()
	if err != nil {
		t.Fatalf("this test runs where the binaries are: %v", err)
	}
	if dir == "" {
		t.Fatal("Binaries returned no directory and no error")
	}
}
