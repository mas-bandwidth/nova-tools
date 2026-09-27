package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

// The row verbs: row add, row del.

func cmdRow(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "row", "wants add or del: row add <table> <row> [<col>=<key> ...], row del <table> <row>")
	}
	switch args[0] {
	case "add":
		return cmdRowAdd(args[1:], stdout, stderr)
	case "del":
		return cmdRowDel(args[1:], stdout, stderr)
	}
	return refuse(stderr, "row", "unknown subverb "+args[0]+"; wants add or del")
}

func cmdRowAdd(args []string, stdout, stderr io.Writer) int {
	const verb = "row add"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
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
	row, err := ntable.RowAdd(ctx, c, pos[0], pos[1], spec)
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	bound := 0
	for _, cell := range row.Cells {
		if cell.Bound {
			bound++
		}
	}
	fmt.Fprintf(stdout, "TABLE ROW ADD table=%s row=%s cols=%d bound=%d\n", pos[0], field(pos[1]), len(row.Cells), bound)
	return 0
}

func cmdRowDel(args []string, stdout, stderr io.Writer) int {
	const verb = "row del"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
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
	existed, err := ntable.RowDel(ctx, c, pos[0], pos[1])
	if err != nil {
		return storeRefusal(stderr, verb, err)
	}
	was := 0
	if existed {
		was = 1
	}
	fmt.Fprintf(stdout, "TABLE ROW DEL table=%s row=%s existed=%d\n", pos[0], field(pos[1]), was)
	return 0
}
