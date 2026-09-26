package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func runSprint(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runSprint()
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout %q; a refusal belongs on stderr", stdout)
	}
	if !strings.Contains(stderr, "run: nova-sprint help") {
		t.Fatalf("stderr %q, want the help door", stderr)
	}
	if strings.Count(stderr, "\n") != 1 {
		t.Fatalf("a bare command printed more than one line:\n%s", stderr)
	}
}

func TestTableRefusalNamesEveryMissingPiece(t *testing.T) {
	t.Parallel()

	code, _, stderr := runSprint("table")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--redis <addr>", "--once", "--loop", "--check", "written nowhere", "run: nova-sprint help"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestRefreshOwnSession(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		code, _, stderr := runSprint("refresh", "--", "true")
		if code != 2 || !strings.Contains(stderr, "setsid") {
			t.Fatalf("windows refresh exit %d stderr %q, want a setsid refusal", code, stderr)
		}
		return
	}
	code, stdout, stderr := runSprint("refresh", "--", "/usr/bin/true")
	if code != 0 {
		t.Fatalf("exit %d; stderr %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "REFRESH SESSION pid=") {
		t.Fatalf("stdout %q, want REFRESH SESSION pid=", stdout)
	}
}

func TestRefreshRefusesAMissingCommand(t *testing.T) {
	t.Parallel()

	code, _, stderr := runSprint("refresh")
	if code != 2 || !strings.Contains(stderr, "--") {
		t.Fatalf("exit %d stderr %q, want a refusal that names --", code, stderr)
	}
}
