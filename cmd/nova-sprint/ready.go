// The ready verb registers itself through the S0 registry (registry.go), so
// adding it never edits main.go. It reads one Snapshot from Redis and asks
// the records through the dealer's seam (internal/nsprint/deal.Records: Redis
// only, never the forge, #3967); it never writes (nova-tools #3109).
package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ready"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "ready",
		Summary: "print the ready antichain; --why <id> prints its first blocker (exit 1 when blocked)",
		Run:     runReady,
	})
}

// readyForge is the records seam; nil is deal.Records over the verb's Redis.
// A test replaces it with a map.
var readyForge = func() deal.PRs { return nil }

func runReady(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ready")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	why := fs.String("why", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ready", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "ready", "takes flags, not positional arguments; usage: nova-sprint ready --redis <addr> [--sprint <name>] [--why <id>]")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "ready", err.Error())
	}
	defer st.Close()
	snap, err := ready.Read(ctx, st.Client(), *sprint)
	if err != nil {
		return refuse(errOut, "ready", err.Error())
	}
	prs := readyForge()
	if prs == nil {
		prs = deal.Records{C: st.Client()}
	}
	verdicts := ready.Evaluate(ctx, snap, prs)

	if *why != "" {
		v, ok, err := ready.Find(verdicts, *why)
		if err != nil {
			return refuse(errOut, "ready --why", err.Error())
		}
		if !ok {
			return refuse(errOut, "ready --why", *why+" is not a queued card or an open task")
		}
		if v.Ready {
			fmt.Fprintf(out, "READY %s/%s\n", v.Item.Sprint, v.Item.ID)
			return 0
		}
		fmt.Fprintln(out, v.Blocker)
		return 1
	}

	n := 0
	for _, v := range verdicts {
		if v.Ready {
			n++
			fmt.Fprintf(out, "READY %s/%s %s\n", v.Item.Sprint, v.Item.ID, v.Item.Kind)
		}
	}
	fmt.Fprintf(out, "ready %d/%d\n", n, len(verdicts))
	return 0
}
