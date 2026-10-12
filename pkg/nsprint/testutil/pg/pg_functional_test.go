//go:build functional

package pg

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
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

// TestPostgresDiesWithItsTestBinary: a test binary killed with SIGKILL runs no
// cleanup, and the postmaster it started must still die: the fixture's sentry
// kills it (sentry_unix.go, the design of pkg/testredis/sentry.go). The child
// is this test binary again; it starts the server, prints the postmaster's pid
// (the first line of <data>/postmaster.pid) and waits on its stdin. The parent
// kills the child and requires the postmaster to be gone within thirty
// seconds.
func TestPostgresDiesWithItsTestBinary(t *testing.T) {
	t.Parallel()
	const mark = "NOVA_PG_ORPHAN_CHILD"
	if os.Getenv(mark) == "1" {
		dir := os.Getenv(mark + "_DIR")
		s, err := StartServer(dir)
		require.NoError(t, err)
		pid, err := postmasterPID(dir)
		require.NoError(t, err)
		fmt.Printf("POSTMASTER %d %s\n", pid, s.Port)
		// The parent holds the other end of this pipe in a binary it kills;
		// nothing here may run a cleanup.
		_, _ = io.Copy(io.Discard, os.Stdin)
		require.Fail(t, "the child's stdin ended and nothing had killed the child")
	}

	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestPostgresDiesWithItsTestBinary$", "-test.count=1")
	child.Env = append(os.Environ(), mark+"=1", mark+"_DIR="+dir)
	stdin, err := child.StdinPipe()
	require.NoError(t, err)
	defer func() {
		// ignored: the child is killed before this closes; a closed pipe has nothing left to say
		_ = stdin.Close()
	}()
	stdout, err := child.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	require.NoError(t, child.Start())
	killed := false
	t.Cleanup(func() {
		if !killed {
			// ignored: the child is this test's own; a kill of an already-gone child is the state wanted
			_ = child.Process.Kill()
			// ignored: the child was just killed; its exit status is that kill
			_ = child.Wait()
		}
		// A failed bound must not leave this exact server behind: stop the
		// postgres found by its data directory, never by a pattern.
		if pid, err := postmasterPID(dir); err == nil && syscall.Kill(pid, 0) == nil {
			stopPostgresAt(t, dir)
		}
	})
	var pid int
	lines := bufio.NewScanner(stdout)
	for lines.Scan() {
		if _, err := fmt.Sscanf(lines.Text(), "POSTMASTER %d", &pid); err == nil && pid != 0 {
			break
		}
	}
	require.NotZero(t, pid, "the child started no postmaster:\n%s", stderr.String())
	require.NoError(t, child.Process.Kill())
	// ignored: the child was just killed; its exit status is that kill
	_ = child.Wait()
	killed = true
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			require.Failf(t, "", "the test binary is gone and its postmaster, pid %d, is alive", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// postmasterPID is the postmaster's pid: the first line of the data
// directory's postmaster.pid.
func postmasterPID(dir string) (int, error) {
	body, err := os.ReadFile(filepath.Join(dir, "data", "postmaster.pid"))
	if err != nil {
		return 0, err
	}
	line, _, _ := strings.Cut(string(body), "\n")
	return strconv.Atoi(strings.TrimSpace(line))
}

// stopPostgresAt stops the postgres whose data directory is under dir, by that
// directory and never by a pattern.
func stopPostgresAt(t *testing.T, dir string) {
	t.Helper()
	bin, err := Binaries()
	if err != nil {
		return
	}
	stop, release := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(bin, "pg_ctl"), "-D", filepath.Join(dir, "data"), "-m", "immediate", "-w", "stop")
	defer release()
	stop.Env = cleanEnv()
	out, err := stop.CombinedOutput()
	if err != nil {
		t.Logf("stop the orphaned throwaway postgres: %v\n%s", err, out)
	}
}
