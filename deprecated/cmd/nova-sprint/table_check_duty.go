// table_check_duty.go: registers the table-check duty with the reconciler (#4341).
// The duty runs once per minute, recounts every cell from the sets, and writes
// drift to the sprint's status line (s:<S> drift field).
package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	registerReconcileDuty("table-check", func(st *store.Store) (reconcileDuty, error) {
		return reconcile.NewTableCheckDuty(st.Client()), nil
	})
}
