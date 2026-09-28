//go:build functional

package main

// connfail_functional_test.go holds what a verb prints when the store cannot
// be opened, from the process itself: the test binary re-runs as nova-redis
// (TestMain, asMainEnv), so what go-redis would write to the process's own
// stderr is seen, which run() over a buffer cannot see. An unreachable
// --addr and a wrong password are each one FAIL line on stderr, exit 2, with
// redisconn's next step and no go-redis pool log (pool.go); with
// NOVA_REDIS_PASSWORD unset, a refused login also names that variable.

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

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

// closedAddr is a loopback address nothing listens on: a port the kernel
// handed this test, closed again.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestOpenFailureIsOneLineExitTwo(t *testing.T) {
	t.Parallel()

	const right, wrong = "synthetic-right-pw-4492", "synthetic-wrong-pw-4492"
	locked := testredis.Start(t, "--requirepass", right)
	down := closedAddr(t)

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
