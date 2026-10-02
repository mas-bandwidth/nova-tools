//go:build functional

package main

import (
	"bytes"
	"context"
	"github.com/redis/go-redis/v9"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/stretchr/testify/require"
)

// relay stands between the tool and a throwaway store, so a test can take
// the store away from the tool's connections without touching the store: cut
// closes every connection (a restart, as the tool sees it), stop also stops
// listening (a store that is gone). With dropFcall set, the first FCALL is
// passed to the store and its reply is read from the store and never
// delivered: the tool's side is closed instead (a lost reply).
type relay struct {
	ln        net.Listener
	target    string
	dropFcall *atomic.Bool
	accepted  atomic.Int64

	mu    sync.Mutex
	pairs []*pair
}

// pair is one connection through the relay: the tool's side, the store's,
// and done, closed when both goroutines that carry its bytes have returned.
// A Close of a connection another goroutine is reading returns before the
// socket is closed (Go closes it when that Read returns), so cut waits on
// done: when cut returns, the tool's side has been sent its end.
type pair struct {
	c, s net.Conn
	drop atomic.Bool
	left atomic.Int32
	done chan struct{}
}

func (p *pair) close() { _ = p.c.Close(); _ = p.s.Close() }

func (p *pair) end() {
	p.close()
	if p.left.Add(-1) == 0 {
		close(p.done)
	}
}

func startRelay(t *testing.T, target string, dropFcall *atomic.Bool) *relay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "%v", err)
	r := &relay{ln: ln, target: target, dropFcall: dropFcall}
	go r.serve()
	t.Cleanup(r.stop)
	return r
}

func (r *relay) addr() string { return r.ln.Addr().String() }

func (r *relay) serve() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		r.accepted.Add(1)
		s, err := net.Dial("tcp", r.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p := &pair{c: c, s: s, done: make(chan struct{})}
		p.left.Store(2)
		r.mu.Lock()
		r.pairs = append(r.pairs, p)
		r.mu.Unlock()
		go r.up(p)
		go r.down(p)
	}
}

