package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// The process running the claimed child invokes beat, including its first
// start acknowledgement. The token is the attempt's fence.
func runTaskBeat(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task beat")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	token := fs.String("token", "", "the attempt's token, from the take")
	idem := fs.String("idem", "", verbflag.HelpIdem)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task beat", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task beat", "takes flags, not positional arguments")
	}
	id := oneID(*ids)
	if id == "" {
		return refuse(errOut, "task beat", "--ids wants one id")
	}
	actor := seatActor()
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task beat", err.Error())
	}
	defer st.Close()
	status, err := task.Beat(ctx, st, task.BeatRequest{
		Sprint: *sprint, ID: id, Token: *token, Actor: actor, Idem: *idem,
	})
	if err != nil {
		return refuse(errOut, "task beat", err.Error())
	}
	fmt.Fprintf(out, "BEAT %s id=%s\n", status, id)
	return status.ExitCode()
}

func runTaskCancel(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task cancel")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	token := fs.String("token", "", "the attempt's token, from the take")
	why := fs.String("why", "", verbflag.HelpWhy)
	idem := fs.String("idem", "", verbflag.HelpIdem)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task cancel", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task cancel", "takes flags, not positional arguments")
	}
	id := oneID(*ids)
	if id == "" {
		return refuse(errOut, "task cancel", "--ids wants one id")
	}
	actor := seatActor()
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task cancel", err.Error())
	}
	defer st.Close()
	status, err := task.Cancel(ctx, st, task.CancelRequest{
		Sprint: *sprint, ID: id, Token: *token, Reason: *why, Actor: actor, Idem: *idem,
	})
	if err != nil {
		return refuse(errOut, "task cancel", err.Error())
	}
	fmt.Fprintf(out, "CANCEL %s id=%s\n", status, id)
	return status.ExitCode()
}
