package main

// nova-tools#3899: batch tests never run in the coordinator's session. The
// stream branch is tested by the CI request nova-sprint land makes, a bench
// claims it, and land waits on ci:<repo>:<head>; land refuses --test on the
// coordinator seat, and preflight's 7.26 reports a go test under the
// coordinator's session.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// This package's tests run under go test, which 7.26 would report: every
// preflight here reads an empty snapshot unless a test swaps its own in.
func init() {
	preflightProcs = func(context.Context) ([]preflight.Proc, error) { return nil, nil }
}

// #3899 through the verb: a go test in the coordinator's session is RED 7.26
// and preflight exits 1; one in another session is not a finding.
func TestPreflightReportsLocalGoTest(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.RequireUserAuth("coordinator", "seat-secret")
	t.Setenv(store.UserEnv, "coordinator")
	t.Setenv(store.PasswordEnvEnv, "NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD")
	t.Setenv("NOVA_TEST_PREFLIGHT_COORDINATOR_PASSWORD", "seat-secret")
	self := os.Getpid()
	snap := []preflight.Proc{
		{PID: 100, PPID: 1, Args: "/opt/claude-code/claude"},
		{PID: 200, PPID: 100, Args: "/bin/zsh -c nova-sprint preflight"},
		{PID: self, PPID: 200, Args: "nova-sprint preflight"},
		{PID: 500, PPID: 1, Args: "/opt/claude-code/claude"},
		{PID: 510, PPID: 500, Args: "go test ./..."},
	}
	prev := preflightProcs
	t.Cleanup(func() { preflightProcs = prev })
	preflightProcs = func(context.Context) ([]preflight.Proc, error) { return snap, nil }
	_, stdout, stderr := runSprint("preflight", "--redis", mr.Addr())
	if !strings.Contains(stdout, "GREEN 7.26 local batch tests: no go test under session root 100") {
		t.Fatalf("another session's go test is a finding:\n%s\n%s", stdout, stderr)
	}
	snap = append(snap, preflight.Proc{PID: 300, PPID: 100, Args: "/bin/bash -c go test ./..."},
		preflight.Proc{PID: 310, PPID: 300, Args: "/usr/local/go/bin/go test ./..."})
	code, stdout, stderr := runSprint("preflight", "--redis", mr.Addr())
	if code != 1 || !strings.Contains(stdout, "RED 7.26 local batch tests: go test pid=310 under session root 100") {
		t.Fatalf("exit %d, want 1 and RED 7.26 naming pid 310:\n%s\n%s", code, stdout, stderr)
	}
}
