//go:build functional

package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testredis"
)

// asToolEnv makes the test binary run as nova-config (TestMain), so a test
// reads the standard error of a real process: go-redis writes its own log
// there, and a line written that way never reaches run's stderr argument.
const asToolEnv = "NOVA_CONFIG_TEST_AS_TOOL"

// asTool runs the test binary as nova-config with exactly env, and returns
// its exit code and standard error.
func asTool(t *testing.T, env map[string]string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = []string{asToolEnv + "=1"}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("%v: stdout %q, want nothing", args, stdout.String())
	}
	return code, stderr.String()
}

// migrated is a fresh database with schema config in it, so apply gets as
// far as Redis whether or not it dials before it reads Postgres.
func migrated(t *testing.T) string {
	t.Helper()
	r := newReal(t, false)
	r.run(t, 0, "migrate")
	return r.env["NOVA_PG_DSN"]
}

// oneRedisLine is the shape of a store that cannot be used: exit 2 and one
// line of standard error naming the store, the class and the next step, with
// no go-redis log line ahead of it.
func oneRedisLine(t *testing.T, code int, stderr, class string) {
	t.Helper()
	if code != 2 {
		t.Fatalf("exit %d, want 2; stderr %q", code, stderr)
	}
	if strings.Count(stderr, "\n") != 1 || !strings.HasSuffix(stderr, "\n") {
		t.Fatalf("stderr is not one line: %q", stderr)
	}
	if strings.Contains(stderr, "pool.go") {
		t.Fatalf("go-redis pool noise on stderr: %q", stderr)
	}
	if !strings.HasPrefix(stderr, "nova-config apply: redis at ") || !strings.Contains(stderr, ": "+class+": ") || !strings.Contains(stderr, "; next: ") {
		t.Fatalf("stderr %q: want the store, %q and the next step", stderr, class)
	}
}

func TestApplyToAStoreThatIsNotThereIsOneLine(t *testing.T) {
	t.Parallel()
	code, stderr := asTool(t, map[string]string{
		"NOVA_PG_DSN":       migrated(t),
		"NOVA_FRIEND":       "rowan",
		"NOVA_SPRINT_REDIS": "127.0.0.1:" + testredis.FreePort(t),
	}, "apply", "--check")
	oneRedisLine(t, code, stderr, "unreachable")
}

func TestApplyWithAWrongPasswordIsOneLine(t *testing.T) {
	t.Parallel()
	const right, wrong = "synthetic-right-pw", "synthetic-wrong-pw"
	addr := testredis.Start(t, testredis.User("bench", right)...)
	code, stderr := asTool(t, map[string]string{
		"NOVA_PG_DSN":                    migrated(t),
		"NOVA_FRIEND":                    "rowan",
		"NOVA_SPRINT_REDIS":              addr,
		"NOVA_SPRINT_REDIS_USER":         "bench",
		"NOVA_SPRINT_REDIS_PASSWORD_ENV": "NOVA_CONFIG_TEST_PW",
		"NOVA_CONFIG_TEST_PW":            wrong,
	}, "apply", "--check")
	oneRedisLine(t, code, stderr, "login refused")
	if strings.Contains(stderr, wrong) || !strings.Contains(stderr, "as user bench (password from NOVA_CONFIG_TEST_PW)") {
		t.Fatalf("stderr %q: want the user and the password's variable, never the password", stderr)
	}
}

// TestApplyWhoseReplyIsLostIsUnconfirmed: a relay in front of a real store
// passes the handshake, then hands the next command on, waits for the store's
// reply and closes the client without it. The store was up and took the
// command, so the line says the reply was lost and the write may have
// committed (class unconfirmed), and exits 1, never 2 with "start the store".
func TestApplyWhoseReplyIsLostIsUnconfirmed(t *testing.T) {
	t.Parallel()
	relay := replyDropper(t, testredis.Start(t))
	code, stderr := asTool(t, map[string]string{
		"NOVA_PG_DSN":       migrated(t),
		"NOVA_FRIEND":       "rowan",
		"NOVA_SPRINT_REDIS": relay,
	}, "apply", "--check")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (ran, unconfirmed); stderr %q", code, stderr)
	}
	if strings.Count(stderr, "\n") != 1 || strings.Contains(stderr, "pool.go") {
		t.Fatalf("stderr is not one line: %q", stderr)
	}
	if !strings.HasPrefix(stderr, "nova-config apply: ") || !strings.Contains(stderr, "redis at "+relay+" ") || !strings.Contains(stderr, "reply lost") || !strings.Contains(stderr, "; next: the write may have committed") {
		t.Fatalf("stderr %q: want the store, the lost reply and the read-back", stderr)
	}
	if strings.Contains(stderr, "start the store") || strings.Contains(stderr, "unreachable") {
		t.Fatalf("stderr %q misdirects a command the store took", stderr)
	}
}

// replyDropper is a loopback relay in front of target: each connection's
// first batch (the handshake) and its reply pass whole; the second batch is
// handed on, and once the store has answered it the client is closed with
// the answer withheld.
func replyDropper(t *testing.T, target string) string {
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
				for batch := 1; ; batch++ {
					k, err := c.Read(buf)
					if k == 0 || err != nil {
						return
					}
					if _, err := up.Write(buf[:k]); err != nil {
						return
					}
					n, err := up.Read(buf)
					if n == 0 || err != nil || batch == 2 {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}
