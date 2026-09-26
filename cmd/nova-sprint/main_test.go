package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Every store client this test binary opens refuses a closed port at once
// rather than after go-redis's 1.7 s of retry waits (nova-tools#4328): a test
// asserts the refusal, never the library's backoff.
func init() { store.NoRetryWaits() }

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
	if !strings.Contains(stderr, "the verbs are ") || !strings.Contains(stderr, "nova-sprint <verb> -h") {
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
	for _, want := range []string{"--redis <addr>", "--once", "--loop", "--check", "written nowhere", "usage: nova-sprint table"} {
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
