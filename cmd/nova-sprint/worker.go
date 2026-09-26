// worker pause|resume|show (nova-tools #4308): one verb over a worker's
// paused flag, friend and bench alike. A worker is what the code calls a
// consumer, bench:<b> or friend:<f> (the table's header already says
// worker). Pausing used to be capacity bench <b> 0 for a bench and capacity
// friend --paused 1 for a friend; this is the one verb, one FCALL each, and
// the deal pass honours the flag for both kinds (a paused worker is dealt
// nothing and keeps working what it holds); the table prints paused in
// status while the worker is up.
//
//	worker pause  --as <bench:<b>|friend:<f>> [--idem <k>] [--redis <addr>]   PAUSED <worker>
//	worker resume --as <bench:<b>|friend:<f>> [--idem <k>] [--redis <addr>]   RESUMED <worker>
//	worker show   [--as <bench:<b>|friend:<f>>] [--redis <addr>]              WORKER <id> slots= paused= tiers= kinds= machine=
//
// A name neither registry holds and no desired hash names is WORKER PAUSE
// REFUSED <worker> why=UNKNOWN ..., exit 1. --redis defaults to
// NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR. One grammar (#4352 A): --as is
// the worker, as on card work; the actor of the receipt is the seat.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

func init() {
	register(Verb{
		Name:    "worker",
		Summary: "pause, resume or show a worker (bench:<b> or friend:<f>)",
		Run:     runWorker,
	})
}

func runWorker(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "worker", "want pause, resume or show")
	}
	switch args[0] {
	case "pause":
		return runWorkerPause(ctx, true, args[1:], out, errOut)
	case "resume":
		return runWorkerPause(ctx, false, args[1:], out, errOut)
	case "show":
		return runWorkerShow(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "worker", fmt.Sprintf("unknown subverb %s; want pause, resume or show", args[0]))
	}
}

// workerArg is the one worker of --as, bench:<b> or friend:<f>.
func workerArg(as string, verb string, errOut io.Writer) (taskcard.Consumer, int) {
	if as == "" {
		return taskcard.Consumer{}, refuse(errOut, verb, "want --as <worker>, bench:<b> or friend:<f>")
	}
	k, err := taskcard.ParseConsumer(as)
	if err != nil {
		return taskcard.Consumer{}, refuse(errOut, verb, err.Error())
	}
	return k, 0
}

func runWorkerPause(ctx context.Context, paused bool, args []string, out, errOut io.Writer) int {
	verb := "worker resume"
	if paused {
		verb = "worker pause"
	}
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	as := fs.String("as", "", verbflag.HelpAs)
	idem := fs.String("idem", "", verbflag.HelpIdem)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments; the worker is --as <worker>")
	}
	k, code := workerArg(*as, verb, errOut)
	if code != 0 {
		return code
	}
	actor := seatActor()
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	word, _, err := capacity.PauseWorker(ctx, st, k.Kind, k.Name, paused, actor, *idem)
	if err != nil {
		var unknown *capacity.UnknownWorker
		if errors.As(err, &unknown) {
			fmt.Fprintf(out, "%s REFUSED %s why=%s\n", strings.ToUpper(verb), k, quoteField(err.Error()))
			return 1
		}
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "%s %s\n", word, k)
	return 0
}

func runWorkerShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "worker show"
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	as := fs.String("as", "", verbflag.HelpAs)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments; one worker is --as <worker>")
	}
	var k taskcard.Consumer
	if *as != "" {
		var code int
		if k, code = workerArg(*as, verb, errOut); code != 0 {
			return code
		}
	}
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	rows, err := capacity.ShowWorkers(ctx, st, k.Kind, k.Name)
	if err != nil {
		var unknown *capacity.UnknownWorker
		if errors.As(err, &unknown) {
			fmt.Fprintf(out, "WORKER SHOW REFUSED %s why=%s\n", k, quoteField(err.Error()))
			return 1
		}
		return refuse(errOut, verb, err.Error())
	}
	for _, r := range rows {
		fmt.Fprintln(out, r.Line())
	}
	return 0
}
