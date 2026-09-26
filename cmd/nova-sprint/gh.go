// The gh verb (nova-tools #4343): what nova-sprint spent on GitHub.
//
//	nova-sprint gh budget [--redis <addr>] [--window <d>]
//
// budget prints one GH BUDGET line (the window's calls, the last
// X-RateLimit-Remaining and its reset) and one GH line per verb and
// endpoint with a call in the window, most calls first. Every call the one
// client (internal/gh) makes is counted as it is made, so the line is the
// truth of the hour, not a sample. Zero GitHub calls. Exit 0, 2 usage, 6
// the store did not answer.
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "gh",
		Summary: "gh budget: GitHub calls per verb and endpoint over the last hour and the remaining quota (#4343)",
		Run:     runGH,
	})
}

func runGH(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "budget" {
		return refuse(errOut, "gh", "wants budget: nova-sprint gh budget [--redis <addr>]")
	}
	const verb = "gh budget"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not "+fs.Arg(0))
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	rep, err := gh.Budget(ctx, st.Client(), time.Now())
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	for _, l := range rep.Lines() {
		fmt.Fprintln(out, l)
	}
	return 0
}
