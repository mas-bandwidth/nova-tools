// Package pg starts the throwaway Postgres the nova-config functional tests
// share, the way internal/nsprint/testutil starts a throwaway Redis: initdb
// and pg_ctl from the binaries on PATH (or the well-known install
// directories, or NOVA_PG_BIN), a free loopback port, everything under the
// test's temporary directory, trust authentication, and the cleanup stops
// it. The process is private. It is not the fleet store, and this file
// names no bench address.
//
// A runner without the binaries FAILS the test with one line naming the
// binary: no evidence is not negative evidence, and a skip here would let a
// green run be a run that never executed the store.
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// BinEnv names a directory holding initdb, pg_ctl and postgres, for a
// machine whose install is somewhere the search below does not look.
const BinEnv = "NOVA_PG_BIN"

// wellKnown are the install directories searched after PATH: Homebrew's
// versioned kegs on macOS, Debian's and Ubuntu's versioned trees on Linux.
var wellKnown = []string{
	"/opt/homebrew/opt/postgresql@16/bin",
	"/opt/homebrew/opt/postgresql@17/bin",
	"/opt/homebrew/opt/postgresql@15/bin",
	"/opt/homebrew/opt/postgresql@14/bin",
	"/usr/local/opt/postgresql@16/bin",
	"/usr/lib/postgresql/16/bin",
	"/usr/lib/postgresql/17/bin",
	"/usr/lib/postgresql/15/bin",
	"/usr/lib/postgresql/14/bin",
	"/usr/pgsql-16/bin",
}

// Binaries finds initdb, pg_ctl and postgres: in NOVA_PG_BIN when set, else
// on PATH, else in the well-known directories. It returns the directory
// holding all three, or an error naming the first binary it could not find
// and where it looked.
func Binaries() (string, error) {
	want := []string{"initdb", "pg_ctl", "postgres"}
	if dir := os.Getenv(BinEnv); dir != "" {
		for _, b := range want {
			if _, err := os.Stat(filepath.Join(dir, b)); err != nil {
				return "", fmt.Errorf("%s=%s holds no %s", BinEnv, dir, b)
			}
		}
		return dir, nil
	}
	if p, err := exec.LookPath("pg_ctl"); err == nil {
		dir := filepath.Dir(p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			dir = filepath.Dir(resolved)
		}
		ok := true
		for _, b := range want {
			if _, err := os.Stat(filepath.Join(dir, b)); err != nil {
				ok = false
			}
		}
		if ok {
			return dir, nil
		}
	}
	for _, dir := range wellKnown {
		ok := true
		for _, b := range want {
			if _, err := os.Stat(filepath.Join(dir, b)); err != nil {
				ok = false
				break
			}
		}
		if ok {
			return dir, nil
		}
	}
	return "", fmt.Errorf("pg_ctl (with initdb and postgres) is not on PATH, not in %s, and not under %s; install postgresql (.github/scripts/install-postgres.sh)", BinEnv, strings.Join(wellKnown, ", "))
}

// Server is one running throwaway Postgres.
type Server struct {
	Dir  string
	Bin  string
	Port string
	// User is the superuser initdb made; trust authentication, no password.
	User string
	next atomic.Int64
	log  string
}

// DSN is the connection string of one database on the server, for pgx: a
// loopback address, trust authentication, no password anywhere.
func (s *Server) DSN(database string) string {
	return fmt.Sprintf("postgres://%s@127.0.0.1:%s/%s?sslmode=disable", s.User, s.Port, database)
}

// pgToolBudget is how long one initdb or pg_ctl call may run: pg_ctl start waits up to
// 60 s for the server itself, and the budget leaves room past that.
const pgToolBudget = 120 * time.Second

