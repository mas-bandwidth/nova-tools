package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The cell verbs: cell add, cell remove, cell move, cell members. A cell
// bound to a set another tool owns is a view: add, remove and move refuse
// it naming the owner; members reads it freely.

const cellWants = "wants add <table> <row> <col> <member>... [--score <n>], remove <table> <row> <col> <member>, move <table> <row> <from-col> <to-col> <member>, or members <table> <row> <col>"

func (app *application) cmdCell(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "cell", cellWants)
	}
	switch args[0] {
	case "add":
		return app.cmdCellAdd(args[1:], stdout, stderr)
	case "remove":
		return app.cmdCellRemove(args[1:], stdout, stderr)
	case "move":
		return app.cmdCellMove(args[1:], stdout, stderr)
	case "members":
		return app.cmdCellMembers(args[1:], stdout, stderr)
	}
	return refuse(stderr, "cell", "unknown subverb "+args[0]+"; "+cellWants)
}

func (app *application) cmdCellAdd(args []string, stdout, stderr io.Writer) int {
	const verb = "cell add"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	score := fs.String("score", "", "the member's score, its place in the set's order (default the unix time in ms)")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 4 {
		return refuse(stderr, verb, "wants a table, a row, a column and one or more members: cell add <table> <row> <col> <member>... [--score <n>]")
	}
	sc, err := scoreOf(*score)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	n, err := ntable.CellsAdd(ctx, c, pos[0], pos[1], pos[2], sc, pos[3:], *write)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CELL table=%s row=%s col=%s n=%d trips=%d\n", pos[0], field(pos[1]), pos[2], n, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

// scoreOf reads --score, the unix time in milliseconds when empty (the
// sprint's sets score a card by its created_at ms, so a set reads oldest
// first).
func scoreOf(s string) (float64, error) {
	if s == "" {
		return float64(nowMillis()), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("--score wants a number, got %q", s)
	}
	return v, nil
}

func (app *application) cmdCellRemove(args []string, stdout, stderr io.Writer) int {
	const verb = "cell remove"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 4 {
		return refuse(stderr, verb, "wants a table, a row, a column and one or more members: cell remove <table> <row> <col> <member>...")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	n, err := ntable.CellsRemove(ctx, c, pos[0], pos[1], pos[2], pos[3:], *write)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CELL table=%s row=%s col=%s n=%d trips=%d\n", pos[0], field(pos[1]), pos[2], n, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func (app *application) cmdCellMove(args []string, stdout, stderr io.Writer) int {
	const verb = "cell move"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 5 {
		return refuse(stderr, verb, "wants a table, a row, the column left, the column joined and one or more members: cell move <table> <row> <from-col> <to-col> <member>...")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	n, err := ntable.CellsMove(ctx, c, pos[0], pos[1], pos[2], pos[3], pos[4:], *write)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	if len(pos) == 5 {
		fmt.Fprintf(stdout, "TABLE MOVE table=%s row=%s member=%s from=%s to=%s n=%d trips=%d\n", pos[0], field(pos[1]), field(pos[4]), pos[2], pos[3], n, trips.N())
	} else {
		fmt.Fprintf(stdout, "TABLE MOVE table=%s row=%s members=%d from=%s to=%s n=%d trips=%d\n", pos[0], field(pos[1]), len(pos)-4, pos[2], pos[3], n, trips.N())
	}
	printReceipt(stdout, write, *receipt)
	return 0
}

func (app *application) cmdCellMembers(args []string, stdout, stderr io.Writer) int {
	const verb = "cell members"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 3 {
		return refuse(stderr, verb, "wants a table, a row and a column: cell members <table> <row> <col>")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	ms, err := ntable.CellMembers(ctx, c, pos[0], pos[1], pos[2])
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CELL table=%s row=%s col=%s n=%d trips=%d\n", pos[0], field(pos[1]), pos[2], len(ms), trips.N())
	for _, m := range ms {
		fmt.Fprintf(stdout, "TABLE MEMBER table=%s row=%s col=%s member=%s score=%s\n", pos[0], field(pos[1]), pos[2], field(m.Member), strconv.FormatFloat(m.Score, 'f', -1, 64))
	}
	return 0
}
