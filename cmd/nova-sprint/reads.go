package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The read verbs: queue, where, inbox, card, check. Each has --json, one
// object for a program; the driver reads the sprint through them.

// summary is the sprint's line: landed / all primaries on the table, percent,
// ETA (the table layer's view line, which has no rate to give an ETA from).
func summary(t ntable.Table) string {
	landed, all := counts(t)
	pct := "0.0%"
	if all > 0 {
		pct = strconv.FormatFloat(100*float64(landed)/float64(all), 'f', 1, 64) + "%"
	}
	return fmt.Sprintf("%d/%d %s -> ETA", landed, all, pct)
}

func counts(t ntable.Table) (landed, all int64) {
	j := t.Column(sprint.Landed)
	for _, r := range t.Rows {
		for k, c := range t.Columns {
			if c.Projection != ntable.Count || k >= len(r.Cells) {
				continue
			}
			all += r.Cells[k].Count
			if k == j {
				landed += r.Cells[k].Count
			}
		}
	}
	return landed, all
}

// queueCard is one card of a queue, for a program.
type queueCard struct {
	ID      string  `json:"id"`
	Table   string  `json:"table"`
	Row     string  `json:"row"`
	Col     string  `json:"col"`
	Primary string  `json:"primary,omitempty"`
	Stream  string  `json:"stream,omitempty"`
	Attempt int     `json:"attempt,omitempty"`
	Gen     int     `json:"gen,omitempty"`
	Head    string  `json:"head,omitempty"`
	Score   float64 `json:"score"`
}

func (a *app) cmdQueue(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("queue")
	as := fs.String("as", "", "a reader (its read cards, asked then reading) or a fleet member (its work cards, ready then working)")
	stream := fs.String("stream", "", "a stream: its merge queue, then its stuck cards")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, "queue", err.Error())
	}
	if len(pos) > 0 || (*as == "") == (*stream == "") {
		return refuse(stderr, "queue", "wants one of --as <reader|member>, --stream <s>")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "queue", err.Error())
	}
	ctx := context.Background()
	var cards []queueCard
	add := func(table string, cs []*sprint.Card) {
		for _, x := range cs {
			cards = append(cards, queueCard{ID: x.ID, Table: table, Row: x.Row, Col: x.Col, Primary: x.F("primary"), Stream: x.F("stream"),
				Attempt: x.Int("attempt"), Gen: x.Int("gen"), Head: x.F("head"), Score: x.Score})
		}
	}
	if *stream != "" {
		cs, err := st.ReadCells(ctx, sprint.Merge, *stream, sprint.Queued, sprint.Stuck)
		if err != nil {
			return a.readFailed("queue", err, stderr)
		}
		add(sprint.Merge, cs)
	} else {
		for _, t := range []struct{ table, a, b string }{{sprint.Readers, sprint.Asked, sprint.Reading}, {sprint.Fleet, sprint.Ready, sprint.Working}} {
			cs, err := st.ReadCells(ctx, t.table, *as, t.a, t.b)
			if err != nil {
				return a.readFailed("queue", err, stderr)
			}
			add(t.table, cs)
		}
	}
	if c.json {
		if cards == nil {
			cards = []queueCard{}
		}
		b, _ := json.Marshal(map[string]any{"as": *as, "stream": *stream, "cards": cards})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	var lines []string
	for _, x := range cards {
		l := fmt.Sprintf("%s %s:%s:%s", x.ID, x.Table, x.Row, x.Col)
		if x.Gen > 0 {
			next := "take"
			if x.Col == sprint.Working {
				next = "finish"
			}
			l += " gen=" + strconv.Itoa(x.Gen) + " " + next + ": " + x.ID + "@" + strconv.Itoa(x.Gen)
		}
		lines = append(lines, l)
	}
	listed(stdout, "CARD", lines, c.max, "queue")
	fmt.Fprintf(stdout, "QUEUE OK cards=%d\n", len(cards))
	return 0
}

