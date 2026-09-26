package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func runTaskList(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task list")
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	as := fs.String("as", "", verbflag.HelpAs)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	state := fs.String("state", "", "list only tasks in this state")
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
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	as := fs.String("as", "", verbflag.HelpAs)
	slots := fs.Int("slots", -1, "the width to set: how many tasks the friend works at once")
	idem := fs.String("idem", "", verbflag.HelpIdem)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task width", err.Error())
	}
	if *as == "" {
		return refuse(errOut, "task width", "--as is required")
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task width", "takes flags, not positional arguments; the width is --slots <n>")
	}
	actor := seatActor()
	*as = strings.TrimPrefix(*as, "friend:")
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(errOut, "task width", err.Error())
	}
	defer st.Close()
	if *slots >= 0 {
		n := *slots
		machine, err := existingMachine(ctx, st, capacity.KindFriend, *as)
		if err != nil {
			return refuse(errOut, "task width", "read "+capacity.DesiredKey(capacity.KindFriend, *as)+" machine: "+err.Error())
		}
		if machine == "" {
			return refuse(errOut, "task width", "friend has no machine; set it with capacity friend --machine")
		}
		if _, err := capacity.SetFriend(ctx, st, *as, machine, n, actor, *idem); err != nil {
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
