package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/conform"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "conform",
		Summary: "fleet bench conformance checks and reporting (#2921)",
		Run:     cmdConform,
	})
}

func cmdConform(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "conform", "want check --all")
	}
	switch args[0] {
	case "check":
		fs := flag.NewFlagSet("conform check", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		redisAddr := fs.String("redis", "127.0.0.1:6379", "")
		all := fs.Bool("all", false, "")
		if err := fs.Parse(args[1:]); err != nil {
			return refuse(errOut, "conform check", err.Error())
		}
		if !*all {
			return refuse(errOut, "conform check", "want --all")
		}
		st, err := store.Open(ctx, *redisAddr)
		if err != nil {
			fmt.Fprintf(errOut, "conform check: %v\n", err)
			return 2
		}
		defer st.Close()

		ok, err := conform.CheckAll(ctx, st.Client())
		if err != nil || !ok {
			fmt.Fprintf(errOut, "conform check: %v\n", err)
			return 1
		}
		fmt.Fprintln(out, "OK")
		return 0
	default:
		return refuse(errOut, "conform", fmt.Sprintf("unknown subverb %s; want check --all", args[0]))
	}
}
