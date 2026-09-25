package main

import (
	"context"
	"flag"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "census",
		Summary: "one pipelined read: --set/--keys-from --fields rows; MISSING when absent",
		Run:     runCensus,
	})
}

func runCensus(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("census", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	set := fs.String("set", "", "")
	keysFrom := fs.String("keys-from", "", "")
	fields := fs.String("fields", "", "")
	
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "census", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "census", "takes no arguments after the flags")
	}
	
	req := store.CensusRequest{Set: *set}
	for _, f := range strings.Split(*fields, ",") {
		if f = strings.TrimSpace(f); f != "" {
			req.Fields = append(req.Fields, f)
		}
	}
	
	if *keysFrom != "" {
		if *keysFrom == "-" {
			req.KeysFrom = os.Stdin
		} else {
			f, err := os.Open(*keysFrom)
			if err != nil {
				return refuse(errOut, "census", err.Error())
			}
			defer f.Close()
			req.KeysFrom = f
		}
	}
	
	if err := req.Check(); err != nil {
		return refuse(errOut, "census", err.Error())
	}
	if *redisAddr == "" {
		return refuse(errOut, "census", "--redis addr is required")
	}
	
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "census", err.Error())
	}
	defer st.Close()
	
	if _, err := store.RunCensus(ctx, st, req, out); err != nil {
		return refuse(errOut, "census", err.Error())
	}
	return 0
}
