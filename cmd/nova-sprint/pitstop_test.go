package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func runPit(t *testing.T, seat string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv(seatEnv, seat)
	var out, errOut bytes.Buffer
	code := run(append([]string{"pitstop"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestPitstopVerbUsage: every usage fault exits 2 before touching Redis
// (miniredis stands in; nothing is written to it).
func TestPitstopVerbUsage(t *testing.T) {
	m := miniredis.RunT(t)
	addr := m.Addr()
	cases := []struct {
		name, seat string
		args       []string
	}{
		{"no subverb", "rowan", nil},
		{"unknown subverb", "rowan", []string{"lift", "--sprint", "s"}},
		{"no sprint", "rowan", []string{"status", "--redis", addr}},
		{"positional", "rowan", []string{"set", "--redis", addr, "--sprint", "s", "extra"}},
		{"why on clear", "rowan", []string{"clear", "--redis", addr, "--sprint", "s", "--why", "x"}},
		{"force on status", "rowan", []string{"status", "--redis", addr, "--sprint", "s", "--force"}},
		{"no by and no seat", "", []string{"set", "--redis", addr, "--sprint", "s"}},
		{"bad flag", "rowan", []string{"set", "--redis", addr, "--sprint", "s", "--nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runPit(t, tc.seat, tc.args...)
			if code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-sprint pitstop") {
				t.Fatalf("exit %d out %q err %q, want 2 and a usage line", code, out, errOut)
			}
		})
	}
	if keys := m.Keys(); len(keys) != 0 {
		t.Fatalf("usage faults wrote %v", keys)
	}
}

// TestPitstopStatusOnMiniredis: status needs no function, so it reads a stop
// written by hand on miniredis.
func TestPitstopStatusOnMiniredis(t *testing.T) {
	m := miniredis.RunT(t)
	m.HSet("s:s1:pitstop", "by", "glenn", "why", "stop", "at", "1790000000000")
	code, out, _ := runPit(t, "", "status", "--redis", m.Addr(), "--sprint", "s1")
	if code != 0 || out != "PITSTOP sprint=s1 set by=glenn at=1790000000000 (2026-09-21T14:13:20Z) why=\"stop\"\n" {
		t.Fatalf("status: %d %q", code, out)
	}
}
