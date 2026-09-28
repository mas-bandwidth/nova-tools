// The progress duty (nova-tools #4319): every `nova-sprint reconcile` pass
// runs it through registerReconcileDuty; once per cfg:progress every_s it
// measures every stream's convergence (PROGRESS lines), and when a stream
// has stalled for the window, a duty refusal has repeated past cfg:progress
// refusals passes, or a release probe failed twice, it sets the sprint's pit
// stop with the diagnosis, prints one EVENT line and sends one wake note to
// the cfg:progress ask list (internal/nsprint/reconcile/progress.go). The
// loop feeds it each pass's refusal sum and the duty errors repeating
// unchanged (namedDuties.repeats) through progressDuty.NotePass.
package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// progressDuty is this instance's progress duty, for the loop's AfterPass.
var progressDuty *reconcile.Progress

func init() {
	registerReconcileDuty("progress", func(st *store.Store) (reconcileDuty, error) {
		progressDuty = &reconcile.Progress{Client: st.Client(), Out: reconcileOut}
		return progressDuty, nil
	})
}
