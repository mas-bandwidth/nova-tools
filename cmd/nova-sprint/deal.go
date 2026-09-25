package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

var openDealStore = func(ctx context.Context, addr string) (*store.Store, error) {
	return store.Open(ctx, addr)
}

func init() {
	register(Verb{
		Name:    "deal",
		Summary: deal.StatusUsage,
		Run:     runDeal,
	})
}

func runDeal(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "deal", "want status")
	}
	switch args[0] {
	case "status":
		return runDealStatus(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "deal", fmt.Sprintf("unknown subverb %s; want status", args[0]))
	}
}

func runDealStatus(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("deal status")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	bench := fs.String("bench", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "deal status", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "deal status", "takes flags, not positional arguments")
	}
	if *sprint == "" {
		return refuse(errOut, "deal status", "--sprint is required")
	}
	st, err := openDealStore(ctx, *redisAddr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint deal status: %v\n", err)
		return 6
	}
	defer st.Close()

	lines, err := deal.Status(ctx, st.Client(), *sprint, *bench)
	if err != nil {
		return refuse(errOut, "deal status", err.Error())
	}
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	return 0
}
