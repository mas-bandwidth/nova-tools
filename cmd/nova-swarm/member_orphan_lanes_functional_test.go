//go:build functional && linux

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// A LANE OUTLIVES NO MEMBER (v1.2.16, pkg/swarm/lanereap.go). A real `nova-swarm member` is
// dealt a read whose harness backgrounds a sleeper in a session of its own (setsid, the way a
// harness's tool shell leaves its parent's, the b1 shape of 2026-10-12) and then blocks. The
// member is killed with SIGKILL: no drain, no LANE END, its native left leading a session no
// signal reached. The sprint's epoch moves on (the second server's queue is epoch 2 and empty),
// so the same launch is never dealt to the member again and adoption never sees it. A member of
// the same name started again over the same root ends the orphaned lane before its first pass:
// the sleeper, the harness and native are gone, and its MEMBER ORPHANS line says so. Three
// processes of no lane of this member, sharing the machine, are untouched: one with no mark,
// one marked under this member's slots by a member of another name, and one marked by a member
// of this name under another slots directory. Red before the fix: the sleeper ran on.
func TestARestartedMemberEndsTheLanesAKilledMemberLeft(t *testing.T) {
	t.Parallel()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid (util-linux) is not installed: the escaping sleeper cannot be started")
	}
	e := newCaseEnv(t, false)
	head := pushedWork(t, e)
	pids := filepath.Join(e.dir, "pids")
	require.NoError(t, os.MkdirAll(pids, 0o755))
	harnessPID, sleeperPID, ready := filepath.Join(pids, "harness"), filepath.Join(pids, "sleeper"), filepath.Join(pids, "ready")
	// the member also runs its harness outside any lane (its model catalog, at start): only the
	// run inside a lane, which carries the lane's mark, starts the sleeper
	harness := e.script(t, "[ -n \"$"+swarm.LaneEnv+"\" ] || exit 0\n"+
		"echo $$ > '"+harnessPID+"'\n"+
		"setsid sleep 600 </dev/null >/dev/null 2>&1 &\necho $! > '"+sleeperPID+"'\n"+
		": > '"+ready+"'\nexec sleep 600")

	root, home, xdg := t.TempDir(), t.TempDir(), t.TempDir()
	slots := filepath.Join(root, "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	// the lane's own pids, ended at the test's end only while they still carry this test's mark
	var lanePIDs []int
	t.Cleanup(func() {
		for _, pid := range lanePIDs {
			killIfLaneUnder(pid, slots)
		}
	})

	first := &ghostSprint{read: member.Packet{
		Card: "a-1.r1.m1", Kind: "read", As: "m1", Primary: "a-1", Worker: "m2",
		Attempt: 1, Gen: 1, Epoch: 1, Head: head, WorkBranch: "sprint/a-1.w1",
		Brief: "a-1: a read whose harness leaves a sleeper\nREPO: " + e.repoURL + "\n",
	}}
	srv1 := httptest.NewServer(first)
	defer srv1.Close()
	m1, out1 := startOrphanMember(t, srv1.URL, harness, root, home, xdg)
	started := func() bool { _, err := os.Stat(ready); return err == nil }
	for until := time.Now().Add(90 * time.Second); !started() && time.Now().Before(until); {
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, started(), "the read's harness never started its sleeper\nstdout:\n%s", out1.String())
	sleeper, harnessP, native := readLanePID(sleeperPID), readLanePID(harnessPID), nativePID(t, slots)
	lanePIDs = append(lanePIDs, sleeper, harnessP, native)
	require.Positive(t, sleeper)
	require.Positive(t, harnessP)
	require.Positive(t, native)

	// the member dies with no drain: SIGKILL to the one process this test started
	require.NoError(t, m1.Process.Kill())
	_ = m1.Wait() // ignored: killed, its exit status is the SIGKILL
	require.True(t, laneProcAlive(sleeper), "the orphan exists: the sleeper outlives the killed member")

	// processes of no lane of this member, sharing the machine
	plain := startProbe(t, nil)
	otherName := startProbe(t, []string{swarm.LaneEnv + "=" + filepath.Join(slots, "a-2.r1.m2.g1.e1"), laneOwnerEnvName + "=m2"})
	otherSlots := startProbe(t, []string{swarm.LaneEnv + "=" + filepath.Join(t.TempDir(), "slots", "a-3.r1.m1.g1.e1"), laneOwnerEnvName + "=m1"})

	// the epoch moved on: the queue is epoch 2 and deals nothing, so no launch is adopted
	srv2 := httptest.NewServer(quietSprint{epoch: 2})
	defer srv2.Close()
	m2, out2 := startOrphanMember(t, srv2.URL, harness, root, home, xdg)
	defer stopOrphanMember(m2)
	gone := func() bool {
		for _, pid := range []int{sleeper, harnessP, native} {
			if laneProcAlive(pid) {
				return false
			}
		}
		return true
	}
	said := func() bool { return strings.Contains(out2.String(), "MEMBER ORPHANS as=m1 lanes=1 ") }
	for until := time.Now().Add(30 * time.Second); (!gone() || !said()) && time.Now().Before(until); {
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, gone(), "the lane a killed member left (sleeper %d, harness %d, native %d) is alive 30s after a member of its name started again\nstdout:\n%s",
		sleeper, harnessP, native, out2.String())
	require.True(t, said(), "the restarted member says what it ended\nstdout:\n%s", out2.String())
	for _, line := range strings.Split(out2.String(), "\n") {
		if strings.HasPrefix(line, "MEMBER ORPHANS ") {
			t.Logf("%s", line)
		}
	}
	require.True(t, laneProcAlive(plain.Process.Pid), "a process with no lane mark is never touched")
	require.True(t, laneProcAlive(otherName.Process.Pid), "a lane of a member of another name, under the same slots, is never touched")
	require.True(t, laneProcAlive(otherSlots.Process.Pid), "a lane of a member of this name under another slots directory is never touched")
}