func (a *app) readFailed(verbName string, err error, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s %s: %s\n", prog, verbName, oneline.Escape(err.Error()))
	if ntable.IsRefusal(err) {
		return 1
	}
	return 2
}

// whereView is the view, for a program.
type whereView struct {
	At      time.Time                               `json:"at"`
	Landed  int64                                   `json:"landed"`
	All     int64                                   `json:"all"`
	Summary string                                  `json:"summary"`
	Tables  map[string]map[string]map[string]string `json:"tables"` // table -> row -> column -> cell as printed
	Streams []sprint.StreamClock                    `json:"streams"`
	Stalled []string                                `json:"stalled,omitempty"`
	Pending string                                  `json:"pending,omitempty"`
}

func (a *app) cmdWhere(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("where")
	watch := fs.Bool("watch", false, "redraw every --every until interrupted")
	every := fs.Duration("every", time.Second, "the redraw interval with --watch")
	stale := fs.Duration("stale", defaultStale, "a stream with no progress for longer is shown stalled")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "where", fmt.Sprint("takes no words ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "where", err.Error())
	}
	for {
		v, frame, err := a.where(context.Background(), st, *stale)
		if err != nil {
			return a.readFailed("where", err, stderr)
		}
		if c.json {
			b, _ := json.Marshal(v)
			fmt.Fprintln(stdout, string(b))
		} else {
			if *watch {
				fmt.Fprint(stdout, "\x1b[H\x1b[2J")
			}
			fmt.Fprint(stdout, frame)
		}
		if !*watch {
			return 0
		}
		a.sleep(*every)
	}
}

