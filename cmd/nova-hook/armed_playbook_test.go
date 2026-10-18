package main

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// armedPlaybookRun hooks ada in as role on a clock at 1000.0, loses the record
// under the hook at its first challenge so it is armed again, and returns the
// lines it printed: two ARMED lines, the first arm and the re-arm.
func armedPlaybookRun(t *testing.T, role string) []string {
	t.Helper()
	r := newRig(t)
	r.clock = time.Unix(1000, 0).UTC()
	out := &streamOut{}
	flushed := false
	out.each = func(l string) {
		switch {
		case strings.HasPrefix(l, "HOOK CHALLENGE") && !flushed:
			flushed = true
			r.h.MR.FlushAll()
		case strings.HasPrefix(l, "HOOK STATE state=none"):
			r.advance(hook.RenewEvery) // the loop saw the record gone: its next renewal is due
		case strings.Contains(l, "was lost under this hook"):
			r.cancel()
		}
	}
	code := run([]string{"hook", "--as", "ada", "--role", role}, strings.NewReader(""), out, io.Discard, r.world())
	require.Equal(t, 0, code, out.all.String())
	return strings.Split(strings.TrimRight(out.all.String(), "\n"), "\n")
}

// assertArmedNotes pins that every HOOK ARMED line carries exactly one note
// naming want, and that no other line names it.
func assertArmedNotes(t *testing.T, lines []string, want, never string) {
	t.Helper()
	all := strings.Join(lines, "\n")
	armed := 0
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "HOOK ARMED") {
			armed++
			named := 0
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "HOOK NOTE") {
				i++
				if strings.Contains(lines[i], want) {
					named++
				}
			}
			assert.Equal(t, 1, named, "ARMED %d carries one note naming %q:\n%s", armed, want, all)
			continue
		}
		assert.NotContains(t, l, want, "only an ARMED line names it:\n%s", all)
	}
	assert.Equal(t, 2, armed, "the first arm and the re-arm:\n%s", all)
	assert.NotContains(t, all, never, all)
}

// TestACoordinatorArmedPushNamesThePlaybook pins R-PLAYBOOK: the coordinator's
// ARMED line, at first arm and at a re-arm after a lost record, carries one
// note naming nova-sprint help playbook as the thing to read now, and no
// other line does.
func TestACoordinatorArmedPushNamesThePlaybook(t *testing.T) {
	t.Parallel()
	lines := armedPlaybookRun(t, string(hook.Coordinator))
	assertArmedNotes(t, lines, "nova-sprint help playbook", "nova-sprint help worker-playbook")
}

// TestAFriendArmedPushNamesTheWorkerPlaybook pins R-WORKER-PLAYBOOK: a
// friend's ARMED line, at first arm and at a re-arm, carries one note naming
// nova-sprint help worker-playbook, and no other line does.
func TestAFriendArmedPushNamesTheWorkerPlaybook(t *testing.T) {
	t.Parallel()
	lines := armedPlaybookRun(t, string(hook.Friend))
	assertArmedNotes(t, lines, "nova-sprint help worker-playbook", "nova-sprint help playbook")
}

// TestOtherRolesArmedPushNamesNoPlaybook pins that the note is the
// coordinator's and the friend's only: a comms hook names neither playbook.
func TestOtherRolesArmedPushNamesNoPlaybook(t *testing.T) {
	t.Parallel()
	all := strings.Join(armedPlaybookRun(t, string(hook.Comms)), "\n")
	assert.NotContains(t, all, "playbook", all)
}
