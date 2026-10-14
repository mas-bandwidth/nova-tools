// Package pg starts the throwaway Postgres the nova-config functional tests
// share, the way pkg/nsprint/testutil starts a throwaway Redis: initdb
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
	"errors"
	"fmt"
	"io"
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

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// BinEnv names a directory holding initdb, pg_ctl and postgres, for a
// machine whose install is somewhere the search below does not look.
const BinEnv = "NOVA_PG_BIN"

// wellKnown are the install directories searched after PATH: Homebrew's
// versioned kegs on macOS, Debian's and Ubuntu's versioned trees on Linux.
var wellKnown = []string{
	"/opt/homebrew/opt/postgresql@18/bin",
	"/opt/homebrew/opt/postgresql@16/bin",
	"/opt/homebrew/opt/postgresql@17/bin",
	"/opt/homebrew/opt/postgresql@15/bin",
	"/opt/homebrew/opt/postgresql@14/bin",
	"/usr/local/opt/postgresql@16/bin",
	"/usr/lib/postgresql/18/bin",
	"/usr/lib/postgresql/16/bin",
	"/usr/lib/postgresql/17/bin",
	"/usr/lib/postgresql/15/bin",
	"/usr/lib/postgresql/14/bin",
	"/usr/pgsql-16/bin",
	"/usr/pgsql-18/bin",
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
	return "", fmt.Errorf("pg_ctl (with initdb and postgres) is not on PATH, not in %s, and not under %s; install postgresql (go run ./tools/ci install-postgres)", BinEnv, strings.Join(wellKnown, ", "))
}

// THE SENTRY. A test's cleanup stops its postmaster, and a cleanup runs only
// when the test binary lives to run it. A binary that times out, calls
// os.Exit, panics off the test's goroutine or is killed runs none, and its
// postmaster would stand until someone stops it by hand. So StartServer enlists
// a sentry of its own: a second copy of the test binary under sentryEnv, its
// own process group, reading a stdin pipe this binary holds. The postmaster
// joins the group as it is forked, before it runs an instruction. When this
// binary is gone, however it went, the kernel closes the pipe, the read ends,
// and the sentry kills its group: postmaster and itself. The design and the
// words are pkg/testredis/sentry.go's, copied for the throwaway Postgres;
// without process groups (Windows) there is no sentry. See sentry_unix.go and
// sentry_other.go.

// sentryEnv marks a copy of the test binary as a sentry. It is set in the
// sentry's environment and nowhere else.
const sentryEnv = "NOVA_PG_SENTRY"

// sentryStands is the one line a sentry prints, once it is about to read.
const sentryStands = "the sentry stands\n"

// sentryWait is how long a sentry may take to say it stands, and how long
// Stop waits for it to end after the pipe closes.
const sentryWait = 30 * time.Second

// post is a sentry standing.
type post struct {
	group int             // its process group, which the postmaster joins
	gone  <-chan struct{} // closed when the sentry's process has ended
	hold  io.Closer       // the other end of its standard input, open for as long as this process lives
}

// alive reports whether the sentry is still standing. A server that cannot
// join a standing sentry is not started.
func (p *post) alive() error {
	if p == nil || p.group == 0 {
		return nil
	}
	select {
	case <-p.gone:
		return fmt.Errorf("the sentry %d has ended while this test binary runs; something killed it", p.group)
	default:
		return nil
	}
}

// stand is a sentry's whole life: say it stands, read until the test binary is
// gone, kill. It returns only when the kill left it alive, which is a test or
// a failure.
func stand(in io.Reader, out io.Writer, kill func() error) int {
	if _, err := io.WriteString(out, sentryStands); err != nil {
		// Nobody reads: the binary that enlisted this sentry is gone already,
		// before it could start a server.
		return 1
	}
	// The read ends when the pipe closes. An error ends it as well, and the
	// answer to a pipe that broke is the same as to one that closed.
	// ignored: a broken pipe and a closed one mean the same here (see the comment above); the kill below is checked
	_, _ = io.Copy(io.Discard, in)
	if err := kill(); err != nil {
		return 1
	}
	return 0
}

