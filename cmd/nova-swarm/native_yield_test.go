package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// CI OVER A CARD'S CHILD (nova-tools#4293; Glenn 2026-10-01: "We really need to have CI
// winning over children, or we will have failed CIs non-stop across the fleet"). A
// member's launch is `nova-swarm native` started by nativeRunner.Start; native steps
// itself to yield.Nice before it starts the wall, so the harness and everything the
// harness runs inherit it, while the member's own loop stays where it is.

// TestANativeRunThatCannotYieldIsTheMachinesRefusal: a launch whose step behind CI fails
// is refused with the reason on its NATIVE REFUSED line. The member reads that one
// refusal as a staging refusal (no child ran, the machine's fault): the finish is
// `staging refused: yield to CI: ...` and the sprint deals the card elsewhere, never a
// failed finish charged to the card. Every other NATIVE REFUSED line, and a yield line
// beside a NATIVE rc line, stays the card's, its reason in the report. A step that works
// writes nothing and lets the run go on.
func TestANativeRunThatCannotYieldIsTheMachinesRefusal(t *testing.T) {
	t.Parallel()
	var ok bytes.Buffer
	require.True(t, yieldNative(func() error { return nil }, &ok), "a step that works refuses nothing")
	assert.Empty(t, ok.String(), "a step that works writes nothing")

	var refused bytes.Buffer
	require.False(t, yieldNative(func() error { return errors.New("nice 15: operation not permitted") }, &refused), "a step that fails is a refusal")
	require.Contains(t, refused.String(), "NATIVE REFUSED: yield to CI: nice 15: operation not permitted")

	other := "NATIVE REFUSED: the harness binary /x is missing; run: nova-swarm native -h\n"
	rc := "NATIVE OK label=c1 job=/j tmp=/t rc=0 wall=1.00s sandbox=none card_sha256=a binary_sha256=b config=- harness=ok budget=unmetered\n"
	cases := []struct {
		name, log, staging, report string
	}{
		{"the yield refusal is a staging refusal", refused.String(), "yield to CI: nice 15: operation not permitted", "no child ran"},
		{"another refusal is the card's, its reason reported", other, "", "native refused: the harness binary /x is missing"},
		{"a yield line beside a NATIVE rc line is not one", refused.String() + rc, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "c1.native.log")
			require.NoError(t, os.WriteFile(logPath, []byte(c.log), 0o644))
			ch := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(dir, "results"), job: filepath.Join(dir, "job"), done: make(chan struct{})}
			res := ch.Result()
			fin, why := member.Judge(res, member.Push{None: "the child committed nothing"})
			assert.Equal(t, member.FinishFailed, fin, "no result is never an ok finish (%s)", why)
			if c.staging != "" {
				assert.Equal(t, member.EndStaging, res.End, "the machine's refusal ends as staging refused")
				assert.True(t, strings.HasPrefix(res.Staging, c.staging), "staging text %q, want it to begin %q", res.Staging, c.staging)
				assert.True(t, strings.HasPrefix(why, member.EndStaging+": "+c.staging), "the finish's reason %q", why)
			} else {
				assert.NotEqual(t, member.EndStaging, res.End, "only the yield refusal, with no rc line, is the machine's")
			}
			assert.Contains(t, res.Report, c.report)
		})
	}
}

// TestAMemberOnAnOSWithNoSetpriorityRefusesToStart: on an OS with no setpriority the
// member will not start (native would refuse every card it took); where a launch can
// step behind CI there is no refusal.
func TestAMemberOnAnOSWithNoSetpriorityRefusesToStart(t *testing.T) {
	t.Parallel()
	assert.Empty(t, yieldRefusal(true, "linux"), "a supported OS starts")
	why := yieldRefusal(false, "plan9")
	assert.True(t, strings.HasPrefix(why, "no setpriority on plan9:"), "the refusal names the OS: %q", why)
	assert.Contains(t, why, "refuse every card")
}

// TestSetpriorityRefusalNamesNoTicket: the refusal a member returns keeps the reason
// and the remedy and carries no ticket number, because a ticket belongs in a comment
// and never in a line the tool prints.
func TestSetpriorityRefusalNamesNoTicket(t *testing.T) {
	t.Parallel()
	why := yieldRefusal(false, "darwin")
	assert.NotContains(t, why, "nova-tools#", "a ticket is a comment, never a line the tool prints: %q", why)
	assert.NotContains(t, why, "#4293", "the ticket's number is gone from the line: %q", why)
	assert.Contains(t, why, "refuse every card", "the reason stays: %q", why)
	assert.Contains(t, why, "run members on darwin or Linux", "the remedy stays: %q", why)
}
