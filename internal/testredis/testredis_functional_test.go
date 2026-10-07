//go:build functional

package testredis

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The functional tier of the package: real redis-servers, and the test binary
// itself standing in for a server that does not come up.

// shimLine is what the unit tier's redis-server says before it exits 86
// (ci.yml, "the unit tier refuses redis-server").
const shimLine = "unit tier: redis-server is functional-only (build tag functional)"

// init is the servers that do not come up. Start runs this test binary as its
// redis-server with `--fake <how>` after the fixed arguments, and the binary
// never reaches its tests:
//
//	exit    says the unit tier's line and exits 86
//	mute    says its pid and stays, never ready
//	deaf    says its pid and that it is ready, and listens nowhere
//	silent  says its pid on standard error, nothing on standard output, and stays
//
// The same binary with the same words is the sentry that does not stand.
func init() {
	at := slices.Index(os.Args, "--fake")
	if at < 0 || at+1 >= len(os.Args) {
		return
	}
	switch os.Args[at+1] {
	case "exit":
		fmt.Fprintln(os.Stderr, shimLine)
		os.Exit(86)
	case "mute":
		fmt.Printf("fake pid=%d\n", os.Getpid())
	case "deaf":
		fmt.Printf("fake pid=%d\n%d:M 27 Sep 2026 18:39:09.517 * Ready to accept connections tcp\n", os.Getpid(), os.Getpid())
	case "silent":
		fmt.Fprintf(os.Stderr, "fake pid=%d\n", os.Getpid())
	}
	// Stay, the way a server does: on a listener nobody is told of.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(87)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			os.Exit(87)
		}
		_ = c.Close()
	}
}

// fake is Start with this test binary for its redis-server.
func fake() launch {
	l := real
	l.look = func(string) (string, error) { return os.Args[0], nil }
	return l
}

// fakePID reads the pid a fake printed out of a failure.
func fakePID(t *testing.T, failure string) int {
	t.Helper()
	m := regexp.MustCompile(`fake pid=(\d+)`).FindStringSubmatch(failure)
	if m == nil {
		require.NotNil(t, m, "the failure does not carry the server's output:\n%s", failure)
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		require.NoError(t, err, err)
	}
	return pid
}

// alive reports whether a process with that id exists.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer p.Release()
	return p.Signal(syscall.Signal(0)) == nil
}

// gone waits for the process to be gone, thirty seconds at most. A process
// this binary did not start is reaped by whoever inherited it, a moment after
// it was killed.
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

