package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// cmdFoldPool folds a pool's usage/*.tsv into the monthly ledger, one row per
// model per day, idempotently.
func cmdFoldPool(args []string, stdout, stderr io.Writer, now time.Time) int {
	_ = now
	fs := newFlagSet("fold-pool")
	pool := fs.String("pool", "", "")
	ledger := fs.String("ledger", "", "")
	since := fs.String("since", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " fold-pool", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if code, refused := noPositional(fs, stderr, "fold-pool"); refused {
		return code
	}
	r := &refusals{token: "FOLD"}
	r.required("pool", *pool, "the pool directory whose usage/*.tsv rows to fold")
	r.required("ledger", *ledger, "the ledger file, <ledger>.tsv, whose header is "+strings.Join(tokens.FoldPoolColumns, ", "))
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	if fi, err := os.Stat(*pool); err != nil || !fi.IsDir() {
		r.add("--pool is not a directory: " + *pool + "; refusing to guess")
		return r.print(stderr)
	}
	if fi, err := os.Stat(*ledger); err == nil && fi.IsDir() {
		r.add("--ledger is a directory: " + *ledger + "; it wants the ledger file")
		return r.print(stderr)
	}
	if strings.TrimSpace(*since) != "" {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(*since)); err != nil {
			r.add("--since is not a stamp: " + *since + "; it wants an RFC 3339 stamp")
			return r.print(stderr)
		}
	}
	groups, tasks, err := tokens.FoldPool(*pool, *since)
	if err != nil {
		r.add("--pool " + *pool + ": " + err.Error())
		return r.print(stderr)
	}
	if err := tokens.WritePoolLedger(*ledger, groups); err != nil {
		r.add("--ledger " + *ledger + ": " + err.Error())
		return r.print(stderr)
	}
	fmt.Fprintf(stdout, "FOLD OK rows=%d tasks=%d days=%d ledger=%s\n",
		len(groups), tasks, tokens.PoolDays(groups), oneline.Field(*ledger))
	return 0
}
