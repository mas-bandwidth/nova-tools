package main

// serve_test.go holds the bind, auth and persistence slice of
// docs/SPEC-REDIS.md (nova-tools #2281 and #3879, behaviours 22, 23, 24 and
// 25 of "Tests this spec demands") against the PRODUCTION path: every test
// drives run(), the same function main() calls. The launch seam is faked for
// 22-24: it records the argv, environment and stdin config `serve` hands to
// redis-server. The restart test (25, #3879) runs the real launch against a
// throwaway redis-server on loopback in the test's temp dir, found through
// internal/nsprint/testutil, which skips on a laptop without the binary and
// fails under NOVA_CI=1.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/redis/go-redis/v9"
)

const fakeRedisServer = "/opt/fake/bin/redis-server"

// serveHarness is the deps `serve` is given plus every launch it made.
type serveHarness struct {
	t        *testing.T
	env      map[string]string
	dir      string
	launches []launchSpec
	onLaunch func(launchSpec) error
	d        deps
}

func newServeHarness(t *testing.T, password string) *serveHarness {
	t.Helper()
	h := &serveHarness{t: t, env: map[string]string{}, dir: filepath.Join(t.TempDir(), "store")}
	if password != "" {
		h.env[PasswordEnv] = password
	}
	h.d = deps{
		now:    func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) },
		getenv: func(k string) string { return h.env[k] },
		dial: func(addr, pw string) redis.Cmdable {
			c := redis.NewClient(&redis.Options{Addr: addr, Password: pw})
			t.Cleanup(func() { _ = c.Close() })
			return c
		},
		environ: func() []string {
			out := []string{"PATH=/usr/bin:/bin", "HOME=/home/bench"}
			for k, v := range h.env {
				out = append(out, k+"="+v)
			}
			return out
		},
		lookPath: func(name string) (string, error) {
			if name != "redis-server" {
				t.Fatalf("serve looked up %q; the instance program is redis-server", name)
			}
			return fakeRedisServer, nil
		},
		launch: func(_ context.Context, spec launchSpec, _, _ io.Writer) error {
			h.launches = append(h.launches, spec)
			if h.onLaunch != nil {
				return h.onLaunch(spec)
			}
			return nil
		},
	}
	return h
}

func (h *serveHarness) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, h.d)
	return code, out.String(), errb.String()
}

// config reads a redis.conf the way redis-server does for the directives this
// slice cares about: one directive per line, arguments split on blanks, a
// double-quoted argument unescaped (\\, \" and \xHH).
func config(t *testing.T, raw []byte) map[string][][]string {
	t.Helper()
	out := map[string][][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		args := splitConfigArgs(t, line)
		out[strings.ToLower(args[0])] = append(out[strings.ToLower(args[0])], args[1:])
	}
	return out
}

func splitConfigArgs(t *testing.T, line string) []string {
	t.Helper()
	var args []string
	for i := 0; i < len(line); {
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) {
			break
		}
		if line[i] != '"' {
			j := strings.IndexByte(line[i:], ' ')
			if j < 0 {
				j = len(line) - i
			}
			args = append(args, line[i:i+j])
			i += j
			continue
		}
		var b strings.Builder
		i++
		closed := false
		for i < len(line) {
			c := line[i]
			switch {
			case c == '"':
				closed = true
				i++
			case c == '\\' && i+1 < len(line) && line[i+1] == 'x' && i+3 < len(line):
				var v byte
				for _, h := range line[i+2 : i+4] {
					v <<= 4
					switch {
					case h >= '0' && h <= '9':
						v |= byte(h - '0')
					case h >= 'a' && h <= 'f':
						v |= byte(h-'a') + 10
					default:
						t.Fatalf("bad \\x escape in %q", line)
					}
				}
				b.WriteByte(v)
				i += 4
				continue
			case c == '\\' && i+1 < len(line):
				b.WriteByte(line[i+1])
				i += 2
				continue
			default:
				b.WriteByte(c)
				i++
				continue
			}
			break
		}
		if !closed {
			t.Fatalf("unterminated quote in config line %q", line)
		}
		args = append(args, b.String())
	}
	return args
}

func only(t *testing.T, cfg map[string][][]string, directive string) []string {
	t.Helper()
	got := cfg[directive]
	if len(got) != 1 {
		t.Fatalf("config carries %d %q lines, want exactly 1: %q", len(got), directive, got)
	}
	return got[0]
}

