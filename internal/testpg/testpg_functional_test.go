//go:build functional

package testpg

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The functional tier of the package: real Postgres servers.

// alive reports whether a process with that id exists.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer p.Release()
	return p.Signal(syscall.Signal(0)) == nil
}

// gone waits for the process to be gone, thirty seconds at most. pg_ctl
// detaches the server, so whoever inherited it reaps it, a moment after it
// ended.
func gone(pid int) bool {
	until := time.Now().Add(30 * time.Second)
	for alive(pid) {
		if time.Now().After(until) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

// kill is the test's own end to a server that should have been gone: a
// failing test leaves nothing behind either.
func kill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// refused reports whether nothing listens at addr.
func refused(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err == nil {
		_ = conn.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

// postmaster is the pid of the server of that directory, from the file the
// server itself wrote.
func postmaster(t *testing.T, dir string) int {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "data", "postmaster.pid"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	first, err := bufio.NewReader(f).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil || pid <= 0 {
		t.Fatalf("postmaster.pid opens with %q", first)
	}
	return pid
}

// running asks pg_ctl whether a server runs on that directory.
func running(t *testing.T, dir string) bool {
	t.Helper()
	bin, err := Binaries()
	if err != nil {
		t.Fatal(err)
	}
	status := exec.Command(filepath.Join(bin, "pg_ctl"), "-D", filepath.Join(dir, "data"), "status")
	status.Env = cleanEnv(os.Getenv, "")
	return status.Run() == nil
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// show is one setting of the server, as the server has it.
func show(t *testing.T, db *sql.DB, setting string) string {
	t.Helper()
	var value string
	if err := db.QueryRowContext(bounded(t), "SELECT current_setting($1)", setting).Scan(&value); err != nil {
		t.Fatalf("current_setting(%s): %v", setting, err)
	}
	return value
}

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

// TestBinariesFindsPostgresOnThisMachine: this test runs where the binaries
// are, and the directory Binaries names holds all three. The answer for a
// runner with none is the unit tier's.
func TestBinariesFindsPostgresOnThisMachine(t *testing.T) {
	t.Parallel()

	dir, err := Binaries()
	if err != nil {
		t.Fatalf("this test runs where the binaries are: %v", err)
	}
	if missing := lacks(dir); dir == "" || missing != "" {
		t.Fatalf("Binaries = %q, which holds no %s", dir, missing)
	}
}

// Four tests start a server at once (an initdb is two seconds of a core, so
// they are four and not two hundred). Each gets a server of its own: no port
// is given out while another test's server stands on it, and each server
// holds the one database its test made, in its own directory.
func TestFourStartsInParallelEachGetTheirOwnServer(t *testing.T) {
	t.Parallel()

	const tests = 4
	var mu sync.Mutex
	stands := map[string]int{} // port -> the test whose server stands on it
	dirs := map[string]bool{}
	pids := map[int]bool{}
	t.Run("all", func(t *testing.T) {
		for i := 0; i < tests; i++ {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				s := Start(t)
				pid := postmaster(t, s.Dir)
				mu.Lock()
				other, shared := stands[s.Port]
				stands[s.Port] = i
				dirs[s.Dir] = true
				pids[pid] = true
				mu.Unlock()
				if shared {
					t.Fatalf("test %d was given port %s while the server of test %d stands on it", i, s.Port, other)
				}
				// Registered after Start, so it runs before the server is
				// stopped: the port is free to give out only from then on.
				t.Cleanup(func() {
					mu.Lock()
					delete(stands, s.Port)
					mu.Unlock()
				})
				mine := s.Database(t)
				var made int
				if err := open(t, mine).QueryRowContext(bounded(t), "SELECT count(*) FROM pg_database WHERE datname ~ '^t[0-9]+_[0-9]+$'").Scan(&made); err != nil || made != 1 {
					t.Fatalf("the server of test %d holds %d databases a test made, %v; want its one", i, made, err)
				}
				if got := show(t, open(t, mine), "data_directory"); !sameFile(got, filepath.Join(s.Dir, "data")) {
					t.Fatalf("the server of test %d keeps its data in %s; want %s", i, got, filepath.Join(s.Dir, "data"))
				}
			})
		}
	})
	if len(dirs) != tests || len(pids) != tests {
		t.Fatalf("%d tests started servers in %d directories as %d processes; want %d of each", tests, len(dirs), len(pids), tests)
	}
	for pid := range pids {
		if !gone(pid) {
			kill(pid)
			t.Errorf("every test has ended and the server with pid %d is alive", pid)
		}
	}
}

func sameFile(a, b string) bool {
	x, err1 := os.Stat(a)
	y, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(x, y)
}

func TestCleanupStopsTheServerAndRemovesItsDirectory(t *testing.T) {
	t.Parallel()

	var s *Server
	var pid int
	t.Run("a test that starts a server", func(t *testing.T) {
		s = Start(t)
		pid = postmaster(t, s.Dir)
		if !alive(pid) || !running(t, s.Dir) {
			t.Fatalf("the server with pid %d is not alive in its own test", pid)
		}
		if _, err := open(t, s.Database(t)).ExecContext(bounded(t), "CREATE TABLE kept (n int)"); err != nil {
			t.Fatal(err)
		}
	})
	if !gone(pid) {
		kill(pid)
		t.Fatalf("the test has ended and its server, pid %d, is alive", pid)
	}
	if _, err := os.Stat(s.Dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the test has ended and its server's directory %s is there: %v", s.Dir, err)
	}
	if !refused(net.JoinHostPort("127.0.0.1", s.Port)) {
		t.Fatalf("the test has ended and port %s still takes clients", s.Port)
	}
}

// StartServer is the TestMain form: the caller names the directory and stops
// the server itself, in the middle of a test when the test is of a store
// that cannot be reached.
func TestStopMakesTheNextQueryFailAtOnce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s, err := StartServer(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Whatever this test comes to, its server is stopped.
	defer func() {
		if err := s.Stop(); err != nil {
			t.Errorf("the last Stop: %v", err)
		}
	}()
	pid := postmaster(t, dir)
	if s.Dir != dir || s.User != "postgres" || !filepath.IsAbs(s.Bin) {
		t.Fatalf("the server is in %q, of user %q, from %q; want the caller's directory, postgres and the binaries", s.Dir, s.User, s.Bin)
	}
	for _, kept := range []string{"data", "postgres.log"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("the server's %s is not under the caller's directory: %v", kept, err)
		}
	}
	db := open(t, s.Database(t))
	ctx := bounded(t)
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}

	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if !gone(pid) || running(t, dir) {
		t.Fatalf("Stop returned and the server, pid %d, is alive", pid)
	}
	// The failure is the kernel's answer, not a deadline that ran out.
	if err := db.PingContext(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a query after Stop = %v; want a connection that ended", err)
	}
	if !refused(net.JoinHostPort("127.0.0.1", s.Port)) {
		t.Fatalf("after Stop port %s still takes clients", s.Port)
	}
	if err := open(t, s.DSN("postgres")).PingContext(ctx); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("a query from a new client after Stop = %v; want connection refused", err)
	}
	// A server that is stopped stays stopped: a second Stop does nothing.
	if err := s.Stop(); err != nil {
		t.Fatalf("a second Stop = %v", err)
	}
	if r := provoke(t, func(tb testing.TB) { s.Database(tb) }); !strings.Contains(r.fatal, "create database t") {
		t.Fatalf("Database on a server that is stopped failed with %q; want the test failed", r.fatal)
	}
	// It does not answer, and ready says so at its bound, with the log.
	if err := s.ready(100 * time.Millisecond); err == nil || !strings.Contains(err.Error(), "did not answer in 100ms") || !strings.Contains(err.Error(), "database system is ready") {
		t.Fatalf("ready on a server that is stopped = %v; want its bound and the server's log", err)
	}
	// A Stop that fails says what pg_ctl said.
	never := &Server{Dir: t.TempDir(), Bin: s.Bin}
	if err := never.Stop(); err == nil || !strings.HasPrefix(err.Error(), "pg_ctl stop: ") {
		t.Fatalf("Stop of a server that never was = %v; want pg_ctl's failure", err)
	}
}

