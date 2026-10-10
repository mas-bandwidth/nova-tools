package main

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// machineWord is the machine's state word a worker's verb carries in its answer, RUNNING or
// STOPPED (docs/SPEC-SPRINT.md section 14, stop cancels jobs): the member reads it off its
// queue (pkg/member queueOut.Machine) and a friend's daemon off its beat
// (pkg/friend ParseMachine), and on STOPPED each cancels its lanes, hands every card
// back with stop-return and starts nothing. "" when the store keeps no machine record yet
// (a sprint never started, a store without the records): nothing is known, and the worker
// goes on as it did.
func machineWord(ctx context.Context, st *store.Store) string {
	m, _, err := st.Machine(ctx)
	if err != nil || m.Since.IsZero() {
		return ""
	}
	return m.StateWord()
}