// kill is the test's own end to a process that should have been gone: a
// failing test leaves nothing behind either.
func kill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// refused reports whether nothing listens at addr. It is asked only of a port
// a live server holds: a port freed a moment ago can be a parallel test's.
func refused(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err == nil {
		_ = conn.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

// dial is a client of one server that asks once: a failure is the server's
// answer, not the third retry's.
func dial(t *testing.T, addr string, login ...string) *redis.Client {
	t.Helper()
	opts := &redis.Options{Addr: addr, MaxRetries: -1, DialerRetries: 1}
	if len(login) == 2 {
		opts.Username, opts.Password = login[0], login[1]
	}
	c := redis.NewClient(opts)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestAServerStartsAndAnswersPing(t *testing.T) {
	t.Parallel()

	addr := Start(t)
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		require.Failf(t, "", "Start = %q, %v; want 127.0.0.1 and a port", addr, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		require.Failf(t, "", "Start = %q; want a port", addr)
	}
	ctx := bounded(t)
	c := dial(t, addr)
	if got, err := c.Ping(ctx).Result(); err != nil || got != "PONG" {
		require.Failf(t, "", "PING = %q, %v", got, err)
	}
	if n, err := c.DBSize(ctx).Result(); err != nil || n != 0 {
		require.Failf(t, "", "a server just started holds %d keys, %v; want none", n, err)
	}
	// Nothing is kept, in the test's own directory.
	for option, want := range map[string]string{"save": "", "appendonly": "no"} {
		if got, err := c.ConfigGet(ctx, option).Result(); err != nil || got[option] != want {
			assert.Failf(t, "", "CONFIG GET %s = %q, %v; want %q", option, got[option], err, want)
		}
	}
	if bin := Program(t); !filepath.IsAbs(bin) {
		assert.Failf(t, "", "Program = %q; want the redis-server on PATH, by its whole path", bin)
	}
}

// Two hundred tests start a server at once, as many at a time as the run
// allows (-parallel). Each gets a server of its own: no address is given out
// while another test's server stands on it, and each server holds the one key
// its test wrote.
func TestTwoHundredStartsInParallelEachGetTheirOwnServer(t *testing.T) {
	t.Parallel()

	const tests = 200
	var mu sync.Mutex
	stands := map[string]int{} // address -> the test whose server stands on it
	dirs := map[string]bool{}
	pids := map[int]bool{}
	t.Run("all", func(t *testing.T) {
		for i := 0; i < tests; i++ {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()
				s := StartServer(t)
				mu.Lock()
				other, shared := stands[s.Addr()]
				stands[s.Addr()] = i
				dirs[s.dir] = true
				pids[s.PID()] = true
				mu.Unlock()
				if shared {
					require.False(t, shared, "test %d was given %s while the server of test %d stands on it", i, s.Addr(), other)
				}
				// Registered after Start, so it runs before the server is
				// stopped: the address is free to give out only from then on.
				t.Cleanup(func() {
					mu.Lock()
					delete(stands, s.Addr())
					mu.Unlock()
				})
				ctx := bounded(t)
				c := dial(t, s.Addr())
				mine := "test " + strconv.Itoa(i)
				if n, err := c.DBSize(ctx).Result(); err != nil || n != 0 {
					require.Failf(t, "", "the server of test %d holds %d keys before the test wrote one, %v", i, n, err)
				}
				if err := c.Set(ctx, "whose", mine, 0).Err(); err != nil {
					require.NoError(t, err, err)
				}
				if n, err := c.DBSize(ctx).Result(); err != nil || n != 1 {
					require.Failf(t, "", "the server of test %d holds %d keys, %v; want its one", i, n, err)
				}
				if got, err := c.Get(ctx, "whose").Result(); err != nil || got != mine {
					require.Failf(t, "", "the server of test %d says it is the server of %q, %v", i, got, err)
				}
			})
		}
	})
	if len(dirs) != tests || len(pids) != tests {
		require.Failf(t, "", "%d tests started servers in %d directories as %d processes; want %d of each", tests, len(dirs), len(pids), tests)
	}
	if len(stands) != 0 {
		require.Len(t, stands, 0, "every test has ended and %d servers still stand: %v", len(stands), stands)
	}
	for pid := range pids {
		if alive(pid) {
			assert.Failf(t, "", "every test has ended and the server with pid %d is alive", pid)
		}
	}
}

func TestCleanupKillsTheServerAndRemovesItsDirectory(t *testing.T) {
	t.Parallel()

	var s *Server
	t.Run("a test that starts a server", func(t *testing.T) {
		s = StartServer(t, "--appendonly", "yes")
		if !alive(s.PID()) {
			require.Failf(t, "", "the server with pid %d is not alive in its own test", s.PID())
		}
		c := dial(t, s.Addr())
		if err := c.Set(bounded(t), "kept", "on disk", 0).Err(); err != nil {
			require.NoError(t, err, err)
		}
		// The test asked for an append-only file: it is in the server's
		// directory, which is the test's.
		if kept, err := os.ReadDir(s.dir); err != nil || len(kept) == 0 {
			require.Failf(t, "", "the server's directory %s holds %d files, %v; want the append-only file", s.dir, len(kept), err)
		}
	})
	if alive(s.PID()) {
		kill(s.PID())
		require.Failf(t, "", "the test has ended and its server, pid %d, is alive", s.PID())
	}
	if _, err := os.Stat(s.dir); !errors.Is(err, fs.ErrNotExist) {
		require.Failf(t, "", "the test has ended and its server's directory %s is there: %v", s.dir, err)
	}
	// The port is not asked: the process that held it is gone, so the port
	// is anyone's, and a parallel test may take it and answer there.
}

func TestAServerWithAUserAndAPasswordRefusesTheDefaultUser(t *testing.T) {
	t.Parallel()

	const password = "the-password-of-this-test"
	addr := Start(t, User("bench", password)...)
	ctx := bounded(t)
	for name, c := range map[string]struct {
		login []string
		want  string
	}{
		"no login":                      {nil, "NOAUTH"},
		"the default user":              {[]string{"default", password}, "WRONGPASS"},
		"the password and no user":      {[]string{"", password}, "WRONGPASS"},
		"the user with another's word":  {[]string{"bench", "not-the-password"}, "WRONGPASS"},
		"the user and the password":     {[]string{"bench", password}, ""},
		"the user, a user nobody added": {[]string{"nobody", password}, "WRONGPASS"},
	} {
		err := dial(t, addr, c.login...).Set(ctx, "k", name, 0).Err()
		switch {
		case c.want == "" && err != nil:
			assert.Failf(t, "", "%s: SET = %v; want it taken", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			assert.Failf(t, "", "%s: SET = %v; want %s", name, err, c.want)
		}
	}
	if who, err := dial(t, addr, "bench", password).Do(ctx, "ACL", "WHOAMI").Text(); err != nil || who != "bench" {
		require.Failf(t, "", "ACL WHOAMI = %q, %v; want bench", who, err)
	}
}

func TestStopMakesTheNextCommandFailAtOnce(t *testing.T) {
	t.Parallel()

	s := StartServer(t)
	// One connection, held: asked again after Stop it answers for itself.
	// A go-redis client would not: its pool drops a connection the server
	// hung up on and dials a new one, to a port that is anyone's once the
	// server is gone.
	held, err := net.Dial("tcp", s.Addr())
	if err != nil {
		require.NoError(t, err, err)
	}
	defer held.Close()
	if deadline, ok := bounded(t).Deadline(); ok {
		if err := held.SetDeadline(deadline); err != nil {
			require.NoError(t, err, err)
		}
	}
	answers := bufio.NewReader(held)
	if _, err := io.WriteString(held, "*1\r\n$4\r\nPING\r\n"); err != nil {
		require.NoError(t, err, err)
	}
	if got, err := answers.ReadString('\n'); err != nil || got != "+PONG\r\n" {
		require.Failf(t, "", "PING before Stop was answered %q, %v; want PONG", got, err)
	}
	pid, addr := s.PID(), s.Addr()
	s.Stop()
	if alive(pid) {
		kill(pid)
		require.Failf(t, "", "Stop returned and the server, pid %d, is alive", pid)
	}
	if s.Addr() != addr || s.PID() != pid {
		require.Failf(t, "", "after Stop the server is %s, pid %d; it was %s, pid %d", s.Addr(), s.PID(), addr, pid)
	}
	// The held connection was hung up on: the failure is the kernel's
	// answer on this test's own socket, not a deadline that ran out. Nothing
	// here dials the port again: it was freed with the process, and a
	// parallel test may take it and answer there. The write may yet be
	// taken by the kernel; the read is the answer.
	_, _ = io.WriteString(held, "*1\r\n$4\r\nPING\r\n")
	if got, err := answers.ReadString('\n'); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		require.Failf(t, "", "PING on the held connection after Stop was answered %q, %v; want a connection that ended", got, err)
	}
	// A second Stop, and the cleanup's after it, only wait.
	s.Stop()
}

// The server listens on 127.0.0.1 and its port. It is asked, and every other
// address of this machine is tried.
func TestTheServerListensOnLoopbackAndNowhereElse(t *testing.T) {
	t.Parallel()

	s := StartServer(t)
	ctx := bounded(t)
	c := dial(t, s.Addr())
	host, port, err := net.SplitHostPort(s.Addr())
	if err != nil {
		require.NoError(t, err, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() || ip.IsUnspecified() {
		require.Failf(t, "", "the server's address is %s; want a loopback address", s.Addr())
	}
	for option, want := range map[string]string{"bind": "127.0.0.1", "port": port, "tls-port": "0", "unixsocket": "", "dir": ""} {
		got, err := c.ConfigGet(ctx, option).Result()
		if err != nil {
			require.NoError(t, err, "CONFIG GET %s: %v", option, err)
		}
		if option == "dir" {
			// The server's own spelling of the directory (/private/var for
			// /var on macOS): the same directory.
			asked, err1 := os.Stat(s.dir)
			told, err2 := os.Stat(got[option])
			if err1 != nil || err2 != nil || !os.SameFile(asked, told) {
				assert.Failf(t, "", "CONFIG GET dir = %q (%v); want the test's directory %q (%v)", got[option], err2, s.dir, err1)
			}
			continue
		}
		if got[option] != want {
			assert.Equal(t, want, got[option], "CONFIG GET %s = %q; want %q", option, got[option], want)
		}
	}

	// Every other address this machine has, on the server's port, all at
	// once. An address that refuses is an address the server does not listen
	// on. Nothing is sent to whatever answers: the server is asked, over
	// loopback, whether the connection reached it. An address that neither
	// answers nor refuses (a tunnel's) is given up after two seconds; a
	// listener on this machine answers from the kernel, at any load.
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		require.NoError(t, err, err)
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
	reached := make([]net.Conn, len(others))
	var dials sync.WaitGroup
	for i, other := range others {
		dials.Add(1)
		go func() {
			defer dials.Done()
			if conn, err := net.DialTimeout("tcp", net.JoinHostPort(other, port), 2*time.Second); err == nil {
				reached[i] = conn
			}
		}()
	}
	dials.Wait()
	for i, conn := range reached {
		if conn == nil {
			continue
		}
		from := conn.LocalAddr().String()
		// One command answered and the next is read after the server has
		// taken every client that had connected by then.
		if err := c.Ping(ctx).Err(); err != nil {
			require.NoError(t, err, err)
		}
		clients, err := c.ClientList(ctx).Result()
		_ = conn.Close()
		if err != nil {
			require.NoError(t, err, err)
		}
		if strings.Contains(clients, "addr="+from+" ") {
			assert.Failf(t, "", "the server took a client on %s, which is not loopback:\n%s", net.JoinHostPort(others[i], port), clients)
		}
	}
}

// THE PORT IS TAKEN, CLOSED AND HANDED OVER. Here the port handed over is one
// another test's server stands on: the second server loses it and exits, and
// everything that answers on that port is the first. Start is never given
// the first server's address for the second, and never asks the first
// anything.
func TestAServerThatLostItsPortIsNeverMistakenForTheOneThatHoldsIt(t *testing.T) {
	t.Parallel()

	first := StartServer(t)
	ctx := bounded(t)
	a := dial(t, first.Addr())
	if err := a.Set(ctx, "whose", "the first test's", 0).Err(); err != nil {
		require.NoError(t, err, err)
	}
	pings := func() string {
		t.Helper()
		stats, err := a.Info(ctx, "commandstats").Result()
		if err != nil {
			require.NoError(t, err, err)
		}
		return regexp.MustCompile(`cmdstat_ping:calls=\d+`).FindString(stats)
	}
	before := pings()
	if before != "cmdstat_ping:calls=1" {
		require.Equal(t, "cmdstat_ping:calls=1", before, "the first server was pinged %q as it started; want once, by its own Start", before)
	}

	_, held, err := net.SplitHostPort(first.Addr())
	if err != nil {
		require.NoError(t, err, err)
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
	second := l.start(t, nil)

	if second.Addr() == first.Addr() || second.PID() == first.PID() {
		require.Failf(t, "", "the second test was given the first test's server: %s, pid %d", second.Addr(), second.PID())
	}
	if len(handed) != 2 || second.Addr() != net.JoinHostPort("127.0.0.1", handed[1]) {
		require.Failf(t, "", "ports handed over: %v, the second server is at %s; want the held port left and the next one taken", handed, second.Addr())
	}
	b := dial(t, second.Addr())
	if n, err := b.DBSize(ctx).Result(); err != nil || n != 0 {
		require.Failf(t, "", "the second test's server holds %d keys, %v; want none: the first test's key is not its", n, err)
	}
	if got, err := a.Get(ctx, "whose").Result(); err != nil || got != "the first test's" {
		require.Failf(t, "", "the first server's key = %q, %v", got, err)
	}
	if after := pings(); after != before {
		require.Equal(t, before, after, "the first server was pinged again while the second started: %s; Start asked a server that is not its own", after)
	}
}

// The same with a port something else holds: Start says nothing to it.
func TestAPortThatSomethingElseHoldsIsLeftAlone(t *testing.T) {
	t.Parallel()

	Program(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		require.NoError(t, err, err)
	}
	defer ln.Close()
	_, held, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		require.NoError(t, err, err)
	}
	l := real
	asked := 0
	l.port = func() (string, error) {
		if asked++; asked == 1 {
			return held, nil
		}
		return real.port()
	}
	s := l.start(t, nil)
	if asked != 2 || s.Addr() == ln.Addr().String() {
		require.Failf(t, "", "%d ports were taken and the server is at %s; want two, and not the held %s", asked, s.Addr(), ln.Addr())
	}
	if err := dial(t, s.Addr()).Ping(bounded(t)).Err(); err != nil {
		require.NoError(t, err, err)
	}
	// Nobody called on the held port.
	if err := ln.(*net.TCPListener).SetDeadline(time.Now()); err != nil {
		require.NoError(t, err, err)
	}
	if conn, err := ln.Accept(); err == nil {
		_ = conn.Close()
		require.Failf(t, "", "Start called on %s, a port its server did not hold", ln.Addr())
	}
}

const childEnv = "NOVA_TESTREDIS_CHILD"

// again is this test binary once more, to run one test of it as a child.
func again(test string, env ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run", "^"+test+"$", "-test.count=1", "-test.v")
	cmd.Env = env
	return cmd
}

func TestStartWithNoRedisServerSkipsOnALaptopAndFailsUnderCI(t *testing.T) {
	t.Parallel()
	switch os.Getenv(childEnv) {
	case "Start":
		Start(t)
		require.Fail(t, "Start returned with no redis-server on PATH")
	case "Program":
		Program(t)
		require.Fail(t, "Program returned with no redis-server on PATH")
	}

	empty := t.TempDir()
	for name, c := range map[string]struct {
		env  []string
		want string
		skip bool
	}{
		"Start on a laptop":    {[]string{childEnv + "=Start"}, "redis-server unavailable", true},
		"Start with NOVA_CI=0": {[]string{childEnv + "=Start", "NOVA_CI=0"}, "redis-server unavailable", true},
		"Start under CI":       {[]string{childEnv + "=Start", "NOVA_CI=1"}, "redis-server is required under NOVA_CI=1", false},
		"Program on a laptop":  {[]string{childEnv + "=Program"}, "redis-server unavailable", true},
		"Program under CI":     {[]string{childEnv + "=Program", "NOVA_CI=1"}, "redis-server is required under NOVA_CI=1", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := append([]string{"PATH=" + empty, "TMPDIR=" + t.TempDir()}, c.env...)
			out, err := again("TestStartWithNoRedisServerSkipsOnALaptopAndFailsUnderCI", env...).CombinedOutput()
			if !strings.Contains(string(out), c.want) || !strings.Contains(string(out), "executable file not found") {
				require.Failf(t, "", "the child said nothing of %q and its cause:\n%s", c.want, out)
			}
			if skipped := strings.Contains(string(out), "--- SKIP"); skipped != c.skip || (err == nil) != c.skip {
				require.Failf(t, "", "the child skipped: %v, ended with %v; want skipped: %v\n%s", skipped, err, c.skip, out)
			}
		})
	}
}

func TestARedisServerThatCannotRunFailsTheTestWithItsName(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "redis-server")
	l := real
	l.look = func(string) (string, error) { return missing, nil }
	r := provoke(t, func(tb testing.TB) { l.start(tb, User("bench", "the-password")) })
	for _, want := range []string{"did not start", missing, `arguments: "--bind" "127.0.0.1" "--port"`, `"--user" "bench" "on" "***"`} {
		if !strings.Contains(r.fatal, want) {
			assert.Contains(t, r.fatal, want, "the failure lacks %q:\n%s", want, r.fatal)
		}
	}
	if strings.Contains(r.fatal, "the-password") || r.skipped != "" {
		require.Failf(t, "", "the failure carries the password, or the test skipped (%q):\n%s", r.skipped, r.fatal)
	}
}

// The unit tier's redis-server exits 86 with one line. Start fails the test
// with the binary, the arguments and that line, after it has taken every port
// it is allowed.
func TestAServerThatExitsFailsTheTestWithWhatItSaid(t *testing.T) {
	t.Parallel()

	l := fake()
	l.tries = 3
	taken := 0
	l.port = func() (string, error) { taken++; return real.port() }
	r := provoke(t, func(tb testing.TB) {
		l.start(tb, append(User("bench", "the-password"), "--fake", "exit"))
	})
	for _, want := range []string{
		os.Args[0] + " exited before it was ready, 3 times of 3",
		"exit status 86",
		`arguments: "--bind" "127.0.0.1" "--port" "`,
		`"--save" "" "--appendonly" "no" "--dir" "`,
		`"--user" "bench" "on" "***" "~*" "&*" "+@all" "--fake" "exit"`,
		"last output:\n" + shimLine,
	} {
		if !strings.Contains(r.fatal, want) {
			assert.Contains(t, r.fatal, want, "the failure lacks %q:\n%s", want, r.fatal)
		}
	}
	if strings.Contains(r.fatal, "the-password") || r.skipped != "" {
		require.Failf(t, "", "the failure carries the password, or the test skipped (%q):\n%s", r.skipped, r.fatal)
	}
	if taken != 3 {
		require.EqualValues(t, 3, taken, "Start took %d ports; want 3, its tries", taken)
	}
}

func TestAServerThatNeverComesUpIsKilledAtTheBound(t *testing.T) {
	t.Parallel()

	l := fake()
	l.wait = 300 * time.Millisecond
	taken := 0
	l.port = func() (string, error) { taken++; return real.port() }
	r := provoke(t, func(tb testing.TB) { l.start(tb, []string{"--fake", "mute"}) })
	for _, want := range []string{
		os.Args[0] + " did not come up within 300ms and was killed",
		`arguments: "--bind" "127.0.0.1" "--port" "`,
		"last output:\nfake pid=",
	} {
		if !strings.Contains(r.fatal, want) {
			assert.Contains(t, r.fatal, want, "the failure lacks %q:\n%s", want, r.fatal)
		}
	}
	if pid := fakePID(t, r.fatal); alive(pid) {
		kill(pid)
		require.Failf(t, "", "Start failed the test and left its server, pid %d, alive", pid)
	}
	if taken != 1 {
		require.EqualValues(t, 1, taken, "Start took %d ports for a server that stayed; want one: the bound is paid once", taken)
	}
}

func TestAServerThatSaysItIsReadyAndDoesNotAnswerFailsTheTest(t *testing.T) {
	t.Parallel()

	r := provoke(t, func(tb testing.TB) { fake().start(tb, []string{"--fake", "deaf"}) })
	for _, want := range []string{
		os.Args[0] + " said it was ready and did not answer at 127.0.0.1:",
		"connection refused",
		`arguments: "--bind" "127.0.0.1" "--port" "`,
		"Ready to accept connections tcp",
	} {
		if !strings.Contains(r.fatal, want) {
			assert.Contains(t, r.fatal, want, "the failure lacks %q:\n%s", want, r.fatal)
		}
	}
	if pid := fakePID(t, r.fatal); alive(pid) {
		kill(pid)
		require.Failf(t, "", "Start failed the test and left its server, pid %d, alive", pid)
	}
}

// A test binary that ends without its cleanups leaves no server: the sentry
// kills it. One that fails or panics in the test runs its cleanups, and the
// directory is gone as well.
func TestAServerDoesNotOutliveItsTestBinary(t *testing.T) {
	t.Parallel()
	if how := os.Getenv(childEnv); how != "" {
		s := StartServer(t)
		fmt.Printf("SERVER %d %s %s\n", s.PID(), s.Addr(), s.dir)
		switch how {
		case "fails":
			require.Fail(t, "the child fails, as it was asked to")
		case "panics":
			panic("the child panics, as it was asked to")
		case "panics off the test's goroutine":
			go panic("the child panics, as it was asked to")
		case "exits":
			os.Exit(3)
		}
		// The others stay until it happens to them.
		_, _ = io.Copy(io.Discard, os.Stdin)
		require.Fail(t, "the child's input ended and nothing had ended the child")
	}

	Program(t)
	for how, c := range map[string]struct {
		cleanups bool
		said     string
	}{
		"fails":                           {true, "the child fails, as it was asked to"},
		"panics":                          {true, "the child panics, as it was asked to"},
		"panics off the test's goroutine": {false, "the child panics, as it was asked to"},
		"exits":                           {false, ""},
		"is killed":                       {false, ""},
		"times out":                       {false, "test timed out after"},
	} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			var e ending
			if how != "times out" {
				e = endBadly(t, how)
			}
			// A test binary's timeout runs from its start, so one that fired
			// before the child had its server shows nothing: the child is
			// given longer.
			for _, timeout := range []string{"1s", "4s", "16s"} {
				if how != "times out" || e.pid != 0 {
					break
				}
				e = endBadly(t, how, "-test.timeout="+timeout)
			}
			if e.pid == 0 {
				require.NotEqualValues(t, 0, e.pid, "the child started no server\n%s", e.said)
			}
			defer kill(e.pid)
			if e.passed {
				require.Failf(t, "", "the child passed; it was to end badly\n%s", e.said)
			}
			if !strings.Contains(e.said, c.said) {
				require.Contains(t, e.said, c.said, "the child did not end the way it was asked to (%q):\n%s", c.said, e.said)
			}
			// The process is asked, not the port: once the server is gone
			// its port is anyone's, and a parallel test may take it.
			if !gone(e.pid) {
				require.Failf(t, "", "the test binary is gone and its server, pid %d, is alive", e.pid)
			}
			if _, err := os.Stat(e.dir); c.cleanups != errors.Is(err, fs.ErrNotExist) {
				require.Failf(t, "", "the server's directory %s: %v; the child ran its cleanups: %v", e.dir, err, c.cleanups)
			}
		})
	}
}