func (a *app) where(ctx context.Context, st *store.Store, stale time.Duration) (whereView, string, error) {
	names := make([]string, len(sprint.ViewOrder))
	for i, t := range sprint.ViewOrder {
		names[i] = st.Names.Table(t)
	}
	shapes, err := st.B.Shapes(ctx, names)
	if err != nil {
		return whereView{}, "", err
	}
	f, err := st.B.ReadFence(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	clocks, err := st.StreamClocks(ctx)
	if err != nil {
		return whereView{}, "", err
	}
	now := a.now()
	v := whereView{At: now, Tables: map[string]map[string]map[string]string{}, Streams: clocks}
	v.Landed, v.All = counts(shapes[0])
	v.Summary = summary(shapes[0])
	if f.Pending != nil {
		v.Pending = f.Pending.ID
	}
	var b strings.Builder
	b.WriteString(now.Format("2006-01-02 15:04:05 MST") + "\n\nSPRINT TABLE\n\n" + v.Summary + "\n\n")
	var parts []string
	for i, t := range shapes {
		logical := sprint.ViewOrder[i]
		rows := map[string]map[string]string{}
		for _, r := range t.Rows {
			cells := map[string]string{}
			for j, col := range t.Columns {
				cells[col.Name] = ntable.CellText(t.Columns, r, j)
			}
			rows[r.Key] = cells
		}
		v.Tables[logical] = rows
		if out := ntable.Render(t, ntable.RenderOpts{Title: logical}); out != "" {
			parts = append(parts, out)
		}
	}
	b.WriteString(strings.Join(parts, "\n"))
	for _, c := range clocks {
		if c.Stalled(now, stale) {
			v.Stalled = append(v.Stalled, c.Stream)
			fmt.Fprintf(&b, "\nstalled: stream %s: no progress for %s (state %s)\n", c.Stream, now.Sub(c.Progress).Round(time.Second), c.State)
		}
	}
	if f.Pending != nil {
		fmt.Fprintf(&b, "\npending: operation %s (%s, since %s); run: nova-sprint repair\n", f.Pending.ID, f.Pending.Verb, f.Pending.At.Local().Format("15:04:05"))
	}
	return v, b.String(), nil
}

func (a *app) cmdInbox(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("inbox")
	open := fs.String("open", "", "list every member and notification of the group of this id")
	read := fs.Bool("read", false, "move the cursor past what is shown: happened notifications before it are not shown again (open judgments always are)")
	deadline := fs.Duration("deadline", defaultDeadline, "a judgment open longer is overdue")
	stale := fs.Duration("stale", defaultStale, "a stream with no progress for longer is shown stalled")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "inbox", fmt.Sprint("takes no words ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "inbox", err.Error())
	}
	if isNumber(*open) {
		return refuse(stderr, "inbox", "group numbers are not accepted: --open wants a group's id, as inbox prints it")
	}
	ctx := context.Background()
	v, err := st.Inbox(ctx, *deadline, *stale, 10000)
	if err != nil {
		return a.readFailed("inbox", err, stderr)
	}
	var opened *sprint.Group
	if *open != "" {
		g, ok := sprint.FindGroup(v.Groups, *open)
		if !ok {
			fmt.Fprintf(stderr, "%s inbox: no group %s now; %s\n", prog, oneline.Escape(*open), oneline.Escape(groupList(v.Groups)))
			return 1
		}
		opened = &g
	}
	if *read && v.Last != "" {
		if err := st.B.SetCursor(ctx, v.Last); err != nil {
			return a.readFailed("inbox", err, stderr)
		}
	}
	if c.json {
		groups := v.Groups
		if groups == nil {
			groups = []sprint.Group{}
		}
		out := map[string]any{"groups": groups, "last": v.Last, "cursor": v.Cursor, "at": a.now()}
		if opened != nil {
			out["open"] = nonNil(opened.Members)
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	now := a.now()
	judg, other := 0, 0
	for _, g := range v.Groups {
		if g.Kind == sprint.Judgment {
			judg++
		} else {
			other++
		}
		fmt.Fprintln(stdout, groupLine(g, now))
		for _, cmd := range g.Commands {
			fmt.Fprintf(stdout, "  %s:\n", oneline.Escape(cmd.Decision))
			for _, l := range cmd.Lines {
				fmt.Fprintf(stdout, "    %s\n", oneline.Escape(l))
			}
		}
		if opened != nil && g.ID == opened.ID {
			for _, m := range g.Members {
				fmt.Fprintf(stdout, "  %s\n", oneline.Escape(m))
			}
			fmt.Fprintf(stdout, "  notes: %s\n", oneline.Escape(strings.Join(g.Notes, " ")))
		}
	}
	fmt.Fprintf(stdout, "INBOX OK judgments=%d happened=%d cursor=%s\n", judg, other, dashed(v.Cursor))
	return 0
}

func dashed(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// groupLine is one inbox group: its kind, its id (what --group takes), a mark
// for a repeat or an overdue one, the type, the stream, its size (what --expect
// takes), how long it waited, the primaries (bounded, then the command that
// lists them all), and the decisions open to the coordinator.
func groupLine(g sprint.Group, now time.Time) string {
	mark := " "
	if g.Marked {
		mark = "!"
	}
	kind := strings.ToUpper(g.Kind)
	l := fmt.Sprintf("%s %s %s %s", kind, g.ID, mark, g.Type)
	if g.Stream != "" {
		l += "  stream=" + g.Stream
	}
	if g.Size > 0 || g.Kind == sprint.Judgment {
		l += "  size=" + strconv.Itoa(g.Size)
	} else {
		l += "  x" + strconv.Itoa(g.Count)
	}
	if g.Kind == sprint.Judgment {
		l += "  waited=" + now.Sub(g.Oldest).Round(time.Second).String()
		if g.Overdue {
			l += " OVERDUE"
		} else if !g.Due.IsZero() {
			l += "  due=" + g.Due.Local().Format("15:04:05")
		}
		if g.Before > 0 {
			l += "  before=" + strconv.Itoa(g.Before)
		}
	}
	if len(g.Primaries) > 0 {
		ps := g.Primaries
		more := ""
		if len(ps) > 8 || g.Size > len(ps) {
			if len(ps) > 8 {
				ps = ps[:8]
			}
			more = ",... all: nova-sprint inbox --open " + g.ID
		}
		l += "  (" + strings.Join(ps, ",") + more + ")"
	}
	if g.What != "" {
		l += "  " + g.What
	}
	if len(g.Commands) > 0 {
		return oneline.Escape(l) // the decisions follow, as commands
	}
	if len(g.Decisions) > 0 {
		l += "  -> " + strings.Join(g.Decisions, " | ")
	}
	if len(g.Notes) > 0 && g.Kind == sprint.Judgment {
		l += "  [answers: " + g.Notes[0]
		if len(g.Notes) > 1 {
			l += " +" + strconv.Itoa(len(g.Notes)-1)
		}
		l += "]"
	}
	return oneline.Escape(l)
}

// cardView is everything about one primary.
type cardView struct {
	Primary *sprint.Card   `json:"primary"`
	Work    []*sprint.Card `json:"work_cards"`
	Reads   []*sprint.Card `json:"read_cards"`
	Merge   *sprint.Card   `json:"merge,omitempty"`
	Open    []sprint.Open  `json:"open,omitempty"`
}

func (a *app) cmdCard(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("card")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, "card", fmt.Sprint("wants one primary id ", err))
	}
	id := pos[0]
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "card", err.Error())
	}
	ctx := context.Background()
	v, err := st.CardOf(ctx, id)
	if err != nil {
		return a.readFailed("card", err, stderr)
	}
	if v.Primary == nil {
		fmt.Fprintf(stderr, "%s card: no primary %s; run: nova-sprint where\n", prog, oneline.Escape(id))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(cardView{Primary: v.Primary, Work: v.Work, Reads: v.Reads, Merge: v.Merge, Open: v.Open})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	printCard(stdout, "PRIMARY", v.Primary)
	for _, x := range v.Work {
		printCard(stdout, "WORK", x)
	}
	for _, x := range v.Reads {
		printCard(stdout, "READ", x)
	}
	if v.Merge != nil {
		printCard(stdout, "MERGE", v.Merge)
	}
	for _, o := range v.Open {
		fmt.Fprintf(stdout, "OPEN %s %s -> %s\n", oneline.Escape(o.Note.ID), oneline.Escape(o.Note.Type), oneline.Escape(strings.Join(o.Note.Decisions, " | ")))
	}
	fmt.Fprintf(stdout, "CARD OK id=%s work_cards=%d read_cards=%d open=%d\n", oneline.Escape(id), len(v.Work), len(v.Reads), len(v.Open))
	return 0
}

func printCard(w io.Writer, kind string, c *sprint.Card) {
	place := "-"
	if c.Placed() {
		place = c.Row + ":" + c.Col
	}
	keys := make([]string, 0, len(c.Fields))
	for k := range c.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var fields []string
	for _, k := range keys {
		fields = append(fields, k+"="+oneline.Field(c.Fields[k]))
	}
	fmt.Fprintf(w, "%s %s place=%s score=%s rev=%d %s\n", kind, oneline.Escape(c.ID), oneline.Escape(place),
		strconv.FormatFloat(c.Score, 'g', -1, 64), c.Rev, strings.Join(fields, " "))
}

func (a *app) cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("check")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "check", fmt.Sprint("takes no words ", err))
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "check", err.Error())
	}
	rep, _, err := st.Check(context.Background(), 5)
	if err != nil {
		return a.readFailed("check", err, stderr)
	}
	code := 0
	if len(rep.Violations) > 0 {
		code = 1
	}
	if c.json {
		if rep.Violations == nil {
			rep.Violations = []sprint.Violation{}
		}
		b, _ := json.Marshal(rep)
		fmt.Fprintln(stdout, string(b))
		return code
	}
	var lines []string
	for _, v := range rep.Violations {
		lines = append(lines, v.String())
	}
	listed(stderr, "VIOLATION", lines, c.max, "check")
	status := "OK"
	if code != 0 {
		status = "FAIL"
	}
	out := stdout
	if code != 0 {
		out = stderr
	}
	inFlight := ""
	if rep.InFlight {
		inFlight = " in_flight=yes"
	}
	fmt.Fprintf(out, "CHECK %s violations=%d pending=%s%s\n", status, len(rep.Violations), dashed(rep.Pending), inFlight)
	return code
}
