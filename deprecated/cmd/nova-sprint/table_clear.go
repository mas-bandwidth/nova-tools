// table_clear.go: `nova-sprint table clear` (#3637). The table's landed and
// done columns go to zero in under a second; waiting, working and merging are
// untouched. The checkpoint is written (and its line printed) before Redis
// changes; internal/nsprint/table/clear.go is the move.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const tableClearWants = "table clear --checkpoint <file> [--redis <addr>] [--friends <a,b,...>] [--by <name>]"

func cmdTableClear(args []string, stdout, stderr io.Writer) int {
	fs := verbflag.New("table clear")
	redisAddr := fs.String("redis", redisDefault(), "")
	friends := fs.String("friends", "", "")
	checkpoint := fs.String("checkpoint", "", "")
	by := fs.String("by", "", "")
	if err := fs.Parse(args); err != nil {
		return tableRefuse(stderr, err.Error()+"; "+tableClearWants)
	}
	if fs.NArg() > 0 {
		return tableRefuse(stderr, "table clear takes flags, not positional arguments; "+tableClearWants)
	}
	addr := taskAddr(*redisAddr)
	if addr == "" || *checkpoint == "" {
		return tableRefuse(stderr, "table clear needs --checkpoint <file> (the record written before anything moves) and --redis <addr> (or NOVA_SPRINT_REDIS / NOVA_REDIS_ADDR); "+tableClearWants)
	}
	who := *by
	if who == "" {
		who = os.Getenv(seatEnv)
	}
	if who == "" {
		who = "table-clear"
	}
	start := time.Now()
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	plan, err := table.PlanClear(ctx, st.Client(), splitRoster(*friends), start)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	if err := writeAtomic(*checkpoint, plan.Checkpoint()); err != nil {
		return tableRefuse(stderr, "checkpoint: "+err.Error()+"; nothing was cleared")
	}
	at := start.UTC().Format(time.RFC3339)
	fmt.Fprintf(stdout, "CHECKPOINT at=%s path=%s landed=%d friends=%d\n", at, *checkpoint, plan.Count(), len(plan.Done))
	receipt := fmt.Sprintf("%s %s %d", at, *checkpoint, plan.Count()+len(plan.Done))
	if err := plan.Apply(ctx, st.Client(), who, "table clear (#3637)", receipt); err != nil {
		fmt.Fprintf(stderr, "nova-sprint table clear: %s; the checkpoint %s holds what was to move\n", oneline.Escape(err.Error()), *checkpoint)
		return 1
	}
	fmt.Fprintf(stdout, "CLEARED landed=%d streams=%d friends=%d by=%s ms=%d\n", plan.Count(), len(plan.Landed), len(plan.Done), who, time.Since(start).Milliseconds())
	return 0
}
