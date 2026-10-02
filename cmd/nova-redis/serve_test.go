package main

// serve_test.go holds the bind, auth and persistence slice of
// docs/SPEC-REDIS.md (nova-tools #2281 and #3879, behaviours 22, 23, 24 and
// 25 of "Tests this spec demands") against the PRODUCTION path: every test
// drives run(), the same function main() calls. The launch seam is faked for
// 22-24: it records the argv, environment and stdin config `serve` hands to
// redis-server. The restart test (25, #3879) runs the real launch against a
// throwaway redis-server on loopback in the test's temp dir, found through
// internal/testredis, which skips on a laptop without the binary and
// fails under NOVA_CI=1.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/secrets"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		environ: func() []string {
			out := []string{"PATH=/usr/bin:/bin", "HOME=/home/bench"}
			for k, v := range h.env {
				out = append(out, k+"="+v)
			}
			return out
		},
		lookPath: func(name string) (string, error) {
			require.Equal(t, "redis-server", name, "serve looked up %q; the instance program is redis-server", name)
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
						require.FailNowf(t, "", "bad \\x escape in %q", line)
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
		require.True(t, closed, "unterminated quote in config line %q", line)
		args = append(args, b.String())
	}
	return args
}

func only(t *testing.T, cfg map[string][][]string, directive string) []string {
	t.Helper()
	got := cfg[directive]
	require.Len(t, got, 1, "config carries %d %q lines, want exactly 1: %q", len(got), directive, got)
	return got[0]
}

// TestBoundToLocalhostAndTailnetOnly: the instance is bound to loopback and
// tailnet addresses only (100.64.0.0/10, fd7a:115c:a1e0::/48). A public, LAN
// or wildcard address, or a hostname, is refused before anything is launched,
// and a missing --bind is refused rather than defaulted.
func TestBoundToLocalhostAndTailnetOnly(t *testing.T) {
	t.Parallel()

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
		if !assert.Zero(t, code, "--bind %s: exit %d launches %d, want 0 and 1; stderr=%q", bind, code, len(h.launches), errb) {
			continue
		}
		if !assert.Len(t, h.launches, 1, "--bind %s: exit %d launches %d, want 0 and 1; stderr=%q", bind, code, len(h.launches), errb) {
			continue
		}
		cfg := config(t, h.launches[0].Config)
		want := strings.Split(bind, ",")
		{
			got := only(t, cfg, "bind")
			assert.Equal(t, strings.Join(want, ","), strings.Join(got, ","), "--bind %s: config binds %q, want exactly %q", bind, got, want)
		}
		{
			got := only(t, cfg, "protected-mode")
			if assert.Len(t, got, 1, "--bind %s: protected-mode %q, want yes", bind, got) {
				assert.Equal(t, "yes", got[0], "--bind %s: protected-mode %q, want yes", bind, got)
			}
		}
		{
			got := only(t, cfg, "port")
			if assert.Len(t, got, 1, "--bind %s: port %q, want 6379", bind, got) {
				assert.Equal(t, "6379", got[0], "--bind %s: port %q, want 6379", bind, got)
			}
		}
		assert.Contains(t, out, "SERVE START bind="+bind+" ", "--bind %s: stdout does not report the bind: %q", bind, out)
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
		if assert.Equal(t, 2, code, "%s (--bind %q): exit %d launches %d, want 2 and 0 (a public bind is refused, never launched)", tc.name, tc.bind, code, len(h.launches)) {
			assert.Len(t, h.launches, 0, "%s (--bind %q): exit %d launches %d, want 2 and 0 (a public bind is refused, never launched)", tc.name, tc.bind, code, len(h.launches))
		}
		assert.True(t, strings.HasPrefix(errb, "nova-redis serve REFUSED: "), "%s: stderr %q is not a serve refusal", tc.name, errb)
	}

	h := newServeHarness(t, "pw-from-nova-secrets")
	code, _, errb := h.run("serve", "--port", "6379", "--dir", h.dir)
	if assert.Equal(t, 2, code, "no --bind: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --bind (never a default)", code, len(h.launches), errb) {
		if assert.Len(t, h.launches, 0, "no --bind: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --bind (never a default)", code, len(h.launches), errb) {
			assert.Contains(t, errb, "--bind is required", "no --bind: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --bind (never a default)", code, len(h.launches), errb)
		}
	}
}

