//go:build functional

package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// TestRestartOnTheSameDirKeepsTheStore is #3879's DONE-WHEN on the production
// path: run() starts a real, throwaway redis-server on loopback through the
// real launch with --dir <d>, a card key and the nova_sprint library are
// written, serve is stopped the way a signal stops it (SIGTERM to the child)
// and started again on the same --dir, and the key is back intact with no TTL,
// and preflight 7.1 reads GREEN against that instance.
func TestRestartOnTheSameDirKeepsTheStore(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	const pw = "pw-from-nova-secrets"
	program := testutil.Program(t)
	h := newServeHarness(t, pw)
	h.d.lookPath = func(string) (string, error) { return program, nil }
	port := testutil.FreePort(t)
	addr := "127.0.0.1:" + port
	const key = "s:T:card:x"
	want := map[string]string{"origin": "nova-tools#3879", "where": "waiting", "stream": "redis"}

	// One serve run: start it, hand the test a client once PING answers,
	// and return the stop that SIGTERMs the child and waits for serve's exit.
	serve := func(label string) (*redis.Client, func()) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		h.d.launch = func(_ context.Context, spec launchSpec, stdout, stderr io.Writer) error {
			return launchRedis(ctx, spec, stdout, stderr)
		}
		var out, errb bytes.Buffer
		done := make(chan int, 1)
		d := h.d
		go func() {
			done <- run([]string{"serve", "--bind", "127.0.0.1", "--port", port, "--dir", h.dir}, &out, &errb, d)
		}()
		c := redis.NewClient(&redis.Options{Addr: addr, Password: pw})
		deadline := time.Now().Add(30 * time.Second)
		for {
			pctx, pcancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := c.Ping(pctx).Err()
			pcancel()
			if err == nil {
				break
			}
			select {
			case code := <-done:
				cancel()
				t.Fatalf("%s: serve exited %d before the instance answered: %v\nstdout %s\nstderr %s", label, code, err, out.String(), errb.String())
			default:
			}
			if time.Now().After(deadline) {
				cancel()
				t.Fatalf("%s: the instance did not answer PING: %v", label, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		stopped := false
		stop := func() {
			if stopped {
				return
			}
			stopped = true
			_ = c.Close()
			cancel()
			select {
			case code := <-done:
				if code != 0 || !strings.Contains(out.String(), "SERVE STOP ") {
					t.Errorf("%s: serve exit %d on SIGTERM, want 0 and SERVE STOP; stdout %q stderr %q", label, code, out.String(), errb.String())
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%s: serve did not exit after SIGTERM", label)
			}
		}
		t.Cleanup(stop)
		return c, stop
	}

	ctx := context.Background()
	c, stop := serve("first")
	if err := c.HSet(ctx, key, want).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load %s: %v", fn.Library, err)
	}
	stop()

	c, stop = serve("restart")
	defer stop()
	got, err := c.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("after restart on the same --dir %s = %v, want %v intact", key, got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("after restart %s %s = %q, want %q", key, k, got[k], v)
		}
	}
	if ttl, err := c.TTL(ctx, key).Result(); err != nil || ttl != -1 {
		t.Errorf("TTL %s = %v (err %v), want -1: the store sets no TTL", key, ttl, err)
	}
	var line *preflight.Line
	lines := preflight.StoreChecks(ctx, c, preflight.Options{Sprint: "T"})
	for i := range lines {
		if lines[i].N == "7.1" {
			line = &lines[i]
		}
	}
	if line == nil || line.Red {
		t.Fatalf("preflight 7.1 against the restarted instance: %v, want GREEN", line)
	}
}
