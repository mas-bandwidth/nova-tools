// The pitstop verb (nova-tools #3371): the sprint's pit stop and its lift are
// one Redis state, s:<S>:pitstop {by, why, at, scope}, never a bus note; the deal
// pass honours it (a pit-stopped sprint deals nothing). set and clear are one FCALL each
// (ns_pitstop_set, ns_pitstop_clear) with a receipt on s:<S>:log; status is
// one HGETALL. --scope (repeatable; set and clear) names streams: set
// --scope <stream>... stops only those (default all); clear --scope
// <stream>... narrows a stop by those, the last scoped stream going lifts it
// whole. Every subverb prints one line: exit 0 done, 1 refused with the
// remedy named, 2 usage (or Redis unreachable).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "pitstop",
		Summary: "set|clear|status --sprint <S> [--scope all|<stream>]... [--why <text>] [--by <who>] [--force]: the sprint's pit stop, one Redis key the dealer honours",
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
	sprint := fs.String("sprint", "", "sprint")
	why := fs.String("why", "", "why the sprint is stopped (set)")
	by := fs.String("by", "", "who sets or clears it; default NOVA_FRIEND")
	force := fs.Bool("force", false, "replace an existing stop (set)")
	idem := fs.String("idem", "", "idempotency marker for the receipt")
	var scope multiFlag
	fs.Var(&scope, "scope", "a stream the stop holds (set) or lifts (clear); repeatable; set --scope all (the default) holds every stream")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, name, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, name, "takes flags, not positional arguments")
	}
	if *sprint == "" {
		return refuse(errOut, name, "--sprint is required")
	}
	if sub != "set" && (*why != "" || *force) {
		return refuse(errOut, name, "--why and --force belong to set")
	}
	if sub == "status" && len(scope) > 0 {
		return refuse(errOut, name, "--scope belongs to set and clear")
	}
	var streams []string
	for _, v := range scope {
		switch {
		case strings.TrimSpace(v) == "":
			return refuse(errOut, name, "--scope is empty")
		case v == "all" && (sub != "set" || len(scope) > 1):
			return refuse(errOut, name, "--scope all is set's default and stands alone; clear without --scope lifts the whole stop")
		case v != "all":
			streams = append(streams, v)
		}
	}
	who := *by
	if who == "" {
		who = os.Getenv(seatEnv)
	}
	if sub != "status" && who == "" {
		return refuse(errOut, name, "--by is required when "+seatEnv+" is empty")
	}
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
