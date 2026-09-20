//go:build linux

// The linux half of the `policy` verb: on this platform the backend is Landlock,
// which takes rules and not a profile document, so the verb must print the
// landlock ruleset the wall would build — never the darwin sandbox-exec
// template, which no linux run uses. In-process like main_test.go's job tests,
// not a subprocess like wall_linux_test.go's: the verb runs NOTHING, so no wall
// goes up and there is nothing to lift.
package main

import (
	"strings"
	"testing"
)

// TestPolicyVerbOnLinuxPrintsLandlockRuleset is issue #1469: `policy` answered
// `POLICY OK backend=landlock` and then printed the text beginning
// `;; nova-sandbox — darwin sandbox-exec profile, TEMPLATE.` — seven kilobytes
// of policy that is not the policy in force.
func TestPolicyVerbOnLinuxPrintsLandlockRuleset(t *testing.T) {
	j := newJob(t)
	code, out, errOut := j.tool(t, j.env(), "policy", "--read", j.read, "--write", j.write, "--net-deny")
	if code != 0 {
		t.Fatalf("policy exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "POLICY OK backend=landlock") {
		t.Fatalf("the POLICY OK line does not name the landlock backend: %q", errOut)
	}
	if strings.Contains(out, "darwin sandbox-exec profile") {
		t.Fatalf("policy on linux printed the darwin sandbox-exec profile template, which no linux run uses")
	}
	// What it prints instead is the ruleset the wall would build: the backend, the
	// read and write sets, and the net promise.
	for _, want := range []string{"backend=landlock", "read=", "write=", "net="} {
		if !strings.Contains(out, want) {
			t.Errorf("the printed landlock ruleset names no %q::\n%s", want, out)
		}
	}
	if !strings.Contains(out, j.read) || !strings.Contains(out, j.write) {
		t.Errorf("the printed landlock ruleset does not carry the caller's own sets:\n%s", out)
	}
}
