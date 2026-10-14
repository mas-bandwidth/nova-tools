package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// TestAMemberOnTheServerHostRunsItsCardsInTheBackground pins the server host
// reserve (docs/SPEC-SWARM.md, the server host reserve): the pure policy, the
// GOMAXPROCS the launch puts on the card child's environment, the background hook
// the launch calls exactly once for the leader, the one MEMBER RESERVE line the
// member prints at its start, and the zero policy a member falls back to when it
// cannot tell it is on the server host. The darwin readback of the state the
// hook sets is the proposed diff in the change's report, not this file: its
// remedy needs a ledger outside this change's file set.
func TestAMemberOnTheServerHostRunsItsCardsInTheBackground(t *testing.T) {
	t.Parallel()

	t.Run("hostReserve", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name       string
			serverHost bool
			goos       string
			cores      int
			want       reservePolicy
		}{
			{"server host darwin 32", true, "darwin", 32, reservePolicy{Background: true, GoMaxProcs: 4}},
			{"server host darwin 8", true, "darwin", 8, reservePolicy{Background: true, GoMaxProcs: 2}},
			{"not server host darwin", false, "darwin", 32, reservePolicy{}},
			{"server host linux", true, "linux", 32, reservePolicy{}},
		}
		for _, c := range cases {
			c := c
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, c.want, hostReserve(c.serverHost, c.goos, c.cores))
			})
		}
	})

	t.Run("card environment", func(t *testing.T) {
		t.Parallel()
		// the member's launch bounds the card's threads on the server host
		on := &nativeRunner{env: []string{"PATH=/bin"}, reserve: reservePolicy{GoMaxProcs: 4}}
		assert.Contains(t, on.childEnvFor("fake/model"), "GOMAXPROCS=4")
		// elsewhere the policy bounds nothing, so no GOMAXPROCS is added
		assert.NotContains(t, reserveEnv([]string{"PATH=/bin"}, reservePolicy{}), "GOMAXPROCS=4")
		// a card env that already sets GOMAXPROCS keeps its value
		own := &nativeRunner{env: []string{"PATH=/bin", "GOMAXPROCS=7"}, reserve: reservePolicy{GoMaxProcs: 4}}
		got := own.childEnvFor("fake/model")
		assert.Contains(t, got, "GOMAXPROCS=7")
		assert.NotContains(t, got, "GOMAXPROCS=4")
	})

	// the launch itself, not the hook alone: a fake native answers with the
	// GOMAXPROCS it ran with, and the runner's own background field carries a
	// fake hook that records the pid, so the pin holds even if the launch's
	// applyBackground call were deleted (a review finding of attempt 1).
	t.Run("the launch calls the background hook once for the leader", func(t *testing.T) {
		t.Parallel()
		start := func(t *testing.T, reserve reservePolicy, env []string, hook func(int) error) (*nativeChild, string) {
			t.Helper()
			dir := t.TempDir()
			gomax := filepath.Join(dir, "gomaxprocs")
			script := filepath.Join(dir, "fake-native")
			write(t, script, "#!/bin/sh\nprintenv GOMAXPROCS >'"+gomax+"' 2>/dev/null || : >'"+gomax+"'\necho 'NATIVE OK label=c1 rc=0 wall=1.00s harness=ok budget=-/1'\nexit 0\n")
			require.NoError(t, os.Chmod(script, 0o755))
			root := t.TempDir()
			r := &nativeRunner{self: script, harness: "h", model: "fake/fake-model", root: root, env: env,
				slots: filepath.Join(root, "slots"), resultsRoot: filepath.Join(root, "results"),
				deadline: time.Minute, tokens: "unmetered", stderr: &bytes.Buffer{}, out: &bytes.Buffer{},
				reserve: reserve, background: hook}
			require.NoError(t, os.MkdirAll(r.slots, 0o755))
			ch, err := r.Start(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1})
			require.NoError(t, err)
			c := ch.(*nativeChild)
			t.Cleanup(func() {
				if c.proc != nil {
					_ = c.proc.Kill() // ignored: the fake native this test started, gone already is the point
					_, _ = c.proc.Wait()
				}
			})
			return c, gomax
		}
		// the fake native writes its answer and exits at once, so the launch's
		// own done channel says when the answer is final: no clock, no polling
		ended := func(t *testing.T, c *nativeChild, gomax string) string {
			t.Helper()
			<-c.Wait()
			b, err := os.ReadFile(gomax)
			require.NoError(t, err, "the fake native's answer: %s", gomax)
			return string(b)
		}
		var pids []int
		hook := func(pid int) error { pids = append(pids, pid); return nil }
		// the server host: the hook runs exactly once, for the leader's pid, and
		// the card child's environment carries the bound
		on, gomax := start(t, reservePolicy{Background: true, GoMaxProcs: 4}, nil, hook)
		assert.Equal(t, []int{on.proc.Pid}, pids, "the launch calls the background hook exactly once, for the leader pid %d", on.proc.Pid)
		require.Equal(t, "4\n", ended(t, on, gomax), "the card child runs with GOMAXPROCS=4")
		// a card env that already sets GOMAXPROCS keeps its value at the launch too
		own, ownGomax := start(t, reservePolicy{Background: true, GoMaxProcs: 4}, []string{"GOMAXPROCS=7"}, hook)
		require.Equal(t, "7\n", ended(t, own, ownGomax), "a card env that already sets GOMAXPROCS keeps its value")
		// elsewhere the launch calls no hook at all and adds no bound: the child
		// runs with the member's own GOMAXPROCS and nothing the reserve set
		want := ""
		if v, ok := os.LookupEnv("GOMAXPROCS"); ok {
			want = v + "\n"
		}
		off, offGomax := start(t, reservePolicy{}, nil, hook)
		require.Equal(t, want, ended(t, off, offGomax), "off the server host the launch bounds nothing")
		assert.Equal(t, []int{on.proc.Pid, own.proc.Pid}, pids, "each server-host launch calls the hook once for its own leader; the zero-policy launch (leader %d) never does", off.proc.Pid)
	})

	// The darwin readback the card asks for (a child `sleep 5` through the same
	// code, its background state read back with taskpolicy) is not committed:
	// its class-test remedy (TestFunctionalImageCarriesEveryBinaryTheTierExecs)
	// needs a row in infra/functional-image/binaries.txt and its tier a
	// //go:build functional && darwin file, neither in this change's file set.
	// Both are the proposed diff in the change's report.

	t.Run("reserve line printed once", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		printReserve(&buf, true, reservePolicy{Background: true, GoMaxProcs: 4}, 32)
		assert.Equal(t, "MEMBER RESERVE server-host=true background=true gomaxprocs=4 cores=32\n", buf.String())
	})

	// the member's own start says the policy in force, once, beside its MEMBER line
	t.Run("the member says its reserve once", func(t *testing.T) {
		t.Parallel()
		run := func(t *testing.T, extra ...string) string {
			t.Helper()
			base := []string{"member", "--as", "m1", "--server", "sprint.test:6390", "--width", "2", "--harness", "/bin/true",
				"--model", "p/m", "--root", t.TempDir(), "--tokens", "unmetered", "--deadline", "30s", "--once"}
			var out, errb bytes.Buffer
			require.Equal(t, 0, cmdMember(append(base, extra...)[1:], &out, &errb, noServer), "stderr: %q", errb.String())
			var lines []string
			for _, l := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(l, "MEMBER RESERVE ") {
					lines = append(lines, l)
				}
			}
			require.Len(t, lines, 1, "the member says MEMBER RESERVE once at its start: %s", out.String())
			return lines[0]
		}
		cores := runtime.NumCPU()
		assert.Equal(t, reserveLine(true, hostReserve(true, runtime.GOOS, cores), cores), run(t, "--server-host"),
			"--server-host says the policy in force on this OS")
		assert.Equal(t, reserveLine(false, reservePolicy{}, cores), run(t),
			"an untold member says the zero policy, whatever its OS")
	})

	t.Run("missing answer means not the server", func(t *testing.T) {
		t.Parallel()
		fail := func() (string, bool) { return "", false }
		onServer := serverHostOf(false, fail, func() (string, error) { return "bench-1", nil })
		assert.False(t, onServer)
		assert.Equal(t, reservePolicy{}, hostReserve(onServer, "darwin", 32))
	})
}
