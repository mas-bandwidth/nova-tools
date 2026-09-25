package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

const digestSince = "2026-09-23T00:00:00Z"

// countingListener is a 127.0.0.1:0 listener that counts accepts: a verb
// that dials Redis before a flag check shows up as one.
func countingListener(t *testing.T) (string, *int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&n, 1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), &n
}

// TestDigestFlagsRefuseBeforeReading: every usage refusal is exit 2, empty
// stdout and exactly the standard one-line refusal, with no dial of the
// named Redis; an unreachable Redis is the same shape after the flags.
func TestDigestFlagsRefuseBeforeReading(t *testing.T) {
	addr, accepts := countingListener(t)
	pre := "nova-sprint digest: "
	post := "; run: nova-sprint help\n"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--redis", addr, "--since", digestSince, "extra"}, "takes flags, not positional arguments"},
		{[]string{"--bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"--since", digestSince}, "wants --redis <host:port>"},
		{[]string{"--redis", addr}, "wants --since <RFC3339 UTC>"},
		{[]string{"--redis", addr, "--since", "2026-09-23"}, "wants --since <RFC3339 UTC>"},
		{[]string{"--redis", addr, "--since", "2026-09-23T00:00:00-04:00"}, "wants --since <RFC3339 UTC>"},
		{[]string{"--redis", addr, "--since", digestSince, "--until", "soon"}, "wants --until <RFC3339 UTC>"},
		{[]string{"--redis", addr, "--since", digestSince, "--until", digestSince}, "since must be before until"},
		{[]string{"--redis", addr, "--since", digestSince, "--repo", "nova-tools"}, `invalid value "nova-tools" for flag -repo: --repo "nova-tools" is not <owner>/<name>`},
	} {
		code, stdout, stderr := runSprint(append([]string{"digest"}, tc.args...)...)
		if code != 2 || stdout != "" || stderr != pre+tc.want+post {
			t.Fatalf("%v: exit %d stdout %q stderr %q, want exit 2 and %q", tc.args, code, stdout, stderr, pre+tc.want+post)
		}
	}
	if n := atomic.LoadInt64(accepts); n != 0 {
		t.Fatalf("a usage refusal dialed Redis %d times", n)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()
	code, stdout, stderr := runSprint("digest", "--redis", closed, "--since", digestSince)
	if code != 2 || stdout != "" || !strings.HasPrefix(stderr, pre+"redis "+closed+": ") || !strings.HasSuffix(stderr, post) || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("closed port: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// digestFixture loads internal/nsprint/digest's fixture (tab-separated
// arguments, \n a newline) into a throwaway redis-server.
func digestFixture(t *testing.T) string {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "nsprint", "digest", "testdata", "digest", "redis.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var args []any
		for _, a := range strings.Split(line, "\t") {
			args = append(args, strings.ReplaceAll(a, `\n`, "\n"))
		}
		if err := c.Do(context.Background(), args...).Err(); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
	}
	return addr
}

// TestDigestCLIReference runs docs/CLI.md's digest subsection: the first
// run's command against the fixture prints the documented output byte for
// byte, and the documented first stumble prints its documented refusal.
func TestDigestCLIReference(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i := strings.Index(doc, "\n### `nova-sprint digest`\n")
	if i < 0 {
		t.Fatal("docs/CLI.md has no ### `nova-sprint digest` subsection")
	}
	sec := doc[i+1:]
	if j := strings.Index(sec[4:], "\n## "); j >= 0 {
		sec = sec[:j+4]
	}
	if j := strings.Index(sec[4:], "\n### "); j >= 0 {
		sec = sec[:j+4]
	}
	if !strings.Contains(sec, "`nova-sprint digest --redis <host:port> --since <RFC3339 UTC> [--until <RFC3339 UTC>] [--repo <owner/name>]...`") {
		t.Fatal("the subsection does not state the invocation")
	}
	var blocks [][]string
	parts := strings.Split(sec, "```text\n")
	for _, p := range parts[1:] {
		body, _, ok := strings.Cut(p, "```")
		if !ok {
			t.Fatal("unterminated text block")
		}
		blocks = append(blocks, strings.Split(strings.TrimSuffix(body, "\n"), "\n"))
	}
	if len(blocks) != 2 {
		t.Fatalf("%d text blocks, want the first run and the first stumble", len(blocks))
	}
	addr := digestFixture(t)
	run := blocks[0]
	args := strings.Fields(strings.ReplaceAll(run[0], "127.0.0.1:6379", addr))
	if len(args) < 2 || args[0] != "nova-sprint" || args[1] != "digest" {
		t.Fatalf("first run command %q", run[0])
	}
	code, stdout, stderr := runSprint(args[1:]...)
	if want := strings.Join(run[1:], "\n") + "\n"; code != 0 || stderr != "" || stdout != want {
		t.Fatalf("first run: exit %d stderr %q\n--- got\n%s--- documented\n%s", code, stderr, stdout, want)
	}
	for _, h := range []string{"\nlanded:\n", "\nholds:\n", "\nreads:\n"} {
		if !strings.Contains(stdout, h) {
			t.Fatalf("first run has no %q header", strings.TrimSpace(h))
		}
	}
	stumble := blocks[1]
	if len(stumble) != 2 {
		t.Fatalf("first stumble block %q, want the command and its one line", stumble)
	}
	args = strings.Fields(stumble[0])
	code, stdout, stderr = runSprint(args[1:]...)
	if code != 2 || stdout != "" || stderr != stumble[1]+"\n" {
		t.Fatalf("first stumble: exit %d stdout %q stderr %q, documented %q", code, stdout, stderr, stumble[1])
	}
}
