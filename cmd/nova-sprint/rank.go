package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

func init() {
	register(Verb{
		Name:    "rank",
		Summary: "print open tasks in take order with upward rank and longest chain",
		Run:     runRank,
	})
}

func runRank(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("rank")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	as := fs.String("as", "", verbflag.HelpAs)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "rank", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "rank", "takes flags, not positional arguments; usage: nova-sprint rank [--redis <addr>] [--sprint <name>] [--as <friend>]")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "rank", err.Error())
	}
	defer st.Close()

	sprints := []string{*sprint}
	if *sprint == "" {
		sprints, err = st.Client().ZRange(ctx, "sprint:order", 0, -1).Result()
		if err != nil {
			return refuse(errOut, "rank", fmt.Sprintf("sprint order: %v", err))
		}
	}
	for _, s := range sprints {
		if s == "" {
			continue
		}
		ranks, err := deal.RankTasks(ctx, st, s, *as, errOut)
		if err != nil {
			return refuse(errOut, "rank", err.Error())
		}
		for _, r := range ranks {
			fmt.Fprintf(out, "RANK %s owner=%s front=%t rank=%d est=%s chain=%s\n",
				r.ID, r.Owner, r.Front, r.Rank, r.Est, r.Chain)
		}
	}
	return 0
}