// ONE MEMBER OF A NAME PER SLOTS DIRECTORY (memberlock.go). A real member m1 runs a read whose
// harness blocks, its lane alive: native, the harness, a sleeper in a session of its own. A
// second `nova-swarm member --as m1` is started over the same root while the first lives (two
// daemons by mistake, a hand-run `member --once`, a card's own test reusing a fleet machine's
// name and root). It is refused before it reaps anything: it exits non-zero within the bound,
// its one REFUSED line names the member that runs and the way out, it says no MEMBER ORPHANS,
// and the first member's lane is alive after it: the sleeper, the harness and native. The lock
// is the first member's alone: its open files hold the lock, native's hold no link to it. Red
// before the lock: the second member took the first's running lane for an orphan and ended it,
// its MEMBER ORPHANS line said so, and it ran on as a second member of the name.
func TestASecondMemberOfTheSameNameIsRefusedAndTheFirstKeepsItsLanes(t *testing.T) {
	t.Parallel()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid (util-linux) is not installed: the escaping sleeper cannot be started")
	}
	e := newCaseEnv(t, false)
	head := pushedWork(t, e)
	pids := filepath.Join(e.dir, "pids")
	require.NoError(t, os.MkdirAll(pids, 0o755))
	harnessPID, sleeperPID, ready := filepath.Join(pids, "harness"), filepath.Join(pids, "sleeper"), filepath.Join(pids, "ready")
	harness := e.script(t, "[ -n \"$"+swarm.LaneEnv+"\" ] || exit 0\n"+
		"echo $$ > '"+harnessPID+"'\n"+
		"setsid sleep 600 </dev/null >/dev/null 2>&1 &\necho $! > '"+sleeperPID+"'\n"+
		": > '"+ready+"'\nexec sleep 600")

	root, home, xdg := t.TempDir(), t.TempDir(), t.TempDir()
	slots := filepath.Join(root, "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	var lanePIDs []int
	t.Cleanup(func() {
		for _, pid := range lanePIDs {
			killIfLaneUnder(pid, slots)
		}
	})

	first := &ghostSprint{read: member.Packet{
		Card: "a-1.r1.m1", Kind: "read", As: "m1", Primary: "a-1", Worker: "m2",
		Attempt: 1, Gen: 1, Epoch: 1, Head: head, WorkBranch: "sprint/a-1.w1",
		Brief: "a-1: a read whose harness blocks\nREPO: " + e.repoURL + "\n",
	}}
	srv1 := httptest.NewServer(first)
	defer srv1.Close()
	m1, out1 := startOrphanMember(t, srv1.URL, harness, root, home, xdg)
	started := func() bool { _, err := os.Stat(ready); return err == nil }
	for until := time.Now().Add(90 * time.Second); !started() && time.Now().Before(until); {
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, started(), "the read's harness never started its sleeper\nstdout:\n%s", out1.String())
	sleeper, harnessP, native := readLanePID(sleeperPID), readLanePID(harnessPID), nativePID(t, slots)
	lanePIDs = append(lanePIDs, sleeper, harnessP, native)
	require.Positive(t, sleeper)
	require.Positive(t, harnessP)
	require.Positive(t, native)

	// a second member of the same name over the same root, while the first lives
	srv2 := httptest.NewServer(quietSprint{epoch: 2})
	defer srv2.Close()
	m2, out2 := startOrphanMember(t, srv2.URL, harness, root, home, xdg)
	exited := make(chan error, 1)
	go func() { exited <- m2.Wait() }()
	select {
	case err := <-exited:
		require.Error(t, err, "the second member of the name exits non-zero, refused\nits output:\n%s", out2.String())
	case <-time.After(30 * time.Second):
		_ = m2.Process.Kill() // ignored: only this test's own process, by its pid
		<-exited
		require.Fail(t, "a second member of the same name ran on",
			"it ran for 30s instead of refusing to start; the first member's lane after it: sleeper %d alive=%v, harness %d alive=%v, native %d alive=%v\nits output:\n%s",
			sleeper, laneProcAlive(sleeper), harnessP, laneProcAlive(harnessP), native, laneProcAlive(native), out2.String())
	}
	require.Contains(t, out2.String(), "nova-swarm member REFUSED: a member named m1 is running over "+slots+" already", "the refusal names the member that runs\n%s", out2.String())
	require.Contains(t, out2.String(), "stop the one that runs, or give this one its own --as or --root", "the refusal says the way out")
	require.NotContains(t, out2.String(), "MEMBER ORPHANS", "a refused member reaps nothing")
	require.True(t, laneProcAlive(sleeper), "the first member's sleeper is alive after the second was refused")
	require.True(t, laneProcAlive(harnessP), "the first member's harness is alive after the second was refused")
	require.True(t, laneProcAlive(native), "the first member's native is alive after the second was refused")

	// the lock is the member's alone: the member's open files hold it, native's hold no link to it
	lockPath := filepath.Join(slots, ".member-m1.lock") // spelt out: memberLockPath in the tree with the lock, so this test builds in the red tree too
	require.True(t, holdsOpen(t, m1.Process.Pid, lockPath), "the running member holds its lock file open")
	require.False(t, holdsOpen(t, native, lockPath), "the lane's native inherits no lock descriptor: the lock is close-on-exec and native is started with its stdio alone")

	// the first member is this test's own process: ended by its pid now, as the first test ends
	// its member, so the cleanup does not wait the drain's thirty seconds on a harness that
	// blocks; the lane it leaves is ended by the cleanup above, by its mark
	require.NoError(t, m1.Process.Kill())
	_ = m1.Wait() // ignored: killed, its exit status is the SIGKILL
}

