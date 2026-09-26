package main

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
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
	t.Parallel()

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
		{[]string{"--redis", addr, "--since", digestSince, "--repo", "nova-tools"}, `--repo "nova-tools" is not <owner>/<name>`},
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
