package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// cmdStats is the epoch's numbers (sprint.Stats): the stages, each member's work,
// each reader's reads and each route's takes, in seconds, from one read of the
// work, fleet and readers tables (the primaries' work and read cards, retired ones
// too, read with them in read sets, never a card at a time).
func (a *app) cmdStats(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("stats")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return refuse(stderr, "stats", argErr("takes no words ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "stats", err.Error())
	}
	s, err := st.Load(context.Background(), []string{sprint.Work, sprint.Fleet, sprint.Readers}, sprint.StatsRecords)
	if err != nil {
		return a.readFailed("stats", err, stderr)
	}
	ps := sprint.Stats(s)
	if c.json {
		b, _ := json.Marshal(ps)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprint(stdout, statsText(ps))
	fmt.Fprintf(stdout, "STATS OK epoch=%d primaries=%d members=%d readers=%d routes=%d\n", ps.Epoch, ps.Primaries, len(ps.Work), len(ps.Reads), len(ps.Routes))
	return 0
}

// statsText is the four tables, seconds as "median max n=<count>".
func statsText(ps sprint.PassStats) string {
	count := func(name string) ntable.Column {
		return ntable.Column{Name: name, Projection: ntable.Text, Fold: ntable.Sum}
	}
	secs := func(name string) ntable.Column {
		return ntable.Column{Name: name, Label: name + " (median max n)", Projection: ntable.Text, Fold: ntable.None}
	}
	row := func(key string, texts ...string) ntable.Row {
		r := ntable.Row{Key: key, Texts: map[string]string{}}
		for i := 0; i+1 < len(texts); i += 2 {
			r.Texts[texts[i]] = texts[i+1]
		}
		return r
	}
	n := strconv.Itoa
	stages := ntable.Table{Columns: []ntable.Column{secs("seconds")}, Rows: []ntable.Row{
		row("deal wait", "seconds", measureText(ps.Stages.DealWait)),
		row("finish to two reads", "seconds", measureText(ps.Stages.FinishToReads)),
		row("accept to land", "seconds", measureText(ps.Stages.AcceptToLand)),
		row("total", "seconds", measureText(ps.Stages.Total)),
	}}
	work := ntable.Table{Columns: []ntable.Column{count("cards"), count("failed"), secs("take wait"), secs("run wall"), secs("report lag")}}
	for _, m := range ps.Work {
		work.Rows = append(work.Rows, row(m.Member, "cards", n(m.Cards), "failed", n(m.Failed),
			"take wait", measureText(m.TakeWait), "run wall", measureText(m.RunWall), "report lag", measureText(m.ReportLag)))
	}
	reads := ntable.Table{Columns: []ntable.Column{count("cards"), secs("begin wait"), secs("run wall"), secs("report lag")}}
	for _, r := range ps.Reads {
		reads.Rows = append(reads.Rows, row(r.Reader, "cards", n(r.Cards),
			"begin wait", measureText(r.BeginWait), "run wall", measureText(r.RunWall), "report lag", measureText(r.ReportLag)))
	}
	routes := ntable.Table{Columns: []ntable.Column{count("takes"), count("ok"), count("failed"), count("provider"), secs("run wall")}}
	for _, r := range ps.Routes {
		routes.Rows = append(routes.Rows, row(r.Route, "takes", n(r.Takes), "ok", n(r.OK), "failed", n(r.Failed), "provider", n(r.Provider),
			"run wall", measureText(r.RunWall)))
	}
	return ntable.Render(stages, ntable.RenderOpts{Title: "stages"}) + "\n" +
		ntable.Render(work, ntable.RenderOpts{Title: "work"}) + "\n" +
		ntable.Render(reads, ntable.RenderOpts{Title: "reads"}) + "\n" +
		ntable.Render(routes, ntable.RenderOpts{Title: "routes"}) + "\n"
}

// measureText is a measure as its cell prints it: the median and the max in seconds
// to a tenth, fixed width so a column's numbers line up, and the count; "-" for none.
func measureText(m sprint.Measure) string {
	if m.N == 0 {
		return "-"
	}
	return fmt.Sprintf("%7.1f %7.1f n=%d", m.Median, m.Max, m.N)
}
