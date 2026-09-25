// pr reap (nova-tools#3156): close the sprint's stale superseded card PRs,
// deciding from the Redis records alone (internal/nsprint/reap).
//
//	nova-sprint pr reap --sprint <S> [--redis <addr>] [--dry-run] [--budget N] [--api <url>]
//
// A card PR is reaped when its card's work landed by another PR, when the
// read at its head scored under 8 and its card's task is closed, or when its
// record says its branch is gone; never with an APPROVE (8+) at its head or
// a live task naming it or its issue. The reason goes on pr:<name>:<n>
// (reap, reap_by, reap_why, reap_close) before the one GitHub write, the PR
// close, at most --budget closes a run (default 10); the rest stay pending
// for the next run. --dry-run prints the decisions and writes nothing.
// One line per reaped, revoked, stuck or missing PR, then the PR REAP
// receipt. Exit 0 done (a failed close is retried next run), 1 any STUCK or
// a Redis error, 2 usage, 6 no Redis.
package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reap"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func runPRReap(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "pr reap"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	dry := fs.Bool("dry-run", false, "")
	budget := fs.Int("budget", reap.DefaultBudget, "")
	api := fs.String("api", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 || *sprint == "" || *budget <= 0 {
		return refuse(errOut, verb, "needs --sprint <S> [--dry-run] [--budget N>0] [--api <url>]")
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
	}
	o := reap.Options{Sprint: *sprint, DryRun: *dry, Budget: *budget}
	if !*dry {
		gh, err := landGitHub(*api, *budget)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		o.Closer = gh
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	rep, err := reap.Run(ctx, st.Client(), o)
	for _, l := range rep.Lines {
		fmt.Fprintln(out, oneline.Escape(l))
	}
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %s\n", verb, oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintln(out, rep.Summary(*sprint, *dry))
	if rep.Stuck > 0 {
		return 1
	}
	return 0
}
