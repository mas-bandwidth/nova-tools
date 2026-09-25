// The batch task subverbs on the ws index (nova-tools #3661, sweep #3647;
// part of #3662): cancel, block, unblock, move and front over --ids @file,
// --stream <s> or --set <key>, and sweep --friend <f>. Each is one Redis
// Function call (internal/nsprint/taskbatch) and prints one line:
//
//	TASK <verb> n=<k> stream=<s> ms=<n>
//	TASK <verb> REFUSED id=<first bad id> why=<why> ms=<n>
//
// Exit 0 moved, 1 refused (nothing changed), 2 could not run. cancel, move
// and front without --ids, --stream or --set stay the one-id queue subverbs
// (task_queue.go).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskbatch"
)

const taskBatchUsage = `nova-sprint task: batch verbs on the ws index (#3661), one Redis Function call each

usage:
  nova-sprint task cancel  (--ids @file | --stream <s> | --set <key>) [--why <evidence>]
  nova-sprint task block   (--ids @file | --stream <s> | --set <key>) (--on "<conditions>" | --reason <text>)
  nova-sprint task unblock (--ids @file | --stream <s> | --set <key>)
  nova-sprint task move    (--ids @file | --stream <s> | --set <key>) (--to-stream <s> | --to-state <st> | --to-friend <f>)
  nova-sprint task front   (--ids @file | --stream <s> | --set <key>)
  nova-sprint task sweep   --friend <f>
  every verb also takes --redis <addr> --sprint <S> --actor <name>

--ids @file reads one id per line (# comments skipped); --ids a,b,c names
them inline. --stream <s> takes the stream's waiting, ready and parked rows
(block: ready and waiting; unblock: waiting; front: waiting and ready).
--set <key> takes a ZSET's or SET's members.
Every row is checked before any is written: a refusal changes nothing and
names the first bad id. move keeps the rows' relative order and queues them
after the destination stream's own work. front sets order 0 and front=1.
sweep: a ready read whose PR head moved since push is cancelled with the two
heads as evidence; a ready task whose sprint is in pit stop moves to waiting.
The sprint (legacy sprint:<S>:idx sets) is --sprint, else FRIEND_QUEUE_SPRINT,
else the first of sprint:order; the actor is --actor, else NOVA_FRIEND.

prints: TASK <verb> n=<k> stream=<s> ms=<n>
        TASK <verb> REFUSED id=<id> why=<why> ms=<n>
exit codes: 0 moved, 1 refused (nothing changed), 2 could not run.
`

// batchSubs are the subverbs that are only batch verbs.
var batchSubs = map[string]bool{"block": true, "unblock": true, "sweep": true}

// isTaskBatch says whether a task subverb call is the batch form: block,
// unblock and sweep always, cancel, move and front when a batch source or
// help is named.
func isTaskBatch(sub string, args []string) bool {
	if batchSubs[sub] {
		return true
	}
	if sub != "cancel" && sub != "move" && sub != "front" {
		return false
	}
	for _, a := range args {
		name := strings.TrimLeft(a, "-")
		if i := strings.Index(name, "="); i >= 0 {
			name = name[:i]
		}
		if strings.HasPrefix(a, "-") && (name == "ids" || name == "stream" || name == "set" || name == "h" || name == "help") {
			return true
		}
	}
	return false
}

