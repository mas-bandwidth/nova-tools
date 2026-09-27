package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// The table verbs: create, drop, list, clear, show, render.

func cmdCreate(args []string, stdout, stderr io.Writer) int {
	const verb = "create"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	columns := fs.String("columns", "", "the columns, name[:projection[:fold[:label]]] each, comma-separated")
	footer := fs.String("footer", ntable.DefaultFooter, "the footer row's label")
	widths := fs.String("width", "", "fixed column widths, col=n,...")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: create <table> --columns <name[:projection[:fold[:label]]],...>")
	}
	if *columns == "" {
		return refuse(stderr, verb, "--columns wants the columns, name[:projection[:fold[:label]]] each, comma-separated")
	}
	cols, err := ntable.ParseColumns(*columns)
	if err != nil {
		return refuse(stderr, verb, "--columns: "+err.Error())
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
	t := ntable.Table{Name: pos[0], Columns: cols, FooterLabel: *footer}
	if !ntable.ValidName(t.Name) {
		return refuse(stderr, verb, "the table name wants letters, digits, _ . and -, got "+strconv.Quote(t.Name))
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	if err := ntable.Create(ctx, c, t, time.Now()); err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CREATE table=%s columns=%d\n", t.Name, len(t.Columns))
	return 0
}

func cmdDrop(args []string, stdout, stderr io.Writer) int {
	const verb = "drop"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: drop <table>")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	n, err := ntable.Drop(ctx, c, pos[0])
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE DROP table=%s rows=%d\n", pos[0], n)
	return 0
}

func cmdList(args []string, stdout, stderr io.Writer) int {
	const verb = "list"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 0 {
		return refuse(stderr, verb, "takes no table name: list")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	names, err := ntable.List(ctx, c)
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	// one pipeline: each table's column order and row count
	pipe := c.Pipeline()
	orders := make([]*redis.StringCmd, len(names))
	rows := make([]*redis.IntCmd, len(names))
	for i, name := range names {
		orders[i] = pipe.HGet(ctx, ntable.DefKey(name), "order")
		rows[i] = pipe.ZCard(ctx, ntable.RowsKey(name))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE LIST tables=%d\n", len(names))
	for i, name := range names {
		cols := 0
		if order, err := orders[i].Result(); err == nil && order != "" {
			cols = len(strings.Split(order, ","))
		}
		fmt.Fprintf(stdout, "TABLE table=%s columns=%d rows=%d\n", name, cols, rows[i].Val())
	}
	return 0
}

func cmdClear(args []string, stdout, stderr io.Writer) int {
	const verb = "clear"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: clear <table>")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	start := time.Now()
	n, err := ntable.Clear(ctx, c, pos[0])
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CLEAR table=%s rows=%d ms=%d\n", pos[0], n, time.Since(start).Milliseconds())
	return 0
}

func cmdShow(args []string, stdout, stderr io.Writer) int {
	const verb = "show"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: show <table>")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	t, err := ntable.Read(ctx, c, pos[0])
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE table=%s columns=%d rows=%d\n", t.Name, len(t.Columns), len(t.Rows))
	for _, r := range t.Rows {
		var b strings.Builder
		fmt.Fprintf(&b, "TABLE ROW table=%s row=%s", t.Name, field(r.Key))
		for j, col := range t.Columns {
			if col.Projection == ntable.Text {
				continue
			}
			v := strconv.FormatInt(r.Cells[j].Count, 10)
			if r.Cells[j].Unread {
				v = "?"
			}
			b.WriteString(" " + col.Name + "=" + v)
		}
		fmt.Fprintln(stdout, b.String())
	}
	return 0
}

// renderFlags declares the render flags render and watch share.
type renderFlags struct {
	hideZero *bool
	widths   *string
}

func declareRenderFlags(fs interface {
	Bool(name string, value bool, usage string) *bool
	String(name, value, usage string) *string
}) renderFlags {
	return renderFlags{
		hideZero: fs.Bool("hide-zero-rows", false, "hide a row whose count cells are all zero"),
		widths:   fs.String("width", "", "fixed column widths for this render, col=n,..."),
	}
}

func (f renderFlags) opts() (ntable.RenderOpts, error) {
	opts := ntable.RenderOpts{HideZeroRows: *f.hideZero}
	if *f.widths != "" {
		w, err := ntable.ParseWidths(*f.widths)
		if err != nil {
			return opts, fmt.Errorf("--width: %w", err)
		}
		opts.Widths = w
	}
	return opts, nil
}

func cmdRender(args []string, stdout, stderr io.Writer) int {
	const verb = "render"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	rf := declareRenderFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: render <table> [--hide-zero-rows] [--width col=n,...]")
	}
	opts, err := rf.opts()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	t, err := ntable.Read(ctx, c, pos[0])
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	// the table and nothing else: an empty table prints nothing at all
	if _, err := io.WriteString(stdout, ntable.Render(t, opts)); err != nil {
		return refuse(stderr, verb, "stdout: "+err.Error())
	}
	return 0
}
