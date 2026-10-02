//go:build linux

// The linux half of the `policy` verb: on this platform the backend is Landlock,
// which takes rules and not a profile document, so the verb must print the
// landlock ruleset the wall would build — never the darwin sandbox-exec
// template, which no linux run uses. In-process like main_test.go's job tests,
// not a subprocess like wall_linux_test.go's: the verb runs NOTHING, so no wall
// goes up and there is nothing to lift.
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPolicyVerbOnLinuxPrintsLandlockRuleset is issue #1469: `policy` answered
// `POLICY OK backend=landlock` and then printed the text beginning
// `;; nova-sandbox — darwin sandbox-exec profile, TEMPLATE.` — seven kilobytes
// of policy that is not the policy in force.
func TestPolicyVerbOnLinuxPrintsLandlockRuleset(t *testing.T) {
	t.Parallel()

	j := newJob(t)
	r := j.run(t, "policy", "--read", j.read, "--write", j.write, "--net-deny")
	require.Equal(t, 0, r.Code, "policy exit %d: %s", r.Code, r.Stderr)
	require.Contains(t, r.Stderr, "POLICY OK backend=landlock", "the POLICY OK line does not name the landlock backend: %q", r.Stderr)
	require.NotContains(t, r.Stdout, "darwin sandbox-exec profile", "policy on linux printed the darwin sandbox-exec profile template, which no linux run uses")
	// What it prints instead is the ruleset the wall would build: the backend, the
	// read and write sets, and the net promise.
	for _, want := range []string{"backend=landlock", "read=", "write=", "net="} {
		assert.Contains(t, r.Stdout, want, "the printed landlock ruleset names no %q::\n%s", want, r.Stdout)
	}
	assert.Contains(t, r.Stdout, j.read, "the printed landlock ruleset does not carry the caller's own sets:\n%s", r.Stdout)
	assert.Contains(t, r.Stdout, j.write, "the printed landlock ruleset does not carry the caller's own sets:\n%s", r.Stdout)
}
