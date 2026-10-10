//go:build functional

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
)

// readyWriter is serve's stdout in the test: it keeps what was written and
// closes ready when redis-server says it accepts connections, so the test
// waits on the server's own word and never sleeps on the wall clock.
type readyWriter struct {
	mu    sync.Mutex
	b     bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *readyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.b.Write(p)
	if strings.Contains(w.b.String(), "Ready to accept connections") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *readyWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// TestAclUsersSurviveARestart is card redis-serve-keeps-acls on the
// production path: run() starts a throwaway redis-server through serve's real
// launch on --dir <d>, nova-redis acl apply creates the four rendered users
// and saves them (saved=acl-file), serve is stopped the way a signal stops it
// and started again on the same --dir with --users naming them, and every user
// still logs in with the password apply gave it; the default user still wants
// the store's password. A restart whose ACL file has lost a user is refused.
// No secret is in serve's or apply's output, the ACL file or the child's
// environment.
func TestAclUsersSurviveARestart(t *testing.T) {
	t.Parallel()

	const pw = "pw!acl#7Qx%v2@Lr^m"
	const seatPW = "seat!pw#4Kd%w9@Tz^n"
	program := testredis.Program(t)
	h := newServeHarness(t, pw)
	fixtureSecret(t, pw, h.dir, program)
	fixtureSecret(t, seatPW, h.dir, program)
	h.d.lookPath = func(string) (string, error) { return program, nil }
	port := testredis.FreePort(t)
	addr := "127.0.0.1:" + port
	users := []string{"coordinator", "bench", "ns-table", "ns-friend"}
	var outputs []string

	serve := func(label string, extra ...string) func() {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		h.d.launch = func(_ context.Context, spec launchSpec, stdout, stderr io.Writer) error {
			for _, e := range spec.Env {
				assert.False(t, strings.Contains(e, pw) || strings.Contains(e, seatPW), "%s: the child environment carries a secret", label)
			}
			return launchRedis(ctx, spec, stdout, stderr)
		}
		out := &readyWriter{ready: make(chan struct{})}
		var errb bytes.Buffer
		done := make(chan int, 1)
		d := h.d
		args := append([]string{"serve", "--bind", "127.0.0.1", "--port", port, "--dir", h.dir}, extra...)
		go func() { done <- run(args, out, &errb, d) }()
		bound := time.NewTimer(30 * time.Second)
		defer bound.Stop()
		select {
		case <-out.ready:
		case code := <-done:
			cancel()
			require.FailNowf(t, "", "%s: serve exited %d before the instance was ready\nstdout %s\nstderr %s", label, code, out.String(), errb.String())
		case <-bound.C:
			cancel()
			require.FailNowf(t, "", "%s: the instance was not ready in 30s\nstdout %s", label, out.String())
		}
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			cancel()
			bound := time.NewTimer(30 * time.Second)
			defer bound.Stop()
			select {
			case code := <-done:
				assert.Zero(t, code, "%s: serve exit %d on SIGTERM; stderr %q", label, code, errb.String())
			case <-bound.C:
				require.FailNowf(t, "", "%s: serve did not exit after SIGTERM", label)
			}
			outputs = append(outputs, out.String(), errb.String())
		}
		t.Cleanup(stop)
		return stop
	}
	aclDeps := realDeps()
	aclDeps.getenv = func(k string) string {
		switch k {
		case PasswordEnv:
			return pw
		case "SEAT_PW":
			return seatPW
		}
		return ""
	}
	acl := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(append([]string{"acl"}, append(args, "--addr", addr)...), &out, &errb, aclDeps)
		outputs = append(outputs, out.String(), errb.String())
		return code, out.String() + errb.String()
	}
	ctx := context.Background()
	login := func(user, password string) error {
		c := redis.NewClient(&redis.Options{Addr: addr, Username: user, Password: password})
		defer func() { _ = c.Close() }()
		return c.Ping(ctx).Err()
	}

	stop := serve("first")
	apply := []string{"apply"}
	for _, u := range users {
		apply = append(apply, "--password-env-for", u+"=SEAT_PW")
	}
	code, out := acl(apply...)
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "ACL APPLY OK users=4 set=4 saved=acl-file", "apply wrote through the store's ACL file")
	stop()

	stop = serve("restart", "--users", strings.Join(users, ","))
	for _, u := range users {
		assert.NoError(t, login(u, seatPW), "%s logs in after the restart with the password apply created it with", u)
	}
	assert.NoError(t, login("", pw), "the default user logs in with the store's password")
	assert.Error(t, login("", ""), "the default user wants a password after the restart (an ACL file never brings it up nopass)")
	assert.Error(t, login("", "not-the-password"), "the default user refuses a wrong password")
	code, out = acl("check")
	assert.Equal(t, 0, code, out)
	assert.Equal(t, 4, strings.Count(out, "ACL OK "), out)
	stop()

	file := filepath.Join(h.dir, "users.acl")
	fi, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the ACL file is 0600 after serve")
	raw, err := os.ReadFile(file)
	require.NoError(t, err)
	for _, s := range []string{pw, seatPW} {
		assert.False(t, secrets.Leaks(string(raw), secrets.NewSecret(s)), "the ACL file holds a password; it holds only hashes")
		for _, o := range outputs {
			assert.False(t, secrets.Leaks(o, secrets.NewSecret(s)), "a secret is in the output: %q", o)
		}
	}

	// A file that has lost a user is refused before anything starts.
	var kept []string
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(l, "user ns-friend ") {
			kept = append(kept, l)
		}
	}
	require.NoError(t, os.WriteFile(file, []byte(strings.Join(kept, "\n")), 0o600))
	var o, e bytes.Buffer
	code = run([]string{"serve", "--bind", "127.0.0.1", "--port", port, "--dir", h.dir, "--users", strings.Join(users, ",")}, &o, &e, h.d)
	assert.Equal(t, 2, code, "stdout %q stderr %q", o.String(), e.String())
	assert.Contains(t, e.String(), "missing the users ns-friend")
}
