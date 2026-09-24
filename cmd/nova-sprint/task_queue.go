// The one task store's subverbs (#3206 rev 5 PR A): the friend-queue verbs
// the task verb lacked. Each mutating subverb is one Redis Function call;
// counts and owners are pipelined reads. There is no block/unblock (ruling
// nova-tools#3516): `task depends` gives a task DEPENDS-ON conditions, and a
// task with an unmet one is waiting, never ready, until `task resolve` or the
// done/close of the task it waits on makes it ready on its own queue.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// queueSeat opens the store for a queue subverb and resolves its sprint
// (--sprint, else the lowest-score member of sprint:order) and actor (the
// seat's NOVA_FRIEND, else --actor). It prints the refusal and returns ok
// false when either is missing.
func queueSeat(ctx context.Context, verb, addr, sprint, actor string, errOut io.Writer) (*store.Store, string, string, bool) {
	if actor == "" {
		actor = os.Getenv(seatEnv)
	}
	if actor == "" {
		refuse(errOut, verb, wantFriend+" ("+seatEnv+" is empty) or --actor")
		return nil, "", "", false
	}
	st, err := openTaskStore(ctx, taskAddr(addr))
	if err != nil {
		refuse(errOut, verb, err.Error())
		return nil, "", "", false
	}
	if sprint == "" {
		order, err := st.Client().ZRange(ctx, "sprint:order", 0, 0).Result()
		if err != nil && err != redis.Nil {
			_ = st.Close()
			refuse(errOut, verb, err.Error())
			return nil, "", "", false
		}
		if len(order) == 0 || order[0] == "" {
			_ = st.Close()
			refuse(errOut, verb, task.ErrNoSprint.Error())
			return nil, "", "", false
		}
		sprint = order[0]
	}
	return st, sprint, actor, true
}

// printReply prints `<VERB> <STATUS> id=<id> [words]` and returns the exit.
func printReply(out io.Writer, word, id string, r task.Reply) int {
	line := fmt.Sprintf("%s %s id=%s", word, r.Status, id)
	if len(r.Args) > 0 {
		line += " " + strings.Join(r.Args, " ")
	}
	_, _ = fmt.Fprintln(out, line)
	return r.ExitCode()
}

func runTaskQueueSub(ctx context.Context, sub string, args []string, out, errOut io.Writer) int {
	verb := "task " + sub
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	id := fs.String("id", "", "")
	as := fs.String("as", "", "")
	to := fs.String("to", "", "")
	evidence := fs.String("evidence", "", "")
	on := fs.String("on", "", "")
	token := fs.String("token", "", "")
	prefix := fs.String("prefix", "", "")
	asserted := fs.Bool("asserted", false, "")
	max := fs.Int("max", 0, "")
	floor := fs.Int("floor", 0, "")
	maxMove := fs.Int("max-move", 0, "")
	releaser := fs.String("releaser", "stella", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	st, S, who, ok := queueSeat(ctx, verb, *redisAddr, *sprint, *actor, errOut)
	if !ok {
		return 2
	}
	defer func() { _ = st.Close() }()

	var r task.Reply
	var err error
	switch sub {
	case "move":
		r, err = task.Move(ctx, st, S, *id, *to, who, *idem)
		if err == nil {
			return printReply(out, "MOVE", *id, r)
		}
	case "close":
		r, err = task.Close(ctx, st, S, *id, *evidence, who, *idem)
		if err == nil {
			return printReply(out, "CLOSE", *id, r)
		}
	case "front":
		r, err = task.Front(ctx, st, S, *id, *as, who, *idem)
		if err == nil {
			return printReply(out, "FRONT", *id, r)
		}
	case "depends":
		r, err = task.Depends(ctx, st, task.DependsRequest{Sprint: S, ID: *id, On: *on, As: *as, Token: *token, Actor: who, Idem: *idem})
		if err == nil {
			return printReply(out, "DEPENDS", *id, r)
		}
	case "resolve":
		r, err = task.Resolve(ctx, st, S, *on, *asserted, who, *idem)
		if err == nil {
			return printReply(out, "RESOLVE", *on, r)
		}
	case "fill":
		if *as == "" {
			return refuse(errOut, verb, "--as is required")
		}
		if *as != who {
			return refuse(errOut, verb, fmt.Sprintf("--as %s is not the seat %s", *as, who))
		}
		claims, ferr := task.Fill(ctx, st, S, *as, *max, who, *idem)
		if ferr != nil {
			return refuse(errOut, verb, ferr.Error())
		}
		if len(claims) == 0 {
			_, _ = fmt.Fprintln(out, "NONE")
		}
		for _, c := range claims {
			_, _ = fmt.Fprintf(out, "CLAIMED %s/%s attempt=%d token=%s\n", c.Sprint, c.ID, c.Attempt, c.Token)
			_, _ = fmt.Fprintf(out, "TASK %s kind=%s ref=%s title=%q\n", c.ID, c.Kind, c.Ref, c.Title)
		}
		return 0
	case "counts":
		if *as == "" {
			return refuse(errOut, verb, "--as is required")
		}
		c, cerr := task.CountsFor(ctx, st, S, *as)
		if cerr != nil {
			return refuse(errOut, verb, cerr.Error())
		}
		_, _ = fmt.Fprintln(out, c.String())
		return 0
	case "owners":
		rows, oerr := task.Owners(ctx, st, S, *prefix)
		if oerr != nil {
			return refuse(errOut, verb, oerr.Error())
		}
		for _, row := range rows {
			_, _ = fmt.Fprintf(out, "%s %s %s\n", row.ID, row.Owner, row.State)
		}
		return 0
	case "rebalance":
		moves, rerr := task.Rebalance(ctx, st, task.RebalanceRequest{
			Sprint: S, Floor: *floor, MaxMove: *maxMove, Releaser: *releaser, Actor: who, Idem: *idem,
		})
		if rerr != nil {
			return refuse(errOut, verb, rerr.Error())
		}
		if len(moves) == 0 {
			_, _ = fmt.Fprintln(out, "REBALANCE none")
		}
		for _, m := range moves {
			_, _ = fmt.Fprintf(out, "REBALANCE %s %s -> %s\n", m.ID, m.From, m.To)
		}
		return 0
	default:
		return refuse(errOut, "task", "unknown subverb "+sub)
	}
	return refuse(errOut, verb, err.Error())
}

// runFriendDown is `friend down|up --as <actor> <f> [--reason r]`: the one
// writer of friend:<f>:down (#3206 PR A). Down refuses push and take, and
// rebalance moves the friend's ready work off on its next pass.
func runFriendDown(ctx context.Context, on bool, args []string, out, errOut io.Writer) int {
	verb := "friend up"
	if on {
		verb = "friend down"
	}
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", "", "")
	actor := fs.String("as", "", "")
	reason := fs.String("reason", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *actor == "" {
		return refuse(errOut, verb, "--as actor is required")
	}
	if fs.NArg() != 1 {
		return refuse(errOut, verb, "want one friend name; flags precede it")
	}
	st, err := openTaskStore(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	r, err := task.FriendDown(ctx, st, fs.Arg(0), on, *reason, *actor, *idem)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	_, _ = fmt.Fprintf(out, "FRIEND %s %s\n", r.Status, fs.Arg(0))
	if r.Status == "DOWN" || r.Status == "UP" || r.Status == "SAME" {
		return 0
	}
	return 2
}
