package testguard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// childMode names what TestGuardChildProcess does when this test binary is
// started again as a child. Empty -- every ordinary run -- means it skips.
const childMode = "NOVA_TESTGUARD_CHILD"

// runChild starts this test binary again, running only TestGuardChildProcess
// in the given mode, under the guard, with GOTMPDIR at one fresh directory and
// every other temp variable at a second, sibling one -- the shape of a CI
// runner whose unit sets GOTMPDIR and not TMPDIR. The environment is the
// child's own (cmd.Env), so the parent stays parallel and the guard's
// process-wide state is never touched here. It returns the GOTMPDIR it set and
// the child's combined output.
func runChild(t *testing.T, mode string) (gotmp string, out string, err error) {
	t.Helper()
	gotmp = t.TempDir()
	out, err = runChildIn(t, mode, "", gotmp)
	return gotmp, out, err
}

// runChildIn is runChild with the child's working directory and its GOTMPDIR
// given as they are, so GOTMPDIR may be relative to dir. An empty dir keeps
// this process's working directory.
func runChildIn(t *testing.T, mode, dir, gotmp string) (string, error) {
	t.Helper()
	other := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "TMPDIR", "TMP", "TEMP", "RUNNER_TEMP", "GOTMPDIR", EnvNoHost, childMode:
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"TMPDIR="+other, "TMP="+other, "TEMP="+other,
		"GOTMPDIR="+gotmp,
		EnvNoHost+"=1",
		childMode+"="+mode,
	)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), bin, "-test.run=^TestGuardChildProcess$", "-test.count=1", "-test.v")
	cmd.Env = env
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	return buf.String(), err
}

// TestFakeUnderGOTMPDIRIsAFake is the CI runner of 2026-09-26: GOTMPDIR set,
// TMPDIR elsewhere, a fake ssh written into the test's own t.TempDir(). The
// child arms the guard from its environment and calls RefuseHosts on that
// fake; it must not panic, and the fake must really have landed under
// GOTMPDIR, or the test proves nothing.
func TestFakeUnderGOTMPDIRIsAFake(t *testing.T) {
	t.Parallel()
	gotmp, out, err := runChild(t, "gotmpdir")
	if err != nil {
		t.Fatalf("a fake in t.TempDir() under GOTMPDIR was refused as a host: %v\n%s", err, out)
	}
	_, rest, ok := strings.Cut(out, "FAKE-OK ")
	if !ok {
		t.Fatalf("the child never reached the seam:\n%s", out)
	}
	fake, _, _ := strings.Cut(rest, "\n")
	// A string prefix, not under(): the child has removed its t.TempDir() by
	// now, so the fake no longer resolves, and MkdirTemp joins GOTMPDIR as given.
	if !strings.HasPrefix(fake, gotmp+string(filepath.Separator)) {
		t.Fatalf("the child's t.TempDir() is not under GOTMPDIR %s: %s; the toolchain no longer follows GOTMPDIR and this test reads nothing", gotmp, fake)
	}
}

// TestRelativeGOTMPDIRIsAFake pins tempRoots' relative-root handling: GOTMPDIR
// is "rgotmp", relative to the child's working directory, so t.TempDir() and
// the fake written into it are relative too. isFakeProgram makes the program
// absolute; the root must be made absolute to match, or the fake is refused as
// a host. With the Abs loop in tempRoots removed this test is red.
func TestRelativeGOTMPDIRIsAFake(t *testing.T) {
	t.Parallel()
	const rel = "rgotmp"
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, rel), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runChildIn(t, "gotmpdir", dir, rel)
	if err != nil {
		t.Fatalf("a fake in t.TempDir() under a relative GOTMPDIR %q was refused as a host: %v\n%s", rel, err, out)
	}
	_, rest, ok := strings.Cut(out, "FAKE-OK ")
	if !ok {
		t.Fatalf("the child never reached the seam:\n%s", out)
	}
	fake, _, _ := strings.Cut(rest, "\n")
	if !strings.HasPrefix(fake, rel+string(filepath.Separator)) {
		t.Fatalf("the child's fake %s is not under the relative GOTMPDIR %s; the toolchain no longer joins GOTMPDIR as given and this test reads nothing", fake, rel)
	}
}

// TestRealSSHIsNotAFake is the other half: adding GOTMPDIR to the roots must
// not make the fleet's ssh a fake. Under the same child environment, the
// system ssh is still refused.
func TestRealSSHIsNotAFake(t *testing.T) {
	t.Parallel()
	const ssh = "/usr/bin/ssh"
	if _, err := os.Stat(ssh); err != nil {
		t.Skipf("%s is not on this machine (%v); there is no real ssh to refuse", ssh, err)
	}
	_, out, err := runChild(t, "realssh")
	if err != nil {
		t.Fatalf("the child failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "REFUSED ") || !strings.Contains(out, `"`+ssh+`"`) {
		t.Fatalf("the armed guard must refuse %s; the child said:\n%s", ssh, out)
	}
}

// TestGuardChildProcess is the child half of the three tests above. Run on its
// own it skips; it only does work when a parent starts it with childMode set.
func TestGuardChildProcess(t *testing.T) {
	t.Parallel()
	switch mode := os.Getenv(childMode); mode {
	case "":
		t.Skip("child half of TestFakeUnderGOTMPDIRIsAFake, TestRelativeGOTMPDIRIsAFake and TestRealSSHIsNotAFake; runs only when started by them")
	case "gotmpdir":
		if !Refusing() {
			t.Fatalf("the child must run under %s=1", EnvNoHost)
		}
		fake := filepath.Join(t.TempDir(), "ssh")
		if runtime.GOOS == "windows" {
			fake += ".bat"
		}
		if err := testbin.WriteExecutable(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		RefuseHosts(fake, "hulk", "uptime") // a panic here fails the parent with the refusal
		fmt.Printf("FAKE-OK %s\n", fake)
	case "realssh":
		if !Refusing() {
			t.Fatalf("the child must run under %s=1", EnvNoHost)
		}
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("the armed guard let the system ssh through")
			}
			fmt.Printf("REFUSED %v\n", r)
		}()
		RefuseHosts("/usr/bin/ssh", "hulk", "uptime")
	default:
		t.Fatalf("unknown %s=%q", childMode, mode)
	}
}
