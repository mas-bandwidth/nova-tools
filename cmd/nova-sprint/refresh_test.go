package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The refresh supervisor (refreshSupervise) is what outlives the verb: it
// holds the log, waits for the command and writes the result by rename.
// These run it in-process with /bin/sh, no store and no wall-clock wait
// (Wait returns when the child exits).

func TestRefreshSupervisorWritesExitAndLogTail(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	session := t.TempDir()
	var stderr bytes.Buffer
	code := refreshSupervise([]string{session, "--", "/bin/sh", "-c", "echo super-out; echo super-err >&2; exit 7"}, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("supervise exit %d stderr %q, want 0 and nothing", code, stderr.String())
	}
	result, err := os.ReadFile(filepath.Join(session, "result"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REFRESH RESULT exit=7 cmd=/bin/sh ", "log=" + filepath.Join(session, "log"), "super-out", "super-err"} {
		if !strings.Contains(string(result), want) {
			t.Fatalf("result lacks %q:\n%s", want, result)
		}
	}
	if strings.Contains(string(result), "wait=") {
		t.Fatalf("a plain exit 7 is no wait error:\n%s", result)
	}
	if _, err := os.Stat(filepath.Join(session, "result.tmp")); err == nil {
		t.Fatal("the rename left result.tmp behind")
	}
	var out bytes.Buffer
	if code := refreshShow([]string{session}, &out, &stderr); code != 0 || out.String() != string(result) {
		t.Fatalf("refresh show exit %d out %q, want 0 and the result", code, out.String())
	}
}

func TestRefreshSupervisorNamesACommandItCannotStart(t *testing.T) {
	t.Parallel()
	session := t.TempDir()
	var stderr bytes.Buffer
	code := refreshSupervise([]string{session, "--", filepath.Join(session, "no-such-command")}, &stderr)
	if code != 0 {
		t.Fatalf("supervise exit %d stderr %q; a start failure is a result, not a lost exit", code, stderr.String())
	}
	result, err := os.ReadFile(filepath.Join(session, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(result), "REFRESH RESULT exit=-1 ") || !strings.Contains(string(result), " wait=start:") {
		t.Fatalf("result %q, want exit=-1 and wait=start: <error>", result)
	}
}

func TestRefreshSupervisorNamesAResultItCannotWrite(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("a read-only directory refuses a write to a non-root POSIX user")
	}
	session := t.TempDir()
	log := filepath.Join(session, "log")
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The log is writable, the directory is not: the command runs, its exit
	// cannot be recorded, and the supervisor says so with the exit it holds.
	if err := os.Chmod(session, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(session, 0o700) })
	var stderr bytes.Buffer
	code := refreshSupervise([]string{session, "--", "/bin/sh", "-c", "exit 7"}, &stderr)
	if code != 1 || !strings.HasPrefix(stderr.String(), "REFRESH RESULT-WRITE-FAILED result="+filepath.Join(session, "result")+" exit=7 err=") {
		t.Fatalf("supervise exit %d stderr %q, want 1 and RESULT-WRITE-FAILED with exit=7", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(session, "result")); err == nil {
		t.Fatal("a result was written into a read-only directory")
	}
}

func TestRefreshSupervisorRefusesABadArgv(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := refreshSupervise([]string{t.TempDir()}, &stderr); code != 2 || !strings.Contains(stderr.String(), "<session> -- <command...>") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
}

func TestRefreshShowRunningAndNoSession(t *testing.T) {
	t.Parallel()
	session := t.TempDir()
	var out, stderr bytes.Buffer
	if code := refreshShow([]string{session}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "no refresh session at "+session) {
		t.Fatalf("no session: exit %d stderr %q", code, stderr.String())
	}
	if err := os.WriteFile(filepath.Join(session, "log"), []byte("so far\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := refreshShow([]string{session}, &out, &stderr); code != 3 || !strings.HasPrefix(out.String(), "REFRESH RUNNING session="+session+" log=") || !strings.Contains(out.String(), "so far") {
		t.Fatalf("running: exit %d out %q", code, out.String())
	}
	if code := refreshShow(nil, &out, &stderr); code != 2 {
		t.Fatalf("no argument: exit %d", code)
	}
}

func TestRefreshRefusesAStateDirItCannotMake(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runSprint("refresh", "--dir", file, "--", "/usr/bin/true")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "refresh directory "+file) {
		t.Fatalf("exit %d stdout %q stderr %q; nothing is started when its log cannot exist", code, stdout, stderr)
	}
}
