package tablemodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/redis/go-redis/v9"
)

// A failed check inside this package is a panic carrying a *Failure, so the
// long lists of assertions in the witnesses and the replay read as the
// statements they are. Every exported entry point recovers it into an error;
// no panic leaves the package.
type Failure struct{ Msg string }

func (f *Failure) Error() string { return f.Msg }

func fail(format string, args ...any) { panic(&Failure{Msg: fmt.Sprintf(format, args...)}) }

func assert(ok bool, format string, args ...any) {
	if !ok {
		fail(format, args...)
	}
}

// guard runs fn and returns the Failure it panicked with as an error.
func guard(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(*Failure)
			if !ok {
				panic(r)
			}
			err = f
		}
	}()
	fn()
	return nil
}

// CannotRun is an error of the environment, not of the check: a program that is
// missing or does not start, an input that cannot be read, a directory that
// cannot be made. A check that ran and said NO is a *Failure.
type CannotRun struct{ Err error }

func (c *CannotRun) Error() string { return c.Err.Error() }
func (c *CannotRun) Unwrap() error { return c.Err }

func cannotRun(err error) error { return &CannotRun{Err: err} }

// Server is a disposable redis-server: a Unix socket in a private directory,
// TCP off, nothing saved. Close kills it and removes the directory.
type Server struct {
	Dir    string
	Socket string
	cmd    *exec.Cmd
	client *redis.Client
	exited chan struct{}
}

// maxSocketPath is below the shortest sockaddr_un path of the platforms this
// runs on (104 bytes on macOS, 108 on Linux, one for the terminator).
const maxSocketPath = 100

type quiet struct{}

func (quiet) Printf(context.Context, string, ...any) {}

var quietOnce sync.Once

// StartServer runs the redis-server program in a new directory under tmp (the
// system temporary directory when empty) and connects to its socket. The
// server has this long to create the socket, and its output is kept in
// Dir/redis.log, which is what a caller reports when it does not come up.
func StartServer(ctx context.Context, program, tmp string, startup time.Duration) (*Server, error) {
	quietOnce.Do(func() { redis.SetLogger(quiet{}) })
	dir, err := os.MkdirTemp(tmp, "tablemodel-")
	if err != nil {
		return nil, cannotRun(err)
	}
	s := &Server{Dir: dir, Socket: filepath.Join(dir, "redis.sock")}
	if len(s.Socket) > maxSocketPath {
		removeDir(dir)
		return nil, cannotRun(fmt.Errorf("the store's socket path is %d bytes and a Unix socket path holds at most %d; set TMPDIR to a shorter directory", len(s.Socket), maxSocketPath))
	}
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	if err != nil {
		removeDir(dir)
		return nil, cannotRun(err)
	}
	defer log.Close()
	// A long-lived child: it runs under the suite's own context, which ends it when it
	// ends, and has no deadline of its own.
	s.cmd = subproc.Long(ctx, program, "--port", "0", "--unixsocket", s.Socket, "--unixsocketperm", "700", "--save", "", "--appendonly", "no")
	s.cmd.Dir = dir
	s.cmd.Stdout, s.cmd.Stderr = log, log
	if err := s.cmd.Start(); err != nil {
		removeDir(dir)
		return nil, cannotRun(fmt.Errorf("cannot start %s: %v", program, err))
	}
	exited := make(chan struct{})
	go func() { s.cmd.Wait(); close(exited) }()
	limit := time.NewTimer(startup)
	defer limit.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(s.Socket); err == nil {
			break
		}
		select {
		case <-exited:
			why := lastLine(filepath.Join(dir, "redis.log"))
			removeDir(dir)
			return nil, cannotRun(fmt.Errorf("disposable redis exited during startup: %s", why))
		case <-limit.C:
			s.kill(exited)
			return nil, cannotRun(fmt.Errorf("disposable redis did not create its socket within %s", startup))
		case <-ctx.Done():
			s.kill(exited)
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
	s.client = redis.NewClient(&redis.Options{
		Network: "unix", Addr: s.Socket, Protocol: 2,
		DialTimeout: 15 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second,
		MaxRetries: -1, PoolSize: 1, DisableIdentity: true,
		// A command's deadline is the context's: a read in progress ends with the
		// suite's budget, not fifteen seconds after it. The fixed timeouts above
		// bound a command on a context with no deadline.
		ContextTimeoutEnabled: true,
	})
	s.exited = exited
	return s, nil
}

// lastLine is the last non-empty line of a file, at most 200 bytes of it.
func lastLine(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "no output"
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return "no output"
	}
	if len(last) > 200 {
		last = last[:200]
	}
	return last
}

func (s *Server) kill(exited chan struct{}) {
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	<-exited
	s.remove()
}

// remove deletes the store's own directory, which sits strictly below the
// directory it was made in.
func (s *Server) remove() { removeDir(s.Dir) }

// removeDir deletes a directory this package made, which sits strictly below
// the directory it was made in.
// ignored: a scratch directory this package made; a leftover sits in its own temp root
func removeDir(dir string) { _ = safepath.RemoveUnder(filepath.Dir(dir), dir) }

// Close stops the server (terminate, then kill after five seconds) and removes
// its directory.
func (s *Server) Close() {
	if s.client != nil {
		s.client.Close()
	}
	s.cmd.Process.Signal(os.Interrupt)
	select {
	case <-s.exited:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-s.exited
	}
	s.remove()
}

// Store is a connection to the disposable server that reads replies the way
// the checks want them: a bulk or status reply is a string, an integer is an
// int64, an array is a []any, and a nil reply is nil. An error reply, or a
// failure of the connection, is a failed check.
type Store struct {
	ctx context.Context
	c   *redis.Client
}

// Store returns the server's connection; ctx bounds every command.
func (s *Server) Store(ctx context.Context) *Store { return &Store{ctx: ctx, c: s.client} }

// Cmd sends one command.
func (r *Store) Cmd(args ...any) any {
	if err := r.ctx.Err(); err != nil {
		fail("the check ran out of time: %v", err)
	}
	v, err := r.c.Do(r.ctx, args...).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		fail("redis %v: %v", args[0], err)
	}
	return normalize(v)
}

func normalize(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case redis.Error:
		fail("redis replied with an error: %v", x)
	}
	return v
}

// The typed reads. A reply of the wrong shape is a failed check, named.

func str(v any) string {
	s, ok := v.(string)
	assert(ok, "reply %v is not a string", v)
	return s
}

func integer(v any) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case string:
		n, err := strconv.Atoi(x)
		assert(err == nil, "reply %q is not an integer", x)
		return n
	}
	fail("reply %v is not an integer", v)
	return 0
}

func list(v any) []any {
	l, ok := v.([]any)
	assert(ok, "reply %v is not a list", v)
	return l
}

// pair is one field and value of a flat reply.
type pair struct{ k, v string }

// pairs reads a flat field, value, field, value reply in reply order.
func pairs(v any) []pair {
	flat := list(v)
	assert(len(flat)%2 == 0, "reply has an odd number of elements")
	out := make([]pair, 0, len(flat)/2)
	for i := 0; i < len(flat); i += 2 {
		out = append(out, pair{str(flat[i]), str(flat[i+1])})
	}
	return out
}

func pairMap(v any) map[string]string {
	m := map[string]string{}
	for _, p := range pairs(v) {
		m[p.k] = p.v
	}
	return m
}
