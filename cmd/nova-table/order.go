package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
)

// The order verbs (the repository owner 2026-09-27: "take column y and put it after column
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

// runWrite is the one skeleton every write verb runs: the dry run's own plan
// (preflight), one store connection, the verb's one call and its receipt. A
// handler keeps only what makes it itself — its flags, its argument checks,
// its call and the line it prints — and the lines between are here once
// (docs/STANDARD.md section 7: duplicated function becomes one lower-level
// module the others depend on; a copy is a bug).
func (app *application) runWrite(stdout, stderr io.Writer, fs *flag.FlagSet, verb, addr string, pos []string, write *ntable.WriteOptions, receipt bool, call func(redis.Cmdable) error, print func(trips int64)) int {
	ctx := context.Background()
	if code, done := app.preflight(stdout, stderr, fs, verb, addr, pos, call); done {
		return code
	}
	st, c, code := app.client(ctx, verb, addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	if err := call(c); err != nil {
		return st.refusal(stderr, verb, err)
	}
	print(trips.N())
	printReceipt(stdout, write, receipt)
	return 0
}

// runRead is the read verbs' half of the same skeleton: one store connection,
// the call and its refusal, and the verb's own print, which returns the exit
// (a read that found an unreadable cell exits 1; most return 0).
func (app *application) runRead(stdout, stderr io.Writer, verb, addr string, call func(redis.Cmdable) error, print func(trips int64) int) int {
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	if err := call(c); err != nil {
		return st.refusal(stderr, verb, err)
	}
	return print(trips.N())
}

// setVerb runs one set change and prints the verb's line through runWrite.
func (app *application) setVerb(fs *flag.FlagSet, pos []string, verb, table, addr string, o ntable.SetOpts, write *ntable.WriteOptions, receipt bool, line string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	call := func(c redis.Cmdable) error { _, err := ntable.Set(ctx, c, table, o, *write); return err }
	return app.runWrite(stdout, stderr, fs, verb, addr, pos, write, receipt, call, func(trips int64) {
		fmt.Fprintf(stdout, "%s trips=%d\n", line, trips)
	})
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
	return app.setVerb(fs, pos, verb, pos[0], *addr, ntable.SetOpts{ColAdd: &col, ColAt: at}, write, *receipt,
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
	return app.setVerb(fs, pos, verb, pos[0], *addr, ntable.SetOpts{ColDel: pos[1]}, write, *receipt,
		fmt.Sprintf("TABLE COL DEL table=%s col=%s", pos[0], pos[1]), stdout, stderr)
}

// axisMove runs col move or row move: one item of an axis placed by
// --first/--last/--before/--after, through the one order set verb. The two
// verbs differ only in the axis they name and the line they print, so they
// are one handler (docs/STANDARD.md section 7: duplicated function becomes
// one lower-level module the others depend on).
func (app *application) axisMove(args []string, stdout, stderr io.Writer, column bool) int {
	verb, what, short := "row move", "row", "row"
	if column {
		verb, what, short = "col move", "column", "col"
	}
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	place := placeFlags(fs, what)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	at, err := place()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 || at == nil {
		return refuse(stderr, verb, "wants a table, a "+what+" and a place: "+verb+" <table> <"+short+"> "+placeUsage)
	}
	move := &ntable.Reorder{Item: pos[1], Place: *at}
	o := ntable.SetOpts{RowMove: move}
	line := fmt.Sprintf("TABLE ROW MOVE table=%s row=%s%s", pos[0], field(pos[1]), placeWord(at))
	if column {
		o = ntable.SetOpts{ColMove: move}
		line = fmt.Sprintf("TABLE COL MOVE table=%s col=%s%s", pos[0], pos[1], placeWord(at))
	}
	return app.setVerb(fs, pos, verb, pos[0], *addr, o, write, *receipt, line, stdout, stderr)
}

func (app *application) cmdColMove(args []string, stdout, stderr io.Writer) int {
	return app.axisMove(args, stdout, stderr, true)
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
	return app.axisMove(args, stdout, stderr, false)
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
	return app.setVerb(fs, pos, verb, pos[0], *addr, ntable.SetOpts{RowOrder: pos[1:]}, write, *receipt,
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
	return app.setVerb(fs, pos, verb, pos[0], *addr, o, write, *receipt, line, stdout, stderr)
}