// TestBoundToLocalhostAndTailnetOnly: the instance is bound to loopback and
// tailnet addresses only (100.64.0.0/10, fd7a:115c:a1e0::/48). A public, LAN
// or wildcard address, or a hostname, is refused before anything is launched,
// and a missing --bind is refused rather than defaulted.
func TestBoundToLocalhostAndTailnetOnly(t *testing.T) {
	for _, bind := range []string{
		"127.0.0.1",
		"::1",
		"100.64.0.1",
		"100.127.255.254",
		"fd7a:115c:a1e0::1",
		"127.0.0.1,100.101.102.103",
	} {
		h := newServeHarness(t, "pw-from-nova-secrets")
		code, out, errb := h.run("serve", "--bind", bind, "--port", "6379", "--dir", h.dir)
		if code != 0 || len(h.launches) != 1 {
			t.Errorf("--bind %s: exit %d launches %d, want 0 and 1; stderr=%q", bind, code, len(h.launches), errb)
			continue
		}
		cfg := config(t, h.launches[0].Config)
		want := strings.Split(bind, ",")
		if got := only(t, cfg, "bind"); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("--bind %s: config binds %q, want exactly %q", bind, got, want)
		}
		if got := only(t, cfg, "protected-mode"); len(got) != 1 || got[0] != "yes" {
			t.Errorf("--bind %s: protected-mode %q, want yes", bind, got)
		}
		if got := only(t, cfg, "port"); len(got) != 1 || got[0] != "6379" {
			t.Errorf("--bind %s: port %q, want 6379", bind, got)
		}
		if !strings.Contains(out, "SERVE START bind="+bind+" ") {
			t.Errorf("--bind %s: stdout does not report the bind: %q", bind, out)
		}
	}

	for _, tc := range []struct{ name, bind string }{
		{"ipv4 wildcard", "0.0.0.0"},
		{"ipv6 wildcard", "::"},
		{"public v4", "8.8.8.8"},
		{"public v6", "2001:4860:4860::8888"},
		{"lan 192.168", "192.168.1.10"},
		{"lan 10/8", "10.0.0.1"},
		{"just above the tailnet", "100.128.0.1"},
		{"just below the tailnet", "100.63.255.255"},
		{"link-local v6 with zone", "fe80::1%en0"},
		{"hostname", "localhost"},
		{"one public among good", "127.0.0.1,8.8.8.8"},
		{"empty entry", "127.0.0.1,"},
		{"empty", ""},
	} {
		h := newServeHarness(t, "pw-from-nova-secrets")
		code, _, errb := h.run("serve", "--bind", tc.bind, "--port", "6379", "--dir", h.dir)
		if code != 2 || len(h.launches) != 0 {
			t.Errorf("%s (--bind %q): exit %d launches %d, want 2 and 0 (a public bind is refused, never launched)", tc.name, tc.bind, code, len(h.launches))
		}
		if !strings.HasPrefix(errb, "nova-redis serve: ") {
			t.Errorf("%s: stderr %q is not a serve refusal", tc.name, errb)
		}
	}

	h := newServeHarness(t, "pw-from-nova-secrets")
	code, _, errb := h.run("serve", "--port", "6379", "--dir", h.dir)
	if code != 2 || len(h.launches) != 0 || !strings.Contains(errb, "--bind is required") {
		t.Errorf("no --bind: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --bind (never a default)", code, len(h.launches), errb)
	}
}