// ending is how a child ended and the server it had started.
type ending struct {
	pid       int
	addr, dir string
	passed    bool
	said      string
}

// endBadly runs this test binary as a child that starts a server and ends the
// way it is asked to.
func endBadly(t *testing.T, how string, flags ...string) ending {
	t.Helper()
	child := again("TestAServerDoesNotOutliveItsTestBinary",
		childEnv+"="+how, "PATH="+os.Getenv("PATH"), "TMPDIR="+t.TempDir())
	child.Args = append(child.Args, flags...)
	held, err := child.StdinPipe()
	if err != nil {
		require.NoError(t, err, err)
	}
	defer held.Close()
	stdout, err := child.StdoutPipe()
	if err != nil {
		require.NoError(t, err, err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		require.NoError(t, err, err)
	}
	server := make(chan string, 1)
	said := make(chan string, 1)
	go func() {
		var all strings.Builder
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			all.WriteString(lines.Text() + "\n")
			if strings.HasPrefix(lines.Text(), "SERVER ") {
				server <- lines.Text()
			}
		}
		close(server)
		said <- all.String()
	}()
	var e ending
	select {
	case line := <-server:
		// No line is a child that ended before it had a server.
		if _, err := fmt.Sscanf(line, "SERVER %d %s %s", &e.pid, &e.addr, &e.dir); err != nil && line != "" {
			assert.Failf(t, "", "the child said %q: %v", line, err)
		}
	case <-time.After(30 * time.Second):
		assert.Fail(t, "the child said nothing of a server in thirty seconds")
	}
	if how == "is killed" || e.pid == 0 {
		// The server stands, and its test binary is killed under it.
		if e.pid != 0 && (!alive(e.pid) || refused(e.addr)) {
			assert.Failf(t, "", "the child's server, pid %d at %s, does not stand", e.pid, e.addr)
		}
		_ = child.Process.Kill()
	}
	e.said = <-said
	e.passed = child.Wait() == nil
	e.said += stderr.String()
	return e
}

