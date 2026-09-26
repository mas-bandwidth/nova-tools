//go:build functional

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

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
	if err := testbin.WriteExecutable(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
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
	// the three members and the stream's stop, landed by structure with the last (#4318)
	if n, _ := c.ZCard(ctx, "ws:"+lsStream+":landed").Result(); n != 4 {
		t.Fatalf("landed %d, want 3 and the stop", n)
	}
}
