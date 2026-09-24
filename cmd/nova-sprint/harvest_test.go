package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// harvestModeRefused runs `card harvest` against a live store with the mode
// flags given and wants the (--once | --loop) usage refusal: exit 2, nothing
// on stdout, and no connection to the store at all, because the refusal comes
// before the store is opened (stella HOLD 4 at ddd06b2a, item 1).
func harvestModeRefused(t *testing.T, mode ...string) {
	t.Helper()
	t.Setenv(store.UserEnv, "")
	mr := miniredis.RunT(t)
	args := append([]string{"card", "harvest", "--redis", mr.Addr(), "--sprint", "s", "--bench", "b"}, mode...)
	code, stdout, stderr := runSprint(args...)
	if code != 2 {
		t.Fatalf("%v: exit %d, want 2 (usage); stderr %q", mode, code, stderr)
	}
	if stdout != "" {
		t.Fatalf("%v: stdout %q; a refusal belongs on stderr", mode, stdout)
	}
	for _, want := range []string{"exactly one of --once or --loop", "(--once | --loop [--every 10s])"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("%v: stderr %q lacks %q", mode, stderr, want)
		}
	}
	if n := mr.TotalConnectionCount(); n != 0 {
		t.Fatalf("%v: %d store connection(s) before the refusal; the mode is checked before store.Open", mode, n)
	}
}

// TestHarvestRefusesNeitherOnceNorLoop: with no mode flag the verb used to
// open Redis and run one mutating pass; it is a usage error.
func TestHarvestRefusesNeitherOnceNorLoop(t *testing.T) {
	harvestModeRefused(t)
}

// TestHarvestRefusesBothOnceAndLoop: both mode flags are the same usage error.
func TestHarvestRefusesBothOnceAndLoop(t *testing.T) {
	harvestModeRefused(t, "--once", "--loop")
}
