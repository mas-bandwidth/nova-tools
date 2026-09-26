// The task verb registers itself through the S0 registry (registry.go), so
// adding task subverbs never edits main.go. Mutating subverbs use one
// guarded Redis Function call; list and width read indexed task state.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "task",
		Summary: "push, take, beat, done, cancel, list and width tasks; the task card verbs: push (--as), take, beat, done, land, cancel, block, unblock, front, move, expire, ls, fsck",
		Run:     runTask,
	})
}

// openTaskStore uses the address the caller supplied for this invocation.
func openTaskStore(ctx context.Context, addr string) (*store.Store, error) {
	return store.Open(ctx, addr)
}

// seatEnv names the seat's friend (#2929): the initiator of every task push and
// take. bin/friend-harness exports it; a shell outside a harness has none and
// is refused, never defaulted.
const seatEnv = "NOVA_FRIEND"

// wantFriend is the one refusal for an empty or unregistered initiator.
const wantFriend = "want NOVA_FRIEND in friends"

// taskAddr is the Redis address of a seat verb: --redis, else the one
// resolver (seat.go), else empty, which store.Open refuses (never a
// localhost default on a mutating verb).
func taskAddr(flagAddr string) string {
	return redisOr(flagAddr)
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
		return refuse(errOut, "task", "want push, take, beat, done, cancel, list or width")
	}
	if isTaskCard(args[0], args[1:]) {
		// #3778: the task card verbs (task_card.go).
		return runTaskCard(ctx, args[0], args[1:], out, errOut)
	}
	switch args[0] {
	case "push":
		return runTaskPush(ctx, args[1:], out, errOut)
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
func taskFlags(name string) *verbflag.Set {
	return verbflag.New(name)
}

func runTaskPush(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task push")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	kind := fs.String("kind", "work", "the task's kind: work or read")
	title := fs.String("title", "", "the task's title")
	effects := fs.String("effects", "none", "the task's side effects: none, or what it writes")
	repo := fs.String("repo", "", verbflag.HelpRepo)
	pr := fs.Int("pr", 0, verbflag.HelpPR)
	head := fs.String("head", "", "the PR's head sha")
	ref := fs.String("ref", "", "the task's ref, <repo>#<n>")
	to := fs.String("to", "", verbflag.HelpTo)
	front := fs.Bool("front", false, "queue the task at the front")
	priority := fs.Int("priority", 0, "the task's priority, higher first")
	payloadSHA := fs.String("payload-sha", "", "the sha of the payload the task carries")
	idem := fs.String("idem", "", verbflag.HelpIdem)
	est := fs.String("est", "", "the estimate in whole minutes, 1..10080")
	// #3206 PR A: DEPENDS-ON and the read rule's author.
	dependsOn := fs.String("depends-on", "", "what the task waits on, comma-separated ids")
	author := fs.String("author", "", "the author of the read rule the task carries")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task push", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task push", "takes flags, not positional arguments")
	}
	id := oneID(*ids)
	if id == "" {
		return refuse(errOut, "task push", "--ids wants one id")
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
		Sprint: *sprint, ID: id, Kind: task.Kind(*kind), Title: *title,
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
		_, _ = fmt.Fprintf(out, "PUSH DOWN id=%s to=%s down=%s\n", id, *to, res.Down)
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
		_, _ = fmt.Fprintf(out, "PUSH %s id=%s waiting=%d\n", res.Status, id, res.Waiting)
	case res.Status == task.PushCreated && res.OnMet:
		_, _ = fmt.Fprintf(out, "PUSH %s id=%s on-met\n", res.Status, id)
	default:
		_, _ = fmt.Fprintf(out, "PUSH %s id=%s\n", res.Status, id)
	}
	return res.Status.ExitCode()
}

