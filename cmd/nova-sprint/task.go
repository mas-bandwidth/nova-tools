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
	"os"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func init() {
	register(Verb{
		Name:    "task",
		Summary: "push, take, beat, done, cancel, list, width, move, close, front, depends, resolve, fill, counts, owners and rebalance tasks",
		Run:     runTask,
	})
}

// openTaskStore uses the address the caller supplied for this invocation.
func openTaskStore(ctx context.Context, addr string) (*store.Store, error) {
	return store.Open(ctx, addr)
}

// queueSubs are the one task store's subverbs (#3206 PR A, task_queue.go).
const queueSubs = "move, close, front, depends, resolve, fill, counts, owners or rebalance"

// seatEnv names the seat's friend (#2929): the initiator of every task push and
// take. bin/friend-harness exports it; a shell outside a harness has none and
// is refused, never defaulted.
const seatEnv = "NOVA_FRIEND"

// wantFriend is the one refusal for an empty or unregistered initiator.
const wantFriend = "want NOVA_FRIEND in friends"

// taskAddr is the Redis address of a seat verb: --redis, else
// NOVA_SPRINT_REDIS, else NOVA_REDIS_ADDR, else empty, which store.Open
// refuses (never a localhost default on a mutating verb).
func taskAddr(flagAddr string) string {
	if flagAddr != "" {
		return flagAddr
	}
	if v := os.Getenv("NOVA_SPRINT_REDIS"); v != "" {
		return v
	}
	return os.Getenv("NOVA_REDIS_ADDR")
}

// refuseSeat maps the seat errors to their one line: ErrNotFriend is
// `want NOVA_FRIEND in friends (NOVA_FRIEND=<i>)`, exit 2.
func refuseSeat(errOut io.Writer, verb, initiator string, err error) int {
	if errors.Is(err, task.ErrNotFriend) {
		return refuse(errOut, verb, fmt.Sprintf("%s (%s=%s)", wantFriend, seatEnv, initiator))
	}
	return refuse(errOut, verb, err.Error())
}

func runTask(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "task", "want push, take, beat, done, cancel, list, width, "+queueSubs)
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
	case "move", "close", "front", "depends", "resolve", "fill", "counts", "owners", "rebalance":
		// #3206 PR A: the one task store's subverbs (task_queue.go).
		return runTaskQueueSub(ctx, args[0], args[1:], out, errOut)
	default:
		return refuse(errOut, "task", fmt.Sprintf("unknown subverb %s; want push, take, beat, done, cancel, list, width, %s", args[0], queueSubs))
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
	idem := fs.String("idem", "", "")
	est := fs.String("est", "", "")
	// #3206 PR A: DEPENDS-ON (--on is friend-queue's spelling), the read
	// rule's author, and push --move (re-own an existing task).
	dependsOn := fs.String("depends-on", "", "")
	fs.StringVar(dependsOn, "on", "", "")
	author := fs.String("author", "", "")
	move := fs.Bool("move", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task push", err.Error())
	}
	if *move {
		moveArgs := []string{"--id", *id, "--to", *to, "--sprint", *sprint, "--redis", *redisAddr, "--idem", *idem}
		return runTaskQueueSub(ctx, "move", moveArgs, out, errOut)
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task push", "takes flags, not positional arguments")
	}
	// #2929: the initiator is the seat, read before any dial; --actor is gone.
	initiator := os.Getenv(seatEnv)
	if initiator == "" {
		return refuse(errOut, "task push", wantFriend+" ("+seatEnv+" is empty)")
	}
	if *kind == "build" {
		return refuse(errOut, "task push", "--kind build: want work")
	}
	st, err := openTaskStore(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, "task push", err.Error())
	}
	defer st.Close()
	res, err := task.PushChecked(ctx, st, task.PushRequest{
		Sprint: *sprint, ID: *id, Kind: task.Kind(*kind), Title: *title,
		Effects: task.Effects(*effects), Repo: *repo, PR: *pr, Head: *head,
		Ref: *ref, To: *to, Front: *front, Priority: *priority,
		PayloadSHA: *payloadSHA, Actor: initiator, Idem: *idem,
		Est: *est, ErrOut: errOut, Initiator: initiator,
		DependsOn: *dependsOn, Author: *author,
	})
	if err != nil {
		return refuseSeat(errOut, "task push", initiator, err)
	}
	if res.Status == task.PushDown {
		// #2929 rev 5: exit 7 means only DOWN, so a router can tell "pick
		// another friend" from "fix the call".
		_, _ = fmt.Fprintf(out, "PUSH DOWN id=%s to=%s down=%s\n", *id, *to, res.Down)
		return res.Status.ExitCode()
	}
	if res.Status == task.PushInvalid && res.Reason != "" {
		_, _ = fmt.Fprintf(errOut, "nova-sprint task push: %s %s\n", res.Reason, *to)
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
	switch {
	case res.Status == task.PushCreated && res.Waiting > 0:
		_, _ = fmt.Fprintf(out, "PUSH %s id=%s waiting=%d\n", res.Status, *id, res.Waiting)
	case res.Status == task.PushCreated && res.OnMet:
		_, _ = fmt.Fprintf(out, "PUSH %s id=%s on-met\n", res.Status, *id)
	default:
		_, _ = fmt.Fprintf(out, "PUSH %s id=%s\n", res.Status, *id)
	}
	return res.Status.ExitCode()
}

func runTaskTake(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task take")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	id := fs.String("id", "", "")
	as := fs.String("as", "", "")
	n := fs.Int("n", 0, "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task take", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task take", "takes flags, not positional arguments")
	}
	// #2929: only --as equal to the initiator takes; --actor is gone.
	initiator := os.Getenv(seatEnv)
	if initiator == "" {
		return refuse(errOut, "task take", wantFriend+" ("+seatEnv+" is empty)")
	}
	if *as == "" {
		return refuse(errOut, "task take", "--as is required")
	}
	st, err := openTaskStore(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, "task take", err.Error())
	}
	defer st.Close()
	if *as != initiator {
		if _, err := task.DenyTake(ctx, st, initiator, *as, *sprint, *id, *idem); err != nil {
			return refuseSeat(errOut, "task take", initiator, err)
		}
		_, _ = fmt.Fprintf(out, "TAKE DENIED id=%s as=%s initiator=%s\n", *id, *as, initiator)
		return 6
	}
	claims, err := task.TakeAvailable(ctx, st, *as, *sprint, *id, *n, initiator, *idem)
	if err != nil {
		return refuseSeat(errOut, "task take", initiator, err)
	}
	if len(claims) == 0 {
		fmt.Fprintln(out, "NONE")
		return 0
	}
	for _, claim := range claims {
		fmt.Fprintf(out, "CLAIMED %s/%s attempt=%d token=%s\n",
			claim.Sprint, claim.ID, claim.Attempt, claim.Token)
		_, _ = fmt.Fprintf(out, "TASK %s kind=%s ref=%s title=%s\n",
			claim.ID, claim.Kind, claim.Ref, strconv.Quote(claim.Title))
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
	as := fs.String("as", "", "")
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
		Verdict: *verdict, Score: scoreText, Head: *head, Actor: *actor, Idem: *idem, As: *as,
	})
	if err != nil {
		return refuse(errOut, "task done", err.Error())
	}
	fmt.Fprintf(out, "DONE %s id=%s\n", status, *id)
	return status.ExitCode()
}
