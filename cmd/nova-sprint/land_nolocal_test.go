package main

// nova-tools#3899: batch tests never run in the coordinator's session. The
// stream branch is tested by the CI request nova-sprint land makes, a bench
// claims it, and land waits on ci:<repo>:<head>; land refuses --test on the
// coordinator seat, and preflight's 7.26 reports a go test under the
// coordinator's session.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

// goTestStub puts a go on PATH that logs each go test and runs nothing (the
// rest go to the real go); the returned func counts the go test invocations.
func goTestStub(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "go-test.log")
	script := "#!/bin/sh\nif [ \"$1\" = test ]; then echo \"$*\" >> '" + log + "'; exit 0; fi\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, err := os.ReadFile(log)
		if err != nil {
			return 0
		}
		return strings.Count(string(b), "\n")
	}
}

func TestLandRefusesLocalTestOnCoordinator(t *testing.T) {
	addr, c, url, _, _, g := lrFixture(t, "true")
	ctx := context.Background()
	// The declared batch test is a go test: land must not run it anywhere
	// on this seat.
	c.Set(ctx, "cfg:land:test:"+lsRepo, "go test ./...", 0)
	if err := c.Do(ctx, "ACL", "SETUSER", "coordinator", "on", ">seat-secret", "~*", "&*", "+@all").Err(); err != nil {
		t.Fatal(err)
	}
	goTests := goTestStub(t)
	turns := benchTurn(t, addr, nil)
	t.Setenv(store.UserEnv, preflight.PreflightSeat)
	t.Setenv(store.PasswordEnvEnv, "NOVA_TEST_LAND_COORDINATOR_PASSWORD")
	t.Setenv("NOVA_TEST_LAND_COORDINATOR_PASSWORD", "seat-secret")

	// --test on the coordinator seat: refused before anything runs, and the
	// remedy names the ci request.
	code, out, errOut := runSprint("land", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--test", "go test ./...")
	if code != 2 || out != "" || strings.Count(errOut, "\n") != 1 || !strings.Contains(errOut, "ci request") {
		t.Fatalf("--test on the coordinator seat: exit %d\nstdout %q\nstderr %q", code, out, errOut)
	}

	// The landing on the coordinator seat: the bench claims the CI request
	// and turns the head green, every member lands, no go test ran here.
	work := filepath.Join(t.TempDir(), "clone")
	code, out, errOut = runSprint("land", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--remote", url, "--ci-url", url,
		"--mirror", "none", "--workdir", work, "--api", g.srv.URL, "--tick", "1ms")
	if code != 0 || !strings.Contains(out, "TEST ci batch=3: ") ||
		!strings.Contains(out, "LANDED repo="+lsRepo+" stream="+lsSlug+" pr=#900 ") ||
		!strings.Contains(out, " members=#3,#1,#2 moved=3 builds=1 resumed=false ci=green ") {
		t.Fatalf("land on the coordinator seat: exit %d\n%s\n%s", code, out, errOut)
	}
	if *turns != 1 {
		t.Fatalf("bench turns %d, want 1: the bench claims the one CI request", *turns)
	}
	if n := goTests(); n != 0 {
		t.Fatalf("%d local go test invocations, want 0", n)
	}
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":landed").Result(); n != 3 {
		t.Fatalf("landed %d, want 3", n)
	}
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
