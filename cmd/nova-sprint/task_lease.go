package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// The process running the claimed child invokes beat, including its first
// start acknowledgement. The token is the attempt's fence.
func runTaskBeat(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task beat")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	id := fs.String("id", "", "")
	token := fs.String("token", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task beat", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task beat", "takes flags, not positional arguments")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task beat", err.Error())
	}
	defer st.Close()
	status, err := task.Beat(ctx, st, task.BeatRequest{
		Sprint: *sprint, ID: *id, Token: *token, Actor: *actor, Idem: *idem,
	})
	if err != nil {
		return refuse(errOut, "task beat", err.Error())
	}
	fmt.Fprintf(out, "BEAT %s id=%s\n", status, *id)
	return status.ExitCode()
}

func runTaskCancel(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task cancel")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	id := fs.String("id", "", "")
	token := fs.String("token", "", "")
	reason := fs.String("reason", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task cancel", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task cancel", "takes flags, not positional arguments")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task cancel", err.Error())
	}
	defer st.Close()
	status, err := task.Cancel(ctx, st, task.CancelRequest{
		Sprint: *sprint, ID: *id, Token: *token, Reason: *reason, Actor: *actor, Idem: *idem,
	})
	if err != nil {
		return refuse(errOut, "task cancel", err.Error())
	}
	fmt.Fprintf(out, "CANCEL %s id=%s\n", status, *id)
	return status.ExitCode()
}
