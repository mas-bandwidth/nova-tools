package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// nova-tools#3550: check and exec refuse a store whose branch has no upstream
// tracking ref (SPEC-SECRETS invariant 8), and neither their help nor the
// refusal said so. A clean local store got through names, gate, place, seal
// --no-pr and seat add, then the two consumption verbs refused at a
// prerequisite no installed text disclosed.

// TestCheckAndExecHelpNameTheUpstreamPrerequisite: each verb's own help says
// the store must be on a named branch with an upstream tracking ref, says why,
// and names the remedy for a local/offline store.
func TestCheckAndExecHelpNameTheUpstreamPrerequisite(t *testing.T) {
	t.Parallel()
	bin := buildNovaSecrets(t)
	for _, args := range [][]string{
		{"check", "--help"},
		{"check", "-h"},
		{"check", "help"},
		{"exec", "--help"},
		{"exec", "-h"},
		{"exec", "help"},
	} {
		out, errOut, code := runNovaSecrets(bin, args...)
		if !assert.Equal(t, 0, code, "%v: exit %d, want 0; stderr=%q", args, code, errOut) {
			continue
		}
		for _, want := range []string{
			"usage: nova-secrets " + args[0] + " [flags]",
			"--store <dir>",
			"named branch",
			"upstream tracking ref",
			"branch.<name>.remote",
			"why:",
			"a value a rotation replaced",
			"a local edit nobody reviewed",
			"--set-upstream-to",
			"local/offline store",
			"git init --bare",
			"push -u origin",
		} {
			assert.Contains(t, out, want, "%v help lacks %q:\n%s", args, want, out)
		}
	}
	// The root help's --store line points at the prerequisite too.
	out, _, code := runNovaSecrets(bin, "help")
	assert.Equal(t, 0, code, "root help --store line does not name the upstream tracking ref (exit %d):\n%s", code, out)
	assert.Contains(t, out, "upstream tracking ref", "root help --store line does not name the upstream tracking ref (exit %d):\n%s", code, out)
}
