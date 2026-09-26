//go:build functional

package main

import (
	"strings"
	"testing"
)

// TestWideTableRowSaysBehind (#4356 C): ns_snapshot reads bench:<b>:play, so
// a bench whose last fleet play stopped in a role has `behind: <role>` in its
// row's why; a bench whose play was ok has none. Real redis-server and the
// real function library: the Lua is the unit.
func TestWideTableRowSaysBehind(t *testing.T) {
	t.Parallel()

	addr := startThrowawayRedis(t)
	loadTableFunction(t, addr)
	seed(t, addr, [][]string{
		{"SADD", "benches", "hulk", "space"},
		{"HSET", "bench:hulk:beat", "at", "1"},
		{"HSET", "bench:hulk:desired", "slots", "1"},
		{"HSET", "bench:hulk:play", "result", "failed:tools", "role", "tools"},
		{"HSET", "bench:space:beat", "at", "1"},
		{"HSET", "bench:space:desired", "slots", "1"},
		{"HSET", "bench:space:play", "result", "ok", "role", "tools"},
	})
	code, stdout, stderr := runSprint("table", "--redis", addr, "--once")
	if code != 0 {
		t.Fatalf("exit %d stderr %q\n%s", code, stderr, stdout)
	}
	var hulk, space string
	for _, line := range strings.Split(stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "bench:hulk "):
			hulk = line
		case strings.HasPrefix(line, "bench:space "):
			space = line
		}
	}
	if !strings.HasSuffix(hulk, "| behind: tools") {
		t.Errorf("hulk row %q, want why behind: tools\n%s", hulk, stdout)
	}
	if space == "" || strings.Contains(space, "behind") {
		t.Errorf("space row %q, want no behind\n%s", space, stdout)
	}
}