// Server is one running throwaway Postgres.
type Server struct {
	Dir  string
	Bin  string
	Port string
	// User is the superuser initdb made; trust authentication, no password.
	User   string
	next   atomic.Int64
	log    string
	cmd    *exec.Cmd     // the foreground postmaster
	exited chan struct{} // closed when the foreground postmaster is gone
	ended  error         // what it ended with; read only after exited is closed
	sentry *post         // kills the postmaster when this test binary is gone
	stop   func() error
}

// DSN is the connection string of one database on the server, for pgx: a
// loopback address, trust authentication, no password anywhere.
func (s *Server) DSN(database string) string {
	return fmt.Sprintf("postgres://%s@127.0.0.1:%s/%s?sslmode=disable", s.User, s.Port, database)
}

// pgToolBudget is how long one initdb or pg_ctl call may run: pg_ctl start waits up to
// 60 s for the server itself, and the budget leaves room past that.
const pgToolBudget = 120 * time.Second

// StartServer runs initdb and a foreground postgres under dir on a free
// loopback port, the postmaster joined to a sentry of its own that kills it
// when this test binary is gone. The caller stops it with Stop. It is the
// TestMain form: one server for a package, one database per test through
// Database.
func StartServer(dir string) (*Server, error) {
	bin, err := Binaries()
	if err != nil {
		return nil, err
	}
	data := filepath.Join(dir, "data")
	logPath := filepath.Join(dir, "postgres.log")
	s := &Server{Dir: dir, Bin: bin, User: "postgres", log: logPath}
	s.stop = sync.OnceValue(s.stopRunning)
	initdb, stopInitdb := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(bin, "initdb"), "-D", data, "-U", s.User, "--auth=trust", "--no-sync", "-E", "UTF8", "--locale=C")
	defer stopInitdb()
	initdb.Env = cleanEnv()
	if out, err := initdb.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("initdb: %v\n%s", err, out)
	}
	at, err := enlist()
	if err != nil {
		return nil, fmt.Errorf("throwaway postgres: %w", err)
	}
	s.sentry = at
	// The socket directory is a fixed short path under the data directory's
	// parent: a macOS t.TempDir path is longer than a unix socket allows.
	// Listening is on loopback only; the socket directory is emptied so no
	// socket is made at all.
	var last string
	for attempt := 0; attempt < 5; attempt++ {
		port, err := freePort()
		if err != nil {
			return nil, errors.Join(err, s.closeSentry())
		}
		if err := s.launch(data, port, at.group); err == nil {
			s.Port = port
			return s, nil
		} else {
			body, _ := os.ReadFile(logPath)
			last = fmt.Sprintf("postgres start on port %s: %v\n%s", port, err, body)
			if !strings.Contains(string(body), "Address already in use") && !strings.Contains(string(body), "could not bind") {
				return nil, errors.Join(fmt.Errorf("%s", last), s.closeSentry())
			}
		}
	}
	return nil, errors.Join(fmt.Errorf("throwaway postgres did not start in five attempts: %s", last), s.closeSentry())
}

