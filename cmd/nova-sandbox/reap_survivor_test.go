package main

import (
	"fmt"
	"io"
	"strings"
	"syscall"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
)

// reapSurvivors is killProcesses's escalation, a step of
// TestReapKillsWhatHeldAnOrphanedVolumeAndDeletesIt (which swaps the reap seams, so it is
// serial and on the allowlist). Every holder is sent SIGTERM, then one grace; only what is
// still alive is sent SIGKILL, then a second grace; what is alive after that is counted in a
// NOTE, and the delete is attempted either way. The grace is the reapGraceSleep seam, so the
// order is read from one log with no clock. Before this step, removing the SIGKILL loop
// left every reap test green (the eleventh probe of the 2026-10-02 compaction).
func reapSurvivors(t *testing.T) {
	for _, c := range []struct {
		name     string
		immortal bool // the survivor outlives SIGKILL too
		order    []string
		says     string
	}{
		{name: "what outlives SIGTERM is killed after the grace", order: []string{"7401:15", "7402:15", "grace", "7401:9", "grace"}},
		{name: "what outlives SIGKILL is counted and the delete still runs", immortal: true,
			order: []string{"7401:15", "7402:15", "grace", "7401:9", "grace"}, says: "SANDBOX NOTE 1 process(es) still hold"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newReapBench(t)
			b.procs[b.volume(t, "orphan", "disk3s9")] = []int{7401, 7402}
			b.alive[7401] = true // 7402 is gone after the TERM; 7401 is not
			swap(t, &reapGraceSleep, func() { b.signals = append(b.signals, "grace") })
			swap(t, &reapSignal, func(pid int, sig syscall.Signal) error {
				b.signals = append(b.signals, fmt.Sprintf("%d:%d", pid, sig))
				if sig == syscall.SIGKILL && !c.immortal {
					b.alive[pid] = false
				}
				return nil
			})
			r := testkit.Streams(func(_ []string, _, stderr io.Writer) int { return reapAll(false, stderr) }).Do(t).Exit(0)
			assert.Equal(t, c.order, b.signals)
			assert.Contains(t, r.Stderr, "SANDBOX REAP volume=nova-orphan procs=2 deleted=yes", r)
			assert.Contains(t, strings.Join(b.vols.calls, " "), "delete:disk3s9")
			if c.says != "" {
				assert.Contains(t, r.Stderr, c.says, r)
			} else {
				assert.NotContains(t, r.Stderr, "still hold", r)
			}
		})
	}
}