// StartServer runs initdb and pg_ctl start under dir on a free loopback
// port. The caller stops it with Stop. It is the TestMain form: one server
// for a package, one database per test through Database.
func StartServer(dir string) (*Server, error) {
	bin, err := Binaries()
	if err != nil {
		return nil, err
	}
	data := filepath.Join(dir, "data")
	logPath := filepath.Join(dir, "postgres.log")
	s := &Server{Dir: dir, Bin: bin, User: "postgres", log: logPath}
	initdb, stopInitdb := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(bin, "initdb"), "-D", data, "-U", s.User, "--auth=trust", "--no-sync", "-E", "UTF8", "--locale=C")
	defer stopInitdb()
	initdb.Env = cleanEnv()
	if out, err := initdb.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("initdb: %v\n%s", err, out)
	}
	// The socket directory is a fixed short path under the data directory's
	// parent: a macOS t.TempDir path is longer than a unix socket allows.
	// Listening is on loopback only; the socket directory is emptied so no
	// socket is made at all.
	var last string
	for attempt := 0; attempt < 5; attempt++ {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		opts := fmt.Sprintf("-p %s -c listen_addresses=127.0.0.1 -c unix_socket_directories='' -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c log_min_messages=warning", port)
		start, stopStart := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(bin, "pg_ctl"), "-D", data, "-l", logPath, "-o", opts, "-w", "-t", "60", "start")
		start.Env = cleanEnv()
		out, err := start.CombinedOutput()
		stopStart()
		if err == nil {
			s.Port = port
			if err := s.ready(); err != nil {
				// ignored: a test fixture's cleanup on the failure path; the ready error is the one returned
				_ = s.Stop()
				return nil, err
			}
			return s, nil
		}
		body, _ := os.ReadFile(logPath)
		last = fmt.Sprintf("pg_ctl start on port %s: %v\n%s\n%s", port, err, out, body)
		if !strings.Contains(string(body), "Address already in use") && !strings.Contains(string(body), "could not bind") {
			return nil, fmt.Errorf("%s", last)
		}
	}
	return nil, fmt.Errorf("throwaway postgres did not start in five attempts: %s", last)
}

// ready pings the server once it reports itself started.
func (s *Server) ready() error {
	db, err := sql.Open("pgx", s.DSN("postgres"))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := db.PingContext(ctx); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			body, _ := os.ReadFile(s.log)
			return fmt.Errorf("throwaway postgres on port %s did not answer: %v\n%s", s.Port, err, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Stop stops the server (immediate mode: nothing here is kept).
func (s *Server) Stop() error {
	stop, stopStop := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(s.Bin, "pg_ctl"), "-D", filepath.Join(s.Dir, "data"), "-m", "immediate", "-w", "stop")
	defer stopStop()
	stop.Env = cleanEnv()
	if out, err := stop.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_ctl stop: %v\n%s", err, out)
	}
	return nil
}

// Database creates a fresh, empty database on the server and returns its
// DSN. Each call is its own database, so parallel tests share the server
// and nothing else.
func (s *Server) Database(t testing.TB) string {
	t.Helper()
	name := fmt.Sprintf("t%d_%d", os.Getpid(), s.next.Add(1))
	db, err := sql.Open("pgx", s.DSN("postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	return s.DSN(name)
}

// Start is the per-test form: a server under t.TempDir(), stopped by the
// cleanup, its DSN returned. A missing binary fails the test with the line
// Binaries returns.
func Start(t testing.TB) *Server {
	t.Helper()
	s, err := StartServer(t.TempDir())
	if err != nil {
		t.Fatalf("throwaway postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Stop(); err != nil {
			t.Logf("stop throwaway postgres: %v", err)
		}
	})
	return s
}

// cleanEnv is the child's environment: PATH and HOME and nothing that could
// point initdb or postgres at a real cluster (PGDATA, PGHOST, PGPORT...).
func cleanEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "LC_ALL=C", "LANG=C"}
	if runtime.GOOS == "windows" {
		env = append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	return env
}

// freePort takes a free loopback port and returns it as text.
func freePort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("loopback port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		return "", err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", err
	}
	return port, nil
}
