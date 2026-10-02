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
)

// TestPolicyVerbOnLinuxPrintsLandlockRuleset is issue #1469: `policy` answered
// `POLICY OK backend=landlock` and then printed the text beginning
// `;; nova-sandbox — darwin sandbox-exec profile, TEMPLATE.` — seven kilobytes
// of policy that is not the policy in force.
func TestPolicyVerbOnLinuxPrintsLandlockRuleset(t *testing.T) {
	t.Parallel()
	j := newJob(t)
	// What it prints is the ruleset the wall would build: the backend, the caller's own
	// read and write sets, and the net promise.
	j.run(t, "policy", "--read", j.read, "--write", j.write, "--net-deny").
		Exit(0).Err("POLICY OK backend=landlock").NotOut("darwin sandbox-exec profile").
		Out("backend=landlock", "read=", "write=", "net=", j.read, j.write)
}
