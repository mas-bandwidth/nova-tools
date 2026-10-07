package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

func init() {
	verbClasses["defects"] = classRead
}

// cmdDefects lists attempts whose blame is coordinator or provider.
func (a *app) cmdDefects(args []string, stdout, stderr io.Writer) int {
	const verb = "defects"
	fs, c := a.verbSetup(verb)
	since := fs.String("since", "", "a duration back from now (24h) or an RFC 3339 time; empty is the epoch")
	class := fs.String("class", "", "one class: brief, paths, test-name, routing, launch-refused, provider, store, take-back")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, verb, argErr("takes no words ", err, pos...))
	}
	var from time.Time
	if *since != "" {
		if d, err := time.ParseDuration(*since); err == nil {
			from = a.now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, *since); err == nil {
			from = t
		} else {
			return refuse(stderr, verb, "--since wants a duration back from now (24h) or an RFC 3339 time, found "+*since)
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Fleet}, nil)
	if err != nil {
		return a.readFailed(verb, err, stderr)
	}
	recs := sprint.FindDefects(s, from, *class)
	if c.json {
		if recs == nil {
			recs = []sprint.DefectRecord{}
		}
		b, _ := json.Marshal(recs)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, r := range recs {
		when := "-"
		if !r.When.IsZero() {
			when = r.When.UTC().Format(time.RFC3339)
		}
		line := fmt.Sprintf("DEFECT %s card=%s worker=%s blame=%s class=%s finding=%q", when, r.Card, r.Worker, r.Blame, r.Class, r.Finding)
		if r.Fix != "" {
			line += " fix=" + r.Fix
		}
		fmt.Fprintln(stdout, line)
	}
	if len(recs) == 0 {
		fmt.Fprintln(stdout, "DEFECTS NONE")
		return 0
	}
	fmt.Fprintf(stdout, "DEFECTS total=%d\n", len(recs))
	return 0
}
