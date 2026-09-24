package main

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// #3320 DONE-WHEN: the preflight verb itself, not only the package's Open,
// authenticates through store.Open as the ACL user NOVA_SPRINT_REDIS_USER
// names, with the password in the variable NOVA_SPRINT_REDIS_PASSWORD_ENV
// names. Against an ACL Redis the coordinator seat gets past AUTH and the
// checks run; with no user the verb refuses (exit 2) and names the
// coordinator seat instead of printing a RED per check as the default user.
func TestPreflightAuthenticatesAsTheNamedUser(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("coordinator", "seat-secret")
	authFailures := []string{"WRONGPASS", "NOAUTH", "invalid username-password"}

	t.Run("coordinator seat gets past AUTH", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_REDIS_USER", "coordinator")
		t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD")
		t.Setenv("NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD", "seat-secret")
		t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "")
		code, stdout, stderr := runSprint("preflight", "--redis", mr.Addr())
		if code == 2 {
			t.Fatalf("exit 2 (could not run) as the coordinator seat; stderr %s", stderr)
		}
		if !strings.Contains(stdout, "7.1") {
			t.Fatalf("no 7.1 check line: the checks did not run as the seat\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		for _, bad := range authFailures {
			if strings.Contains(stdout+stderr, bad) {
				t.Fatalf("preflight did not get past AUTH as the coordinator seat (%s)\nstdout:\n%s\nstderr:\n%s", bad, stdout, stderr)
			}
		}
	})

	t.Run("no user refuses and names the coordinator seat", func(t *testing.T) {
		t.Setenv("NOVA_SPRINT_REDIS_USER", "")
		t.Setenv("NOVA_SPRINT_REDIS_PASSWORD_ENV", "")
		t.Setenv("NOVA_REDIS_BENCH_PASSWORD", "seat-secret")
		code, stdout, stderr := runSprint("preflight", "--redis", mr.Addr())
		if code != 2 {
			t.Fatalf("exit %d, want 2: with no seat user preflight must refuse, not check as the default user\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("a refusal printed check lines on stdout:\n%s", stdout)
		}
		for _, want := range []string{"coordinator", "NOVA_SPRINT_REDIS_USER", "default user"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("refusal does not name %q:\n%s", want, stderr)
			}
		}
	})
}
