// Package testredis is a throwaway redis-server for one test.
//
// Start runs the redis-server on PATH as a child of the test binary and
// returns its address. What a test can rely on:
//
// WHAT IT LISTENS ON. One TCP listener on 127.0.0.1, the IPv4 loopback, on a
// port the kernel chose. No other interface, no IPv6, no TLS port and no Unix
// socket. The address is host:port and not a socket path because every caller
// hands it to a Redis client's Addr or to a tool's --redis flag, and both dial
// TCP. An extra argument that would move the listener is refused.
//
// IT IS THIS TEST'S ALONE. The port is taken from the kernel, closed and handed
// to redis-server, so another process can bind it in between. A server is
// therefore ready only when its OWN output says so, never because something
// answered on the port; one that lost its port exits and another port is
// taken. Many tests may call Start at once.
//
// NOTHING IS KEPT. --save "" and no append-only file, in the test's own
// t.TempDir(). A test that wants persistence asks for it in the extra
// arguments, and the files still land in that directory.
//
// IT DOES NOT OUTLIVE ITS TEST. The test's cleanup kills the server and waits
// until the process is gone, then the testing package removes the directory:
// after a pass, a failure or a panic in the test. When the test binary itself
// dies without its cleanups (a timeout, os.Exit, a kill) the sentry kills the
// server: see sentry.go. A server of a test binary that was killed leaves its
// directory, as every t.TempDir() of that binary does.
//
// A MISSING redis-server skips the test on a laptop and fails it under
// NOVA_CI=1, which the CI workflows set: a skip there would let a green run be
// a run that never executed the store. A redis-server that is found and does
// not come up fails the test anywhere.
//
// THE IMAGE OF A STORE. Image reads every key under a prefix, with its type
// and a sum of its content and expiry time, by SCAN and pipelines, from any
// server; Diff names the keys added, removed and changed between two images,
// in key order. A test that must show a step wrote nothing takes an image
// before the step and one after it and expects an empty Diff: see image.go.
//
// A STORE AT A DISTANCE. Far puts a proxy in front of a store a test owns,
// listening on the loopback, that holds each write of the client back by a
// delay before it forwards it, so a limit is judged at the distance the real
// store stands at and not at the loopback's. It stops with the test.
package testredis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// CIEnv is set to "1" by the CI workflows. A missing redis-server fails the
// test in that environment. Anywhere else it skips, so a laptop without the
// binary can still run the rest of the suite.
const CIEnv = "NOVA_CI"

// Absent is Start's answer when redis-server is not on PATH. CI fails closed.
// A skip here would let a green run be a run that never executed the store.
func Absent(t testing.TB, cause error) {
	t.Helper()
	absent(t, cause, os.Getenv)
}

func absent(t testing.TB, cause error, getenv func(string) string) {
	t.Helper()
	if cause == nil {
		t.Fatal("Absent requires the error from looking up redis-server")
	}
	if getenv(CIEnv) == "1" {
		t.Fatalf("redis-server is required under NOVA_CI=1: %v", cause)
	}
	t.Skipf("redis-server unavailable: %v", cause)
}

// Start runs a throwaway redis-server and returns its address, 127.0.0.1 and a
// port, once the server has said it is ready and has answered a PING there (a
// NOAUTH answer is a live server: the default user may be off). It listens on
// that one loopback TCP port and nowhere else, keeps nothing, and is killed by
// the test's cleanup.
//
// The extra arguments follow the fixed ones, so a test can add users
// (testredis.User), an ACL file, or persistence inside its own directory.
// Start refuses the ones that are its own: bind, port, tls-port and
// unixsocket (where it listens), dir and include (where its files are),
// logfile and loglevel (the output it reads), daemonize and supervised (the
// process it holds).
//
// A server that does not come up fails the test with the binary, the
// arguments and the last of its output. Start waits thirty seconds for one
// server and takes five ports before it gives up.
func Start(t testing.TB, extra ...string) string {
	t.Helper()
	return real.start(t, extra).Addr()
}

// StartServer is Start with the server in hand, for a test that stops it
// before the test ends.
func StartServer(t testing.TB, extra ...string) *Server {
	t.Helper()
	return real.start(t, extra)
}

// Program is the redis-server on PATH, or Absent's answer when there is none.
// A test that launches the server through its own production path (nova-redis
// serve) takes the program from here, so the missing-binary rule stays one.
func Program(t testing.TB) string {
	t.Helper()
	return real.program(t)
}

// FreePort is a loopback port that was free a moment ago, as text. The kernel
// chose it. Redis treats --port 0 as "do not listen", so a test that starts a
// server itself takes the port here and hands it over.
func FreePort(t testing.TB) string {
	t.Helper()
	return real.freePort(t)
}

// User is the extra arguments of a server that requires a login: the default
// user off, and one named user with a password and every permission. A client
// that does not log in is answered NOAUTH.
func User(name, password string) []string {
	return []string{"--user", "default", "off", "--user", name, "on", ">" + password, "~*", "&*", "+@all"}
}