// TestAuthFromNovaSecretsNeverAPlaintextArgument: the password is read at run
// time from NOVA_REDIS_PASSWORD, which `nova-secrets exec` fills; it reaches
// redis-server on stdin only, never in its argv, never in its environment,
// never in a file on the bench, and never in what serve prints. There is no
// flag that takes it, and without it serve refuses rather than running open.
func TestAuthFromNovaSecretsNeverAPlaintextArgument(t *testing.T) {
	const pw = `n0va-s3cret "quoted" \back value`
	secret := secrets.NewSecret(pw)
	h := newServeHarness(t, pw)
	code, out, errb := h.run("serve", "--bind", "127.0.0.1,100.100.1.2", "--port", "6380", "--dir", h.dir)
	if code != 0 || len(h.launches) != 1 {
		t.Fatalf("serve with the secret in the environment: exit %d launches %d stderr %q", code, len(h.launches), errb)
	}
	spec := h.launches[0]
	if spec.Program != fakeRedisServer {
		t.Errorf("program %q, want the one found on PATH %q", spec.Program, fakeRedisServer)
	}
	if strings.Join(spec.Args, " ") != "-" {
		t.Errorf("redis-server argv %q, want only \"-\" (config on stdin)", spec.Args)
	}
	for _, a := range append([]string{spec.Program}, spec.Args...) {
		if secrets.Leaks(a, secret) {
			t.Errorf("the secret is in the argv: %q", a)
		}
	}
	for _, e := range spec.Env {
		if strings.HasPrefix(e, PasswordEnv+"=") || secrets.Leaks(e, secret) {
			t.Errorf("the child environment carries the secret: %q", e)
		}
	}
	if len(spec.Env) == 0 {
		t.Errorf("the child environment is empty; want the parent's minus %s", PasswordEnv)
	}
	if got := only(t, config(t, spec.Config), "requirepass"); len(got) != 1 || got[0] != pw {
		t.Errorf("requirepass reads back as %q, want the nova-secrets value", got)
	}
	if secrets.Leaks(out, secret) || secrets.Leaks(errb, secret) {
		t.Errorf("serve printed the secret: stdout=%q stderr=%q", out, errb)
	}
	if !strings.Contains(out, " auth=on ") {
		t.Errorf("stdout does not report auth=on: %q", out)
	}
	err := filepath.Walk(h.dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if secrets.Leaks(string(b), secret) {
			t.Errorf("the secret is in plaintext on the bench: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	h = newServeHarness(t, "")
	code, _, errb = h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	if code != 2 || len(h.launches) != 0 || !strings.Contains(errb, "nova-secrets exec") {
		t.Errorf("no secret: exit %d launches %d stderr %q; want 2, 0 and a refusal naming nova-secrets exec", code, len(h.launches), errb)
	}

	for _, flagName := range []string{"--password", "--requirepass", "--pass"} {
		h = newServeHarness(t, pw)
		code, _, _ = h.run("serve", "--bind", "127.0.0.1", "--port", "6379", flagName, "on-the-command-line")
		if code != 2 || len(h.launches) != 0 {
			t.Errorf("%s: exit %d launches %d; a secret argument must be refused, not taken", flagName, code, len(h.launches))
		}
	}
}

// TestPersistenceIsAOFWithNoEviction: the config serve hands redis-server is
// the fleet store's rules (nova-tools #3879; the rowan-tools
// nova-redis.conf.j2 template it replaces): the AOF on and fsynced every
// second, an RDB snapshot every 60 s after any write as the second copy, and
// no eviction, so a full instance refuses a write rather than drop a card.
// Nothing sets a TTL policy: no volatile-* eviction, no dump file named
// elsewhere, no include that could turn any of it back.
func TestPersistenceIsAOFWithNoEviction(t *testing.T) {
	h := newServeHarness(t, "pw-from-nova-secrets")
	code, out, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	if code != 0 || len(h.launches) != 1 {
		t.Fatalf("serve: exit %d launches %d stderr %q", code, len(h.launches), errb)
	}
	cfg := config(t, h.launches[0].Config)
	for directive, want := range map[string]string{
		"appendonly":                "yes",
		"appendfsync":               "everysec",
		"save":                      "60 1",
		"maxmemory-policy":          "noeviction",
		"notify-keyspace-events":    "Ex",
		"latency-monitor-threshold": "100",
		"dir":                       h.dir,
	} {
		if got := strings.Join(only(t, cfg, directive), " "); got != want {
			t.Errorf("%s %q, want %q (the fleet store's rule)", directive, got, want)
		}
	}
	for _, d := range []string{"appendfilename", "appenddirname", "dbfilename", "include", "rdb-del-sync-files"} {
		if _, ok := cfg[d]; ok {
			t.Errorf("config carries %q; the store's files live under --dir by their default names", d)
		}
	}
	if !strings.Contains(out, " persistence=aof ") || !strings.Contains(out, " dir="+h.dir+" ") {
		t.Errorf("stdout does not report persistence=aof and the --dir: %q", out)
	}

	for _, tc := range []struct{ name, dir, why string }{
		{"relative", "store", "absolute"},
		{"a file", filepath.Join(h.dir, "file"), "not a directory"},
	} {
		if tc.name == "a file" {
			if err := os.WriteFile(tc.dir, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		h := newServeHarness(t, "pw-from-nova-secrets")
		code, _, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", tc.dir)
		if code != 2 || len(h.launches) != 0 || !strings.Contains(errb, tc.why) {
			t.Errorf("--dir %s: exit %d launches %d stderr %q; want 2, 0 and a refusal saying %q", tc.name, code, len(h.launches), errb, tc.why)
		}
	}
	h = newServeHarness(t, "pw-from-nova-secrets")
	code, _, errb = h.run("serve", "--bind", "127.0.0.1", "--port", "6379")
	if code != 2 || len(h.launches) != 0 || !strings.Contains(errb, "--dir is required") {
		t.Errorf("no --dir: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --dir (a store never lands in a guessed dir)", code, len(h.launches), errb)
	}
}

// TestRestartOnTheSameDirKeepsTheStore is #3879's DONE-WHEN on the production
// path: run() starts a real, throwaway redis-server on loopback through the
// real launch with --dir <d>, a card key and the nova_sprint library are
// written, serve is stopped the way a signal stops it (SIGTERM to the child)
// and started again on the same --dir, and the key is back intact with no TTL,
// and preflight 7.1 reads GREEN against that instance.
func TestRestartOnTheSameDirKeepsTheStore(t *testing.T) {
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