// The sentry itself: it stands, the servers are in its hands, and when its
// input closes it kills them and is gone.
func TestTheSentryKillsItsServersWhenItsInputCloses(t *testing.T) {
	t.Parallel()

	Program(t)
	at, err := enlist(realSentry)
	if err != nil {
		require.NoError(t, err, err)
	}
	if at.group <= 0 || !alive(at.group) {
		require.Failf(t, "", "the sentry's group is %d; want the pid of a process that is alive", at.group)
	}
	defer kill(at.group)
	l := real
	l.sentry = &sentry{enlist: func() (*post, error) { return at, nil }}
	var servers []*Server
	for i := 0; i < 3; i++ {
		s := l.start(t, nil)
		if err := dial(t, s.Addr()).Ping(bounded(t)).Err(); err != nil {
			require.NoError(t, err, err)
		}
		servers = append(servers, s)
	}

	// What the kernel does when the test binary is gone.
	if err := at.hold.Close(); err != nil {
		require.NoError(t, err, err)
	}
	select {
	case <-at.gone:
	case <-time.After(30 * time.Second):
		require.Fail(t, "the sentry's input closed thirty seconds ago and the sentry is alive")
	}
	for _, s := range servers {
		select {
		case <-s.exited:
		case <-time.After(30 * time.Second):
			require.Failf(t, "", "the sentry is gone and the server with pid %d is alive; nobody stopped it", s.PID())
		}
		// The process is asked, not the port: the port of a server that
		// has exited is anyone's, and a parallel test may take it.
		if alive(s.PID()) {
			assert.Failf(t, "", "the server with pid %d at %s outlived its sentry", s.PID(), s.Addr())
		}
	}

	// With the sentry gone no server is started: Start refuses, and a start
	// that did not ask could not join the group.
	if r := provoke(t, func(tb testing.TB) { l.start(tb, nil) }); !strings.Contains(r.fatal, "no server is started without its sentry") {
		require.Contains(t, r.fatal, "no server is started without its sentry", "Start after the sentry ended failed with %q; want the refusal", r.fatal)
	}
	r := provoke(t, func(tb testing.TB) {
		l.run(tb, Program(tb), tb.TempDir(), FreePort(tb), nil, at.group)
	})
	if !strings.Contains(r.fatal, "did not start") {
		require.Contains(t, r.fatal, "did not start", "a server started into the group of a sentry that is gone: %q; want the start to fail", r.fatal)
	}
}

