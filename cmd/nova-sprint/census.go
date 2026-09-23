// The census verb (nova-tools #3037) registers itself through the S0 registry
// (registry.go), so adding it never edits main.go. It replaces the redis-pipe
// prototype: one pipelined read of every key in a registry set or a key list,
// one line per key, MISSING <key> for a key not in Redis, then a CENSUS count
// line. It is read-only. The rows and counts come from
// internal/nsprint/store/census.go; this file only parses flags.
//
//	nova-sprint census --redis <addr> --set benches|friends|sprint:<name>:<state> --fields f1,f2
//	nova-sprint census --redis <addr> --keys-from <file|-> --fields f1,f2
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
		Summary: "read fields of every key in a set or key list in one pipeline; MISSING <key> when absent",
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
		if *fields != "" {
			req.Fields = append(req.Fields, strings.TrimSpace(f))
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
