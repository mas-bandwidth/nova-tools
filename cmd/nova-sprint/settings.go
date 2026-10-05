package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// listSettings is set --list: every policy number as the tick reads it now, one line each,
// with where its value came from (nova-config, nova-sprint set, default, or compiled for a
// number that is not a setting yet), then the count (docs/SPEC-SPRINT.md section 11, "The
// policy numbers").
func listSettings(ctx context.Context, st *store.Store, stdout io.Writer) error {
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return err
	}
	if snap.Policy, err = st.Policy(ctx); err != nil {
		return err
	}
	all := snap.PolicySettings()
	for _, p := range all {
		fmt.Fprintf(stdout, "SETTING %s\n", p)
	}
	fmt.Fprintf(stdout, "SETTINGS n=%d; change one: nova-config sprint set --<name> <value> --as <you>, then nova-config apply --kind sprint\n", len(all))
	return nil
}
