//go:build functional

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox/darwincheck"
	"github.com/mas-bandwidth/nova-tools/profiles"
	"github.com/stretchr/testify/require"
)

// The darwin check (internal/sandbox/darwincheck, run by tools/sandboxcheck), run against
// the profile THIS TOOL generates rather than the one the check fills for itself. One text,
// filled two ways: if the generator and the check ever disagree, this is where it shows, and
// it shows as a named check rather than as a job that dies in its first second. It is behind
// the functional tag because the real wall it measures needs real time: the check's socket
// wait polls a live nc listener, and the unit tier does not wait.
func TestTheCheckScriptPassesAgainstTheToolsProfile(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	bin := filepath.Join(t.TempDir(), "nova-sandbox")
	build := exec.Command("go", "build", "-o", bin, "./cmd/nova-sandbox")
	build.Dir = repoRoot(t)
	build.Env = goenv.Clean(os.Environ())
	buildOut, err := build.CombinedOutput()
	require.NoError(t, err, "building the tool: %v\n%s", err, buildOut)
	// Test 16 is absolute: no test reaches outside t.TempDir() or touches the network.
	// The check's own scratch is handed to it, and its two DNS checks curl a third-party
	// host -- right for the operator run and for the mac CI job, where the spec's work list
	// puts them, and wrong for a Go test on a machine with an egress policy, where they
	// would go red for a reason that is not about the wall.
	var out, errOut bytes.Buffer
	code := darwincheck.Run(darwincheck.Options{
		Template:  profiles.DarwinTemplate,
		Scratch:   filepath.Join(t.TempDir(), "check"),
		NoNetwork: true,
		Fill:      bin,
		Stdout:    &out,
		Stderr:    &errOut,
	}, darwincheck.OSSystem{})
	require.Equal(t, 0, code, "the darwin check against the tool's generated profile failed: exit %d\n%s%s", code, out.String(), errOut.String())
	// The count is the CHECK's, and no number here or in the spec states it: a test that
	// named one would go red every time a check was added. What is asserted is that the
	// check ran a real suite and that none of it failed.
	n := strings.Count(out.String(), "CHECK OK name=")
	require.GreaterOrEqual(t, n, 20, "only %d checks passed; the check's own count is higher than that:\n%s", n, out.String())
	n = strings.Count(out.String(), "CHECK SKIP name=")
	require.Equal(t, 2, n, "want the two DNS checks skipped under NoNetwork, got %d SKIP lines:\n%s", n, out.String())
	require.NotContains(t, out.String(), "CHECK FAIL", "a check failed against the tool's profile:\n%s", out.String())
}
