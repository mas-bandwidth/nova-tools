//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewVerbYieldsABuildingTestingSkeleton proves a new-verb skeleton builds,
// vets, tests and makes under the real go and make, measured over 5 s on the
// 2026-09-25 PR run, so it is the functional tier, not a unit wait (#4221).
func TestNewVerbYieldsABuildingTestingSkeleton(t *testing.T) {
	t.Parallel()
	_, bin, tree := scaffoldTree(t, "./cmd/nova-ci")

	// Run new-verb verb
	out := runScaffoldCmd(t, bin, "new-verb", "--root", tree, "nova-ci", "probe")
	for _, want := range []string{
		"cmd/nova-ci/probe.go",
		"cmd/nova-ci/probe_test.go",
		"cmd/nova-ci/testdata/probe/fixture.txt",
		"make/verb_nova-ci_probe.mk",
	} {
		assert.Contains(t, out, want, "new-verb did not report writing %s:\n%s", want, out)
		_, err := os.Stat(filepath.Join(tree, filepath.FromSlash(want)))
		assert.NoError(t, err, "new-verb did not write %s: %v", want, err)
	}

	// DISPATCH: new-verb prints the exact case to add and leaves the switch alone
	dispatch := "\tcase \"probe\":\n\t\treturn cmdProbe(args[1:], stdout, stderr)\n"
	assert.Contains(t, out, "add to the dispatch switch in cmd/nova-ci", "new-verb did not print the dispatch case %q:\n%s", dispatch, out)
	assert.Contains(t, out, dispatch, "new-verb did not print the dispatch case %q:\n%s", dispatch, out)
	mainGo := filepath.Join(tree, "cmd", "nova-ci", "main.go")
	src, err := os.ReadFile(mainGo)
	require.NoError(t, err)
	assert.NotContains(t, string(src), "cmdProbe", "new-verb edited the dispatch switch in %s; it must only print the case", mainGo)

	// BUILDING: the copied tree builds cleanly
	runIn(t, tree, "go", "build", "./cmd/nova-ci")
	runIn(t, tree, "go", "vet", "./cmd/nova-ci")

	// TESTING: the fixture test passes
	got := runIn(t, tree, "go", "test", "-v", "-count=1", "-run", "TestCmdProbe", "./cmd/nova-ci")
	assert.True(t, strings.Contains(got, "PASS") || strings.Contains(got, "ok"), "go test of the CLI verb skeleton did not pass:\n%s", got)

	// Makefile integration: make test-verb-nova-ci-probe runs and passes
	got = runIn(t, tree, "make", "-f", "Makefile", "test-verb-nova-ci-probe")
	assert.True(t, strings.Contains(got, "PASS") || strings.Contains(got, "ok"), "make test-verb-nova-ci-probe did not pass:\n%s", got)

	// The printed case is exact: pasted under the switch, the verb runs
	const sw = "\tswitch args[0] {\n"
	require.Contains(t, string(src), sw, "%s has no %q to paste the case under", mainGo, sw)
	require.NoError(t, os.WriteFile(mainGo, []byte(strings.Replace(string(src), sw, sw+dispatch, 1)), 0o644))
	got = runIn(t, tree, "go", "run", "./cmd/nova-ci", "probe")
	assert.Contains(t, got, "nova-ci probe: OK", "nova-ci probe after pasting the printed case did not run the verb:\n%s", got)

	// Write discipline: second run refuses rather than overwrite
	cmd := exec.Command(bin, "new-verb", "--root", tree, "nova-ci", "probe")
	cmd.Env = goenv.Clean(os.Environ())
	combined, err := cmd.CombinedOutput()
	assert.Error(t, err, "second new-verb run was not refused (err %v):\n%s", err, combined)
	assert.Contains(t, string(combined), "already exists", "second new-verb run was not refused (err %v):\n%s", err, combined)

	// Invalid names are refused
	cmd = exec.Command(bin, "new-verb", "--root", tree, "nova-ci", "Invalid Verb!")
	cmd.Env = goenv.Clean(os.Environ())
	combined, err = cmd.CombinedOutput()
	assert.Error(t, err, "new-verb with invalid verb name was not refused (err %v):\n%s", err, combined)
	assert.Contains(t, string(combined), "verb name", "new-verb with invalid verb name was not refused (err %v):\n%s", err, combined)

	// A tool with no func main is refused and nothing is written
	cmd = exec.Command(bin, "new-verb", "--root", tree, "ghost", "probe")
	cmd.Env = goenv.Clean(os.Environ())
	combined, err = cmd.CombinedOutput()
	assert.Error(t, err, "new-verb into a tool with no func main was not refused (err %v):\n%s", err, combined)
	assert.Contains(t, string(combined), "no func main", "new-verb into a tool with no func main was not refused (err %v):\n%s", err, combined)
	_, err = os.Stat(filepath.Join(tree, "cmd", "ghost"))
	assert.Error(t, err, "new-verb into a tool with no func main wrote cmd/ghost")
}
