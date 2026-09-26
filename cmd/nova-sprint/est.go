package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
)

func init() {
	register(Verb{
		Name:    "est",
		Summary: "print estimate vs actual for closed tasks per owner",
		Run:     runEst,
	})
}

func runEst(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("est")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	owner := fs.String("owner", "", "the GitHub owner")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "est", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "est", "takes flags, not positional arguments")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "est", err.Error())
	}
	defer st.Close()

	sprintName := *sprint
	if sprintName == "" {
		sprints, err := st.Client().ZRange(ctx, "sprint:order", -1, -1).Result()
		if err == nil && len(sprints) > 0 {
			sprintName = sprints[0]
		}
	}
	if sprintName == "" {
		return refuse(errOut, "est", "--sprint is required")
	}

	lines, err := fold.EstimateLines(ctx, st.Client(), sprintName, *owner)
	if err != nil {
		return refuse(errOut, "est", err.Error())
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	return 0
}
