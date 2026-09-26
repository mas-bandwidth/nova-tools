package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// beatStarts records every beat loop a verb in this test binary starts,
// keyed by the loop's --redis: the binary is not nova-sprint, so nothing is
// ever started for real. A started fake loop is pid 4242, and it is alive.
var beatStarts = struct {
	sync.Mutex
	by map[string][][]string
}{by: map[string][][]string{}}

func init() {
	friendLoop = loopStarter{
		start: func(argv []string, _ string) (int, error) {
			addr := ""
			if i := slices.Index(argv, "--redis"); i >= 0 && i+1 < len(argv) {
				addr = argv[i+1]
			}
			beatStarts.Lock()
			defer beatStarts.Unlock()
			beatStarts.by[addr] = append(beatStarts.by[addr], argv)
			return 4242, nil
		},
		alive: func(pid int) bool { return pid == 4242 },
		log:   testBeatLog,
	}
}

// testBeatLog is the fake loops' log path: nothing is ever written there.
func testBeatLog(friend string) (string, error) {
	return filepath.Join(os.TempDir(), "nova-sprint-test-beatloop", friend, "beatloop.log"), nil
}

// startsFor is the loops started with --redis addr.
func startsFor(addr string) [][]string {
	beatStarts.Lock()
	defer beatStarts.Unlock()
	return slices.Clone(beatStarts.by[addr])
}

// TestFriendPullAndWorkWhoFlags: one word each for --model, --harness and
// --child on friend pull, card work and task take, refused before any store
// is touched; --lease wants --loop and --loop is not --once.
func TestFriendPullAndWorkWhoFlags(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"friend", "pull", "--as", "friend:rowan", "--model", "opus 5.5", "--redis", "127.0.0.1:1"},
		{"card", "work", "--as", "friend:rowan", "--fill", "--child", "a b", "--redis", "127.0.0.1:1"},
		{"task", "take", "--actor", "rowan", "--harness", "claude code", "--redis", "127.0.0.1:1"},
		{"friend", "beat", "--as", "friend:rowan", "--lease", "t", "--redis", "127.0.0.1:1"},
		{"friend", "beat", "--as", "friend:rowan", "--loop", "--once", "--redis", "127.0.0.1:1"},
	} {
		code, out, errOut := runSprint(args...)
		if code != 2 || out != "" || !strings.HasPrefix(errOut, "nova-sprint "+args[0]+" "+args[1]+": ") {
			t.Fatalf("%v: exit %d %q %q", args, code, out, errOut)
		}
	}
}
