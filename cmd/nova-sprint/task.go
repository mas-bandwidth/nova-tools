// The task verb registers itself through the S0 registry (registry.go), so
// adding task subverbs never edits main.go. Mutating subverbs use one
// guarded Redis Function call; list and width read indexed task state.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func init() {
	register(Verb{
		Name:    "task",
		Summary: "push, take, beat, done, cancel, list and width tasks",
		Run:     runTask,
	})
}

// openTaskStore uses the address the caller supplied for this invocation.
func openTaskStore(ctx context.Context, addr string) (*store.Store, error) {
	return store.Open(ctx, addr)
}

func runTask(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "task", "want push, take, beat, done, cancel, list or width")
	}
	switch args[0] {
	case "push":
		return runTaskPush(ctx, args[1:], out, errOut)
	case "take":
		return runTaskTake(ctx, args[1:], out, errOut)
	case "done":
		return runTaskDone(ctx, args[1:], out, errOut)
	case "beat":
		return runTaskBeat(ctx, args[1:], out, errOut)
	case "cancel":
		return runTaskCancel(ctx, args[1:], out, errOut)
	case "list":
		return runTaskList(ctx, args[1:], out, errOut)
	case "width":
		return runTaskWidth(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "task", fmt.Sprintf("unknown subverb %s; want push, take, beat, done, cancel, list or width", args[0]))
	}
}

// taskFlags is the flag set shared by the subverbs, quiet on parse error so
// the verb prints one line the way main.go does.
func taskFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

func runTaskPush(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task push")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	id := fs.String("id", "", "")
	kind := fs.String("kind", "work", "")
	title := fs.String("title", "", "")
	effects := fs.String("effects", "none", "")
	repo := fs.String("repo", "", "")
	pr := fs.Int("pr", 0, "")
	head := fs.String("head", "", "")
	ref := fs.String("ref", "", "")
	to := fs.String("to", "", "")
	front := fs.Bool("front", false, "")
	priority := fs.Int("priority", 0, "")
	payloadSHA := fs.String("payload-sha", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	est := fs.String("est", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task push", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task push", "takes flags, not positional arguments")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task push", err.Error())
	}
	defer st.Close()
	res, err := task.PushChecked(ctx, st, task.PushRequest{
		Sprint: *sprint, ID: *id, Kind: task.Kind(*kind), Title: *title,
		Effects: task.Effects(*effects), Repo: *repo, PR: *pr, Head: *head,
		Ref: *ref, To: *to, Front: *front, Priority: *priority,
		PayloadSHA: *payloadSHA, Actor: *actor, Idem: *idem,
		Est: *est, ErrOut: errOut,
	})
	if err != nil {
		return refuse(errOut, "task push", err.Error())
	}
	if res.Overlap != nil {
		// #3067: both ids on one line, on stdout like every PUSH word.
		fmt.Fprintf(out, "PUSH %s %s\n", res.Status, res.Overlap)
		return res.Status.ExitCode()
	}
	if res.Status == task.PushInvalid {
		if *est != "" && !task.IsValidEst(*est) {
			fmt.Fprintf(out, "INVALID est=%s: whole minutes 1..10080\n", *est)
			return res.Status.ExitCode()
		}
	}
	fmt.Fprintf(out, "PUSH %s id=%s\n", res.Status, *id)
	return res.Status.ExitCode()
}

func runTaskTake(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task take")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	id := fs.String("id", "", "")
	as := fs.String("as", "", "")
	n := fs.Int("n", 0, "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task take", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task take", "takes flags, not positional arguments")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task take", err.Error())
	}
	defer st.Close()
	claims, err := task.TakeAvailable(ctx, st, *as, *sprint, *id, *n, *actor, *idem)
	var blocked *task.BlockedError
	if errors.As(err, &blocked) {
		// #2939: exit 7, new because 3-6 are taken.
		fmt.Fprintf(out, "BLOCKED needs %s\n", strings.Join(blocked.Needs, " "))
		return 7
	}
	if err != nil {
		return refuse(errOut, "task take", err.Error())
	}
	if len(claims) == 0 {
		fmt.Fprintln(out, "NONE")
		return 0
	}
	for _, claim := range claims {
		fmt.Fprintf(out, "CLAIMED %s/%s attempt=%d token=%s\n",
			claim.Sprint, claim.ID, claim.Attempt, claim.Token)
	}
	return 0
}

func runTaskDone(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task done")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	id := fs.String("id", "", "")
	token := fs.String("token", "", "")
	evidence := fs.String("evidence", "", "")
	verdict := fs.String("verdict", "", "")
	score := fs.Int("score", 0, "")
	head := fs.String("head", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task done", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task done", "takes flags, not positional arguments")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task done", err.Error())
	}
	defer st.Close()
	scoreText := ""
	if *score != 0 {
		scoreText = strconv.Itoa(*score)
	}
	status, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: *sprint, ID: *id, Token: *token, Evidence: *evidence,
		Verdict: *verdict, Score: scoreText, Head: *head, Actor: *actor, Idem: *idem,
	})
	if err != nil {
		return refuse(errOut, "task done", err.Error())
	}
	fmt.Fprintf(out, "DONE %s id=%s\n", status, *id)
	return status.ExitCode()
}
