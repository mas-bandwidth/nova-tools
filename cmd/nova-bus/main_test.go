package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func now() time.Time {
	t, err := time.Parse(time.RFC3339, "2026-09-09T12:34:56Z")
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

type result struct {
	code   int
	stdout string
	stderr string
}

func invoke(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut, now())
	return result{code, out.String(), errOut.String()}
}

func invokeWithDeps(t *testing.T, deps busDeps, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runWithDeps(args, strings.NewReader(stdin), &out, &errOut, now(), deps)
	return result{code, out.String(), errOut.String()}
}

func (r result) mustCode(t *testing.T, want int) result {
	t.Helper()
	if r.code != want {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, want, r.stdout, r.stderr)
	}
	return r
}

func (r result) mustContain(t *testing.T, stream, want string) result {
	t.Helper()
	got := r.stdout
	name := "stdout"
	if stream == "stderr" {
		got, name = r.stderr, "stderr"
	}
	if !strings.Contains(got, want) {
		t.Fatalf("%s does not contain %q:\n%s", name, want, got)
	}
	return r
}

func TestUsageAndUnknownVerb(t *testing.T) {
	t.Parallel()
	invoke(t, "").mustCode(t, 2).mustContain(t, "stderr", "nova-bus:")
	invoke(t, "", "help").mustCode(t, 0).mustContain(t, "stdout", "usage:")
	invoke(t, "", "wibble").mustCode(t, 2).mustContain(t, "stderr", `unknown subcommand "wibble"`)
}

// The wait usage must say plainly that an unadvanced cursor makes wait return at once
// -- so a caller with a backlog knows to run inbox first -- and the example loop must
// show --advance, which is what makes the second wait a real one. (#328)
func TestWaitUsageStatesUnadvancedCursorReturnsAtOnce(t *testing.T) {
	t.Parallel()
	banner := invoke(t, "", "help").mustCode(t, 0).stdout
	if !strings.Contains(banner, "unadvanced cursor makes wait return AT ONCE") {
		t.Fatalf("the usage text does not say plainly that an unadvanced cursor makes wait return at once:\n%s", banner)
	}
	if !strings.Contains(banner, "--advance --remote origin --branch main") {
		t.Fatalf("the wait example loop does not show --advance:\n%s", banner)
	}
}

func TestCheckRefusesABusWithNoRoster(t *testing.T) {
	t.Parallel()
	invoke(t, "", "check", "--bus", t.TempDir(), "--full").mustCode(t, 2).mustContain(t, "stderr", "participants.json")
}
