//go:build unix

package sprinttable

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("SPRINT_TABLE_HELPER") {
	case "unit":
		unitHelper()
	case "child":
		childHelper()
	default:
		os.Exit(m.Run())
	}
}

// unitHelper is the loop unit. It starts the refresh as its own session
// unless SPRINT_NO_SETSID=1, then waits to be killed with its process group.
func unitHelper() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	child := exec.Command(os.Args[0])
	child.Env = helperEnv("child")
	if os.Getenv("SPRINT_NO_SETSID") != "1" {
		if err := ApplyOwnSession(child); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	<-ch
	os.Exit(0)
}

// childHelper is the refresh. It announces its pid and then waits. A signal
// delivered to the unit's process group reaches it only when it did not
// call setsid.
func childHelper() {
	pid := os.Getpid()
	if path := os.Getenv("SPRINT_PIDFILE"); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	f, err := os.OpenFile(os.Getenv("SPRINT_READY"), os.O_WRONLY, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(f, "%d\n", pid)
	f.Close()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	<-ch
	os.Exit(0)
}

func helperEnv(role string) []string {
	drop := map[string]bool{"SPRINT_TABLE_HELPER": true, "SPRINT_NO_SETSID": true}
	var out []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if drop[key] {
			continue
		}
		out = append(out, e)
	}
	return append(out, "SPRINT_TABLE_HELPER="+role)
}

func TestRefreshSurvivesUnitProcessGroupKill(t *testing.T) {
	pid := startRefresh(t, true)
	sid, err := syscall.Getsid(pid)
	if err != nil {
		t.Fatal(err)
	}
	if sid != pid {
		t.Fatalf("refresh sid %d, pid %d; setsid makes the process its own session leader", sid, pid)
	}
	if err := syscall.Kill(-unitPID(t), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitUnit(t)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("refresh pid %d died with the unit's process group: %v", pid, err)
	}
}

func TestRefreshInTheUnitGroupDiesWithTheUnit(t *testing.T) {
	pid := startRefresh(t, false)
	sid, err := syscall.Getsid(pid)
	if err != nil {
		t.Fatal(err)
	}
	if sid == pid {
		t.Fatalf("refresh sid %d equals pid; this case is the one that did not call setsid", pid)
	}
	if err := syscall.Kill(-unitPID(t), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitUnit(t)
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("refresh pid %d stayed alive after its process group was killed", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// unitHolder is the unit process for the current test, set by startRefresh
// and read by the kill. It is not shared across parallel tests: these two
// tests do not call t.Parallel.
var (
	heldUnit *exec.Cmd
	heldErr  bytes.Buffer
	heldWait sync.Once
)

func unitPID(t *testing.T) int {
	t.Helper()
	if heldUnit == nil || heldUnit.Process == nil {
		t.Fatal("no unit process")
	}
	return heldUnit.Process.Pid
}

func startRefresh(t *testing.T, ownSession bool) int {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready.fifo")
	if err := syscall.Mkfifo(ready, 0o600); err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(dir, "child.pid")
	pids := readPID(ready)

	heldErr.Reset()
	heldWait = sync.Once{}
	unit := exec.Command(os.Args[0])
	unit.Env = append(os.Environ(),
		"SPRINT_TABLE_HELPER=unit",
		"SPRINT_READY="+ready,
		"SPRINT_PIDFILE="+pidfile,
	)
	if !ownSession {
		unit.Env = append(unit.Env, "SPRINT_NO_SETSID=1")
	}
	unit.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	unit.Stderr = &heldErr
	if err := unit.Start(); err != nil {
		t.Fatal(err)
	}
	heldUnit = unit
	t.Cleanup(func() {
		reapUnit(pidfile)
		if t.Failed() && heldErr.Len() > 0 {
			t.Log(heldErr.String())
		}
	})

	select {
	case pid := <-pids:
		if pid <= 0 {
			t.Fatalf("refresh announced pid %d", pid)
		}
		return pid
	case <-time.After(30 * time.Second):
		t.Fatalf("refresh never announced its pid; unit stderr: %s", heldErr.String())
		return 0
	}
}

func waitHeld() {
	heldWait.Do(func() {
		if heldUnit != nil {
			_ = heldUnit.Wait()
		}
	})
}

func waitUnit(t *testing.T) {
	t.Helper()
	done := make(chan struct{}, 1)
	go func() {
		waitHeld()
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("unit did not exit after SIGTERM to its process group")
	}
}

func reapUnit(pidfile string) {
	if heldUnit != nil && heldUnit.Process != nil {
		_ = syscall.Kill(-heldUnit.Process.Pid, syscall.SIGKILL)
	}
	waitHeld()
	raw, err := os.ReadFile(pidfile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func readPID(path string) <-chan int {
	out := make(chan int, 1)
	go func() {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		var pid int
		if _, err := fmt.Fscan(f, &pid); err != nil {
			return
		}
		out <- pid
	}()
	return out
}
