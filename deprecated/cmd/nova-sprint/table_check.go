// table_check.go: `nova-sprint table check` (#4341). Renders the sprint table
// and recounts every cell from the underlying Redis sets in one pipeline
// (SCARD/ZCARD per state). Prints "cell, table, sets, ok|DRIFT" and exits 1 on drift.
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const tableCheckWants = "table check [--redis <addr>] [--sprint <name>]"

func cmdTableCheckVerb(args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("table check")
	redisAddr := fs.String("redis", redisDefault(), "")
	sprint := fs.String("sprint", "", "")
	if err := fs.Parse(args); err != nil {
		return tableRefuse(stderr, err.Error()+"; "+tableCheckWants)
	}
	if fs.NArg() > 0 {
		return tableRefuse(stderr, "table check takes flags, not positional arguments; "+tableCheckWants)
	}
	addr := taskAddr(*redisAddr)
	if addr == "" {
		return tableRefuse(stderr, "table check needs --redis <addr> (or NOVA_SPRINT_REDIS / NOVA_REDIS_ADDR); "+tableCheckWants)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	res, err := table.CheckSets(ctx, st.Client(), *sprint, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint table check: %s\n", oneline.Escape(err.Error()))
		return 1
	}
	for _, l := range res.Lines() {
		fmt.Fprintln(stdout, l)
	}
	if res.HasDrift() {
		return 1
	}
	return 0
}
