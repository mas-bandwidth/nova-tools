// The ready verb registers itself through the S0 registry (registry.go), so
// adding it never edits main.go. It reads one Snapshot from Redis and asks
// the forge by REST through the dealer's seam (internal/nsprint/deal.GH);
// it never writes (nova-tools #3109).
package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"

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

// readyForge is the forge seam; a test replaces it with a map (CI-NET).
var readyForge = func(st *store.Store) deal.PRs {
	return deal.GH{Client: gh.New("ready", st.Client()), Redis: st.Client()}
}

func runReady(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("ready")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	why := fs.String("why", "", verbflag.HelpWhy)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "ready", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "ready", "takes flags, not positional arguments")
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
	verdicts := ready.Evaluate(ctx, snap, readyForge(st))

	if *why != "" {
		id := strings.TrimPrefix(strings.TrimSpace(*why), "task:") // the bare id and task:<id> are one card
		v, ok, err := ready.Find(verdicts, id)
		if err != nil {
			return refuse(errOut, "ready --why", err.Error())
		}
		if !ok {
			return readyWhyCard(ctx, st, id, out, errOut)
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

// readyWhyCard is `ready --why` of a task card on the stream line (#4399:
// `ready --why p1` on a waiting card was refused as "not a queued card"):
// where it is, and for a waiting card each DEPENDS-ON entry the way the
// reconciler's waiting-resolve duty reads it, with the line that moves it.
// Exit 1 while it waits, 0 otherwise.
func readyWhyCard(ctx context.Context, st *store.Store, id string, out, errOut io.Writer) int {
	w, found, err := reconcile.Explain(ctx, st.Client(), id)
	if err != nil {
		return refuse(errOut, "ready --why", err.Error())
	}
	if !found {
		return refuse(errOut, "ready --why", id+" is not a queued card, an open task or a task card (task:"+id+" has no where)")
	}
	stream := quoteField(w.Stream)
	list := func(xs []string) string {
		if len(xs) == 0 {
			return "-"
		}
		return strings.Join(xs, ",")
	}
	switch {
	case w.Where == "ready":
		fmt.Fprintf(out, "READY %s/%s\n", stream, w.ID)
		return 0
	case w.Where != "waiting":
		fmt.Fprintf(out, "%s %s stream=%s\n", strings.ToUpper(w.Where), w.ID, stream)
		return 0
	case w.Empty:
		fmt.Fprintf(out, "WAITING %s stream=%s blocked_on=empty: no DEPENDS-ON, so the reconciler leaves it waiting; run: nova-sprint task move --ids %s --where ready\n", w.ID, stream, w.ID)
	case w.Released():
		met := list(w.Met)
		if w.None {
			met = "none"
		}
		fmt.Fprintf(out, "WAITING %s stream=%s met=%s: the reconciler's next pass moves it to ready; run: nova-sprint reconcile --once\n", w.ID, stream, met)
	default:
		fmt.Fprintf(out, "WAITING %s stream=%s on=%s unknown=%s met=%s\n", w.ID, stream, list(w.On), list(w.Unknown), list(w.Met))
	}
	return 1
}
