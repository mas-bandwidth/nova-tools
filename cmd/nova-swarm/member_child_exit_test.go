package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// nativeResult reads one native child's result from a log line it is given, with the
// harness name the runner started it under.
func nativeResult(t *testing.T, log, harness string) member.Result {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "c1.native.log"), log)
	done := make(chan struct{})
	close(done)
	c := &nativeChild{card: "c1", logPath: filepath.Join(dir, "c1.native.log"), harness: harness, done: done}
	return c.Result()
}

// A child that exits 134 carries its real exit code and the harness that ran it, where the
// member's finish names them (--exit 134 --harness opencode): a 134 abort must not arrive
// as a no-result (nova-sprint #129; the member's a-member-reports-a-childs-exit-and-harness).
func TestAChildThatExits134CarriesItsExitAndHarness(t *testing.T) {
	t.Parallel()
	r := nativeResult(t, "NATIVE INCOMPLETE label=c1 job=/x rc=134 wall=1.00s sandbox=landlock harness=ok budget=unmetered\n", "opencode")
	assert.Equal(t, 134, r.Exit, "the child's real exit code")
	assert.Equal(t, "opencode", r.Harness, "the harness that ran it")
}

// A 137 child carries its exit too; a child that exits 0 and native's own rc=-1 end carry
// none, so only a real non-zero exit is named.
func TestOnlyARealNonZeroExitIsCarried(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		log  string
		exit int
	}{
		"a kill carries 137":            {log: "NATIVE INCOMPLETE label=c1 job=/x rc=137 wall=1.00s sandbox=landlock harness=ok budget=unmetered\n", exit: 137},
		"a clean run carries none":      {log: "NATIVE OK label=c1 job=/x rc=0 wall=1.00s sandbox=landlock harness=ok budget=unmetered\n", exit: 0},
		"native's own end carries none": {log: "NATIVE INCOMPLETE label=c1 job=/x rc=-1 wall=1.00s sandbox=landlock harness=ok budget=unmetered\n", exit: 0},
	} {
		r := nativeResult(t, c.log, "opencode")
		assert.Equal(t, c.exit, r.Exit, name)
	}
}