// holdsOpen is whether pid has path among its open files (/proc/<pid>/fd).
func holdsOpen(t *testing.T, pid int, path string) bool {
	t.Helper()
	ents, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/fd")
	require.NoError(t, err, "listing the open files of pid %d", pid)
	for _, e := range ents {
		if target, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/" + e.Name()); err == nil && target == path {
			return true
		}
	}
	return false
}

// laneOwnerEnvName is the environment name a member writes its own name under in each lane
// (swarm.LaneOwnerEnv), spelt out here so this test also builds against a tree without it, the
// red run's; pkg/swarm TestLaneOwnerEnvIsItsWireName holds the two equal.
const laneOwnerEnvName = "NOVA_SWARM_LANE_OWNER"

// startOrphanMember starts one `nova-swarm member --as m1` over server, its environment
// scrubbed as the other real-member tests do: a private HOME and XDG_CONFIG_HOME and no NOVA_*
// variable of the host's, so no seat can forward a verb and no lane of an outer member is
// inherited.
func startOrphanMember(t *testing.T, server, harness, root, home, xdg string) (*exec.Cmd, *laneOut) {
	t.Helper()
	cmd := exec.Command(builtTool, "member", "--as", "m1", "--server", strings.TrimPrefix(server, "http://"),
		"--harness", harness, "--model", "p/m", "--tokens", "unmetered", "--deadline", "10m",
		"--root", root, "--every", "1s", "--no-wall", "--disk-floor", "0")
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg, "PATH=" + builtPath, mirrorsEnv + "="}
	for _, kv := range os.Environ() {
		n, _, _ := strings.Cut(kv, "=")
		if n != "HOME" && n != "XDG_CONFIG_HOME" && n != "PATH" && !strings.HasPrefix(n, "NOVA_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	out := &laneOut{}
	cmd.Stdout, cmd.Stderr = out, out
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { stopOrphanMember(cmd) })
	return cmd, out
}

