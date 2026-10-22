// THE SCAFFOLDING VERBS, proven the way a contributor meets them (nova-tools#2498 S5 part 2).
//
// `nova-ci new-rule <name>` lays down a new class rule's skeleton:
// - internal/ci/<name>_class_test.go
// - internal/ci/testdata/<name>/fixture.txt
// - make/rule_<name>.mk
//
// `nova-ci new-verb <tool> <verb>` lays down a new CLI verb's skeleton:
// - cmd/<tool>/<verb>.go
// - cmd/<tool>/<verb>_test.go
// - cmd/<tool>/testdata/<verb>/fixture.txt
// - make/verb_<tool>_<verb>.mk
//
// Both verbs ensure the new skeleton immediately builds, tests, and conforms to
// write confinement. new-verb refuses a tool with no func main and prints the
// exact dispatch case to add; it never edits the switch itself.
//
// Each test copies into its scratch tree only the module's packages that the
// patterns it builds depend on, never all of cmd/,
// internal/, tools/ and profiles/ (rowan hold 6 on #3616: 0.15 s -> 54 s), and
// the two tests run in parallel. The verb's generated tests stand alone; the
// rule fixture also needs the existing class tests' repoRoot helper.
//
// Those two build, vet and test the copied tree through the go command, so they
// are not unit tests: the verb's lives in scaffold_functional_test.go and the
// rule's in slow_test.go. This file keeps the in-process half, the dry run and
// the refusals, which start no program.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// In process, on a temp tree: --dry-run lists exactly the files the real run
// writes and writes none; a second run, a bad name, no checkout and a tool
// with no func main are each one refusal at exit 2 that names the verb's help.
func TestScaffoldVerbsDryRunAndRefuse(t *testing.T) {
	t.Parallel()
	tree := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tree, "go.mod"), []byte("module example.com/m\n"), 0o644))

	code, dry, stderr := runCI(t, []string{"new-rule", "--root", tree, "--dry-run", "demo"}, "")
	require.Equal(t, 0, code, stderr)
	entries, err := os.ReadDir(tree)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "--dry-run wrote into the tree")
	assert.Contains(t, dry, "nova-ci new-rule NOTE --dry-run wrote nothing")

	code, wrote, stderr := runCI(t, []string{"new-rule", "--root", tree, "demo"}, "")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, strings.Count(dry, "would write "), strings.Count(wrote, "wrote "), "the dry run and the run name different files:\n%s\n%s", dry, wrote)
	assert.Equal(t, strings.ReplaceAll(strings.Split(dry, "nova-ci new-rule NOTE")[0], "would write ", "wrote "), wrote)

	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"new-rule", "--root", tree, "demo"}, "already exists"},
		{[]string{"new-rule", "--root", tree, "--dry-run", "demo"}, "already exists"},
		{[]string{"new-rule", "--root", tree, "Bad Name"}, "rule name"},
		{[]string{"new-rule", "--root", t.TempDir(), "demo"}, "no go.mod"},
		{[]string{"new-verb", "--root", tree, "ghost", "probe"}, "no func main"},
		{[]string{"new-verb", "--root", tree, "ghost"}, "new-verb wants two arguments"},
	} {
		code, stdout, stderr := runCI(t, c.args, "")
		assert.Equal(t, 2, code, "%v", c.args)
		assert.Empty(t, stdout, "%v", c.args)
		assert.Contains(t, stderr, c.want, "%v", c.args)
		assert.Regexp(t, `^nova-ci new-(rule|verb) REFUSED: .*; run: nova-ci new-(rule|verb) -h\n$`, stderr, "%v", c.args)
	}
}