// TestAuthFromNovaSecretsNeverAPlaintextArgument: the password is read at run
// time from NOVA_REDIS_PASSWORD, which `nova-secrets exec` fills; it reaches
// redis-server on stdin only, never in its argv, never in its environment,
// never in a file on the bench, and never in what serve prints. There is no
// flag that takes it, and without it serve refuses rather than running open.
func TestAuthFromNovaSecretsNeverAPlaintextArgument(t *testing.T) {
	t.Parallel()

	const pw = `n0va-s3cret "quoted" \back value`
	secret := secrets.NewSecret(pw)
	h := newServeHarness(t, pw)
	code, out, errb := h.run("serve", "--bind", "127.0.0.1,100.100.1.2", "--port", "6380", "--dir", h.dir)
	require.Zero(t, code, "serve with the secret in the environment: exit %d launches %d stderr %q", code, len(h.launches), errb)
	require.Len(t, h.launches, 1, "serve with the secret in the environment: exit %d launches %d stderr %q", code, len(h.launches), errb)
	spec := h.launches[0]
	assert.Equal(t, fakeRedisServer, spec.Program, "program %q, want the one found on PATH %q", spec.Program, fakeRedisServer)
	assert.Equal(t, "-", strings.Join(spec.Args, " "), "redis-server argv %q, want only \"-\" (config on stdin)", spec.Args)
	for _, a := range append([]string{spec.Program}, spec.Args...) {
		assert.False(t, secrets.Leaks(a, secret), "the secret is in the argv: %q", a)
	}
	for _, e := range spec.Env {
		if assert.False(t, strings.HasPrefix(e, PasswordEnv+"="), "the child environment carries the secret: %q", e) {
			assert.False(t, secrets.Leaks(e, secret), "the child environment carries the secret: %q", e)
		}
	}
	assert.NotEmpty(t, spec.Env, "the child environment is empty; want the parent's minus %s", PasswordEnv)
	{
		got := only(t, config(t, spec.Config), "requirepass")
		if assert.Len(t, got, 1, "requirepass reads back as %q, want the nova-secrets value", got) {
			assert.Equal(t, pw, got[0], "requirepass reads back as %q, want the nova-secrets value", got)
		}
	}
	if assert.False(t, secrets.Leaks(out, secret), "serve printed the secret: stdout=%q stderr=%q", out, errb) {
		assert.False(t, secrets.Leaks(errb, secret), "serve printed the secret: stdout=%q stderr=%q", out, errb)
	}
	assert.Contains(t, out, " auth=on ", "stdout does not report auth=on: %q", out)
	err := filepath.Walk(h.dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		assert.False(t, secrets.Leaks(string(b), secret), "the secret is in plaintext on the bench: %s", p)
		return nil
	})
	require.NoError(t, err, err)

	h = newServeHarness(t, "")
	code, _, errb = h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	if assert.Equal(t, 2, code, "no secret: exit %d launches %d stderr %q; want 2, 0 and a refusal naming nova-secrets exec", code, len(h.launches), errb) {
		if assert.Len(t, h.launches, 0, "no secret: exit %d launches %d stderr %q; want 2, 0 and a refusal naming nova-secrets exec", code, len(h.launches), errb) {
			assert.Contains(t, errb, "nova-secrets exec", "no secret: exit %d launches %d stderr %q; want 2, 0 and a refusal naming nova-secrets exec", code, len(h.launches), errb)
		}
	}

	for _, flagName := range []string{"--password", "--requirepass", "--pass"} {
		h = newServeHarness(t, pw)
		code, _, _ = h.run("serve", "--bind", "127.0.0.1", "--port", "6379", flagName, "on-the-command-line")
		if assert.Equal(t, 2, code, "%s: exit %d launches %d; a secret argument must be refused, not taken", flagName, code, len(h.launches)) {
			assert.Len(t, h.launches, 0, "%s: exit %d launches %d; a secret argument must be refused, not taken", flagName, code, len(h.launches))
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
	t.Parallel()

	h := newServeHarness(t, "pw-from-nova-secrets")
	code, out, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", h.dir)
	require.Zero(t, code, "serve: exit %d launches %d stderr %q", code, len(h.launches), errb)
	require.Len(t, h.launches, 1, "serve: exit %d launches %d stderr %q", code, len(h.launches), errb)
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
		{
			got := strings.Join(only(t, cfg, directive), " ")
			assert.Equal(t, want, got, "%s %q, want %q (the fleet store's rule)", directive, got, want)
		}
	}
	for _, d := range []string{"appendfilename", "appenddirname", "dbfilename", "include", "rdb-del-sync-files"} {
		{
			_, ok := cfg[d]
			assert.False(t, ok, "config carries %q; the store's files live under --dir by their default names", d)
		}
	}
	if assert.Contains(t, out, " persistence=aof ", "stdout does not report persistence=aof and the --dir: %q", out) {
		assert.Contains(t, out, " dir="+h.dir+" ", "stdout does not report persistence=aof and the --dir: %q", out)
	}

	for _, tc := range []struct{ name, dir, why string }{
		{"relative", "store", "absolute"},
		{"a file", filepath.Join(h.dir, "file"), "not a directory"},
	} {
		if tc.name == "a file" {
			{
				err := os.WriteFile(tc.dir, []byte("x"), 0o600)
				require.NoError(t, err, err)
			}
		}
		h := newServeHarness(t, "pw-from-nova-secrets")
		code, _, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379", "--dir", tc.dir)
		if assert.Equal(t, 2, code, "--dir %s: exit %d launches %d stderr %q; want 2, 0 and a refusal saying %q", tc.name, code, len(h.launches), errb, tc.why) {
			if assert.Len(t, h.launches, 0, "--dir %s: exit %d launches %d stderr %q; want 2, 0 and a refusal saying %q", tc.name, code, len(h.launches), errb, tc.why) {
				assert.Contains(t, errb, tc.why, "--dir %s: exit %d launches %d stderr %q; want 2, 0 and a refusal saying %q", tc.name, code, len(h.launches), errb, tc.why)
			}
		}
	}
	h = newServeHarness(t, "pw-from-nova-secrets")
	code, _, errb = h.run("serve", "--bind", "127.0.0.1", "--port", "6379")
	if assert.Equal(t, 2, code, "no --dir: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --dir (a store never lands in a guessed dir)", code, len(h.launches), errb) {
		if assert.Len(t, h.launches, 0, "no --dir: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --dir (a store never lands in a guessed dir)", code, len(h.launches), errb) {
			assert.Contains(t, errb, "--dir is required", "no --dir: exit %d launches %d stderr %q; want 2, 0 and a refusal naming --dir (a store never lands in a guessed dir)", code, len(h.launches), errb)
		}
	}
}

