package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func runTaskList(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task list")
	addr := fs.String("redis", redisDefault(), "")
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

func runTaskWidth(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task width")
	addr := fs.String("redis", redisDefault(), "")
	as := fs.String("as", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task width", err.Error())
	}
	if *as == "" {
		return refuse(errOut, "task width", "--as is required")
	}
	if fs.NArg() > 1 {
		return refuse(errOut, "task width", "takes at most one slot count")
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(errOut, "task width", err.Error())
	}
	defer st.Close()
	if fs.NArg() == 1 {
		n, err := strconv.Atoi(fs.Arg(0))
		if err != nil || n < 0 {
			return refuse(errOut, "task width", "slots must be a nonnegative integer")
		}
		machine, err := existingMachine(ctx, st, capacity.KindFriend, *as)
		if err != nil {
			return refuse(errOut, "task width", "read "+capacity.DesiredKey(capacity.KindFriend, *as)+" machine: "+err.Error())
		}
		if machine == "" {
			return refuse(errOut, "task width", "friend has no machine; set it with capacity friend --machine")
		}
		if _, err := capacity.SetFriend(ctx, st, *as, machine, n, *actor, *idem); err != nil {
			return refuseCapacity(errOut, "task width", err)
		}
	}
	width, err := task.GetWidth(ctx, st, *as)
	if err != nil {
		return refuse(errOut, "task width", err.Error())
	}
	fmt.Fprintf(out, "WIDTH %s desired=%d leased=%d free=%d\n", *as, width.Desired, width.Leased, width.Free)
	return 0
}
