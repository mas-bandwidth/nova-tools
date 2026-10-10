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

func (a *app) cmdDefects(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("defects")
	sinceStr := fs.String("since", "", "list defects since duration back from now (e.g. 24h, 10m) or RFC 3339 timestamp")
	classStr := fs.String("class", "", "filter defects by class (brief, paths, test-name, routing, launch-refused, provider, store, take-back)")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "defects", argErr("takes no positional arguments ", err, pos...))
	}
	var since time.Time
	if *sinceStr != "" {
		if d, err := time.ParseDuration(*sinceStr); err == nil {
			since = a.now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, *sinceStr); err == nil {
			since = t
		} else {
			return refuse(stderr, "defects", "--since wants a duration back from now (e.g. 10m) or an RFC 3339 time, found "+*sinceStr)
		}
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "defects", err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Fleet}, sprint.StatsRecords)
	if err != nil {
		return a.readFailed("defects", err, stderr)
	}
	records := sprint.FindDefects(s, since, *classStr)
	if c.json {
		b, _ := json.Marshal(records)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(records) == 0 {
		fmt.Fprintln(stdout, "DEFECTS NONE")
		return 0
	}
	for _, d := range records {
		when := d.When.Format(time.RFC3339)
		if d.When.IsZero() {
			when = "-"
		}
		line := fmt.Sprintf("DEFECT %s card=%s worker=%s blame=%s class=%s finding=%q",
			when, d.Card, d.Worker, d.Blame, d.Class, d.Finding)
		if d.Fix != "" {
			line += " fix=" + d.Fix
		}
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "DEFECTS total=%d\n", len(records))
	return 0
}
