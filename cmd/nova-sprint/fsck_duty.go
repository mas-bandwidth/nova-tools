// The fsck duty (nova-tools#3925, no ghost cards): every `nova-sprint
// reconcile` pass runs it after the expire duty, through
// registerReconcileDuty; it walks every sprint once per reconcile.FsckEvery
// (card fsck --repair over each, and the retire of every closed sprint's
// unfinished cards), and its DUTY line prints only when it fixed something
// (internal/nsprint/reconcile/fsck.go).
package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	registerReconcileDuty("fsck", func(st *store.Store) (reconcileDuty, error) {
		return &reconcile.Fsck{Client: st.Client()}, nil
	})
}
