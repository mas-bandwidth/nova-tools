//go:build darwin && functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
)

// profiles/darwin-check.sh, run against the profile THIS TOOL generates rather than the
// one the script fills for itself. One text, filled two ways: if the generator and the
// script ever disagree, this is where it shows, and it shows as a named check rather
// than as a job that dies in its first second.
func TestTheCheckScriptPassesAgainstTheToolsProfile(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	root := repoRoot(t)
	script := filepath.Join(root, "profiles", "darwin-check.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("skipped: %s is not in this checkout", script)
	}
	bin := filepath.Join(t.TempDir(), "nova-sandbox")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-sandbox")
	build.Dir = root
	build.Env = goenv.Clean(os.Environ())
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the tool: %v\n%s", err, out)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	// Test 16 is absolute: no test reaches outside t.TempDir() or touches the network.
	// The script's own scratch lives beside it, in the repo working tree, and its two DNS
	// checks curl a third-party host — right for the operator run and for the mac CI job,
	// where the spec's work list puts them, and wrong for a Go test on a machine with an
	// egress policy, where they would go red for a reason that is not about the wall.
	cmd.Env = append(os.Environ(),
		"NOVA_SANDBOX_FILL="+bin,
		"NOVA_CHECK_SCRATCH="+filepath.Join(t.TempDir(), "check"),
		"NOVA_CHECK_NO_NETWORK=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("darwin-check.sh against the tool's generated profile failed: %v\n%s", err, out)
	}
	// The count is the SCRIPT's, and no number here or in the spec states it: a test that
	// named one would go red every time a check was added. What is asserted is that the
	// script ran a real suite and that none of it failed.
	if n := strings.Count(string(out), "CHECK OK name="); n < 20 {
		t.Fatalf("only %d checks passed; the script's own count is higher than that:\n%s", n, out)
	}
	if n := strings.Count(string(out), "CHECK SKIP name="); n != 2 {
		t.Fatalf("want the two DNS checks skipped under NOVA_CHECK_NO_NETWORK, got %d SKIP lines:\n%s", n, out)
	}
	if strings.Contains(string(out), "CHECK FAIL") {
		t.Fatalf("a check failed against the tool's profile:\n%s", out)
	}
}