func TestServeFailuresHaveRemedies(t *testing.T) {
	t.Parallel()

	t.Run("missing executable", func(t *testing.T) {
		t.Parallel()
		h := newServeHarness(t, "fixture-secret-only")
		h.d.lookPath = func(string) (string, error) { return "", errors.New("not found") }
		code, out, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir)
		const want = "SERVE FAIL err=redis-server not found on PATH: not found remedy=\"install redis-server (Redis 7 or later) so it is on PATH, then run nova-redis serve again\"\n"
		require.True(t, code == 1 && out == "" && errb == want && len(h.launches) == 0,
			"missing executable: exit %d stdout %q stderr %q launches %d", code, out, errb, len(h.launches))
	})

	t.Run("child failure", func(t *testing.T) {
		t.Parallel()
		h := newServeHarness(t, "fixture-secret-only")
		storeRoot := t.TempDir()
		h.dir = filepath.Join(storeRoot, "store's space")
		h.onLaunch = func(launchSpec) error { return errors.New("exit status 1") }
		code, out, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6380", "--dir", h.dir)
		remedy := "run: ls -ld -- '" + strings.ReplaceAll(storeRoot, "'", "'\\''") + string(os.PathSeparator) + "store'\\''s space'; compare directory access and the explicit --bind/--port with the launch error and any redis-server output"
		want := fmt.Sprintf("SERVE FAIL err=exit status 1 remedy=%q\n", remedy)
		require.True(t, code == 1 && errb == want && len(h.launches) == 1,
			"child failure: exit %d stderr %q launches %d", code, errb, len(h.launches))
		assert.Equal(t, h.dir, h.launches[0].Dir,
			"launch directory must match the diagnostic directory")
		assert.True(t, strings.HasPrefix(out, "SERVE START bind=127.0.0.1 port=6380 ") &&
			strings.Count(out, "\n") == 1 && !strings.Contains(out, "SERVE STOP") &&
			!strings.Contains(out, "fixture-secret-only"),
			"failed launch must report only START, without a success STOP or password: %q", out)
	})
}
