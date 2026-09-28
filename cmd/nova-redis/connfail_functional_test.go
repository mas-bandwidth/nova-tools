//go:build functional

package main

// connfail_functional_test.go holds what a verb prints when the store cannot
// be opened, from the process itself: the test binary re-runs as nova-redis
// (TestMain, asMainEnv), so what go-redis would write to the process's own
// stderr is seen, which run() over a buffer cannot see. An unreachable
// --addr and a wrong password are each one FAIL line on stderr, exit 2, with
// redisconn's next step and no go-redis pool log (pool.go); a spill whose
// EXEC reply is lost after the store took it is exit 1, unconfirmed; with
// NOVA_REDIS_PASSWORD unset, a refused login also names that variable.

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// asMainEnv, set to 1 in a child's environment, makes the test binary run
// main() on its arguments instead of the tests.
const asMainEnv = "NOVA_REDIS_TEST_AS_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(asMainEnv) == "1" {
		main()
	}
	os.Exit(m.Run())
}

// asMain runs this test binary as nova-redis with args and exactly env (plus
// asMainEnv), and returns its exit code, stdout and stderr.
func asMain(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, args...)
	cmd.Env = append([]string{asMainEnv + "=1"}, env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

// refusingAddr is a loopback address this test holds for its whole life, on
// which every connection is accepted and closed at once, before a byte of the
// handshake is answered: a store that cannot be reached, and a port that no
// other process can take between the test choosing it and the child dialing
// it.
func refusingAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { _ = l.Close(); <-done })
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return l.Addr().String()
}

func TestOpenFailureIsOneLineExitTwo(t *testing.T) {
	t.Parallel()

	const right, wrong = "synthetic-right-pw-4492", "synthetic-wrong-pw-4492"
	locked := testredis.Start(t, "--requirepass", right)
	down := refusingAddr(t)

	cases := []struct {
		label, addr, class, next string
		env                      []string
	}{
		{"unreachable", down, "class=unreachable", "next: start the store or correct the address", nil},
		{"wrong password", locked, "class=auth-refused", "next: name the user", []string{PasswordEnv + "=" + wrong}},
		{"no password", locked, "class=auth-refused", "remedy=\"nova-redis reads the store's password from " + PasswordEnv + ", which is not set", nil},
	}
	verbs := [][]string{
		{"spill", "--owner", "rowan", "--name", "note", "--ttl", "1m", "--value", "hi"},
		{"recall", "--owner", "rowan", "--name", "note"},
	}
	for _, c := range cases {
		for _, v := range verbs {
			args := append([]string{v[0], "--addr", c.addr}, v[1:]...)
			code, stdout, stderr := asMain(t, c.env, args...)
			if code != 2 {
				t.Errorf("%s %s exits %d, want 2; stdout=%q stderr=%q", c.label, v[0], code, stdout, stderr)
			}
			if stdout != "" {
				t.Errorf("%s %s printed on stdout: %q", c.label, v[0], stdout)
			}
			if n := strings.Count(stderr, "\n"); n != 1 || !strings.HasSuffix(stderr, "\n") {
				t.Errorf("%s %s stderr is %d lines, want exactly one: %q", c.label, v[0], n, stderr)
			}
			if strings.Contains(stderr, "pool.go") {
				t.Errorf("%s %s let go-redis's pool log through: %q", c.label, v[0], stderr)
			}
			want := strings.ToUpper(v[0]) + " FAIL "
			for _, part := range []string{want, c.class, c.next, c.addr} {
				if !strings.Contains(stderr, part) {
					t.Errorf("%s %s stderr lacks %q: %q", c.label, v[0], part, stderr)
				}
			}
			if strings.Contains(stderr, right) || strings.Contains(stderr, wrong) {
				t.Errorf("%s %s printed a password: %q", c.label, v[0], stderr)
			}
		}
	}
}

// TestSpillWhoseExecReplyIsLostIsUnconfirmed is Stella's probe on #4504: a
// relay in front of a real throwaway store forwards everything and drops the
// reply to the batch that carries EXEC, closing the client once the store has
// answered it, so the transaction committed and the tool never heard. The
// store was up and the write landed, so spill must not say "could not run"
// (exit 2, "start the store"): it exits 1, says confirmation was lost and the
// write may have committed, and its remedy is a read-back, never a re-spill.
func TestSpillWhoseExecReplyIsLostIsUnconfirmed(t *testing.T) {
	t.Parallel()

	store := testredis.Start(t)
	relay := execReplyDropper(t, store)
	code, stdout, stderr := asMain(t, nil, "spill", "--addr", relay, "--owner", "rowan", "--name", "note", "--ttl", "10m", "--value", "hi")

	if code != 1 {
		t.Errorf("spill with its EXEC reply lost exits %d, want 1 (ran, unconfirmed); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("spill with its EXEC reply lost printed on stdout: %q", stdout)
	}
	if n := strings.Count(stderr, "\n"); n != 1 {
		t.Errorf("stderr is %d lines, want one: %q", n, stderr)
	}
	for _, part := range []string{
		"SPILL UNCONFIRMED key=rowan:note ",
		"confirmation was lost after the transaction was sent, so the write may have committed",
		"nova-redis recall --addr " + relay + " --owner rowan --name note",
	} {
		if !strings.Contains(stderr, part) {
			t.Errorf("stderr lacks %q: %q", part, stderr)
		}
	}
	for _, wrong := range []string{"start the store", "class=unreachable", "pool.go"} {
		if strings.Contains(stderr, wrong) {
			t.Errorf("stderr holds %q, which misdirects a write that landed: %q", wrong, stderr)
		}
	}

	// The store, read directly: exactly one transaction committed, the value
	// is there and it carries its TTL.
	ctx := context.Background()
	conn, err := redisconn.Open(ctx, redisconn.Options{Addr: store}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	stats, err := conn.Client().Info(ctx, "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stats, "cmdstat_exec:calls=1,") {
		t.Errorf("the store ran EXEC other than exactly once: %q", stats)
	}
	if v, err := conn.Client().HGet(ctx, "rowan:note", fieldValue).Result(); err != nil || v != "hi" {
		t.Errorf("rowan:note %s = %q, %v; want the committed hi", fieldValue, v, err)
	}
	if ttl, err := conn.Client().PTTL(ctx, "rowan:note").Result(); err != nil || ttl <= 0 {
		t.Errorf("rowan:note PTTL = %v, %v; want a positive TTL", ttl, err)
	}
}

// execReplyDropper is a loopback relay in front of target that forwards every
// byte both ways, except the store's reply to a client batch that carries
// EXEC: that reply is read from the store until its EXEC array has begun (so
// the transaction has run) and then the client is hung up on without it.
func execReplyDropper(t *testing.T, target string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var open []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range open {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	const execWire = "*1\r\n$4\r\nEXEC\r\n"
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			mu.Lock()
			open = append(open, c, up)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = c.Close(); _ = up.Close() }()
				buf := make([]byte, 64<<10)
				for {
					// Requests and replies alternate: read the client's batch,
					// hand it on, then relay the store's reply to it.
					k, err := c.Read(buf)
					if k == 0 || err != nil {
						return
					}
					batch := string(buf[:k])
					if _, err := up.Write(buf[:k]); err != nil {
						return
					}
					// go-redis writes command names in lower case.
					if strings.Contains(strings.ToUpper(batch), execWire) {
						var reply strings.Builder
						for !strings.Contains(reply.String(), "*3\r\n") {
							n, err := up.Read(buf)
							reply.Write(buf[:n])
							if err != nil {
								return
							}
						}
						return // hang up on the client: the reply is dropped
					}
					n, err := up.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}
