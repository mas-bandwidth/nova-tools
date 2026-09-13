package main

import (
	"os"
	"strings"
	"testing"
)

// SPEC-SANDBOX test 11: THIS TOOL HAS NO WAY TO RUN A COMMAND UNWALLED. Nothing pinned
// that, and for a while the spec documented a `--no-sandbox` flag printing a
// `SANDBOX UNSANDBOXED` line that the binary has never had (security#30, the spec bug).
// The binary was fail-closed throughout -- it is the document that was wrong -- and this
// is the test that keeps them together: the flag is refused like any other flag the tool
// does not have, and the command does not run. The unsandboxed run a caller may take is
// nova-swarm's, announced by RUN UNSANDBOXED, and SPEC-SWARM's own tests pin that line.
func TestThereIsNoWayToRunAnUnwalledCommand(t *testing.T) {
	j := newJob(t)
	marker := j.base + "/the-command-ran"
	sh := j.shell(t)
	for _, flag := range []string{"--no-sandbox", "--no_sandbox", "--unsandboxed", "--disable-sandbox"} {
		args := append([]string{"--read", j.read, "--write", j.write, flag, "--"}, sh...)
		code, _, errOut := j.tool(t, j.env(), append(args, "touch "+marker)...)
		if code != 125 {
			t.Fatalf("%s: exit %d, want 125; a tool whose reason is containment has no switch that turns it off: %s", flag, code, errOut)
		}
		if !strings.Contains(errOut, "SANDBOX REFUSED reason=no_command:") ||
			!strings.Contains(errOut, "is not a flag this tool has") {
			t.Errorf("%s was refused off the grammar the spec fixes, got %q", flag, errOut)
		}
		if strings.Contains(errOut, "UNSANDBOXED") {
			t.Errorf("%s printed an UNSANDBOXED line; that line is nova-swarm's alone: %q", flag, errOut)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s RAN THE COMMAND", flag)
		}
	}
}