// up carries the tool's bytes to the store, marking the first FCALL.
func (r *relay) up(p *pair) {
	defer p.end()
	buf := make([]byte, 64<<10)
	var tail []byte
	for {
		n, err := p.c.Read(buf)
		if n > 0 {
			seen := append(tail, buf[:n]...)
			if r.dropFcall != nil && bytes.Contains(seen, []byte("\r\nfcall\r\n")) && r.dropFcall.CompareAndSwap(false, true) {
				p.drop.Store(true)
			}
			if len(seen) > 16 {
				seen = seen[len(seen)-16:]
			}
			tail = append([]byte(nil), seen...)
			if _, werr := p.s.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// down carries the store's bytes to the tool, unless the reply is the one
// to lose: read from the store, proving the write reached it, then dropped.
func (r *relay) down(p *pair) {
	defer p.end()
	buf := make([]byte, 64<<10)
	for {
		n, err := p.s.Read(buf)
		if n > 0 {
			if p.drop.Load() {
				return
			}
			if _, werr := p.c.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// cut closes every connection through the relay and keeps listening. It
// returns when every socket it closed is closed.
func (r *relay) cut() {
	r.mu.Lock()
	pairs := r.pairs
	r.pairs = nil
	r.mu.Unlock()
	for _, p := range pairs {
		p.close()
	}
	for _, p := range pairs {
		<-p.done
	}
}

// stop closes the listener and every connection.
func (r *relay) stop() {
	_ = r.ln.Close()
	r.cut()
}

// oneFailureLine checks the shape every dial and login failure has: exit 2,
// nothing on stdout, one stderr line with redisconn's next step, and no line
// of go-redis's own pool log.
func oneFailureLine(t *testing.T, what string, code int, stdout, stderr string, want ...string) {
	t.Helper()
	require.EqualValues(t, 2, code, "%s: exit %d stdout %q stderr %q", what, code, stdout, stderr)
	require.Empty(t, stdout, "%s: exit %d stdout %q stderr %q", what, code, stdout, stderr)
	require.EqualValues(t, 1, strings.Count(stderr, "\n"), "%s: exit %d stdout %q stderr %q", what, code, stdout, stderr)
	require.True(t, strings.HasSuffix(stderr, "\n"), "%s: exit %d stdout %q stderr %q", what, code, stdout, stderr)
	require.NotContains(t, stderr, "pool.go", "%s: exit %d stdout %q stderr %q", what, code, stdout, stderr)
	require.Contains(t, stderr, "; next: ", "%s: exit %d stdout %q stderr %q", what, code, stdout, stderr)
	require.NotContains(t, stderr, "; run: nova-table help", "%s: exit %d stdout %q stderr %q", what, code, stdout, stderr)
	for _, w := range want {
		require.Contains(t, stderr, w, "%s: stderr %q wants %q", what, stderr, w)
	}
}

// TestUnreachableStoreIsOneLine: nothing listening at a port, and a socket
// path that is not there, each refuse a verb in one line, exit 2.
func TestUnreachableStoreIsOneLine(t *testing.T) {
	t.Parallel()
	port := "127.0.0.1:" + testredis.FreePort(t)
	code, stdout, stderr := runTable("list", "--redis", port)
	oneFailureLine(t, "closed port", code, stdout, stderr, "nova-table list: redis at "+port, ": unreachable: ", "connection refused", "next: start the store or correct the address")

	sock := t.TempDir() + "/absent.sock"
	code, stdout, stderr = runTable("show", "demo", "--redis", sock)
	oneFailureLine(t, "absent socket", code, stdout, stderr, "nova-table show: redis at "+sock, ": unreachable: ")

	// The shell dials nothing on entry; the first line that needs the store
	// fails in the same one line, then names its input line.
	var out, errs bytes.Buffer
	code = (&application{in: strings.NewReader("version\nlist\n")}).run([]string{"shell", "--redis", port}, &out, &errs)
	lines := strings.Split(strings.TrimSuffix(errs.String(), "\n"), "\n")
	require.EqualValues(t, 2, code, "shell: exit %d stdout %q stderr %q", code, &out, &errs)
	require.Len(t, lines, 2, "shell: exit %d stdout %q stderr %q", code, &out, &errs)
	require.Contains(t, lines[0], ": unreachable: ", "shell: exit %d stdout %q stderr %q", code, &out, &errs)
	require.Equal(t, "nova-table shell: line 2 failed (exit 2)", lines[1], "shell: exit %d stdout %q stderr %q", code, &out, &errs)
	require.NotContains(t, errs.String(), "pool.go", "shell: exit %d stdout %q stderr %q", code, &out, &errs)
}

// noEnv is an empty environment: the default user, no password.
func noEnv(string) string { return "" }

// TestRefusedLoginIsOneLine: a store that wants a login, given the wrong
// password, refuses the verb in one line, exit 2, naming the user and the
// variable and never the password; the right password is let in. The
// environment is the test's own (application.getenv), so it runs in
// parallel.
func TestRefusedLoginIsOneLine(t *testing.T) {
	t.Parallel()
	const user, right, wrong = "tabler", "synthetic-right-7f3a", "synthetic-wrong-51c9"
	addr := testredis.Start(t, testredis.User(user, right)...)
	admin := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: right})
	defer admin.Close()
	require.NoError(t, fn.Load(context.Background(), admin))
	env := map[string]string{
		"NOVA_SPRINT_REDIS_USER":         user,
		"NOVA_SPRINT_REDIS_PASSWORD_ENV": "NOVA_TABLE_TEST_PASSWORD",
	}
	runAs := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		code := (&application{getenv: func(k string) string { return env[k] }}).run(args, &out, &errs)
		return code, out.String(), errs.String()
	}

	env["NOVA_TABLE_TEST_PASSWORD"] = wrong
	code, stdout, stderr := runAs("list", "--redis", addr)
	oneFailureLine(t, "wrong password", code, stdout, stderr, "as user "+user+" (password from NOVA_TABLE_TEST_PASSWORD)", ": login refused: ", "WRONGPASS")
	require.NotContains(t, stderr, wrong, "a password reached stderr: %q", stderr)
	require.NotContains(t, stderr, right, "a password reached stderr: %q", stderr)

	env["NOVA_TABLE_TEST_PASSWORD"] = ""
	code, stdout, stderr = runAs("list", "--redis", addr)
	oneFailureLine(t, "empty password", code, stdout, stderr, "NOVA_TABLE_TEST_PASSWORD is empty")

	// The shell refuses an empty password on entering, before any line.
	var shellOut, shellErrs bytes.Buffer
	code = (&application{in: strings.NewReader("version\nlist\n"), getenv: func(k string) string { return env[k] }}).run([]string{"shell", "--redis", addr}, &shellOut, &shellErrs)
	oneFailureLine(t, "shell, empty password", code, shellOut.String(), shellErrs.String(), "nova-table shell: ", "NOVA_TABLE_TEST_PASSWORD is empty")

	delete(env, "NOVA_SPRINT_REDIS_USER")
	code, stdout, stderr = runAs("list", "--redis", addr)
	oneFailureLine(t, "no login", code, stdout, stderr, "as the default user, no password", ": login refused: ", "NOAUTH", "name the user (NOVA_SPRINT_REDIS_USER)")

	env["NOVA_SPRINT_REDIS_USER"] = user
	env["NOVA_TABLE_TEST_PASSWORD"] = right
	{
		code, stdout, stderr = runAs("list", "--redis", addr)
		require.EqualValues(t, 0, code, "right password: exit %d stdout %q stderr %q", code, stdout, stderr)
		require.Equal(t, "TABLE LIST tables=0 trips=1\n", stdout, "right password: exit %d stdout %q stderr %q", code, stdout, stderr)
		require.Empty(t, stderr, "right password: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	var out, errs bytes.Buffer
	code = (&application{in: strings.NewReader("list\n"), getenv: func(k string) string { return env[k] }}).run([]string{"shell", "--redis", addr}, &out, &errs)
	require.EqualValues(t, 0, code, "shell with the right password: exit %d stdout %q stderr %q", code, &out, &errs)
	require.Equal(t, "TABLE LIST tables=0 trips=1\n", out.String(), "shell with the right password: exit %d stdout %q stderr %q", code, &out, &errs)
	require.EqualValues(t, 0, errs.Len(), "shell with the right password: exit %d stdout %q stderr %q", code, &out, &errs)
}

// restartReader hands the shell one line per read, and before the second
// line closes every connection the shell holds, as a store restart does.
type restartReader struct {
	lines []string
	cut   func()
	n     int
}

func (r *restartReader) Read(p []byte) (int, error) {
	if r.n >= len(r.lines) {
		return 0, io.EOF
	}
	if r.n == 1 {
		r.cut()
	}
	n := copy(p, r.lines[r.n])
	r.n++
	return n, nil
}

// TestShellLineAfterRestart: the connection a shell holds is closed by the
// store between two lines (tla/TableSession.tla StoreDown then StoreUp, the
// store restarted). The next line takes one of the two paths RunVerb allows
// on a live connection: Answered, the pool saw the old connection closed and
// dialed a new one before sending; or Lost, the line was written to the
// closed connection, fails in one line with exit 2 and is not sent again.
// Which one is a race between the store's close reaching this process and
// the line, so the test holds both to the model and does not pick one. The
// line after that is answered either way, on one new connection.
func TestShellLineAfterRestart(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	r := startRelay(t, addr, nil)
	in := &restartReader{lines: []string{"create jobs --columns ready\n", "row add jobs after\n", "list\n"}, cut: r.cut}
	var out, errs bytes.Buffer
	code := (&application{in: in}).run([]string{"shell", "--redis", r.addr(), "--receipt=false", "--keep-going"}, &out, &errs)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	rows, err := c.XLen(context.Background(), ntable.ChangesKey("jobs")).Result()
	require.NoError(t, err, "%v", err)
	lines := strings.Split(strings.TrimSuffix(errs.String(), "\n"), "\n")
	answered := code == 0 && errs.Len() == 0 && rows == 2 && strings.Contains(out.String(), "TABLE ROW ADD table=jobs row=after")
	// Lost: one line, one remedy, show (the write may have committed).
	lost := code == 2 && rows == 1 && len(lines) == 2 && strings.HasPrefix(lines[0], "nova-table row add: ") &&
		strings.HasSuffix(lines[0], "; run: nova-table show 'jobs'") && !strings.Contains(lines[0], "; next: ") &&
		lines[1] == "nova-table shell: line 2 failed (exit 2)"
	t.Logf("after the restart the line took Answered=%v Lost=%v; stderr %q", answered, lost, &errs)
	require.True(t, (answered || lost), "restart: exit %d changes=%d connections=%d stdout %q stderr %q", code, rows, r.accepted.Load(), &out, &errs)
	require.Contains(t, out.String(), "TABLE LIST tables=1 trips=1", "restart: exit %d changes=%d connections=%d stdout %q stderr %q", code, rows, r.accepted.Load(), &out, &errs)
	require.EqualValues(t, 2, r.accepted.Load(), "restart: exit %d changes=%d connections=%d stdout %q stderr %q", code, rows, r.accepted.Load(), &out, &errs)
}
