// The blocked verb and the blocked-resolve duty (nova-tools#3366, spec
// #3364; contract: rowan-new specs/ws-index.md). Blocked is the waiting set,
// ws:<stream>:waiting: a task whose DEPENDS-ON (task:<id> blocked_on) is not
// yet landed in its base. internal/nsprint/blocked holds the rules.
//
//	nova-sprint blocked list    [--stream <s>] --redis <addr>
//	nova-sprint blocked resolve [--stream <s>] [--mirror-root <dir>] [--as <who>] --redis <addr>
//
// list prints one row per waiting task, streams in rank order, each oldest
// first (created_at, task, parent, age, stream), then BLOCKED n=<k>. resolve
// runs one pass now and prints UNBLOCK <stream> <task> parent=<cond> at
// <sha8> per release and PARK <stream> <task> no-parent:<cond> per park,
// then RESOLVED released=<n> parked=<m> waiting=<w>. Nobody needs to run it:
// the reconciler runs the same pass every second as the blocked-resolve duty,
// so a task moves to ready from the landed event (the lander's pr:<repo>:<n>
// or task:<id> landed state, or the base tip moving in the bench mirror),
// never by hand. Exit 0 done, 1 a move was refused, 2 could not run. (The
// file sorts after consume.go so the consumer duties keep their place at the
// front of the reconciler's pass.)
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/blocked"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// blockedDutyName is the duty's name on the reconciler's DUTY lines.
const blockedDutyName = "blocked-resolve"

func init() {
	register(Verb{
		Name:    "blocked",
		Summary: "blocked list|resolve: the waiting sets oldest first; waiting -> ready once every DEPENDS-ON is landed (the reconciler runs resolve every pass)",
		Run:     runBlocked,
	})
	registerReconcileDuty(blockedDutyName, func(st *store.Store) (reconcileDuty, error) {
		return &blockedDuty{r: &blocked.Resolver{
			Client: st.Client(), Git: blocked.Mirror{Root: blocked.DefaultRoot()}, By: blockedDutyName,
		}}, nil
	})
}

// blockedDuty is one resolve pass per reconciler pass. Its Resolver keeps
// the mirror answers between passes (landed for good, unlanded until the
// base tip moves), so an idle pass is four Redis reads and one rev-parse per
// repo still awaited.
type blockedDuty struct{ r *blocked.Resolver }

func (d *blockedDuty) Run(ctx context.Context, _ *reconcile.Lease) (reconcile.Counts, error) {
	res, err := d.r.Pass(ctx, "")
	c := reconcile.Counts{Released: len(res.Released), Parked: len(res.Parked)}
	if err == nil && len(res.Refused) > 0 {
		err = fmt.Errorf("%d moves refused, first %s: %s", len(res.Refused), res.Refused[0].ID, res.Refused[0].Why)
	}
	return c, err
}

func runBlocked(ctx context.Context, args []string, out, errOut io.Writer) int {
	sub, rest, code, ok := subverb(args, "blocked", "list or resolve", errOut)
	if !ok {
		return code
	}
	switch sub {
	case "list":
		return runBlockedList(ctx, rest, out, errOut)
	case "resolve":
		return runBlockedResolve(ctx, rest, out, errOut)
	}
	return refuse(errOut, "blocked", "unknown subverb "+sub+"; want list or resolve")
}

func runBlockedList(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("blocked list", out, errOut)
	stream := w.fs.String("stream", "", "")
	if _, code, ok := w.parse(args, 0, "[--stream <s>] --redis <addr>"); !ok {
		return code
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	rows, err := blocked.List(ctx, st.Client(), *stream)
	if err != nil {
		return w.done(err, "")
	}
	now := time.Now()
	streams := map[string]bool{}
	for _, r := range rows {
		streams[r.Stream] = true
		parent := r.On
		if parent == "" {
			parent = "-"
		}
		fmt.Fprintf(out, "%s %s parent=%s age=%s stream=%s\n", time.UnixMilli(r.Created).UTC().Format(time.RFC3339),
			r.ID, quoteField(parent), r.Age(now), quoteField(r.Stream))
	}
	return w.done(nil, fmt.Sprintf("BLOCKED n=%d streams=%d", len(rows), len(streams)))
}

func runBlockedResolve(ctx context.Context, args []string, out, errOut io.Writer) int {
	w := newWSCmd("blocked resolve", out, errOut)
	stream := w.fs.String("stream", "", "")
	root := w.fs.String("mirror-root", blocked.DefaultRoot(), "")
	if _, code, ok := w.parse(args, 0, "[--stream <s>] [--mirror-root <dir>] --redis <addr>"); !ok {
		return code
	}
	st, code, ok := w.open(ctx)
	if !ok {
		return code
	}
	defer st.Close()
	r := &blocked.Resolver{Client: st.Client(), Git: blocked.Mirror{Root: *root}, By: *w.by}
	res, err := r.Pass(ctx, *stream)
	if err != nil {
		return w.done(err, "")
	}
	for _, x := range res.Released {
		fmt.Fprintf(out, "UNBLOCK %s %s parent=%s at %s\n", quoteField(x.Stream), x.ID, x.Parent, x.SHA)
	}
	for _, x := range res.Parked {
		fmt.Fprintf(out, "PARK %s %s no-parent:%s\n", quoteField(x.Stream), x.ID, x.Parent)
	}
	for _, u := range res.Unknown {
		fmt.Fprintf(out, "UNKNOWN %s\n", u)
	}
	for _, x := range res.Refused {
		fmt.Fprintf(out, "REFUSED %s %s\n", x.ID, x.Why)
	}
	line := fmt.Sprintf("RESOLVED released=%d parked=%d waiting=%d unknown=%d refused=%d",
		len(res.Released), len(res.Parked), res.Waiting, len(res.Unknown), len(res.Refused))
	code = w.done(nil, line)
	if code == 0 && len(res.Refused) > 0 {
		return 1
	}
	return code
}
