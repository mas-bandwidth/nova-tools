package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
)

func (app *application) cmdMember(args []string, stdout, stderr io.Writer) int {
	const verb = "member create"
	if len(args) == 0 || args[0] != "create" {
		return refuse(stderr, "member", "wants create <table> <id>")
	}
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	write, receipt := app.writeFlags(fs)
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, verb, "wants a table and a new member ID: member create <table> <id>")
	}
	ctx := context.Background()
	call := func(c redis.Cmdable) error { return ntable.MemberCreate(ctx, c, pos[0], pos[1], *write) }
	if code, done := app.preflight(stdout, stderr, fs, verb, *addr, pos, call); done {
		return code
	}
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	if err := call(c); err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE MEMBER CREATE table=%s member=%s trips=%d\n", pos[0], field(pos[1]), trips.N())
	printReceipt(stdout, write, *receipt)
	return 0
}

func (app *application) cmdCheck(args []string, stdout, stderr io.Writer) int {
	const verb = "check"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 {
		return refuse(stderr, verb, "wants one table name: check <table>")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	report, err := ntable.Check(ctx, c, pos[0])
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE CHECK table=%s epoch=%d revision=%d members=%d cells=%d trips=%d\n", pos[0], report.Epoch, report.Revision, report.Members, report.Cells, trips.N())
	return 0
}

func (app *application) cmdMemberFind(args []string, stdout, stderr io.Writer) int {
	const verb = "member find"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 2 {
		return refuse(stderr, verb, "wants a table and a member ID: member find <table> <id>")
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	loc, err := ntable.MemberFind(ctx, c, pos[0], pos[1])
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	fmt.Fprintf(stdout, "TABLE MEMBER table=%s member=%s state=%s", pos[0], field(pos[1]), loc.State)
	if loc.State == "placed" {
		fmt.Fprintf(stdout, " row=%s col=%s", field(loc.Row), loc.Column)
	}
	fmt.Fprintf(stdout, " epoch=%d table_revision=%d trips=%d\n", loc.Epoch, loc.Revision, trips.N())
	return 0
}

// cellList is a repeatable --cell flag.
type cellList []string

func (l *cellList) String() string     { return strings.Join(*l, ",") }
func (l *cellList) Set(v string) error { *l = append(*l, v); return nil }

// splitCell reads a place, row:column, as the tool prints it: the column is
// what follows the last colon.
func splitCell(s string) (row, col string, ok bool) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// cmdMemberRead reads members in one exchange: their place, score, revision
// and fields, and the members that are missing, with the table's revision and
// epoch. It is the read set the batch's guards are prepared from.
func (app *application) cmdMemberRead(args []string, stdout, stderr io.Writer) int {
	const verb = "member read"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	var cells cellList
	fs.Var(&cells, "cell", "read every member of the cell `row:col` (repeatable); the ids are then not given")
	atEpoch := fs.String("at-epoch", "", "read a materialised epoch instead of the active one")
	asJSON := fs.Bool("json", false, "print the reading (or the refusal) as one JSON object instead of the lines")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) < 1 || (len(pos) == 1 && len(cells) == 0) || (len(pos) > 1 && len(cells) > 0) {
		return refuse(stderr, verb, "wants a table and member IDs, or a table and --cell <row:col>: member read <table> <id>... | member read <table> --cell <row:col>")
	}
	scope := ntable.ReadSetScope{}
	if len(cells) > 0 {
		for _, c := range cells {
			row, col, ok := splitCell(c)
			if !ok {
				return refuse(stderr, verb, fmt.Sprintf("--cell wants <row>:<col>, not %q", c))
			}
			scope.Selection = append(scope.Selection, ntable.CellSelection{Row: row, Col: col})
		}
	} else {
		scope.Members = pos[1:]
	}
	var epoch []uint64
	if *atEpoch != "" {
		n, err := strconv.ParseUint(*atEpoch, 10, 64)
		if err != nil {
			return refuse(stderr, verb, "--at-epoch wants an unsigned integer")
		}
		epoch = append(epoch, n)
	}
	ctx := context.Background()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	trips := st.CountTrips()
	res, err := ntable.ReadSet(ctx, c, pos[0], scope, epoch...)
	if err != nil {
		return st.refusal(stderr, verb, err)
	}
	if *asJSON {
		return printJSON(stdout, stderr, verb, readJSON(res, trips.N()))
	}
	fmt.Fprintf(stdout, "TABLE READ table=%s epoch=%d table_revision=%d members=%d missing=%d trips=%d\n",
		pos[0], res.Epoch, res.Revision, len(res.Members), len(res.Missing), trips.N())
	for _, m := range res.Members {
		place, score := "-", "-"
		if m.Placed {
			place, score = m.Row+":"+m.Col, m.ScoreText
		}
		fields, _ := json.Marshal(m.Fields)
		fmt.Fprintf(stdout, "MEMBER %s place=%s score=%s member_revision=%d fields=%s\n", field(m.ID), field(place), score, m.Revision, fields)
	}
	for _, id := range res.Missing {
		fmt.Fprintf(stdout, "MISSING %s\n", field(id))
	}
	return 0
}

// printJSON writes v as one line of JSON.
func printJSON(stdout, stderr io.Writer, verb string, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return refuse(stderr, verb, "cannot render the reading as JSON: "+err.Error())
	}
	fmt.Fprintf(stdout, "%s\n", b)
	return 0
}

func readJSON(res ntable.ReadSetResult, trips int64) map[string]any {
	members := make([]map[string]any, 0, len(res.Members))
	for _, m := range res.Members {
		var place, score any
		if m.Placed {
			place, score = m.Row+":"+m.Col, m.ScoreText
		}
		fields := m.Fields
		if fields == nil {
			fields = map[string]string{}
		}
		members = append(members, map[string]any{
			"id": m.ID, "place": place, "score": score,
			"member_revision": strconv.FormatUint(m.Revision, 10), "fields": fields,
		})
	}
	missing := res.Missing
	if missing == nil {
		missing = []string{}
	}
	return map[string]any{
		"table": res.Table, "epoch": strconv.FormatUint(res.Epoch, 10), "table_revision": strconv.FormatUint(res.Revision, 10),
		"members": members, "missing": missing, "trips": trips,
	}
}