// The server listens on 127.0.0.1 and its port. It is asked, and every other
// address of this machine is tried.
func TestTheServerListensOnLoopbackAndNowhereElse(t *testing.T) {
	t.Parallel()

	s := Start(t)
	db := open(t, s.DSN("postgres"))
	for setting, want := range map[string]string{
		"listen_addresses":        "127.0.0.1",
		"unix_socket_directories": "",
		"port":                    s.Port,
		"fsync":                   "off",
	} {
		if got := show(t, db, setting); got != want {
			t.Errorf("%s = %q; want %q", setting, got, want)
		}
	}
	if got := show(t, db, "data_directory"); !sameFile(got, filepath.Join(s.Dir, "data")) {
		t.Errorf("data_directory = %q; want the caller's %s", got, filepath.Join(s.Dir, "data"))
	}
	var from string
	if err := db.QueryRowContext(bounded(t), "SELECT host(inet_client_addr())").Scan(&from); err != nil || from != "127.0.0.1" {
		t.Errorf("the server sees its client at %q, %v; want 127.0.0.1", from, err)
	}
	sockets, err := filepath.Glob(filepath.Join(s.Dir, "data", ".s.PGSQL.*"))
	if err != nil || len(sockets) != 0 {
		t.Errorf("the server made sockets %v, %v; want none", sockets, err)
	}

	// Every other address this machine has, on the server's port, all at
	// once. An address that refuses is an address the server does not listen
	// on, and nothing is sent to whatever answers. An address that neither
	// answers nor refuses (a tunnel's) is given up after two seconds; a
	// listener on this machine answers from the kernel, at any load.
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	others := []string{"::1"}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			others = append(others, ipn.IP.String())
		}
	}
	if len(others) == 1 {
		t.Log("this machine has no address but loopback; the server was asked, the addresses could not be tried")
	}
	var dials sync.WaitGroup
	for _, other := range others {
		dials.Add(1)
		go func() {
			defer dials.Done()
			if conn, err := net.DialTimeout("tcp", net.JoinHostPort(other, s.Port), 2*time.Second); err == nil {
				_ = conn.Close()
				t.Errorf("something takes clients on %s; the port is this server's, and it is to listen on 127.0.0.1 alone", net.JoinHostPort(other, s.Port))
			}
		}()
	}
	dials.Wait()
}