// Server is one running throwaway redis-server.
type Server struct {
	addr   string
	dir    string
	cmd    *exec.Cmd
	out    *tail
	exited chan struct{}
	ended  error // what the process ended with; read only after exited is closed
	stop   sync.Once
}

// Addr is the server's address, 127.0.0.1 and its port. It stays the same
// after Stop, when nothing listens there any more.
func (s *Server) Addr() string { return s.addr }

// PID is the server's process id.
func (s *Server) PID() int { return s.cmd.Process.Pid }

// Stop kills the server and returns when the process is gone, so the next
// command to its address is refused at once: the unreachable store of a test.
// The test's cleanup calls it too; a second call only waits.
func (s *Server) Stop() {
	s.stop.Do(func() {
		// An error here is a process that has already ended, which is the
		// state Stop is asked for; the wait below is the proof either way.
		// ignored: a process that has already ended is the state Stop is asked for (see the comment above); the wait is the proof
		_ = s.cmd.Process.Kill()
	})
	<-s.exited
}

// launch is everything Start takes from outside itself, so a test can stand
// in for any of it without touching the process's environment.
type launch struct {
	inTest func() bool                       // testing.Testing
	look   func(file string) (string, error) // exec.LookPath
	getenv func(key string) string           // os.Getenv
	port   func() (string, error)            // a loopback port that is free now
	sentry *sentry                           // kills the servers of a test binary that is gone
	wait   time.Duration                     // how long one server may take to come up
	tries  int                               // how many ports are taken before Start gives up
}

// real is what Start runs with.
var real = launch{
	inTest: testing.Testing,
	look:   exec.LookPath,
	getenv: os.Getenv,
	port:   func() (string, error) { return freePort(net.Listen) },
	sentry: &sentry{enlist: func() (*post, error) { return enlist(realSentry) }},
	wait:   30 * time.Second,
	tries:  5,
}

// ioWait bounds the wait for a killed server's output to end. A child the
// server forked can hold the pipe open after the server is gone.
const ioWait = 5 * time.Second

// refuse panics outside a test binary. The package starts servers for tests
// and for nothing else; a tool that imported it would be a tool that can
// start a store nobody asked for.
func (l launch) refuse() {
	if !l.inTest() {
		panic("testredis: used outside a test binary; it starts servers for tests and for nothing else")
	}
}

func (l launch) program(t testing.TB) string {
	t.Helper()
	l.refuse()
	bin, err := l.look("redis-server")
	if err != nil {
		absent(t, err, l.getenv)
	}
	return bin
}

func (l launch) freePort(t testing.TB) string {
	t.Helper()
	l.refuse()
	port, err := l.port()
	if err != nil {
		t.Fatalf("testredis: %v", err)
	}
	return port
}

// freePort takes a loopback port from the kernel and gives it back.
func freePort(listen func(network, address string) (net.Listener, error)) (string, error) {
	ln, err := listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("no loopback port: %w", err)
	}
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), ln.Close()
}

// start is Start. THE PORT IS TAKEN, CLOSED AND HANDED OVER, so another
// process can bind it in between: with a package's tests in parallel that is
// another test's redis-server. Ours then exits on "Address already in use"
// while a PING to the port is answered by the other one, and a test that took
// the answer for readiness would silently share a store. So run reads the
// server's own output, and a server that exited is started again on a fresh
// port.
func (l launch) start(t testing.TB, extra []string) *Server {
	t.Helper()
	l.refuse()
	if err := check(extra); err != nil {
		t.Fatalf("testredis: %v", err)
	}
	bin := l.program(t)
	group, err := l.sentry.group()
	if err != nil {
		t.Fatalf("testredis: no server is started without its sentry: %v", err)
	}
	dir := t.TempDir()
	for try := 1; ; try++ {
		s, up := l.run(t, bin, dir, l.freePort(t), extra, group)
		if up {
			return s
		}
		if try >= l.tries {
			t.Fatalf("testredis: %s exited before it was ready, %d times of %d; the last time: %v\narguments: %s\nlast output:\n%s",
				bin, try, l.tries, s.ended, commandLine(redact(s.cmd.Args[1:])), s.out)
		}
	}
}

