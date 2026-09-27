package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The row verbs: row add, set, hide, show, del here; move, order, sort in order.go.

func cmdRow(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "row", "wants add, set, hide, show, move, order, sort or del: row add <table> <row> ..., row set <table> <row> <col>=<value> ..., row hide|show <table> <row> ..., row move <table> <row> "+placeUsage+", row order <table> <row> ..., row sort <table> [--by name|label|<col>] [--desc] [--keep], row del <table> <row>")
	}
	switch args[0] {
	case "add":
		return cmdRowAdd(args[1:], stdout, stderr)
	case "del":
		return cmdRowDel(args[1:], stdout, stderr)
	case "set":
		return cmdRowSet(args[1:], stdout, stderr)
	case "hide":
		return cmdRowsHide(args[1:], stdout, stderr, true)
	case "show":
		return cmdRowsHide(args[1:], stdout, stderr, false)
	case "move":
		return cmdRowMove(args[1:], stdout, stderr)
	case "order":
		return cmdRowOrder(args[1:], stdout, stderr)
	case "sort":
		return cmdRowSort(args[1:], stdout, stderr)
	}
	return refuse(stderr, "row", "unknown subverb "+args[0]+"; wants add, set, hide, show, move, order, sort or del")
}

func cmdRowAdd(args []string, stdout, stderr io.Writer) int {
	const verb = "row add"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	write, receipt := writeFlags(fs)
	label := fs.String("label", "", "the row's label, the row header cell (default the row key)")
	exclude := fs.String("exclude", "", "one member the row's counts and members leave out")
	owner := fs.String("owner", "", "the verb that writes the row's bound sets, named by the refusal of a write here")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 2 {
		return refuse(stderr, verb, "wants a table and a row: row add <table> <row> [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]")
	}
	spec := ntable.RowSpec{Label: *label, Exclude: *exclude, Owner: *owner}
	// batch: row add <table> <r1> <r2> ... (no bindings, no label): one call
	if len(pos) > 2 {
		batch := true
		for _, p := range pos[1:] {
			if strings.Contains(p, "=") {
				batch = false
			}
		}
		if batch {
			ctx := context.Background()
			st, c, code := client(ctx, verb, *addr, stderr)
			if code != 0 {
				return code
			}
			defer st.Close()
			trips := st.CountTrips()
			n, err := ntable.RowsAddWithSpec(ctx, c, pos[0], pos[1:], spec, *write)
			if err != nil {
				return storeRefusal(stderr, verb, err)
			}
			fmt.Fprintf(stdout, "TABLE ROWS ADD table=%s rows=%d trips=%d\n", pos[0], n, trips.N())
			printReceipt(stdout, write, *receipt)
			return 0
		}
	}
	for _, bind := range pos[2:] {
		col, key, ok := strings.Cut(bind, "=")
		if !ok || col == "" || key == "" {
			return refuse(stderr, verb, "a binding wants <col>=<key>, the set another tool owns, got "+bind)
		}
		if spec.Binds == nil {
			spec.Binds = map[string]string{}
		}
		spec.Binds[col] = key
	}
	if len(spec.Binds) > 0 && spec.Owner == "" {
		return refuse(stderr, verb, "a row that binds a set wants --owner <verb>, the verb that writes it, so a write here can name it")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	row, err := ntable.RowAdd(ctx, c, pos[0], pos[1], spec, *write)
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	bound := 0
	for _, cell := range row.Cells {
		if cell.Bound {
			bound++
		}
	}
	fmt.Fprintf(stdout, "TABLE ROW ADD table=%s row=%s cols=%d bound=%d trips=%d\n", pos[0], field(pos[1]), len(row.Cells), bound, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func cmdRowDel(args []string, stdout, stderr io.Writer) int {
	const verb = "row del"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	write, receipt := writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, verb, "wants a table and a row: row del <table> <row>")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	existed, err := ntable.RowDel(ctx, c, pos[0], pos[1], *write)
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	was := 0
	if existed {
		was = 1
	}
	fmt.Fprintf(stdout, "TABLE ROW DEL table=%s row=%s existed=%d trips=%d\n", pos[0], field(pos[1]), was, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

// cmdRowSet: row set <table> <row> <col>=<value> ...: a text column's value
// for one row, several columns in one call.
func cmdRowSet(args []string, stdout, stderr io.Writer) int {
	const verb = "row set"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	write, receipt := writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 3 {
		return refuse(stderr, verb, "wants a table, a row and at least one <col>=<value>: row set <table> <row> <col>=<value> ...")
	}
	texts := map[string]string{}
	for _, kv := range pos[2:] {
		col, v, ok := strings.Cut(kv, "=")
		if !ok || col == "" {
			return refuse(stderr, verb, "wants <col>=<value>, not "+kv)
		}
		texts[col] = v
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	n, err := ntable.RowSet(ctx, c, pos[0], pos[1], texts, *write)
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE ROW SET table=%s row=%s cols=%d trips=%d\n", pos[0], pos[1], n, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

// cmdRowsHide: row hide|show <table> <row> ...: rows hidden from the render
// but kept (and counted in the folds), or shown again; one call.
func cmdRowsHide(args []string, stdout, stderr io.Writer, hide bool) int {
	verb := "row show"
	if hide {
		verb = "row hide"
	}
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	write, receipt := writeFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 2 {
		return refuse(stderr, verb, "wants a table and at least one row: "+verb+" <table> <row> ...")
	}
	ctx := context.Background()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	n, err := ntable.RowsHide(ctx, c, pos[0], hide, pos[1:], *write)
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	word := "shown"
	if hide {
		word = "hidden"
	}
	fmt.Fprintf(stdout, "TABLE ROWS %s table=%s rows=%d trips=%d\n", strings.ToUpper(word), pos[0], n, trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}
