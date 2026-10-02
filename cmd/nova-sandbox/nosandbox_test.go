package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC-SANDBOX test 11: THIS TOOL HAS NO WAY TO RUN A COMMAND UNWALLED. For a while the
// spec documented a `--no-sandbox` flag printing `SANDBOX UNSANDBOXED` that the binary has
// never had (security#30, the spec bug; the binary was fail-closed throughout). The flag is
// refused like any other the tool does not have, and the command does not run. The
// unsandboxed run a caller may take is nova-swarm's RUN UNSANDBOXED, pinned by SPEC-SWARM.
func TestThereIsNoWayToRunAnUnwalledCommand(t *testing.T) {
	t.Parallel()
	j := newJob(t)
	marker := j.base + "/the-command-ran"
	for _, flag := range []string{"--no-sandbox", "--no_sandbox", "--unsandboxed", "--disable-sandbox"} {
		r := j.run(t, append(append([]string{"--read", j.read, "--write", j.write, flag, "--"}, j.shell(t)...), "touch "+marker)...)
		require.Equal(t, 125, r.Code, "a tool whose reason is containment has no switch that turns it off: %s", r)
		// refused off the grammar the spec fixes, and UNSANDBOXED is nova-swarm's line alone
		assert.Contains(t, r.Stderr, "SANDBOX REFUSED reason=bad_flag: unknown flag "+flag+"; the flags are --read, ", r)
		assert.Contains(t, r.Stderr, "; run: nova-sandbox help\n", r)
		assert.NotContains(t, r.Stderr, "UNSANDBOXED", r)
		require.Error(t, statErr(marker), "%s RAN THE COMMAND", flag)
	}
}