// launch runs postgres in the foreground under the given port, its output to
// the existing log file and the postmaster joined to the sentry's group. It
// returns once the server has said it is ready and answered a ping, or the
// error that stopped it.
func (s *Server) launch(data, port string, group int) error {
	if err := s.sentry.alive(); err != nil {
		return fmt.Errorf("the throwaway postgres cannot join its sentry, so it is not started: %w", err)
	}
	log, err := os.OpenFile(s.log, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() {
		// ignored: the child inherited this file descriptor; the parent's copy of a log file has nothing left to say
		_ = log.Close()
	}()
	args := []string{"-D", data, "-p", port, "-c", "listen_addresses=127.0.0.1", "-c", "unix_socket_directories=", "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off", "-c", "log_min_messages=warning"}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := subproc.Long(ctx, filepath.Join(s.Bin, "postgres"), args...)
	cmd.Env = cleanEnv()
	cmd.Stdout, cmd.Stderr = log, log
	join(cmd, group)
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("the throwaway postgres did not join its sentry (group %d): %w", group, err)
	}
	s.cmd = cmd
	s.exited = make(chan struct{})
	go func() {
		s.ended = cmd.Wait()
		cancel()
		close(s.exited)
	}()
	s.Port = port
	if err := s.ready(); err != nil {
		select {
		case <-s.exited:
		default:
			stop, stopStop := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(s.Bin, "pg_ctl"), "-D", data, "-m", "immediate", "-w", "stop")
			stop.Env = cleanEnv()
			if stop.Run() != nil {
				// ignored: the postmaster may have ended during pg_ctl; the wait below proves it is gone
				_ = cmd.Process.Kill()
			}
			stopStop()
			<-s.exited
		}
		return err
	}
	return nil
}

// readyLine is what postgres prints once it listens. The log line is the
// server's own word that it is up; the ping then proves it answers.
const readyLine = "database system is ready to accept connections"

// ready reads the log until it carries the readiness line, then pings the
// server.
func (s *Server) ready() error {
	db, err := sql.Open("pgx", s.DSN("postgres"))
	if err != nil {
		return err
	}
	defer func() {
		_ = db.Close() // ignored: closing a temporary connection opened only to check server readiness
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-s.exited:
			body, _ := os.ReadFile(s.log)
			return fmt.Errorf("postgres exited before ready: %v\n%s", s.ended, body)
		default:
		}
		body, _ := os.ReadFile(s.log)
		if !strings.Contains(string(body), readyLine) {
			if time.Now().After(deadline) {
				return fmt.Errorf("throwaway postgres on port %s did not report readiness:\n%s", s.Port, body)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if err := db.PingContext(ctx); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			body, _ := os.ReadFile(s.log)
			return fmt.Errorf("throwaway postgres on port %s did not answer: %v\n%s", s.Port, err, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Stop stops the server (immediate mode: nothing here is kept), waits for the
// foreground postmaster, then closes the sentry's pipe and waits for it.
func (s *Server) Stop() error {
	return s.stop()
}

func (s *Server) stopRunning() (result error) {
	defer func() { result = errors.Join(result, s.closeSentry()) }()
	stop, stopStop := subproc.CommandFor(context.Background(), pgToolBudget, filepath.Join(s.Bin, "pg_ctl"), "-D", filepath.Join(s.Dir, "data"), "-m", "immediate", "-w", "stop")
	defer stopStop()
	stop.Env = cleanEnv()
	if out, err := stop.CombinedOutput(); err != nil {
		if s.cmd != nil {
			// ignored: an already-ended postmaster is the same cleanup state; the wait below proves it is gone
			_ = s.cmd.Process.Kill()
		}
		if s.exited != nil {
			<-s.exited
		}
		return fmt.Errorf("pg_ctl stop: %v\n%s", err, out)
	}
	if s.exited != nil {
		<-s.exited
	}
	return nil
}

// closeSentry closes the sentry's pipe and waits for the sentry to end. It is
// idempotent and safe when there is no sentry (no process groups).
func (s *Server) closeSentry() error {
	if s.sentry == nil || s.sentry.group == 0 {
		return nil
	}
	if s.sentry.hold != nil {
		// ignored: closing an already-closed pipe still wakes the sentry, and a second close has nothing left to say
		_ = s.sentry.hold.Close()
	}
	select {
	case <-s.sentry.gone:
		return nil
	case <-time.After(sentryWait):
		return fmt.Errorf("the sentry %d did not end after its pipe closed", s.sentry.group)
	}
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
	defer func() {
		_ = db.Close() // ignored: closing a temporary connection used only for database setup
	}()
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
