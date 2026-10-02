package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// ask <primary> --instead <reader> (the owner, 2026-10-01: "get the verbs in
// man."): on a STOPPED machine and on a RUNNING one the read is taken off the
// reader and asked of another reader in one step, the reader's late report is
// refused naming the retirement, and --instead with --another, --group or two
// primaries is refused with nothing changed.
func TestAskInsteadTakesOneReadOffOneReaderStoppedAndRunning(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	readers := []string{"reader-a", "reader-b", "reader-c", "reader-d"}
	ta.ok("init --readers " + strings.Join(readers, ",") + " --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.deal(2)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1")
	ta.failOnce("m1", "s1-2.w1@1", "tests red")
	ta.ok("ask s1-1")
	var takenBack []string // a reader whose read was taken back is never asked the attempt again
	holds := func() (held, free []string) {
		for _, rd := range readers {
			var q struct{ Cards []queueCard }
			ta.json("queue --as "+rd, &q)
			if len(q.Cards) == 1 {
				held = append(held, rd)
			} else if !slices.Contains(takenBack, rd) {
				free = append(free, rd)
			}
		}
		return held, free
	}
	held, _ := holds()
	require.Len(t, held, 2)

	for _, line := range []string{
		"ask s1-1 --instead " + held[0] + " --another",
		"ask s1-1 s1-2 --instead " + held[0],
		"ask --group " + ta.group(sprint.NWorkFailed, "s1").ID + " --instead " + held[0],
	} {
		code, out, errs := ta.do(line)
		assert.NotEqual(t, 0, code, line)
		assert.Contains(t, out+errs, "--instead takes back one read of one primary", line)
	}

	for _, machine := range []string{"stop", "start"} {
		ta.ok(machine)
		held, free := holds()
		require.Len(t, held, 2, machine)
		require.NotEmpty(t, free, machine)
		out := ta.ok("ask s1-1 --instead " + held[0])
		assert.Contains(t, out, "MOVED s1-1 asked of "+free[0]+"; its read taken back from "+held[0]+" (instead)", machine)
		code, out, errs := ta.do("read --as " + held[0] + " --ok " + sprint.ReadCardID("s1-1", 1, held[0]))
		assert.NotEqual(t, 0, code, machine)
		assert.Contains(t, out+errs, "the coordinator took the read back and asked another reader instead", machine)
		now, _ := holds()
		assert.ElementsMatch(t, []string{held[1], free[0]}, now, machine)
		takenBack = append(takenBack, held[0])
		ta.clean()
	}
}