// THE PORT IS TAKEN, CLOSED AND HANDED OVER. Here the port handed over is one
// something else holds: the server loses it, the start fails, and the next
// port is taken. Nothing is said to whatever holds the port.
func TestAStartThatLostItsPortIsMadeAgainOnAnother(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, held, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	l := real
	var handed []string
	l.port = func() (string, error) {
		if len(handed) == 0 {
			handed = append(handed, held)
			return held, nil
		}
		port, err := real.port()
		handed = append(handed, port)
		return port, err
	}
	s := l.start(t)
	if len(handed) != 2 || s.Port != handed[1] || s.Port == held {
		t.Fatalf("ports handed over: %v, the server is on %s; want the held port %s left and the next one taken", handed, s.Port, held)
	}
	if got := show(t, open(t, s.DSN("postgres")), "port"); got != s.Port {
		t.Fatalf("the server says its port is %s; Start says %s", got, s.Port)
	}
	if err := ln.(*net.TCPListener).SetDeadline(time.Now()); err != nil {
		t.Fatal(err)
	}
	if conn, err := ln.Accept(); err == nil {
		_ = conn.Close()
		t.Fatalf("Start called on %s, a port its server did not hold", ln.Addr())
	}
}

func TestAStartThatLosesEveryPortIsAnErrorThatSaysSo(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, held, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	l := real
	l.tries = 2
	taken := 0
	l.port = func() (string, error) { taken++; return held, nil }
	dir := t.TempDir()
	s, err := l.startServer(dir)
	if s != nil || err == nil {
		t.Fatalf("StartServer = %v, %v; want an error", s, err)
	}
	for _, want := range []string{"throwaway postgres did not start in 2 attempts", "pg_ctl start on port " + held, "could not bind"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
	if taken != 2 || running(t, dir) {
		t.Fatalf("%d ports were taken, a server runs: %v; want 2, the tries, and none", taken, running(t, dir))
	}
}

// A start that failed and did not lose its port fails the same way on any
// port: it is returned as it is.
func TestAStartThatFailsForAnotherReasonIsNotMadeAgain(t *testing.T) {
	t.Parallel()

	l := real
	taken := 0
	l.port = func() (string, error) { taken++; return "0", nil }
	dir := t.TempDir()
	s, err := l.startServer(dir)
	if s != nil || err == nil || !strings.Contains(err.Error(), "pg_ctl start on port 0") || strings.Contains(err.Error(), "attempts") {
		t.Fatalf("StartServer on port 0 = %v, %v; want pg_ctl's failure, once", s, err)
	}
	if taken != 1 || running(t, dir) {
		t.Fatalf("%d ports were taken, a server runs: %v; want one and none", taken, running(t, dir))
	}

	// And with no port to take there is nothing to start.
	l.port = func() (string, error) { return "", errors.New("no descriptors left") }
	if s, err := l.startServer(t.TempDir()); s != nil || err == nil || !strings.Contains(err.Error(), "no descriptors left") {
		t.Fatalf("StartServer with no port = %v, %v; want the cause", s, err)
	}
}

func TestAnInitdbThatFailsIsAnErrorWithWhatItSaid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data", "somebody's"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := StartServer(dir)
	if s != nil || err == nil || !strings.HasPrefix(err.Error(), "initdb: ") || !strings.Contains(err.Error(), "exists but is not empty") {
		t.Fatalf("StartServer over a directory that holds something = %v, %v; want initdb's refusal", s, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "somebody's")); err != nil {
		t.Fatalf("what the directory held is gone: %v", err)
	}
}

