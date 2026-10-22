//go:build unix && functional

// The lane-end tests start a fake native and the sleepers its card leaves behind, real
// processes in sessions of their own, and ask the kernel whether the lane's reap ended them:
// functional by the house rule (docs/TESTING.md, "The two tiers").

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// A LANE ENDS EVERYTHING ITS CARD STARTED (v1.2.15). Fleet member b1, 2026-10-12: a card's
// run backgrounded 16 shell busy loops through its slot's shell shim at 02:23Z, the card
// finished (failed), the epoch was cleared, and the loops held the machine at 100% for 13
// hours. The lane had ended its own child and nothing else; the loops sat in a session of
// their own, parented by systemd --user. These tests drive one lane per way a lane ends,
// with a fake native that backgrounds a sleeper in its own session and one that leaves it
// (setsid), and hold the member to: no process the lane started is alive 2 s after the lane
// ended, and the lane's LANE END line says how many it found alive and killed (reaped=N).

// laneOut is a writer the lane's goroutine and the test both touch.
type laneOut struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *laneOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *laneOut) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

// laneEndCase is one way a lane ends. A fake native that ends by itself prints line and
// exits code; one that waits is ended by the test: "stop" is the machine's stop (the lane
// cancelled, then handed back with stop-return: member.Stopper), "member" is the member's
// own stop (its drain bound passed, or an interrupt: the runner ends every lane it runs).
type laneEndCase struct {
	name string
	line string
	code int
	by   string
	end  string // the member.End the lane's log reads as, "" when none is asserted
}

var laneEndCases = []laneEndCase{
	{name: "finish-ok", line: "NATIVE OK label=c1 rc=0 wall=1.00s harness=ok budget=-/1"},
	{name: "finish-failed", line: "NATIVE FAIL label=c1 rc=1 wall=1.00s harness=fail budget=-/1", code: 1},
	{name: "idle-reap", line: "NATIVE FAIL label=c1 rc=-1 wall=1.00s harness=killed budget=-/1 reason=idle-reap", code: 1, end: member.EndIdleReap},
	{name: "deadline", line: "NATIVE FAIL label=c1 rc=-1 wall=1.00s harness=killed budget=-/1", code: 1, end: member.EndDeadline},
	{name: "machine-stop", by: "stop"},
	{name: "member-stop", by: "member"},
}

// escapeCommand is the command that runs `sleep 600` in a session of its own, the way a
// harness's tool shell leaves the lane's: setsid (util-linux), or "" where it is not installed
// (macOS), and the escaping sleeper is left out.
func escapeCommand() string {
	if _, err := exec.LookPath("setsid"); err == nil {
		return "setsid sleep 600"
	}
	return ""
}

// laneFixture is a fake native for one end: it backgrounds `sleep 600` (its pid written to
// plain), and where it can one more in a session of its own (escaped), marks ready, then
// prints its NATIVE line and exits, or waits for a TERM and ends as a terminated run.
type laneFixture struct {
	script, plain, escaped, ready string
	sleepers                      int
}

func newLaneFixture(t *testing.T, c laneEndCase) *laneFixture {
	t.Helper()
	dir := t.TempDir()
	f := &laneFixture{script: filepath.Join(dir, "fake-native"), plain: filepath.Join(dir, "plain.pid"),
		escaped: filepath.Join(dir, "escaped.pid"), ready: filepath.Join(dir, "ready"), sleepers: 1}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("sleep 600 >/dev/null 2>&1 &\necho $! > '" + f.plain + "'\n")
	if esc := escapeCommand(); esc != "" {
		b.WriteString(esc + " >/dev/null 2>&1 &\necho $! > '" + f.escaped + "'\n")
		f.sleepers++
	}
	b.WriteString(": > '" + f.ready + "'\n")
	if c.by != "" {
		b.WriteString("trap 'echo \"NATIVE FAIL label=c1 rc=-1 wall=1.00s harness=killed budget=-/1 reason=terminated\"; exit 143' TERM\n")
		// sh runs a trap only once the command in the foreground ends, so the step is
		// how long the stop waits to be heard: short, so the test does not wait it out.
		b.WriteString("while :; do sleep 0.01; done\n")
	} else {
		b.WriteString("echo '" + c.line + "'\nexit " + strconv.Itoa(c.code) + "\n")
	}
	require.NoError(t, os.WriteFile(f.script, []byte(b.String()), 0o755))
	t.Cleanup(func() {
		// only the sleepers this fixture started, by the pids it recorded
		for _, pid := range f.pids() {
			_ = syscall.Kill(pid, syscall.SIGKILL) // ignored: gone already is the point
		}
	})
	return f
}

// pids are the sleepers' pids the fixture recorded, waiting up to 5 s for them.
func (f *laneFixture) pids() []int {
	var out []int
	for _, path := range []string{f.plain, f.escaped} {
		if pid := readLanePID(path); pid > 0 {
			out = append(out, pid)
		}
	}
	return out
}

