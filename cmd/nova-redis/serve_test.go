package main

// serve_test.go holds the bind, auth and persistence slice of
// docs/SPEC-REDIS.md (nova-tools #2281, behaviours 22, 23, 24 and 25 of "Tests
// this spec demands") against the PRODUCTION path: every test drives run(),
// the same function main() calls. The launch seam is faked: it records the
// argv, environment and stdin config `serve` hands to redis-server, and for
// the restart test it stands a fresh miniredis up per launch. Nothing here
// starts a redis-server or opens a socket beyond miniredis on loopback.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
	"github.com/redis/go-redis/v9"
)

const fakeRedisServer = "/opt/fake/bin/redis-server"

// serveHarness is the deps `serve` is given plus every launch it made.
type serveHarness struct {
	t        *testing.T
	env      map[string]string
	tempRoot string
	launches []launchSpec
	onLaunch func(launchSpec) error
	d        deps
}

func newServeHarness(t *testing.T, password string) *serveHarness {
	t.Helper()
	h := &serveHarness{t: t, env: map[string]string{}, tempRoot: t.TempDir()}
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
		tempRoot: func() string { return h.tempRoot },
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
		code, out, errb := h.run("serve", "--bind", bind, "--port", "6379")
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
		code, _, errb := h.run("serve", "--bind", tc.bind, "--port", "6379")
		if code != 2 || len(h.launches) != 0 {
			t.Errorf("%s (--bind %q): exit %d launches %d, want 2 and 0 (a public bind is refused, never launched)", tc.name, tc.bind, code, len(h.launches))
		}
		if !strings.HasPrefix(errb, "nova-redis serve: ") {
			t.Errorf("%s: stderr %q is not a serve refusal", tc.name, errb)
		}
	}

	h := newServeHarness(t, "pw-from-nova-secrets")
	code, _, errb := h.run("serve", "--port", "6379")
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
	code, out, errb := h.run("serve", "--bind", "127.0.0.1,100.100.1.2", "--port", "6380")
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
	err := filepath.Walk(h.tempRoot, func(p string, fi os.FileInfo, err error) error {
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
	code, _, errb = h.run("serve", "--bind", "127.0.0.1", "--port", "6379")
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

// TestPersistenceOffNoRDBNoAOF: the config serve hands redis-server turns off
// both persistence engines -- `save ""` (no RDB snapshot points) and
// `appendonly no` (no AOF) -- and says nothing that turns either back on.
func TestPersistenceOffNoRDBNoAOF(t *testing.T) {
	h := newServeHarness(t, "pw-from-nova-secrets")
	code, out, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379")
	if code != 0 || len(h.launches) != 1 {
		t.Fatalf("serve: exit %d launches %d stderr %q", code, len(h.launches), errb)
	}
	cfg := config(t, h.launches[0].Config)
	if got := only(t, cfg, "save"); len(got) != 1 || got[0] != "" {
		t.Errorf("save %q, want the single empty argument (no RDB snapshot points)", got)
	}
	if got := only(t, cfg, "appendonly"); len(got) != 1 || got[0] != "no" {
		t.Errorf("appendonly %q, want no (no AOF)", got)
	}
	for _, d := range []string{"appendfilename", "appenddirname", "dbfilename", "include", "rdb-del-sync-files"} {
		if _, ok := cfg[d]; ok {
			t.Errorf("config carries %q; persistence off means no file for a restart to load", d)
		}
	}
	if !strings.Contains(out, " persistence=off ") {
		t.Errorf("stdout does not report persistence=off: %q", out)
	}
}

// TestRestartIsACleanSlate: every launch gets its own fresh, empty working
// directory, so a restart has no RDB or AOF to replay even if an earlier run
// left one behind. The fake launch stands a new miniredis up per launch and,
// the way redis-server does, replays a dump.rdb or appendonly.aof it finds in
// its dir; a key spilled into the first instance is gone from the second.
func TestRestartIsACleanSlate(t *testing.T) {
	const pw = "pw-from-nova-secrets"
	h := newServeHarness(t, pw)
	var instances []*miniredis.Miniredis
	h.onLaunch = func(spec launchSpec) error {
		cfg := config(t, spec.Config)
		dir := only(t, cfg, "dir")[0]
		if dir != spec.Dir {
			t.Errorf("config dir %q is not the launch dir %q", dir, spec.Dir)
		}
		mr := miniredis.RunT(t)
		mr.RequireAuth(only(t, cfg, "requirepass")[0])
		for _, f := range []string{"dump.rdb", "appendonly.aof"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				if err := mr.Set("replayed:"+f, "a restart loaded a persistence file"); err != nil {
					t.Fatal(err)
				}
			}
		}
		instances = append(instances, mr)
		return nil
	}

	if code, _, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379"); code != 0 {
		t.Fatalf("first serve: exit %d stderr %q", code, errb)
	}
	first := instances[0]
	if code, out, errb := h.run("spill", "--addr", first.Addr(), "--owner", "rowan", "--name", "note", "--ttl", "10m", "--value", "hi"); code != 0 {
		t.Fatalf("spill into the first instance: exit %d stdout %q stderr %q", code, out, errb)
	}
	if !first.Exists("rowan:note") {
		t.Fatal("the spill did not land in the first instance; this test would prove nothing")
	}
	// As if the first instance had written persistence files after all.
	for _, f := range []string{"dump.rdb", "appendonly.aof"} {
		if err := os.WriteFile(filepath.Join(h.launches[0].Dir, f), []byte("REDIS0011"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first.Close()

	if code, _, errb := h.run("serve", "--bind", "127.0.0.1", "--port", "6379"); code != 0 {
		t.Fatalf("restart: exit %d stderr %q", code, errb)
	}
	if h.launches[1].Dir == h.launches[0].Dir {
		t.Fatalf("the restart reused the first run's dir %s; it must start from a fresh one", h.launches[0].Dir)
	}
	entries, err := os.ReadDir(h.launches[1].Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the restart's dir %s holds %d entries at launch; want none", h.launches[1].Dir, len(entries))
	}
	second := instances[1]
	if keys := second.Keys(); len(keys) != 0 {
		t.Errorf("the restarted instance holds %q; a restart is a clean slate", keys)
	}
	code, out, _ := h.run("recall", "--addr", second.Addr(), "--owner", "rowan", "--name", "note")
	if code != 1 || !strings.HasPrefix(out, "RECALL MISSING key=rowan:note") {
		t.Errorf("recall after restart: exit %d stdout %q; want 1 and RECALL MISSING", code, out)
	}
}
