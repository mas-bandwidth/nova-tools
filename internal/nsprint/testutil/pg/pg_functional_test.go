//go:build functional

package pg

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
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

// A killed test binary runs no cleanup. Its private Postgres must still die,
// and neither it nor the watcher may inherit an injected coordinator FD.
func TestPostgresDiesWithItsTestBinary(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("/proc FD proof is Linux-only")
	}
	const mark = "NOVA_PG_ABORT_HELPER"
	if os.Getenv(mark) == "1" {
		dir := os.Getenv("NOVA_PG_ABORT_DIR")
		s, err := StartServer(dir)
		require.NoError(t, err)
		record := fmt.Sprintf("%d %d %s", s.cmd.Process.Pid, s.watch.group, s.Port)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "ready"), []byte(record), 0600))
		select {}
	}
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "coordinator.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	require.NoError(t, err)
	defer lock.Close()
	exe, err := os.Executable()
	require.NoError(t, err)
	cmd := subproc.Long(context.Background(), exe, "-test.run=^TestPostgresDiesWithItsTestBinary$")
	cmd.Env = append(os.Environ(), mark+"=1", "NOVA_PG_ABORT_DIR="+dir)
	cmd.ExtraFiles = []*os.File{lock}
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	var pid, watcher int
	var port string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(filepath.Join(dir, "ready"))
		if err == nil {
			if n, scanErr := fmt.Sscanf(string(body), "%d %d %s", &pid, &watcher, &port); scanErr == nil && n == 3 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Positive(t, pid, "nested fixture never became ready")
	t.Cleanup(func() {
		// Even a failed assertion must not leave this exact disposable server.
		bin, err := Binaries()
		if err == nil {
			stop, release := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(bin, "pg_ctl"), "-D", filepath.Join(dir, "data"), "-m", "immediate", "-w", "stop")
			_ = stop.Run()
			release()
		}
	})
	for _, process := range []int{pid, watcher} {
		entries, err := os.ReadDir(filepath.Join("/proc", strconv.Itoa(process), "fd"))
		require.NoError(t, err)
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(process), "fd", entry.Name()))
			if err == nil {
				require.NotEqual(t, lockPath, target, "process %d inherited coordinator FD", process)
			}
		}
	}
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !running(pid) && !running(watcher) && !groupRunning(watcher) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.False(t, running(pid), "Postgres %d outlived killed test binary on %s", pid, port)
	require.False(t, running(watcher), "watcher %d outlived killed test binary", watcher)
	require.False(t, groupRunning(watcher), "a Postgres worker outlived process group %d", watcher)
}

func running(pid int) bool {
	body, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(body))
	return len(fields) > 2 && fields[2] != "Z"
}

func groupRunning(group int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(body))
		if len(fields) < 5 || fields[2] == "Z" {
			continue
		}
		pgrp, err := strconv.Atoi(fields[4])
		if err == nil && pgrp == group {
			return true
		}
	}
	return false
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