// stopOrphanMember ends a member this test started, by its own pid: a terminate, then a kill
// after the wall-clock law's thirty seconds. A member already ended is left as it is.
func stopOrphanMember(cmd *exec.Cmd) {
	if cmd.ProcessState != nil {
		return
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }() // ignored: the exit is read by its close
	_ = cmd.Process.Signal(syscall.SIGTERM)       // ignored: a member that already exited
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill() // ignored: only this test's own process, by its pid
		<-exited
	}
}

// startProbe starts `sleep 600` in a session of its own with the host's environment, less any
// lane entries, plus extra: a process sharing the machine with the lane.
func startProbe(t *testing.T, extra []string) *exec.Cmd {
	t.Helper()
	c := exec.Command("sleep", "600")
	env := environWithoutName(environWithoutName(os.Environ(), swarm.LaneEnv), laneOwnerEnvName)
	c.Env = append(env, extra...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, c.Start())
	t.Cleanup(func() {
		_ = c.Process.Kill() // ignored: the probe this test started
		_ = c.Wait()         // ignored: as above
	})
	return c
}

// nativePID is the pid the member recorded for its one launch under slots (<launch>.pid).
func nativePID(t *testing.T, slots string) int {
	t.Helper()
	var pid int
	read := func() bool {
		files, _ := filepath.Glob(filepath.Join(slots, "*.pid")) // ignored: a pattern without meta-errors
		if len(files) != 1 {
			return false
		}
		b, err := os.ReadFile(files[0])
		if err != nil {
			return false
		}
		fields := strings.Fields(string(b))
		if len(fields) == 0 {
			return false
		}
		pid, err = strconv.Atoi(fields[0])
		return err == nil && pid > 0
	}
	for until := time.Now().Add(10 * time.Second); !read() && time.Now().Before(until); {
		time.Sleep(20 * time.Millisecond)
	}
	if pid <= 0 {
		var names []string
		ents, _ := os.ReadDir(slots) // ignored: an unreadable directory lists nothing, which the message shows
		for _, e := range ents {
			names = append(names, e.Name())
		}
		require.Fail(t, "the member recorded no one pid file", "under %s: %v", slots, names)
	}
	return pid
}

// killIfLaneUnder kills pid only while its environment still holds a lane mark under slots,
// this test's own, so a pid the kernel gave to another process since is never signalled.
func killIfLaneUnder(pid int, slots string) {
	if pid <= 1 {
		return
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return
	}
	want := []byte(swarm.LaneEnv + "=" + slots + string(filepath.Separator))
	for _, ent := range bytes.Split(raw, []byte{0}) {
		if bytes.HasPrefix(ent, want) {
			_ = syscall.Kill(pid, syscall.SIGKILL) // ignored: gone already is the end wanted
			return
		}
	}
}

// quietSprint is a sprint server whose epoch moved on: the queue deals this member nothing,
// and every other verb is answered empty.
type quietSprint struct{ epoch int }

func (s quietSprint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req sprintwire.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var resp sprintwire.Response
	for _, v := range req.Verbs {
		if len(v) > 0 && v[0] == "queue" {
			resp.Results = append(resp.Results, sprintwire.Result{Stdout: fmt.Sprintf(`{"as":"m1","epoch":%d,"width":2,"cards":[],"machine":"RUNNING"}`, s.epoch)})
			continue
		}
		resp.Results = append(resp.Results, sprintwire.Result{})
	}
	_ = json.NewEncoder(w).Encode(resp) // ignored: a member that hung up reads nothing
}