func runTaskBatch(ctx context.Context, sub string, args []string, out, errOut io.Writer) int {
	verb := "task " + sub
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, _ = io.WriteString(out, taskBatchUsage)
		return 0
	}
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	actor := fs.String("actor", "", "")
	idsFlag := fs.String("ids", "", "")
	stream := fs.String("stream", "", "")
	set := fs.String("set", "", "")
	on := fs.String("on", "", "")
	reason := fs.String("reason", "", "")
	why := fs.String("why", "", "")
	fs.StringVar(why, "evidence", "", "")
	toStream := fs.String("to-stream", "", "")
	toState := fs.String("to-state", "", "")
	toFriend := fs.String("to-friend", "", "")
	friend := fs.String("friend", "", "")
	help := fs.Bool("help", false, "")
	fs.BoolVar(help, "h", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *help {
		_, _ = io.WriteString(out, taskBatchUsage)
		return 0
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}

	req := taskbatch.Request{Verb: sub, Why: *why, Stream: *stream, Set: *set}
	if sub != "sweep" {
		ids, err := readBatchIDs(*idsFlag)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		req.IDs = ids
		if *idsFlag != "" && len(ids) == 0 {
			return refuse(errOut, verb, "--ids names no ids")
		}
		n := 0
		for _, s := range []string{*idsFlag, *stream, *set} {
			if s != "" {
				n++
			}
		}
		if n != 1 {
			return refuse(errOut, verb, "want exactly one of --ids @file, --stream <s> and --set <key>")
		}
	}
	switch sub {
	case "block":
		if (*on == "") == (*reason == "") {
			return refuse(errOut, verb, `want --on "<conditions>" or --reason <text>`)
		}
		req.Param = *on
		if *reason != "" {
			req.Why = *reason
		} else if req.Why == "" {
			req.Why = "on " + *on
		}
	case "move":
		n := 0
		for _, s := range []string{*toStream, *toState, *toFriend} {
			if s != "" {
				n++
			}
		}
		if n != 1 {
			return refuse(errOut, verb, "want exactly one of --to-stream <s>, --to-state <st> and --to-friend <f>")
		}
		switch {
		case *toStream != "":
			req.Verb, req.Param = taskbatch.MoveStream, *toStream
		case *toState != "":
			req.Verb, req.Param = taskbatch.MoveState, *toState
		default:
			req.Verb, req.Param = taskbatch.MoveFriend, *toFriend
		}
	case "sweep":
		if *friend == "" {
			return refuse(errOut, verb, "--friend <f> is required")
		}
		if *idsFlag != "" || *stream != "" || *set != "" {
			return refuse(errOut, verb, "sweep reads the friend's ready index; drop --ids, --stream and --set")
		}
	}
	if *sprint == "" {
		*sprint = os.Getenv("FRIEND_QUEUE_SPRINT")
	}
	st, S, who, ok := queueSeat(ctx, verb, *redisAddr, *sprint, *actor, errOut)
	if !ok {
		return 2
	}
	defer func() { _ = st.Close() }()
	req.Sprint, req.By = S, who
	if sub != "sweep" {
		if err := req.Check(); err != nil {
			return refuse(errOut, verb, err.Error())
		}
	}

	call := taskbatch.FCall(st.Client())
	start := time.Now()
	var res taskbatch.Result
	var err error
	if sub == "sweep" {
		res, err = taskbatch.Sweep(ctx, call, taskbatch.SweepRequest{Sprint: S, By: who, Friend: *friend})
	} else {
		res, err = taskbatch.Batch(ctx, call, req)
	}
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if !res.OK {
		_, _ = fmt.Fprintf(out, "TASK %s REFUSED id=%s why=%s ms=%d\n", sub, quoteField(res.ID), res.Why, ms)
		return 1
	}
	line := fmt.Sprintf("TASK %s n=%d stream=%s ms=%d", sub, res.N, quoteField(res.Stream), ms)
	if sub == "sweep" {
		line += fmt.Sprintf(" cancelled=%d waiting=%d skipped=%d", res.Cancelled, res.Waiting, res.Skipped)
	}
	_, _ = fmt.Fprintln(out, line)
	return 0
}

// readBatchIDs reads --ids: @file (one id per line) or a comma list.
func readBatchIDs(v string) ([]string, error) {
	if v == "" {
		return nil, nil
	}
	if strings.HasPrefix(v, "@") {
		f, err := os.Open(v[1:])
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return taskbatch.ReadIDs(f)
	}
	var ids []string
	for _, id := range strings.Split(v, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// quoteField keeps a stream name with spaces ("swarm: cards") one field.
func quoteField(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}
