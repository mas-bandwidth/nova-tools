package config

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pe is a constructed pgconn.PgError, the way the driver returns one.
func pe(code, msg string) *pgconn.PgError {
	return &pgconn.PgError{Severity: "ERROR", Code: code, Message: msg}
}

func TestStoreErrors(t *testing.T) {
	t.Parallel()
	login := `postgres refused the login for nova: check NOVA_PG_PASSWORD; run: PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN" -c 'select 1'`
	for code, want := range map[string]string{
		"42P01": SchemaMissing, "42703": SchemaMissing, "3F000": SchemaMissing,
		"28P01": login, "28000": login,
		"42501": "nova may not add machines",
		"57014": `store error (SQLSTATE 57014): canceling statement due to timeout; check the store answers, then try again; run: psql "$NOVA_PG_DSN" -c 'select 1'`,
	} {
		err := storeError(fmt.Errorf("list machine: %w", pe(code, "permission denied for table machines\nLINE 1: SELECT")), "nova", "add")
		if code == "57014" {
			err = storeError(pe(code, "canceling statement due to timeout. Try again.\nmore"), "nova", "read")
		}
		assert.EqualError(t, err, want, code)
	}
	assert.EqualError(t, storeError(errors.New("line one\n\tline two"), "u", "read"), "postgres: connect failed")
	assert.EqualError(t, storeError(pe("42501", ""), "u", "read"), "u may not read the table")
	assert.NoError(t, storeError(nil, "u", "read"))
}

func TestConnectErrors(t *testing.T) {
	t.Parallel()
	cfg, err := pgx.ParseConfig("postgres://nova@db.example:5432/mydb")
	require.NoError(t, err)
	assert.EqualError(t, connectError(cfg, pe("3D000", "database \"mydb\" does not exist")), "database mydb does not exist on db.example; run: createdb -h db.example -p 5432 -U nova mydb")
	assert.EqualError(t, connectError(cfg, pe("28P01", "password authentication failed")), `postgres refused the login for nova: check NOVA_PG_PASSWORD; run: PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN" -c 'select 1'`)
	assert.EqualError(t, connectError(cfg, fmt.Errorf("dial: %w", syscall.ECONNREFUSED)), `cannot reach postgres at db.example:5432 (NOVA_PG_DSN): connection refused; check NOVA_PG_DSN or run: psql "$NOVA_PG_DSN" -c 'select 1'`)
	notURL := "not a postgres:// URL (want postgres://USER@HOST:PORT/DB?sslmode=disable)"
	for dsn, want := range map[string]string{"postgres://u@h:99999/x": "port 99999 out of range", "mysql://u@h/x": notURL, "h=1": notURL, "postgres://u@h:5432/x": ""} {
		assert.Equal(t, want, DSNProblem(dsn), dsn)
	}
	_, err = Open(t.Context(), "postgres://u:secret@h:99999/x", "")
	assert.EqualError(t, err, "the postgres dsn is wrong: port 99999 out of range")
}

func TestCause(t *testing.T) {
	t.Parallel()
	dial := func(e error) error {
		return fmt.Errorf("failed to connect to `host=h`: h:5432 (h): dial error: %w", &net.OpError{Op: "dial", Net: "tcp", Err: e})
	}
	assert.Equal(t, "connection refused", cause(dial(&os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED})))
	assert.Equal(t, "no such host", cause(dial(&net.DNSError{Err: "no such host", Name: "h"})))
	assert.Equal(t, "timed out", cause(dial(os.ErrDeadlineExceeded)))
	assert.Equal(t, "timed out", cause(context.DeadlineExceeded))
	assert.Equal(t, "connect failed", cause(errors.New("x\ny")))
}