// A server that started and does not answer in time is stopped, not left.
func TestAServerThatStartedAndDoesNotAnswerIsStopped(t *testing.T) {
	t.Parallel()

	l := real
	l.wait = time.Nanosecond
	dir := t.TempDir()
	s, err := l.startServer(dir)
	if s != nil || err == nil || !strings.Contains(err.Error(), "did not answer in 1ns") {
		t.Fatalf("StartServer with a nanosecond to answer in = %v, %v; want an error", s, err)
	}
	if running(t, dir) {
		if bin, err := Binaries(); err == nil {
			_ = (&Server{Dir: dir, Bin: bin}).Stop()
		}
		t.Fatal("the server that did not answer was left running")
	}
}

// A start pg_ctl gave up waiting for is an error, and the server it left
// starting is stopped, not left. pg_ctl -t 0 gives up before any server can
// be up.
func TestAStartThatPgCtlGaveUpOnIsStopped(t *testing.T) {
	t.Parallel()

	l := real
	l.up = 0
	taken := 0
	l.port = func() (string, error) { taken++; return real.port() }
	dir := t.TempDir()
	s, err := l.startServer(dir)
	if s != nil || err == nil || !strings.Contains(err.Error(), "server did not start in time") {
		t.Fatalf("StartServer that pg_ctl waits no time for = %v, %v; want pg_ctl's timeout", s, err)
	}
	if taken != 1 {
		t.Fatalf("%d ports were taken; want one: a timeout is not a lost port", taken)
	}
	// The server's own record: a server was there, pg_ctl stop took it and
	// removed its postmaster.pid. Asking pg_ctl status alone could race a
	// server that had not written the file yet.
	if !strings.Contains(err.Error(), "the server pg_ctl gave up waiting for was stopped") {
		t.Errorf("the error does not say the server was stopped:\n%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "postmaster.pid")); err == nil || running(t, dir) {
		if bin, err := Binaries(); err == nil {
			_ = (&Server{Dir: dir, Bin: bin}).Stop()
		}
		t.Fatal("the server pg_ctl gave up on was left running")
	}
}

// A lost port is read from this attempt's part of the log: pg_ctl -l
// appends, and an earlier attempt's "could not bind" is not this one's.
func TestAnEarlierLostPortIsNotReadAsThisAttempts(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, held, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	l := real
	var handed []string
	l.port = func() (string, error) {
		if len(handed) == 0 {
			handed = append(handed, held)
			return held, nil
		}
		// The second attempt fails for another reason: postgres refuses
		// the port before it tries to bind.
		handed = append(handed, "0")
		return "0", nil
	}
	dir := t.TempDir()
	s, err := l.startServer(dir)
	if s != nil || err == nil {
		t.Fatalf("StartServer = %v, %v; want an error", s, err)
	}
	if len(handed) != 2 || strings.Contains(err.Error(), "attempts") || !strings.Contains(err.Error(), "pg_ctl start on port 0") {
		t.Fatalf("ports handed over: %v; error:\n%v\nwant two, and the second attempt's failure returned as it is", handed, err)
	}
	if strings.Contains(err.Error(), "could not bind") {
		t.Fatalf("the second attempt's failure carries the first attempt's log:\n%v", err)
	}
	if running(t, dir) {
		t.Fatal("a server runs")
	}
}

// Start's cleanup says so when its Stop fails, and the test goes on.
func TestACleanupThatCannotStopSaysSo(t *testing.T) {
	t.Parallel()

	var logged []string
	var s *Server
	t.Run("a test whose server loses its binaries", func(t *testing.T) {
		r := &logs{TB: t, said: &logged}
		s = real.start(r)
		// The server is stopped by the test, and then cannot be found by
		// the cleanup: what is left to fail is pg_ctl itself.
		if err := s.Stop(); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.stopped = false
		s.Bin = filepath.Join(s.Dir, "no-binaries-here")
		s.mu.Unlock()
	})
	if len(logged) != 1 || !strings.Contains(logged[0], "stop throwaway postgres: pg_ctl stop: ") {
		t.Fatalf("the cleanup logged %q; want its Stop's failure", logged)
	}
}

// logs is a testing.TB that keeps what is logged.
type logs struct {
	testing.TB
	said *[]string
}

func (l *logs) Logf(format string, args ...any) {
	*l.said = append(*l.said, strings.TrimSpace(fmt.Sprintf(format, args...)))
}
