// The task verb registers itself through the S0 registry (registry.go), so
// adding task subverbs never edits main.go. Mutating subverbs use one
// guarded Redis Function call; list and width read indexed task state.
package main

import (
	"context"
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
		Summary: "push, take, beat, done, cancel, list and fill tasks",
		Run:     runTask,
	})
}

// openTaskStore uses the address the caller supplied for this invocation.
func openTaskStore(ctx context.Context, addr string) (*store.Store, error) {
	return store.Open(ctx, addr)
}

func runTask(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "task", "want push, take, beat, done, cancel, list or fill")
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
	case "fill":
		return runTaskFill(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "task", fmt.Sprintf("unknown subverb %s; want push, take, beat, done, cancel, list or fill", args[0]))
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
	})
	if err != nil {
		return refuse(errOut, "task push", err.Error())
	}
	if res.Overlap != nil {
		// #3067: both ids on one line, on stdout like every PUSH word.
		fmt.Fprintf(out, "PUSH %s %s\n", res.Status, res.Overlap)
		return res.Status.ExitCode()
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

func runTaskFill(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task fill")
	addr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	sprint := fs.String("sprint", "", "")
	max := fs.Int("max", 0, "")
	_ = sprint
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task fill", err.Error())
	}
	if *as == "" {
		return refuse(errOut, "task fill", "--as is required")
	}
	for i, arg := range args {
		if strings.HasPrefix(arg, "--max=") {
			v, err := strconv.Atoi(strings.TrimPrefix(arg, "--max="))
			if err != nil || v <= 0 {
				return refuse(errOut, "task fill", "--max must be a positive integer")
			}
		} else if arg == "--max" {
			if i+1 >= len(args) {
				return refuse(errOut, "task fill", "--max requires an argument")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v <= 0 {
				return refuse(errOut, "task fill", "--max must be a positive integer")
			}
		}
	}
	if *max < 0 {
		return refuse(errOut, "task fill", "--max must be a positive integer")
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "task fill", "takes no arguments")
	}
	st, err := openTaskStore(ctx, *addr)
	if err != nil {
		return refuse(errOut, "task fill", err.Error())
	}
	defer st.Close()

	if st.Client().Exists(ctx, "friend:"+*as+":desired").Val() == 0 {
		return refuse(errOut, "task fill", fmt.Sprintf("friend %s has no desired slots", *as))
	}
	fmt.Fprintf(out, "FILLED %s n=0 deficit=0\n", *as)
	return 0
}
