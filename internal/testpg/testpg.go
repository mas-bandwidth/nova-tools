// Package testpg is a throwaway Postgres for one test, or for one package's
// tests through its TestMain.
//
// StartServer runs initdb and pg_ctl from the binaries on PATH (or the
// well-known install directories, or NOVA_PG_BIN). What a test can rely on:
//
// WHAT IT LISTENS ON. One TCP listener on 127.0.0.1, the IPv4 loopback, on a
// port the kernel chose, and no Unix socket (unix_socket_directories is
// empty: a macOS t.TempDir path is longer than a socket's name may be). Trust
// authentication, no password anywhere.
//
// IT IS THE CALLER'S ALONE. The port is taken from the kernel, closed and
// handed to postgres, so another process can bind it in between. pg_ctl
// reports a start only when the server of THIS data directory is up, so a
// server that lost its port is a failed start, and another port is taken.
// Every Database is a fresh, empty database: parallel tests share the server
// and nothing else.
//
// NOTHING IS KEPT. The data directory and the log are under the directory the
// caller gave, which for Start is the test's own t.TempDir(), with fsync off.
//
// IT STOPS WITH THE TEST. Start's cleanup stops the server (immediate mode)
// and the testing package removes the directory: after a pass, a failure or a
// panic in the test. StartServer's caller stops it with Stop. pg_ctl detaches
// the server from the test binary, so a test binary that dies without its
// cleanups (a timeout, os.Exit, a kill) leaves the server running:
// internal/testredis has a sentry for that, this package has none.
//
// A runner without the binaries FAILS the test with one line naming the
// binary: no evidence is not negative evidence, and a skip here would let a
// green run be a run that never executed the store.
package testpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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

// programs are what a directory holds to be a Postgres install.
var programs = []string{"initdb", "pg_ctl", "postgres"}

// launch is everything StartServer takes from outside itself, so a test can
// stand in for any of it without touching the process's environment.
type launch struct {
	inTest func() bool                       // testing.Testing
	getenv func(key string) string           // os.Getenv
	look   func(file string) (string, error) // exec.LookPath
	known  []string                          // wellKnown
	port   func() (string, error)            // a loopback port that is free now
	up     time.Duration                     // how long pg_ctl waits for a start (-t, whole seconds)
	wait   time.Duration                     // how long a started server may take to answer
	tries  int                               // how many ports are taken before StartServer gives up
}

// real is what StartServer runs with.
var real = launch{
	inTest: testing.Testing,
	getenv: os.Getenv,
	look:   exec.LookPath,
	known:  wellKnown,
	port:   func() (string, error) { return freePort(net.Listen) },
	up:     30 * time.Second,
	wait:   30 * time.Second,
	tries:  5,
}

// refuse panics outside a test binary. The package starts servers for tests
// and for nothing else.
func (l launch) refuse() {
	if !l.inTest() {
		panic("testpg: used outside a test binary; it starts servers for tests and for nothing else")
	}
}

// Binaries finds initdb, pg_ctl and postgres: in NOVA_PG_BIN when set, else
// on PATH, else in the well-known directories. It returns the directory
// holding all three, or an error naming the first binary it could not find
// and where it looked.
func Binaries() (string, error) { return real.binaries() }

func (l launch) binaries() (string, error) {
	if dir := l.getenv(BinEnv); dir != "" {
		if missing := lacks(dir); missing != "" {
			return "", fmt.Errorf("%s=%s holds no %s", BinEnv, dir, missing)
		}
		return dir, nil
	}
	if p, err := l.look("pg_ctl"); err == nil {
		dir := filepath.Dir(p)
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			dir = filepath.Dir(resolved)
		}
		if lacks(dir) == "" {
			return dir, nil
		}
	}
	for _, dir := range l.known {
		if lacks(dir) == "" {
			return dir, nil
		}
	}
	return "", fmt.Errorf("pg_ctl (with initdb and postgres) is not on PATH, not in %s, and not under %s; install postgresql (.github/scripts/install-postgres.sh)", BinEnv, strings.Join(l.known, ", "))
}

// lacks is the first program dir does not hold, or "" when it holds them all.
func lacks(dir string) string {
	for _, program := range programs {
		if _, err := os.Stat(filepath.Join(dir, program)); err != nil {
			return program
		}
	}
	return ""
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
	env  []string

	mu      sync.Mutex
	stopped bool
}

// DSN is the connection string of one database on the server, for pgx: a
// loopback address, trust authentication, no password anywhere.
func (s *Server) DSN(database string) string {
	return fmt.Sprintf("postgres://%s@127.0.0.1:%s/%s?sslmode=disable", s.User, s.Port, database)
}

// StartServer runs initdb and pg_ctl start under dir on a free loopback
// port. The caller stops it with Stop. It is the TestMain form: one server
// for a package, one database per test through Database.
//
// A start that lost its port to another process is made again on a fresh
// one, five times at most; any other failure is returned with what pg_ctl
// and the server's log said. A start pg_ctl does not see finish within thirty
// seconds is an error, and the server it left starting is stopped; so is a
// server that started and does not answer within thirty seconds.
func StartServer(dir string) (*Server, error) { return real.startServer(dir) }

