package ci

// THE CHECKER MATCHED ITSELF, AND --apply KILLED WHAT IT MATCHED.
//
// `tools/bench-standard.sh` counts a runner's listener processes with
//
//	ps -eo pid=,args= | awk -v dir="$d" 'index($0, dir) {print $1}'
//
// and the awk's OWN command line carries the directory, because it is right there in
// `-v dir=...`. So the checker appears in its own process table, is counted as a second
// listener, is declared STRAY because it descends from no systemd unit -- and `--apply`
// then sent it a signal. Two ways that is dangerous: a pid that has already exited can have
// been recycled by the kernel and belong to something else entirely, and a runner that is
// RUNNING A JOB has a second process (the worker) that the same rule calls stray.
//
// Two rules come out of it, and both are held here:
//
//  1. A process check matches the PROGRAM it is looking for, never "any line mentioning the
//     directory". The fake table below is exactly the dangerous case: the only line holding
//     the runner directory is the checker's own awk.
//  2. A remedy never signals a process it did not start. The script starts nothing, so it
//     kills nothing: it names the stray and the unit to stop it through.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// benchStandardPath is the script this test holds.
func benchStandardPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "tools", "bench-standard.sh")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("tools/bench-standard.sh not found above the test")
	return ""
}

// TestTheListenerCheckDoesNotMatchItself runs the script's OWN matcher line against a
// process table whose only mention of the runner directory is the checker.
func TestTheListenerCheckDoesNotMatchItself(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the bench standard is a POSIX shell script")
	}
	raw, err := os.ReadFile(benchStandardPath(t))
	if err != nil {
		t.Fatal(err)
	}
	matcher := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "ps -eo pid=,args=") && strings.Contains(line, "awk") {
			matcher = strings.TrimSpace(line)
			break
		}
	}
	if matcher == "" {
		t.Fatal("the script carries no `ps -eo pid=,args= | awk` listener matcher; if it moved, move this test with it")
	}
	// The matcher as the script runs it: pids="$(...)".
	inner := matcher
	if i := strings.Index(inner, "$("); i >= 0 {
		inner = inner[i+2:]
		if j := strings.LastIndex(inner, ")"); j >= 0 {
			inner = inner[:j]
		}
	}

	dir := t.TempDir()
	runner := filepath.Join(dir, "runner-nova-tools-1")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A process table with NO listener in it: one awk (the checker itself, carrying the
	// directory in its own argv) and one unrelated process.
	table := "  4242 awk -v dir=" + runner + " index($0, dir) {print $1}\n" +
		"  4243 /usr/bin/ssh nova@hulk bash -s\n"
	if err := os.WriteFile(filepath.Join(bin, "ps"), []byte("#!/bin/sh\ncat <<'NOVA_PS'\n"+table+"NOVA_PS\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "PATH=" + bin + ":$PATH\nd=" + runner + "\npids=\"$(" + inner + ")\"\nprintf '%s' \"$pids\"\n"
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("running the matcher: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("the listener check matched %q in a table whose only mention of the runner directory is the CHECKER ITSELF; --apply then signals that pid, which by then may belong to anything", got)
	}
}

// TestTheBenchStandardSignalsNothing: it starts no process, so it kills none. The remedy for
// a stray listener is the unit it should have been started by.
func TestTheBenchStandardSignalsNothing(t *testing.T) {
	raw, err := os.ReadFile(benchStandardPath(t))
	if err != nil {
		t.Fatal(err)
	}
	for n, line := range strings.Split(string(raw), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		for _, bad := range []string{"kill ", "kill -", "pkill", "killall"} {
			if strings.Contains(code, bad) {
				t.Errorf("line %d signals a process it did not start: %s", n+1, code)
			}
		}
	}
}
