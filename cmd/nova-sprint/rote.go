package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// rote and note --rote (nova-tools #3110; #2756 v6 4.11 and 11.9): each
// mind's hand work by class, and the top class as the next mechanism
// (reduce-rote-work-daily). The counting is in internal/nsprint/fold, which
// prints the same lines in the fold.
func init() {
	register(Verb{Name: "note", Summary: fold.NoteSummary, Run: cmdNote})
	register(Verb{Name: "rote", Summary: fold.RoteSummary, Run: cmdRote})
}

// roteNow is the clock for --since <duration> and a note's at; tests may set it.
var roteNow = time.Now

func refuseVerb(stderr io.Writer, verb, what string) int {
	fmt.Fprintf(stderr, "nova-sprint %s: %s; run: nova-sprint help\n", verb, oneline.Escape(what))
	return 2
}

func quietFlags(name string) *flag.FlagSet {
	return verbflag.New(name)
}

// cmdNote is `note --rote <class> --as <mind> --store <host:port>`. Exit 0
// the note is on rote:log, 2 refused or could not run.
func cmdNote(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := quietFlags("note")
	class := fs.String("rote", "", "")
	mind := fs.String("as", "", "")
	addr := fs.String("store", "", "")
	what := fs.String("what", "", "")
	mech := fs.String("mech", "", "")
	if err := fs.Parse(args); err != nil {
		return refuseVerb(stderr, "note", err.Error()+"; it wants --rote <class> --as <mind> --store <host:port>")
	}
	var problems []string
	if *class == "" {
		problems = append(problems, "--rote <class> names the hand work (the only note this cut records)")
	}
	if *mind == "" {
		problems = append(problems, "--as <mind> names who did it by hand")
	}
	if *addr == "" {
		problems = append(problems, "--store <host:port> is the sprint's Redis")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments: "+strings.Join(fs.Args(), " "))
	}
	if len(problems) > 0 {
		return refuseVerb(stderr, "note", strings.Join(problems, "; "))
	}
	n := fold.RoteNote{Mind: *mind, Class: *class, What: *what, Mech: *mech}
	if err := n.Check(); err != nil {
		return refuseVerb(stderr, "note", err.Error())
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuseVerb(stderr, "note", err.Error())
	}
	defer st.Close()
	id, err := fold.Note(ctx, st.Client(), n, roteNow())
	if err != nil {
		return refuseVerb(stderr, "note", err.Error())
	}
	fmt.Fprintf(stdout, "NOTE ROTE mind=%s class=%s id=%s\n", n.Mind, n.Class, id)
	return 0
}

// cmdRote is `rote --store <host:port> [--since <t>] [--until <t>] [--sprint <S>]`.
// Exit 0 printed, 2 refused or could not run.
func cmdRote(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := quietFlags("rote")
	addr := fs.String("store", "", "")
	since := fs.String("since", "", "")
	until := fs.String("until", "", "")
	sprint := fs.String("sprint", "", "")
	if err := fs.Parse(args); err != nil {
		return refuseVerb(stderr, "rote", err.Error()+"; it wants --store <host:port> [--since <t>]")
	}
	var problems []string
	if *addr == "" {
		problems = append(problems, "--store <host:port> is the sprint's Redis")
	}
	if fs.NArg() > 0 {
		problems = append(problems, "takes flags, not positional arguments: "+strings.Join(fs.Args(), " "))
	}
	var w fold.Window
	now := roteNow()
	for _, f := range []struct {
		name, v string
		to      *time.Time
	}{{"--since", *since, &w.Since}, {"--until", *until, &w.Until}} {
		if f.v == "" {
			continue
		}
		t, err := roteTime(f.v, now)
		if err != nil {
			problems = append(problems, f.name+" "+err.Error())
			continue
		}
		*f.to = t
	}
	var extra []string
	if *sprint != "" {
		extra = append(extra, "s:"+*sprint+":log")
	}
	if len(problems) > 0 {
		return refuseVerb(stderr, "rote", strings.Join(problems, "; "))
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuseVerb(stderr, "rote", err.Error())
	}
	defer st.Close()
	r, err := fold.ReadRote(ctx, st.Client(), w, extra...)
	if err != nil {
		return refuseVerb(stderr, "rote", err.Error())
	}
	fold.PrintRote(stdout, "ROTE", r)
	return 0
}

// roteTime reads an RFC 3339 time, or a duration back from now (24h).
func roteTime(v string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("%q is neither an RFC 3339 time nor a duration back from now (24h)", v)
}
