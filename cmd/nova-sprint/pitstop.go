// The pitstop verb (nova-tools #3371): the sprint's pit stop and its lift are
// one Redis state, s:<S>:pitstop {by, why, at, scope}, never a bus note; every
// automatic actor on the sprint table honours it (pitstop.Held, Glenn
// 2026-09-25 5:50 PM ET): the reconciler skips its duties, the route loop
// passes no rule; beats and the table go on. set and clear are one FCALL each
// (ns_pitstop_set, ns_pitstop_clear) with a receipt on s:<S>:log; status is
// one HGETALL. --stream <a,b> (set and clear) names streams: set --stream
// <a,b> stops only those (default all); clear --stream <a,b> narrows a stop
// by those, the last scoped stream going lifts it whole; clear --why records
// why it was lifted. A key of another type at s:<S>:pitstop (the 09-23 string) is ours
// (#3887): set replaces it without --force and clear lifts it, each naming it
// replaced_by/was_by=wrongtype:<type>; status prints it with the remedy, never
// WRONGTYPE. The live table reads this one key. Every subverb prints one
// line: exit 0 done, 1 refused with the remedy named, 2 usage (or Redis
// unreachable).
package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "pitstop",
		Summary: "set|clear|status --sprint <S> [--stream all|<a,b>] [--why <text>] [--force]: the sprint's pit stop, one Redis key every automatic duty and the route loop honour",
		Run:     runPitstop,
	})
}

func runPitstop(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "pitstop", "want set, clear or status")
	}
	switch args[0] {
	case "set", "clear", "status":
	default:
		return refuse(errOut, "pitstop", fmt.Sprintf("unknown subverb %s; want set, clear or status", args[0]))
	}
	sub := args[0]
	name := "pitstop " + sub
	fs, addr := lifeFlags(name)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	why := fs.String("why", "", verbflag.HelpWhy)
	force := fs.Bool("force", false, "replace an existing stop (set)")
	idem := fs.String("idem", "", verbflag.HelpIdem)
	scopeFlag := fs.String("stream", "", verbflag.HelpStream)
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, name, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, name, "takes flags, not positional arguments")
	}
	if *sprint == "" {
		return refuse(errOut, name, "--sprint is required")
	}
	if sub == "status" && (*why != "" || *force) {
		return refuse(errOut, name, "--why and --force belong to set and clear")
	}
	if sub == "status" && *scopeFlag != "" {
		return refuse(errOut, name, "--stream belongs to set and clear")
	}
	scope := verbflag.List(*scopeFlag)
	if *scopeFlag != "" && len(scope) == 0 {
		return refuse(errOut, name, "--stream is empty")
	}
	var streams []string
	for _, v := range scope {
		switch {
		case v == "all" && (sub != "set" || len(scope) > 1):
			return refuse(errOut, name, "--stream all is set's default and stands alone; clear without --stream lifts the whole stop")
		case v != "all":
			streams = append(streams, v)
		}
	}
	// the actor is the seat (#4352 A): --by is retired
	who := seatActor()
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, name, err.Error())
	}
	defer st.Close()
	c := st.Client()
	S := oneline.Field(*sprint)

	switch sub {
	case "status":
		stop, err := pitstop.Read(ctx, c, *sprint)
		if err != nil && strings.HasPrefix(err.Error(), "WRONGTYPE") {
			typ, _ := c.Type(ctx, pitstop.Key(*sprint)).Result()
			val, _ := c.Get(ctx, pitstop.Key(*sprint)).Result()
			fmt.Fprintf(out, "PITSTOP sprint=%s set by=wrongtype:%s why=%s; remedy: nova-sprint pitstop clear --sprint %s (or set) repairs it\n",
				S, oneline.Field(typ), oneline.Quote(val), S)
			return 0
		}
		if err != nil {
			return refuse(errOut, name, err.Error())
		}
		fmt.Fprintln(out, stop.Line())
		return 0
	case "set":
		r, err := pitstop.Set(ctx, c, *sprint, who, *why, *force, *idem, streams...)
		if err != nil {
			return refuse(errOut, name, err.Error())
		}
		switch r.Outcome {
		case pitstop.Unknown:
			fmt.Fprintf(errOut, "REFUSED pitstop set: sprint=%s is unknown (no s:%s status); remedy: name an opened sprint (nova-sprint sprint status)\n", S, S)
			return 1
		case pitstop.Exists:
			fmt.Fprintf(errOut, "REFUSED pitstop set: sprint=%s already stopped by=%s at=%d why=%s; remedy: nova-sprint pitstop clear --sprint %s, or set --force to replace it\n",
				S, oneline.Field(r.Prior.By), r.Prior.At, oneline.Quote(r.Prior.Why), S)
			return 1
		}
		line := fmt.Sprintf("PITSTOP SET sprint=%s by=%s at=%d scope=%s why=%s", S, oneline.Field(who), r.At, scopeField(streams), oneline.Quote(*why))
		if r.Prior.Set {
			line += fmt.Sprintf(" replaced_by=%s replaced_why=%s", oneline.Field(r.Prior.By), oneline.Quote(r.Prior.Why))
		}
		fmt.Fprintln(out, line)
		return 0
	default: // clear
		r, err := pitstop.Clear(ctx, c, *sprint, who, *idem, streams...)
		if err != nil {
			return refuse(errOut, name, err.Error())
		}
		switch r.Outcome {
		case pitstop.None:
			fmt.Fprintf(errOut, "REFUSED pitstop clear: sprint=%s has no pit stop; remedy: nothing to lift (nova-sprint pitstop status --sprint %s)\n", S, S)
			return 1
		case pitstop.NotIn:
			fmt.Fprintf(errOut, "REFUSED pitstop clear: sprint=%s stop does not hold stream=%s; nothing lifted; remedy: nova-sprint pitstop status --sprint %s names the scope\n",
				S, oneline.Quote(r.Stream), S)
			return 1
		}
		kind, lifted := "CLEAR", ""
		if r.Outcome == pitstop.Narrowed {
			kind = "NARROW"
		}
		if len(streams) > 0 {
			lifted = " lifted=" + scopeField(streams)
		}
		fmt.Fprintf(out, "PITSTOP %s sprint=%s by=%s at=%d%s was_by=%s was_at=%d was_why=%s\n",
			kind, S, oneline.Field(who), r.At, lifted, oneline.Field(r.Prior.By), r.Prior.At, oneline.Quote(r.Prior.Why))
		return 0
	}
}

// scopeField is `all`, or the named streams quoted and comma-joined.
func scopeField(streams []string) string {
	if len(streams) == 0 {
		return "all"
	}
	q := make([]string, len(streams))
	for i, s := range streams {
		q[i] = oneline.Quote(s)
	}
	return strings.Join(q, ",")
}
