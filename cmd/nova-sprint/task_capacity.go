package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func runTaskList(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task list")
	addr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	sprint := fs.String("sprint", "", "")
	state := fs.String("state", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task list", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "task list", "takes flags, not positional arguments")
	}
	if *as == "" {
		return refuse(errOut, "task list", "--as is required")
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(errOut, "task list", err.Error())
	}
	defer st.Close()
	rows, err := task.ListStore(ctx, st, task.ListRequest{Sprint: *sprint, As: *as, State: *state})
	if err != nil {
		return refuse(errOut, "task list", err.Error())
	}
	for _, row := range rows {
		fmt.Fprintf(out, "TASK %s/%s state=%s\n", row.Sprint, row.ID, row.State)
	}
	fmt.Fprintf(out, "COUNT %d\n", len(rows))
	return 0
}