// run is one server on one port. It reports whether the server is up; one
// that is not has exited before it was ready, and Start takes another port.
// A server that stays and does not come up fails the test here.
func (l launch) run(t testing.TB, bin, dir, port string, extra []string, group int) (*Server, bool) {
	t.Helper()
	args := arguments(dir, port, extra)
	// A long-lived child: a cancellable context and no deadline, released when the wait
	// returns; Stop is what ends it.
	ctx, release := context.WithCancel(context.Background())
	s := &Server{
		addr:   net.JoinHostPort("127.0.0.1", port),
		dir:    dir,
		cmd:    subproc.Long(ctx, bin, args...),
		out:    &tail{ready: make(chan struct{})},
		exited: make(chan struct{}),
	}
	s.cmd.Dir = dir
	s.cmd.Stdout, s.cmd.Stderr = s.out, s.out
	s.cmd.WaitDelay = ioWait
	join(s.cmd, group)
	until := time.Now().Add(l.wait)
	if err := s.cmd.Start(); err != nil {
		release()
		t.Fatalf("testredis: %s did not start: %v\narguments: %s", bin, err, commandLine(redact(args)))
	}
	go func() {
		s.ended = s.cmd.Wait()
		release()
		close(s.exited)
	}()
	t.Cleanup(s.Stop)
	bound := time.NewTimer(l.wait)
	defer bound.Stop()
	select {
	case <-s.exited:
		return s, false
	case <-bound.C:
		s.Stop()
		t.Fatalf("testredis: %s did not come up within %v and was killed\narguments: %s\nlast output:\n%s",
			bin, l.wait, commandLine(redact(args)), s.out)
	case <-s.out.ready:
	}
	if err := ping(s.addr, until); err != nil {
		s.Stop()
		t.Fatalf("testredis: %s said it was ready and did not answer at %s: %v\narguments: %s\nlast output:\n%s",
			bin, s.addr, err, commandLine(redact(args)), s.out)
	}
	return s, true
}

// ping sends one PING and reads one line. PONG is a server; so is NOAUTH, the
// answer of a server whose default user is off.
func ping(addr string, until time.Time) error {
	conn, err := net.DialTimeout("tcp", addr, time.Until(until))
	if err != nil {
		return err
	}
	defer conn.Close()
	bound := conn.SetDeadline(until)
	_, sent := io.WriteString(conn, "PING\r\n")
	line, read := bufio.NewReader(conn).ReadString('\n')
	if err := errors.Join(bound, sent, read); err != nil {
		return err
	}
	if strings.HasPrefix(line, "+PONG") || strings.HasPrefix(line, "-NOAUTH") {
		return nil
	}
	return fmt.Errorf("it answered PING with %q", strings.TrimSpace(line))
}

// arguments is the server's command line: the fixed arguments, then the
// test's.
func arguments(dir, port string, extra []string) []string {
	return append([]string{"--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--dir", dir}, extra...)
}

// own are the options Start sets or depends on, and why a test may not.
var own = map[string]string{
	"bind":       "the server listens on 127.0.0.1 and nowhere else",
	"port":       "the port is the one the kernel chose",
	"tls-port":   "the server listens on one loopback TCP port and nowhere else",
	"unixsocket": "the server listens on one loopback TCP port and nowhere else",
	"dir":        "the server's files are in the test's own directory, removed with it",
	"include":    "a file of options can carry any of the others",
	"logfile":    "Start reads the server's output to know it is ready",
	"loglevel":   "Start reads the server's output to know it is ready",
	"daemonize":  "a server that detaches outlives its test",
	"supervised": "the test binary is the server's only supervisor",
}

// check refuses the extra arguments that would undo what Start promises.
// redis-server appends every argument that opens with -- to its options as
// written, so such an argument is read the way the server reads it: line by
// line, the first word of a line the option.
func check(extra []string) error {
	for i, arg := range extra {
		text, option := strings.CutPrefix(arg, "--")
		if !option {
			if i == 0 {
				return fmt.Errorf("the extra arguments open with %q, which is not an option (--name): redis-server would read it as one more value of --dir", arg)
			}
			continue
		}
		for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
			words := strings.Fields(line)
			if len(words) == 0 {
				continue
			}
			if why, mine := own[strings.ToLower(words[0])]; mine {
				return fmt.Errorf("the extra argument --%s is refused: %s", words[0], why)
			}
		}
	}
	return nil
}

// redact is the arguments as a failure message may show them: a password is
// not in them. A test's password is a test value, and a failure message is
// still a log somebody else reads.
func redact(args []string) []string {
	out := make([]string, len(args))
	secret := false
	for i, arg := range args {
		text, option := strings.CutPrefix(arg, "--")
		switch {
		case option:
			// An option that carries its value in the same argument shows
			// its name and nothing of the value.
			name, inline := text, false
			if at := strings.IndexAny(text, " \t\r\n"); at >= 0 {
				name, inline = text[:at], true
			}
			out[i] = arg
			if inline {
				out[i] = "--" + name + " ***"
			}
			name = strings.ToLower(name)
			secret = !inline && (name == "requirepass" || name == "masterauth")
		case secret || strings.HasPrefix(arg, ">") || strings.HasPrefix(arg, "<") || strings.HasPrefix(arg, "#") || strings.HasPrefix(arg, "!"):
			out[i] = "***"
			secret = false
		default:
			out[i] = arg
		}
	}
	return out
}

// commandLine is the arguments on one line, every word quoted, so an empty
// argument (--save "") and one with a space in it read as what they are.
func commandLine(args []string) string {
	words := make([]string, len(args))
	for i, arg := range args {
		words[i] = strconv.Quote(arg)
	}
	return strings.Join(words, " ")
}
