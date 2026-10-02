package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The table verbs: create, drop, list, clear, show, render.

// refuseColumns reports a column list that ParseColumns would not accept. A list
// past the column bound is the store's kind of no, as `col add` past it is: exit
// 1, the bound and the count, and how to get under it. Any other fault is usage.
func refuseColumns(stderr io.Writer, verb string, err error) int {
	var limit *ntable.LimitError
	if errors.As(err, &limit) {
		return refused(stderr, verb, "--columns: "+limit.Error()+"; "+limit.Advice())
	}
	return refuse(stderr, verb, "--columns: "+err.Error())
}

func (app *application) cmdCreate(args []string, stdout, stderr io.Writer) int {
	const verb = "create"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	epochKey := fs.String("epoch-key", "", "hash key naming the epoch domain (empty means epoch 0)")
	epochField := fs.String("epoch-field", "n", "field in the epoch hash")
	memberPrefix := fs.String("member-prefix", "", "member record prefix (default table::member:)")
	columns := fs.String("columns", "", "the columns, name[:projection[:fold[:label]]] each, comma-separated; pct defaults to the pooled fold, sum to the sum fold")
	footer := fs.String("footer", ntable.DefaultFooter, "the footer row's label (none by default)")
	widths := fs.String("width", "", "fixed column widths, col=n,...")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: create <table> --columns <name[:projection[:fold[:label]]],...>")
	}
	if *columns == "" {
		return refuse(stderr, verb, "--columns wants the columns, name[:projection[:fold[:label]]] each, comma-separated; pct defaults to the pooled fold, sum to the sum fold")
	}
	cols, err := ntable.ParseColumns(*columns)
	if err != nil {
		return refuseColumns(stderr, verb, err)
	}
	if *widths != "" {
		w, err := ntable.ParseWidths(*widths)
		if err != nil {
			return refuse(stderr, verb, "--width: "+err.Error())
		}
		for name, n := range w {
			i := -1
			for j, c := range cols {
				if c.Name == name {
					i = j
				}
			}
			if i < 0 {
				return refuse(stderr, verb, "--width names column "+name+", which --columns does not declare")
			}
			cols[i].Width = n
		}
	}
	t := ntable.Table{EpochKey: *epochKey, EpochField: *epochField, MemberPrefix: *memberPrefix, Name: pos[0], Columns: cols, FooterLabel: *footer}
	if !ntable.ValidName(t.Name) {
		return refuse(stderr, verb, "the table name wants letters, digits, _ . and -, got "+strconv.Quote(t.Name))
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	if err := ntable.Create(ctx, c, t, time.Now(), *write); err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CREATE table=%s columns=%d trips=%d\n", t.Name, len(t.Columns), trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func (app *application) cmdSet(args []string, stdout, stderr io.Writer) int {
	const verb = "set"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	footer := fs.String("footer", "\x00", "the footer row's label ('' for none)")
	rename := fs.String("rename", "", "the table's new name")
	columns := fs.String("columns", "", "the columns, replaced in place (the create grammar); rows kept")
	hide := fs.String("hide", "", "columns to hide (kept, read, used by formulas; not drawn), comma-separated")
	show := fs.String("show", "", "hidden columns to draw again, comma-separated")
	hiddenTable := fs.Bool("hidden", false, "the whole table kept and read, not drawn by watch")
	visibleTable := fs.Bool("visible", false, "the whole table drawn again by watch")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: set <table> [--footer <label>] [--rename <name>] [--columns <spec>]")
	}
	o := ntable.SetOpts{Rename: *rename}
	if *columns != "" {
		if o.Columns, err = ntable.ParseColumns(*columns); err != nil {
			return refuseColumns(stderr, verb, err)
		}
	}
	if *footer != "\x00" {
		f := *footer
		o.Footer = &f
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	if *hide != "" {
		o.Hide = strings.Split(*hide, ",")
	}
	if *show != "" {
		o.Show = strings.Split(*show, ",")
	}
	if *hiddenTable || *visibleTable {
		v := *visibleTable && !*hiddenTable
		o.Visible = &v
	}
	n, err := ntable.Set(ctx, c, pos[0], o, *write)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	line := "TABLE SET table=" + pos[0]
	if o.Visible != nil {
		line += fmt.Sprintf(" visible=%v", *o.Visible)
	}
	if o.Footer != nil {
		line += fmt.Sprintf(" footer=%q", *o.Footer)
	}
	if o.Rename != "" {
		line += fmt.Sprintf(" renamed=%s moved=%d", o.Rename, n)
	}
	if len(o.Columns) > 0 {
		line += fmt.Sprintf(" columns=%d", len(o.Columns))
	}
	if len(o.Hide) > 0 {
		line += fmt.Sprintf(" hide=%q", strings.Join(o.Hide, ","))
	}
	if len(o.Show) > 0 {
		line += fmt.Sprintf(" show=%q", strings.Join(o.Show, ","))
	}
	if o.Hidden != nil {
		line += fmt.Sprintf(" hidden=%q", strings.Join(*o.Hidden, ","))
	}
	fmt.Fprintf(stdout, "%s trips=%d\n", line, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func (app *application) cmdDrop(args []string, stdout, stderr io.Writer) int {
	const verb = "drop"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	definition := fs.Bool("definition", false, "also remove the saved column definition, the identity hash and the rows of every epoch; keep the definition snapshots of earlier epochs")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: drop <table>")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	drop := ntable.Drop
	if *definition {
		drop = ntable.DropDefinition
	}
	n, err := drop(ctx, c, pos[0], *write)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE DROP table=%s rows=%d trips=%d\n", pos[0], n, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func (app *application) cmdList(args []string, stdout, stderr io.Writer) int {
	const verb = "list"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 0 {
		return refuse(stderr, verb, "takes no table name: list")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	summaries, err := ntable.Summaries(ctx, c)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE LIST tables=%d trips=%d\n", len(summaries), trips.N())
	for _, row := range summaries {
		fmt.Fprintf(stdout, "TABLE table=%s columns=%d rows=%d\n", row.Name, row.Columns, row.Rows)
	}
	return 0
}

func (app *application) cmdClear(args []string, stdout, stderr io.Writer) int {
	const verb = "clear"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: clear <table>")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	start := time.Now()
	n, err := ntable.Clear(ctx, c, pos[0], *write)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CLEAR table=%s rows=%d ms=%d trips=%d\n", pos[0], n, time.Since(start).Milliseconds(), trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func (app *application) cmdShow(args []string, stdout, stderr io.Writer) int {
	const verb = "show"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	atEpoch := fs.String("at-epoch", "", "inspect a materialised epoch instead of the active one")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: show <table>")
	}
	var epoch uint64
	if *atEpoch != "" {
		epoch, err = strconv.ParseUint(*atEpoch, 10, 64)
		if err != nil {
			return refuse(stderr, verb, "--at-epoch wants an unsigned integer")
		}
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	var t ntable.Table
	if *atEpoch == "" {
		t, err = ntable.Read(ctx, c, pos[0])
	} else {
		t, err = ntable.ReadAt(ctx, c, pos[0], epoch)
	}
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE table=%s columns=%d rows=%d trips=%d epoch=%d revision=%d", t.Name, len(t.Columns), len(t.Rows), trips.N(), t.Epoch, t.Revision)
	if t.Sort != "" {
		fmt.Fprintf(stdout, " sort=%s", t.Sort)
	}
	fmt.Fprintln(stdout)
	for _, r := range t.Rows {
		var b strings.Builder
		fmt.Fprintf(&b, "TABLE ROW table=%s row=%s", t.Name, field(r.Key))
		for j, col := range t.Columns {
			b.WriteString(" " + col.Name + "=" + field(ntable.CellText(t.Columns, r, j)))
		}
		fmt.Fprintln(stdout, b.String())
	}
	// the table's properties, one line each, in name order (L1 contract
	// amendment, table properties, section 4)
	for _, name := range slices.Sorted(maps.Keys(t.Props)) {
		fmt.Fprintf(stdout, "TABLE PROP table=%s %s=%s\n", t.Name, name, field(t.Props[name]))
	}
	// A cell that did not come back prints as ? above, never as a false 0; show is
	// the record of the table, so it also says which cell and why, and exits 1.
	unread := 0
	for _, r := range t.Rows {
		for j, col := range t.Columns {
			if j < len(r.Cells) && r.Cells[j].Unread {
				unread++
				fmt.Fprintf(stderr, "nova-table show: warning: table %q row %q column %q cannot be read: %s; run: nova-table set -h, and write the cell again\n", t.Name, oneline.Escape(r.Key), col.Name, oneline.Escape(r.Cells[j].UnreadWhy))
			}
		}
	}
	if unread > 0 {
		fmt.Fprintf(stderr, "nova-table show: %d cell(s) printed as ? could not be read; run: nova-table check '%s'\n", unread, strings.ReplaceAll(t.Name, "'", `'\''`))
		return 1
	}
	return 0
}

// renderFlags declares the render flags render and watch share.
type renderFlags struct {
	widths     *string
	labelWidth *int
}

func declareRenderFlags(fs interface {
	Bool(name string, value bool, usage string) *bool
	Int(name string, value int, usage string) *int
	String(name, value, usage string) *string
}) renderFlags {
	return renderFlags{
		widths:     fs.String("width", "", "fixed column widths for this render, col=n,..."),
		labelWidth: fs.Int("label-width", 0, "fixed width of the row-label column for this render (0: as wide as the labels)"),
	}
}

func (f renderFlags) opts() (ntable.RenderOpts, error) {
	opts := ntable.RenderOpts{LabelWidth: *f.labelWidth}
	if *f.labelWidth < 0 {
		return opts, fmt.Errorf("--label-width: %d is negative", *f.labelWidth)
	}
	if *f.widths != "" {
		w, err := ntable.ParseWidths(*f.widths)
		if err != nil {
			return opts, fmt.Errorf("--width: %w", err)
		}
		opts.Widths = w
	}
	return opts, nil
}

func (app *application) cmdRender(args []string, stdout, stderr io.Writer) int {
	const verb = "render"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	view := fs.String("view", "", "render a stored view once, including its title and summary")
	atEpoch := fs.String("at-epoch", "", "read a saved epoch snapshot (table targets only)")
	rf := declareRenderFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if (*view != "" && len(pos) != 0) || (*view == "" && len(pos) != 1) {
		return refuse(stderr, verb, "wants one table name or --view <name>: render <table> | --view <name> [--width col=n,...]")
	}
	if *view != "" && *atEpoch != "" {
		return refuse(stderr, verb, "--at-epoch applies to a table; stored views read the active epochs")
	}
	opts, err := rf.opts()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	var epoch uint64
	if *atEpoch != "" {
		epoch, err = strconv.ParseUint(*atEpoch, 10, 64)
		if err != nil {
			return refuse(stderr, verb, "--at-epoch wants an unsigned integer")
		}
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	if *view != "" {
		text, err := viewReader(c, *view, opts, false)(ctx)
		if err != nil {
			return st.refusal(stderr, verb, err)
		}
		return publish("", text, stdout, stderr, verb)
	}
	var t ntable.Table
	if *atEpoch == "" {
		t, err = ntable.Read(ctx, c, pos[0])
	} else {
		t, err = ntable.ReadAt(ctx, c, pos[0], epoch)
	}
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	// the table and nothing else: an empty table prints its header and footer
	opts.Title = t.Name
	if _, err := io.WriteString(stdout, ntable.Render(t, opts)); err != nil {
		return refuse(stderr, verb, "stdout: "+err.Error())
	}
	return 0
}

// cmdView manages presentation configuration independently of table receipts.
func (app *application) cmdView(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "view", "wants set, state, show, list or del")
	}
	sub := args[0]
	if sub != "set" && sub != "state" && sub != "show" && sub != "list" && sub != "del" {
		return refuse(stderr, "view", "unknown subverb "+sub+"; wants set, state, show, list or del")
	}
	verb := "view " + sub
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	var tables, title, summary string
	var clearState bool
	if sub == "state" {
		fs.BoolVar(&clearState, "clear", false, "clear the state: the summary line shows the counts again")
	}
	if sub == "set" {
		fs.StringVar(&tables, "tables", "", "the tables, comma-separated, in order")
		fs.StringVar(&title, "title", "", "the view's title line")
		fs.StringVar(&summary, "summary", "", "a count column in the first table to count as done (x/y z% -> ETA)")
	}
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if sub == "list" && len(pos) != 0 {
		return refuse(stderr, verb, "takes no view name")
	}
	switch {
	case sub == "state" && clearState && len(pos) != 1:
		return refuse(stderr, verb, "wants one view name with --clear: view state <name> (<text> | --clear)")
	case sub == "state" && !clearState && len(pos) != 2:
		return refuse(stderr, verb, "wants a view name and its state text: view state <name> (<text> | --clear)")
	case sub == "state" && !clearState && (pos[1] == "" || !ntable.ValidViewState(pos[1])):
		return refuse(stderr, verb, fmt.Sprintf("a state is one line of at most %d bytes; --clear removes it", ntable.MaxViewState))
	case sub != "list" && sub != "state" && len(pos) != 1:
		return refuse(stderr, verb, "wants one view name")
	}
	var list []string
	if sub == "set" {
		for _, n := range strings.Split(tables, ",") {
			if n = strings.TrimSpace(n); n != "" {
				list = append(list, n)
			}
		}
		if len(list) == 0 {
			return refuse(stderr, verb, "wants --tables <a,b,...>")
		}
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	switch sub {
	case "set":
		if err := ntable.ViewSet(ctx, c, ntable.View{Name: pos[0], Tables: list, Title: title, Summary: summary}); err != nil {
			return st.refusal(stderr, verb, err)
		}
		fmt.Fprintf(stdout, "VIEW SET view=%s tables=%s title=%q summary=%s trips=%d\n", pos[0], strings.Join(list, ","), title, field(summary), trips.N())
	case "state":
		text := ""
		if !clearState {
			text = pos[1]
		}
		if err := ntable.ViewState(ctx, c, pos[0], text); err != nil {
			return st.refusal(stderr, verb, err)
		}
		fmt.Fprintf(stdout, "VIEW STATE view=%s state=%q trips=%d\n", pos[0], text, trips.N())
	case "show":
		v, err := ntable.ViewGet(ctx, c, pos[0])
		if err != nil {
			return st.refusal(stderr, verb, err)
		}
		fmt.Fprintf(stdout, "VIEW view=%s tables=%s title=%q summary=%s state=%q trips=%d\n", v.Name, strings.Join(v.Tables, ","), v.Title, field(v.Summary), v.State, trips.N())
	case "list":
		names, err := ntable.ViewList(ctx, c)
		if err != nil {
			return st.refusal(stderr, verb, err)
		}
		fmt.Fprintf(stdout, "VIEW LIST views=%d trips=%d\n", len(names), trips.N())
		for _, name := range names {
			fmt.Fprintf(stdout, "VIEW view=%s\n", name)
		}
	case "del":
		n, err := ntable.ViewDelete(ctx, c, pos[0])
		if err != nil {
			return st.refusal(stderr, verb, err)
		}
		fmt.Fprintf(stdout, "VIEW DEL view=%s existed=%d trips=%d\n", pos[0], n, trips.N())
	}
	return 0
}
