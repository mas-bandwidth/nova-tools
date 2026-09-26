// card stitch (nova-tools#4317): a plan and its stitch's brief, from the
// records.
//
//	nova-sprint card stitch --ids <parent|stitch> [--write] [--redis <addr>]
//	nova-sprint card stitch --drop <child> [--redis <addr>] (the seat is who dropped it)
//
// --drop <child> drops a child that ended done/fail (a cancelled child) from
// its plan (taskcard.DropChild): the parent's children and the stitch's
// edges lose it, through the one move, and the brief is rewritten, so a
// stuck plan moves on. Receipt: STITCH DROP parent=<p> child=<c>
// children=<n> state=<s>; a child still live, landed or of no plan is
// refused by name with the remedy.
//
// Prints the plan's line (its derived state and its children's counts), one
// line per child, then the stitch's generated brief (taskcard.StitchBrief:
// every child's PR, RESULT.md summary and read score) as the records hold
// it now. --write stores that brief onto the stitch's body (what the
// waiting resolver does when it releases the stitch), so the coordinator
// can refresh it by hand after a child's late read. One receipt:
//
//	PLAN id=<parent> state=<s> children=<n> stitch=<id>:<where> written=<0|1> ms=<ms>
//
// Exit 0, 1 refused (not a plan, no record) with the remedy, 2 usage.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdCardStitch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	const verb = "card stitch"
	fs := verbflag.New(verb)
	ids := fs.String("ids", "", verbflag.HelpIDs)
	write := fs.Bool("write", false, "write the stitch brief onto the stitch card")
	drop := fs.String("drop", "", "a child id to drop from its plan")
	addr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	id := new(string)
	*id = oneID(*ids)
	if (*id == "") == (*drop == "") || fs.NArg() > 0 || (*drop != "" && *write) {
		return refuse(stderr, verb, "wants --ids <parent|stitch> [--write], or --drop <child>, and --redis <addr> (or NOVA_SPRINT_REDIS)")
	}
	raddr := taskAddr(*addr)
	if raddr == "" {
		return refuse(stderr, verb, "needs --redis <addr> or NOVA_SPRINT_REDIS")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := store.Open(ctx, raddr)
	if err != nil {
		return refuse(stderr, verb, "redis: "+err.Error())
	}
	defer func() { _ = st.Close() }()
	start := time.Now()
	c := st.Client()
	if *drop != "" {
		p, err := taskcard.DropChild(ctx, c, *drop, seatActor())
		if err != nil {
			why := err.Error()
			if w, ok := taskcard.IsRefused(err); ok {
				why = w
			}
			fmt.Fprintf(stdout, "REFUSED card stitch drop=%s why=%s\n", *drop, oneline.Field(why))
			return 1
		}
		fmt.Fprintf(stdout, "STITCH DROP parent=%s child=%s children=%d state=%s ms=%d\n", p.ID, *drop, len(p.Children), p.State(), time.Since(start).Milliseconds())
		return 0
	}
	// The parent: the id, or the stitch's parent field.
	parent := *id
	if rec, err := c.HMGet(ctx, taskcard.Key(*id), taskcard.FieldParent, taskcard.FieldPhase, "kind").Result(); err == nil {
		get := func(i int) string {
			s, _ := rec[i].(string)
			return s
		}
		if get(2) != taskcard.KindPlan && get(1) == taskcard.PhaseStitch && get(0) != "" {
			parent = get(0)
		}
	}
	p, err := taskcard.ReadPlan(ctx, c, parent)
	if err != nil {
		fmt.Fprintf(stdout, "REFUSED card stitch id=%s why=%s remedy=%s\n", *id, oneline.Field(err.Error()), oneline.Field("push the parent, then nova-sprint card cut --parent "+parent+" --from <children.tsv>"))
		return 1
	}
	if p.Stitch.ID == "" && len(p.Children) == 0 {
		fmt.Fprintf(stdout, "REFUSED card stitch id=%s why=%s remedy=%s\n", *id, oneline.Field("task:"+parent+" is not a plan"), oneline.Field("nova-sprint card cut --parent "+parent+" --from <children.tsv> cuts its children and stitch"))
		return 1
	}
	fmt.Fprintln(stdout, p.Line())
	for _, ch := range p.Children {
		fmt.Fprintf(stdout, "  child %s %s pr=%s score=%s\n", ch.ID, orDash(ch.Where), orDash(ch.PRRef()), orDash(ch.Score))
	}
	brief := taskcard.StitchBrief(p)
	written := 0
	if *write {
		if p.Stitch.ID == "" {
			fmt.Fprintf(stdout, "REFUSED card stitch id=%s why=%s remedy=%s\n", *id, oneline.Field("task:"+parent+" has no stitch"), oneline.Field("nova-sprint card cut --parent "+parent+" --from <children.tsv>"))
			return 1
		}
		if _, brief, err = taskcard.WriteStitchBrief(ctx, c, p.Stitch.ID); err != nil {
			fmt.Fprintf(stdout, "REFUSED card stitch id=%s why=%s\n", *id, oneline.Field(err.Error()))
			return 1
		}
		written = 1
	}
	fmt.Fprintln(stdout, strings.TrimRight(brief, "\n"))
	stitch := "-"
	if p.Stitch.ID != "" {
		stitch = p.Stitch.ID + ":" + orDash(p.Stitch.Where)
	}
	fmt.Fprintf(stdout, "PLAN id=%s state=%s children=%d stitch=%s written=%d ms=%d\n", p.ID, p.State(), len(p.Children), stitch, written, time.Since(start).Milliseconds())
	return 0
}
