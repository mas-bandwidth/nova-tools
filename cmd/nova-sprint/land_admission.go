package main

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// pauseAdmission is SprintPause.tla AdmitLand, read before each batch begins
// external work. A batch already under way keeps its push and report paths.
func (l *lander) pauseAdmission(ctx context.Context) string {
	l.a.serial.Lock()
	m, _, err := l.st.Machine(ctx)
	l.a.serial.Unlock()
	if err != nil {
		return "landing admission cannot read the machine: " + oneline.Err(err) + "; queued cards stay; run: nova-sprint where"
	}
	if !m.Running() {
		return "the machine is STOPPED: new landings are held; queued cards stay; run: nova-sprint start after settling STOP receipts"
	}
	if m.Paused {
		return "the machine is PAUSED: new landings are held; queued cards stay; run: nova-sprint unpause"
	}
	return ""
}