func TestASentryThatDoesNotStandIsAnError(t *testing.T) {
	t.Parallel()

	self := func() (string, error) { return os.Args[0], nil }
	for name, c := range map[string]struct {
		spec sentrySpec
		want []string
	}{
		"the test binary is not found": {
			sentrySpec{exe: func() (string, error) { return "", errors.New("no /proc") }, wait: time.Minute},
			[]string{"was not found", "no /proc"},
		},
		"the test binary does not run": {
			sentrySpec{exe: func() (string, error) { return filepath.Join(t.TempDir(), "gone.test"), nil }, wait: time.Minute},
			[]string{"did not start", "gone.test"},
		},
		"it exits": {
			sentrySpec{exe: self, args: []string{"--fake", "exit"}, wait: time.Minute},
			[]string{"does not stand", "EOF", shimLine},
		},
		"it says something else": {
			sentrySpec{exe: self, args: []string{"--fake", "mute"}, wait: time.Minute},
			[]string{"does not stand", `it said "fake pid=`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			at, err := enlist(c.spec)
			if err == nil {
				kill(at.group)
				require.Failf(t, "", "enlist = a sentry in group %d; want an error", at.group)
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					assert.Failf(t, "", "the error lacks %q: %v", want, err)
				}
			}
			if m := regexp.MustCompile(`fake pid=(\d+)`).FindStringSubmatch(err.Error()); m != nil {
				if pid, _ := strconv.Atoi(m[1]); alive(pid) {
					kill(pid)
					require.Failf(t, "", "a sentry that does not stand was left alive, pid %d", pid)
				}
			}
		})
	}
}

func TestASentryThatSaysNothingIsKilledAtTheBound(t *testing.T) {
	t.Parallel()

	at, err := enlist(sentrySpec{
		exe:  func() (string, error) { return os.Args[0], nil },
		args: []string{"--fake", "silent"},
		wait: 300 * time.Millisecond,
	})
	if err == nil {
		kill(at.group)
		require.Failf(t, "", "enlist = a sentry in group %d; want an error", at.group)
	}
	if !strings.Contains(err.Error(), "does not stand") || !strings.Contains(err.Error(), "it said nothing in 300ms") {
		require.Failf(t, "", "enlist = %v; want the bound", err)
	}
	if pid := fakePID(t, err.Error()); alive(pid) {
		kill(pid)
		require.Failf(t, "", "a sentry that does not stand was left alive, pid %d", pid)
	}
}