func readLanePID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// laneProcAlive is whether pid names a live process: a zombie (ended, its parent not yet
// reaping it) is not alive.
func laneProcAlive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndex(string(b), ")"); i >= 0 {
			if f := strings.Fields(string(b)[i+1:]); len(f) > 0 && (f[0] == "Z" || f[0] == "X") {
				return false
			}
		}
	}
	return true
}

func waitForLaneReady(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool { _, err := os.Stat(path); return err == nil }, 30*time.Second, time.Millisecond, "the fake native never started its sleepers")
}

var laneEndLine = regexp.MustCompile(`(?m)^LANE END card=c1 .*\breaped=(\d+)\b.*$`)

// TestALaneEndLeavesNoProcessOfItsCardAlive is the defect's test, one subtest per way a
// lane ends: 2 s after the lane ended no process its card started is alive, the one that
// left the lane's session included, and the lane's LANE END line says reaped=<the sleepers>.
func TestALaneEndLeavesNoProcessOfItsCardAlive(t *testing.T) {
	t.Parallel()
	for _, c := range laneEndCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newLaneFixture(t, c)
			root := t.TempDir()
			out := &laneOut{}
			r := &nativeRunner{self: f.script, harness: "h", model: "fake/fake-model", root: root, slots: filepath.Join(root, "slots"),
				resultsRoot: filepath.Join(root, "results"), deadline: time.Minute, tokens: "unmetered", stderr: &bytes.Buffer{}, out: out}
			require.NoError(t, os.MkdirAll(r.slots, 0o755))
			ch, err := r.Start(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1})
			require.NoError(t, err)
			child := ch.(*nativeChild)
			t.Cleanup(func() {
				if child.proc != nil {
					_ = child.proc.Kill() // ignored: the fake native this test started, gone already when the lane ended
				}
			})
			waitForLaneReady(t, f.ready)
			switch c.by {
			case "stop":
				require.Positive(t, child.Stop(), "the machine's stop signals the lane's native")
			case "member":
				ender, ok := any(r).(interface{ endLanes() int })
				require.True(t, ok, "the member's stop has no way to end its lanes: native and every process its card started outlive the member")
				require.Equal(t, 1, ender.endLanes(), "the member's stop ends the one lane it runs")
			}
			require.Eventually(t, child.Done, 60*time.Second, time.Millisecond, "the lane ends")
			if c.end != "" {
				require.Equal(t, c.end, child.Result().End, "the fixture is the end it names")
			}
			pids := f.pids()
			require.Len(t, pids, f.sleepers, "the fixture recorded each sleeper it started")
			require.Eventually(t, func() bool {
				for _, pid := range pids {
					if laneProcAlive(pid) {
						return false
					}
				}
				return true
			}, 2*time.Second, time.Millisecond, "a process the lane's card started (%v) is alive 2s after the lane ended (%s)", pids, c.name)
			m := laneEndLine.FindStringSubmatch(out.String())
			require.NotNil(t, m, "the lane's end says what it reaped: %q", out.String())
			require.Equal(t, strconv.Itoa(f.sleepers), m[1], "reaped= counts the processes the lane found alive and killed")
		})
	}
}

// TestALaneFixtureLeavesItsSleepersWhenNothingReapsThem proves the fixture can fail the
// test above: run alone, with no lane around it, the fake native exits and leaves each of its
// sleepers alive (each sleeps 600 s, so only a reap ends it within the test's 2 s; the
// unpatched tree's run of the test above is the measured red).
func TestALaneFixtureLeavesItsSleepersWhenNothingReapsThem(t *testing.T) {
	t.Parallel()
	f := newLaneFixture(t, laneEndCases[0])
	cmd := exec.Command(f.script)
	require.NoError(t, cmd.Run())
	pids := f.pids()
	require.Len(t, pids, f.sleepers)
	for _, pid := range pids {
		require.True(t, laneProcAlive(pid), "the fixture's sleeper %d outlives the fake native when nothing reaps it", pid)
	}
}

// TestAQuietLaneEndReapsNothing pins the ordinary case: a lane whose card left nothing
// running says reaped=0.
func TestAQuietLaneEndReapsNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-native")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'NATIVE OK label=c1 rc=0 wall=1.00s harness=ok budget=-/1'\nexit 0\n"), 0o755))
	root := t.TempDir()
	out := &laneOut{}
	r := &nativeRunner{self: script, harness: "h", model: "fake/fake-model", root: root, slots: filepath.Join(root, "slots"),
		resultsRoot: filepath.Join(root, "results"), deadline: time.Minute, tokens: "unmetered", stderr: &bytes.Buffer{}, out: out}
	require.NoError(t, os.MkdirAll(r.slots, 0o755))
	ch, err := r.Start(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1})
	require.NoError(t, err)
	require.Eventually(t, ch.Done, 30*time.Second, time.Millisecond)
	m := laneEndLine.FindStringSubmatch(out.String())
	require.NotNil(t, m, "every lane end says what it reaped: %q", out.String())
	require.Equal(t, "0", m[1])
}