func runTaskDone(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("task done")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	token := fs.String("token", "", "the attempt's token, from the take")
	evidence := fs.String("evidence", "", "what proves the work is done: a PR, a url, a line")
	verdict := fs.String("verdict", "", "a read's verdict")
	score := fs.Int("score", 0, "a read's score, 1-10")
	head := fs.String("head", "", "the PR's head sha the read is of")
	idem := fs.String("idem", "", verbflag.HelpIdem)
	as := fs.String("as", "", verbflag.HelpAs)
	// nova-tools #3092 rev 7: a review closes on the comment as posted. The
	// shared parser (internal/nsprint/disposition) reads its typed line and
	// ns_task_done records it with the close in one atomic call.
	bodyFile := fs.String("body-file", "", "the review comment as posted, a file; carries verdict, score and head")
	url := fs.String("url", "", "the comment's url (with --body-file)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "task done", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "task done", "takes flags, not positional arguments")
	}
	id := oneID(*ids)
	if id == "" {
		return refuse(errOut, "task done", "--ids wants one id")
	}
	actor := seatActor()
	var typed *task.TypedLine
	if *bodyFile != "" {
		if *verdict != "" || *score != 0 || *head != "" {
			return refuse(errOut, "task done", "--body-file carries verdict, score and head; drop --verdict, --score and --head")
		}
		body, err := os.ReadFile(*bodyFile)
		if err != nil {
			return refuse(errOut, "task done", err.Error())
		}
		res := disposition.Parse(string(body))
		if res.Outcome != disposition.Record {
			// A review closes on a record: prose, NORECORD and a malformed
			// line all refuse the close.
			_, _ = fmt.Fprintf(out, "REFUSED %s id=%s\n", res, id)
			return 2
		}
		if res.Line.Type != disposition.TypeDisposition {
			_, _ = fmt.Fprintf(out, "REFUSED not-a-disposition id=%s\n", id)
			return 2
		}
		typed = &task.TypedLine{Type: string(res.Line.Type), Who: res.Line.Who, Head: res.Line.Head,
			Verdict: res.Line.Verdict, Score: strconv.Itoa(res.Line.Score), Kind: res.Line.Kind,
			Scope: res.Line.Scope, Reason: res.Line.Reason, URL: *url}
		if typed.URL == "" {
			typed.URL = *evidence
		}
		typed.CommentID = disposition.CommentID(typed.URL)
	} else if *url != "" {
		return refuse(errOut, "task done", "--url goes with --body-file")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "task done", err.Error())
	}
	defer st.Close()
	if typed != nil && typed.Verdict == "HOLD" && typed.Kind == "" {
		// The classifier names another pull by number, so it needs the
		// task's PR.
		prText, err := st.Client().HGet(ctx, "task:"+id, "pr").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return refuse(errOut, "task done", err.Error())
		}
		pr := 0
		if prText != "" {
			// A pr field that is not a number is a broken record, not PR 0.
			if pr, err = strconv.Atoi(prText); err != nil {
				return refuse(errOut, "task done", "task:"+id+" pr="+prText+" is not a number")
			}
		}
		typed.KindDerived = disposition.Classify(typed.Reason, pr)
	}
	scoreText := ""
	if *score != 0 {
		scoreText = strconv.Itoa(*score)
	}
	res, err := task.DoneTyped(ctx, st, task.DoneRequest{
		Sprint: *sprint, ID: id, Token: *token, Evidence: *evidence,
		Verdict: *verdict, Score: scoreText, Head: *head, Actor: actor, Idem: *idem, As: *as,
		Typed: typed,
	})
	if err != nil {
		return refuse(errOut, "task done", err.Error())
	}
	switch {
	case res.Status == task.DoneRefused:
		_, _ = fmt.Fprintf(out, "REFUSED %s id=%s\n", res.Why, id)
	case len(res.Record) > 0:
		_, _ = fmt.Fprintf(out, "DONE %s id=%s RECORD %s\n", res.Status, id, strings.Join(res.Record, " "))
	default:
		_, _ = fmt.Fprintf(out, "DONE %s id=%s\n", res.Status, id)
	}
	return res.Status.ExitCode()
}
