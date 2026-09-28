package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The order verbs (Glenn 2026-09-27: "take column y and put it after column
// z", "friends on top, machines on bottom"): col add, col del, col move, row
// move, row order, row sort. Each is one call to the staged set kernel, so
// each is atomic and leaves one receipt (tla/TableOrder.tla).

const placeUsage = "--first | --last | --before <name> | --after <name>"

// placeFlags declares the four ways to name a place.
func placeFlags(fs interface {
	String(name, value, usage string) *string
	Bool(name string, value bool, usage string) *bool
}, what string) func() (*ntable.Place, error) {
	first := fs.Bool("first", false, "put it first")
	last := fs.Bool("last", false, "put it last")
	before := fs.String("before", "", "put it just before this "+what)
	after := fs.String("after", "", "put it just after this "+what)
	return func() (*ntable.Place, error) {
		var got []ntable.Place
		if *first {
			got = append(got, ntable.Place{Where: "first"})
		}
		if *last {
			got = append(got, ntable.Place{Where: "last"})
		}
		if *before != "" {
			got = append(got, ntable.Place{Where: "before", Ref: *before})
		}
		if *after != "" {
			got = append(got, ntable.Place{Where: "after", Ref: *after})
		}
		switch len(got) {
		case 0:
			return nil, nil
		case 1:
			return &got[0], nil
		}
		return nil, fmt.Errorf("wants one place, not %d: %s", len(got), placeUsage)
	}
}

// setVerb runs one set change and prints the verb's line.
func (app *application) setVerb(verb, table, addr string, o ntable.SetOpts, write *ntable.WriteOptions, receipt bool, line string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	if _, err := ntable.Set(ctx, c, table, o, *write); err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "%s trips=%d\n", line, trips.N())
	printReceipt(stdout, write, receipt)
	return 0
}

const colUsage = "wants add, del or move: col add <table> <column spec> [" + placeUsage + "], col del <table> <col>, col move <table> <col> " + placeUsage

func (app *application) cmdCol(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "col", colUsage)
	}
	switch args[0] {
	case "add":
		return app.cmdColAdd(args[1:], stdout, stderr)
	case "del":
		return app.cmdColDel(args[1:], stdout, stderr)
	case "move":
		return app.cmdColMove(args[1:], stdout, stderr)
	}
	return refuse(stderr, "col", "unknown subverb "+args[0]+"; "+colUsage)
}

func (app *application) cmdColAdd(args []string, stdout, stderr io.Writer) int {
	const verb = "col add"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	place := placeFlags(fs, "column")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, verb, "wants a table and one column: col add <table> <name[:projection[:fold[:label]]]> ["+placeUsage+"]")
	}
	col, err := ntable.ParseColumn(pos[1])
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	at, err := place()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	return app.setVerb(verb, pos[0], *addr, ntable.SetOpts{ColAdd: &col, ColAt: at}, write, *receipt,
		fmt.Sprintf("TABLE COL ADD table=%s col=%s%s", pos[0], col.Name, placeWord(at)), stdout, stderr)
}

func (app *application) cmdColDel(args []string, stdout, stderr io.Writer) int {
	const verb = "col del"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, verb, "wants a table and a column: col del <table> <col>")
	}
	return app.setVerb(verb, pos[0], *addr, ntable.SetOpts{ColDel: pos[1]}, write, *receipt,
		fmt.Sprintf("TABLE COL DEL table=%s col=%s", pos[0], pos[1]), stdout, stderr)
}

func (app *application) cmdColMove(args []string, stdout, stderr io.Writer) int {
	const verb = "col move"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	place := placeFlags(fs, "column")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	at, err := place()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 || at == nil {
		return refuse(stderr, verb, "wants a table, a column and a place: col move <table> <col> "+placeUsage)
	}
	return app.setVerb(verb, pos[0], *addr, ntable.SetOpts{ColMove: &ntable.Reorder{Item: pos[1], Place: *at}}, write, *receipt,
		fmt.Sprintf("TABLE COL MOVE table=%s col=%s%s", pos[0], pos[1], placeWord(at)), stdout, stderr)
}

func placeWord(p *ntable.Place) string {
	if p == nil {
		return " place=last"
	}
	if p.Ref == "" {
		return " place=" + p.Where
	}
	return " place=" + p.Where + " of=" + field(p.Ref)
}

func (app *application) cmdRowMove(args []string, stdout, stderr io.Writer) int {
	const verb = "row move"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	place := placeFlags(fs, "row")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	at, err := place()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 || at == nil {
		return refuse(stderr, verb, "wants a table, a row and a place: row move <table> <row> "+placeUsage)
	}
	return app.setVerb(verb, pos[0], *addr, ntable.SetOpts{RowMove: &ntable.Reorder{Item: pos[1], Place: *at}}, write, *receipt,
		fmt.Sprintf("TABLE ROW MOVE table=%s row=%s%s", pos[0], field(pos[1]), placeWord(at)), stdout, stderr)
}

func (app *application) cmdRowOrder(args []string, stdout, stderr io.Writer) int {
	const verb = "row order"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 2 {
		return refuse(stderr, verb, "wants a table and the rows that go first, in order; the rest keep their order: row order <table> <row> <row> ...")
	}
	return app.setVerb(verb, pos[0], *addr, ntable.SetOpts{RowOrder: pos[1:]}, write, *receipt,
		fmt.Sprintf("TABLE ROW ORDER table=%s first=%s", pos[0], field(strings.Join(pos[1:], ","))), stdout, stderr)
}

func (app *application) cmdRowSort(args []string, stdout, stderr io.Writer) int {
	const verb = "row sort"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	by := fs.String("by", "name", "name (the row key), label, a count column or a text column")
	desc := fs.Bool("desc", false, "largest or last first")
	keep := fs.Bool("keep", false, "a standing sort (by name or label): rows added later take their place")
	manual := fs.Bool("manual", false, "end a standing sort; the rows stay where they are and are placed by hand again")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table: row sort <table> [--by name|label|<col>] [--desc] [--keep] | --manual")
	}
	if *manual && (*keep || *desc) {
		return refuse(stderr, verb, "--manual ends a standing sort and takes no --keep or --desc")
	}
	o := ntable.SetOpts{RowSort: &ntable.Sort{By: *by, Desc: *desc, Keep: *keep, Manual: *manual}}
	line := fmt.Sprintf("TABLE ROW SORT table=%s by=%s desc=%v keep=%v", pos[0], field(*by), *desc, *keep)
	if *manual {
		line = fmt.Sprintf("TABLE ROW SORT table=%s manual=true", pos[0])
	}
	return app.setVerb(verb, pos[0], *addr, o, write, *receipt, line, stdout, stderr)
}
