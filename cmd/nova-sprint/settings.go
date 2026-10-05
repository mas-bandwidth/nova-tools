package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// listSettings is set --list: every policy number as the tick reads it now, one line each,
// with where its value came from (nova-sprint set, default, or compiled for a number that
// is not a setting yet), then the count (docs/SPEC-SPRINT.md section 11, "The policy
// numbers").
func listSettings(ctx context.Context, st *store.Store, stdout io.Writer) error {
	snap, err := st.Load(ctx, []string{sprint.Work}, nil)
	if err != nil {
		return err
	}
	all := snap.PolicySettings()
	for _, p := range all {
		fmt.Fprintf(stdout, "SETTING %s\n", p)
	}
	fmt.Fprintf(stdout, "SETTINGS n=%d; friend_idle, friend_stall_after and friend_stall_step change by nova-sprint set; nova-config sets none yet\n", len(all))
	return nil
}