func (l launch) startServer(dir string) (*Server, error) {
	l.refuse()
	bin, err := l.binaries()
	if err != nil {
		return nil, err
	}
	data := filepath.Join(dir, "data")
	s := &Server{Dir: dir, Bin: bin, User: "postgres", log: filepath.Join(dir, "postgres.log"), env: cleanEnv(l.getenv, runtime.GOOS)}
	initdb := exec.Command(filepath.Join(bin, "initdb"), "-D", data, "-U", s.User, "--auth=trust", "--no-sync", "-E", "UTF8", "--locale=C")
	initdb.Env = s.env
	if out, err := initdb.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("initdb: %v\n%s", err, out)
	}
	// Listening is on loopback only; the socket directory is emptied so no
	// socket is made at all.
	for try := 1; ; try++ {
		port, err := l.port()
		if err != nil {
			return nil, err
		}
		opts := fmt.Sprintf("-p %s -c listen_addresses=127.0.0.1 -c unix_socket_directories='' -c fsync=off -c synchronous_commit=off -c full_page_writes=off -c log_min_messages=warning", port)
		// pg_ctl -l appends: this attempt's log is what is written after
		// here, so an earlier attempt's lost port is not read as this one's.
		from := logSize(s.log)
		start := exec.Command(filepath.Join(bin, "pg_ctl"), "-D", data, "-l", s.log, "-o", opts, "-w", "-t", strconv.Itoa(int(l.up/time.Second)), "start")
		start.Env = s.env
		out, err := start.CombinedOutput()
		if err == nil {
			s.Port = port
			if err := s.ready(l.wait); err != nil {
				return nil, errors.Join(err, s.Stop())
			}
			return s, nil
		}
		// pg_ctl -w that gave up waiting leaves the server it started
		// running: it is stopped here, not left.
		var left error
		if strings.Contains(string(out), "did not start in time") {
			stopped, err := stopStarting(bin, data, s.env, l.wait)
			if stopped {
				out = append(out, "the server pg_ctl gave up waiting for was stopped\n"...)
			}
			left = err
		}
		body := logSince(s.log, from)
		said := fmt.Sprintf("pg_ctl start on port %s: %v\n%s\n%s", port, err, out, body)
		if left != nil {
			return nil, errors.Join(errors.New(said), left)
		}
		if !strings.Contains(body, "Address already in use") && !strings.Contains(body, "could not bind") {
			return nil, errors.New(said)
		}
		if try >= l.tries {
			return nil, fmt.Errorf("throwaway postgres did not start in %d attempts: %s", try, said)
		}
	}
}

// open is a pool of connections to one database of the server. A
// connection string that does not parse is an error here, before anything is
// dialled. Importing the driver registers it, so a caller's
// sql.Open("pgx", dsn) works as it did.
func (s *Server) open(database string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(s.DSN(database))
	if err != nil {
		return nil, err
	}
	return stdlib.OpenDB(*cfg), nil
}

// ready pings the server once it reports itself started.
func (s *Server) ready(wait time.Duration) error {
	db, err := s.open("postgres")
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	for {
		err := db.PingContext(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			body, _ := os.ReadFile(s.log)
			return fmt.Errorf("throwaway postgres on port %s did not answer in %v: %v\n%s", s.Port, wait, err, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Stop stops the server (immediate mode: nothing here is kept) and returns
// when it is gone, so the next query is refused at once: the unreachable
// store of a test. A server that is stopped stays stopped: a second Stop,
// and the cleanup's after a test's own, do nothing.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	if err := stop(s.Bin, filepath.Join(s.Dir, "data"), s.env); err != nil {
		return err
	}
	s.stopped = true
	return nil
}

// stop stops the server of one data directory, immediate mode, and returns
// when it is gone.
func stop(bin, data string, env []string) error {
	cmd := exec.Command(filepath.Join(bin, "pg_ctl"), "-D", data, "-m", "immediate", "-w", "stop")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_ctl stop: %v\n%s", err, out)
	}
	return nil
}

// stopStarting stops the server a pg_ctl start gave up waiting for, and
// says whether there was one. That server may not have written its
// postmaster.pid yet, and pg_ctl stop finds the server by it: the file is
// waited for, wait at most. A server that never wrote it has ended by itself
// (its start failed), and there is nothing to stop.
func stopStarting(bin, data string, env []string, wait time.Duration) (bool, error) {
	until := time.Now().Add(wait)
	for {
		if _, err := os.Stat(filepath.Join(data, "postmaster.pid")); err == nil {
			return true, stop(bin, data, env)
		}
		if time.Now().After(until) {
			return false, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logSize is how long the log is now; a log not yet written is empty.
func logSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// logSince is what the log holds from offset on.
func logSince(path string, offset int64) string {
	body, err := os.ReadFile(path)
	if err != nil || offset > int64(len(body)) {
		return string(body)
	}
	return string(body[offset:])
}

// Database creates a fresh, empty database on the server and returns its
// DSN. Each call is its own database, so parallel tests share the server
// and nothing else.
func (s *Server) Database(t testing.TB) string {
	t.Helper()
	name := fmt.Sprintf("t%d_%d", os.Getpid(), s.next.Add(1))
	db, err := s.open("postgres")
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
	return real.start(t)
}

func (l launch) start(t testing.TB) *Server {
	t.Helper()
	s, err := l.startServer(t.TempDir())
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
func cleanEnv(getenv func(string) string, goos string) []string {
	env := []string{"PATH=" + getenv("PATH"), "HOME=" + getenv("HOME"), "LC_ALL=C", "LANG=C"}
	if goos == "windows" {
		env = append(env, "SYSTEMROOT="+getenv("SYSTEMROOT"))
	}
	if tmp := getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	return env
}

// freePort takes a loopback port from the kernel and gives it back.
func freePort(listen func(network, address string) (net.Listener, error)) (string, error) {
	ln, err := listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("loopback port: %v", err)
	}
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), ln.Close()
}
