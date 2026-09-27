//go:build functional

package main

import (
	"context"
	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
	"strings"
	"testing"
)

func throwaway(t *testing.T) string { t.Helper(); return firstRunStore(t) }

// TestARefusalSaysWhatTheInputWants: every usage refusal names the shape
// it wants and the door, in one line, exit 2; a store's no is exit 1.
func TestARefusalSaysWhatTheInputWants(t *testing.T) {
	t.Parallel()

	addr := throwaway(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"create", "--redis", addr}, "wants one table name: create <table> --columns"},
		{[]string{"create", "demo", "--redis", addr}, "--columns wants the columns, name[:projection[:fold[:label]]] each"},
		{[]string{"create", "demo", "--columns", "a:rows", "--redis", addr}, `column a wants a projection of count, members, first, last, text or pct(<count column>), not "rows"`},
		{[]string{"create", "demo", "--columns", "a", "--width", "b=3", "--redis", addr}, "--width names column b, which --columns does not declare"},
		{[]string{"create", "bad name", "--columns", "a", "--redis", addr}, "the table name wants letters, digits, _ . and -"},
		{[]string{"row", "--redis", addr}, "wants add, set, hide, show, del"},
		{[]string{"row", "add", "demo", "--redis", addr}, "wants a table and a row: row add <table> <row>"},
		{[]string{"row", "add", "demo", "r", "ready=", "--redis", addr}, "a binding wants <col>=<key>"},
		{[]string{"row", "add", "demo", "r", "ready=ws:s:ready", "--redis", addr}, "a row that binds a set wants --owner <verb>"},
		{[]string{"cell", "--redis", addr}, "wants add, remove, move, members"},
		{[]string{"cell", "add", "demo", "r", "c", "--redis", addr}, "wants a table, a row, a column and one or more members"},
		{[]string{"cell", "add", "demo", "r", "c", "m", "--score", "x", "--redis", addr}, `--score wants a number, got "x"`},
		{[]string{"cell", "move", "demo", "r", "c", "--redis", addr}, "wants a table, a row, the column left, the column joined and one or more members"},
		{[]string{"render", "--redis", addr}, "wants one table name: render <table>"},
		{[]string{"render", "demo", "--width", "a=x", "--redis", addr}, "--width: width \"a=x\" wants col=n"},
		{[]string{"watch", "--redis", addr}, "wants the tables to watch, comma-separated"},
		{[]string{"watch", "demo", "--every", "0s", "--redis", addr}, "--every wants a duration between 1ms and 1h"},
		{[]string{"list", "demo", "--redis", addr}, "takes no table name: list"},
		{[]string{"show", "--bogus", "--redis", addr}, "flag provided but not defined: -bogus"},
	} {
		code, stdout, stderr := runTable(c.args...)
		if code != 2 || stdout != "" {
			t.Errorf("%v: exit %d stdout %q, want 2 and nothing", c.args, code, stdout)
		}
		if !strings.Contains(stderr, c.want) || !strings.HasSuffix(stderr, "; run: nova-table help\n") || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%v: stderr %q, want one line holding %q and the door", c.args, stderr, c.want)
		}
	}
	code, _, stderr := runTable("create", "demo", "--columns", "a")
	if code != 2 || !strings.Contains(stderr, "--redis <addr> is required") {
		t.Fatalf("no address: exit %d stderr %q", code, stderr)
	}
	code, stdout, stderr := runTable(at(addr, "render", "nope")...)
	if code != 1 || stdout != "" || !strings.Contains(stderr, `table "nope": no such table; run: nova-table create`) {
		t.Fatalf("render of no table: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

// TestFlagsMayFollowTheWords: parseInterleaved reads the flags wherever
// they stand on the line.
func TestFlagsMayFollowTheWords(t *testing.T) {
	t.Parallel()

	addr := throwaway(t)
	for _, args := range [][]string{
		{"create", "demo", "--columns", "a,b", "--redis", addr},
		{"create", "--columns", "a,b", "demo", "--redis", addr},
		{"create", "--redis", addr, "demo", "--columns", "a,b"},
	} {
		if code, stdout, stderr := runTable(args...); code != 0 || stdout != "TABLE CREATE table=demo columns=2 trips=1\n" {
			t.Fatalf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

// TestASittingThroughTheVerbs: the plain-command verbs end to end on an
// in-process store, and the typed line each prints; a bound cell is a view
// the cell verbs refuse, naming its owner, exit 1, and nothing is written.
func TestASittingThroughTheVerbs(t *testing.T) {
	t.Parallel()

	addr := throwaway(t)
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"create", "jobs", "--columns", "ready,working,who:members:union", "--footer", "all", "--width", "who=6"}, "TABLE CREATE table=jobs columns=3 trips=1\n"},
		{[]string{"create", "jobs", "--columns", "ready,working,who:members:union", "--footer", "all", "--width", "who=6"}, "TABLE CREATE table=jobs columns=3 trips=1\n"},
		{[]string{"row", "add", "jobs", "build"}, "TABLE ROW ADD table=jobs row=build cols=3 bound=0 trips=1\n"},
		{[]string{"row", "add", "jobs", "the tests", "--label", "tests"}, "TABLE ROW ADD table=jobs row=\"the tests\" cols=3 bound=0 trips=1\n"},
		{[]string{"cell", "add", "jobs", "build", "ready", "b1", "--score", "1"}, "TABLE CELL table=jobs row=build col=ready n=1 trips=1\n"},
		{[]string{"cell", "add", "jobs", "build", "ready", "b2", "--score", "2"}, "TABLE CELL table=jobs row=build col=ready n=2 trips=1\n"},
		{[]string{"cell", "add", "jobs", "build", "who", "ann", "--score", "1"}, "TABLE CELL table=jobs row=build col=who n=1 trips=1\n"},
		{[]string{"cell", "add", "jobs", "the tests", "who", "bo", "--score", "1"}, "TABLE CELL table=jobs row=\"the tests\" col=who n=1 trips=1\n"},
		{[]string{"cell", "remove", "jobs", "build", "ready", "b2"}, "TABLE CELL table=jobs row=build col=ready n=1 trips=1\n"},
		{[]string{"cell", "members", "jobs", "build", "ready"}, "TABLE CELL table=jobs row=build col=ready n=1 trips=1\nTABLE MEMBER table=jobs row=build col=ready member=b1 score=1\n"},
		{[]string{"show", "jobs"}, "TABLE table=jobs columns=3 rows=2 trips=1 epoch=0 revision=9\nTABLE ROW table=jobs row=build ready=1 working=0 who=ann\nTABLE ROW table=jobs row=\"the tests\" ready=0 working=0 who=bo\n"},
		{[]string{"render", "jobs"}, "jobs  | ready | working | who\n" +
			"------+-------+---------+-------\n" +
			"build |     1 |       0 | ann\n" +
			"tests |     0 |       0 | bo\n" +
			"------+-------+---------+-------\n" +
			"all   |     1 |       0 | ann,bo\n"},
		{[]string{"render", "jobs", "--hide-zero-rows", "--label-width", "8"}, "jobs     | ready | working | who\n" +
			"---------+-------+---------+-------\n" +
			"build    |     1 |       0 | ann\n" +
			"---------+-------+---------+-------\n" +
			"all      |     1 |       0 | ann,bo\n"}, // the fold is the column's, hidden rows included
		{[]string{"list"}, "TABLE LIST tables=1 trips=1\nTABLE table=jobs columns=3 rows=2\n"},
		// a definition changed in place (set: footer, columns, rename; row set: a text cell), rows kept
		{[]string{"set", "jobs", "--footer", "sum"}, "TABLE SET table=jobs footer=\"sum\" trips=1\n"},
		{[]string{"set", "jobs", "--columns", "ready,working,who:members:union,pct:pct(ready):pooled:ready%,note:text:none"}, "TABLE SET table=jobs columns=5 trips=1\n"},
		{[]string{"row", "set", "jobs", "build", "note=green"}, "TABLE ROW SET table=jobs row=build cols=1 trips=1\n"},
		{[]string{"render", "jobs", "--hide-zero-rows"}, "jobs  | ready | working | who    | ready% | note\n" +
			"------+-------+---------+--------+--------+------\n" +
			"build |     1 |       0 | ann    | 100.0% | green\n" +
			"------+-------+---------+--------+--------+------\n" +
			"sum   |     1 |       0 | ann,bo | 100.0% |\n"},
		{[]string{"set", "jobs", "--rename", "work"}, "TABLE SET table=jobs renamed=work moved=11 trips=1\n"},
		{[]string{"list"}, "TABLE LIST tables=1 trips=1\nTABLE table=work columns=5 rows=2\n"},
		{[]string{"set", "work", "--rename", "jobs"}, "TABLE SET table=work renamed=jobs moved=11 trips=1\n"},
		{[]string{"row", "del", "jobs", "the tests"}, "TABLE ROW DEL table=jobs row=\"the tests\" existed=1 trips=1\n"},
		{[]string{"row", "del", "jobs", "the tests"}, "TABLE ROW DEL table=jobs row=\"the tests\" existed=0 trips=1\n"},
		{[]string{"drop", "jobs", "--definition"}, "TABLE DROP table=jobs rows=1 trips=1\n"},
		{[]string{"list"}, "TABLE LIST tables=0 trips=1\n"},
	}
	for _, s := range steps {
		code, stdout, stderr := runTable(at(addr, s.args...)...)
		if code != 0 || stderr != "" || stdout != s.want {
			t.Fatalf("%v: exit %d stderr %q\nstdout:\n%s\nwant:\n%s", s.args, code, stderr, stdout, s.want)
		}
	}
	// a second create with another definition is the store's no
	if code, _, stderr := runTable(at(addr, "create", "jobs", "--columns", "a")...); code != 0 {
		t.Fatalf("create after drop: exit %d stderr %q", code, stderr)
	}
	code, _, stderr := runTable(at(addr, "create", "jobs", "--columns", "a,b")...)
	if code != 1 || !strings.Contains(stderr, `table "jobs": exists with another definition; run: nova-table set`) {
		t.Fatalf("create with another definition: exit %d stderr %q", code, stderr)
	}
}

// TestABoundCellIsAViewTheWritesRefuse: a row bound to a set another tool
// owns reads and renders freely (render, show, cell members), and cell
// add, cell remove and cell move refuse it naming the owner verb, exit 1,
// writing nothing.
func TestABoundCellIsAViewTheWritesRefuse(t *testing.T) {
	t.Parallel()

	addr := firstRunStore(t)
	mr := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = mr.Close() })
	ctx := context.Background()
	if _, err := mr.ZAdd(ctx, "ws:s:ready", redis.Z{Score: 1, Member: "c1"}).Result(); err != nil {
		t.Fatal(err)
	}
	if _, err := mr.ZAdd(ctx, "ws:s:ready", redis.Z{Score: 2, Member: "s:sentinel"}).Result(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"create", "views", "--columns", "ready,working"},
		{"row", "add", "views", "s", "ready=ws:s:ready", "working=ws:s:working", "--owner", "nova-sprint task move", "--exclude", "s:sentinel"},
	} {
		if code, _, stderr := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
	code, stdout, stderr := runTable(at(addr, "render", "views")...)
	want := "views | ready | working\n" +
		"------+-------+--------\n" +
		"s     |     1 |       0\n" +
		"------+-------+--------\n" +
		"      |     1 |       0\n"
	if code != 0 || stdout != want {
		t.Fatalf("render of a bound row: exit %d stderr %q\n%s", code, stderr, stdout)
	}
	if code, stdout, _ := runTable(at(addr, "cell", "members", "views", "s", "ready")...); code != 0 || stdout != "TABLE CELL table=views row=s col=ready n=1 trips=1\nTABLE MEMBER table=views row=s col=ready member=c1 score=1\n" {
		t.Fatalf("cell members of a bound cell: exit %d\n%s", code, stdout)
	}
	for _, args := range [][]string{
		{"cell", "add", "views", "s", "ready", "c2"},
		{"cell", "remove", "views", "s", "ready", "c1"},
		{"cell", "move", "views", "s", "ready", "working", "c1"},
	} {
		code, stdout, stderr := runTable(at(addr, args...)...)
		verb := strings.Join(args[:2], " ")
		if code != 1 || stdout != "" || (!strings.HasPrefix(stderr, "nova-table "+verb+": ") || !strings.Contains(stderr, "views.s.ready is bound to ws:s:ready, owned elsewhere; run: nova-sprint task move")) {
			t.Fatalf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	if n, err := mr.ZScore(ctx, "ws:s:ready", "c1").Result(); err != nil || n != 1 {
		t.Fatalf("the bound set was written: %v %v", n, err)
	}
	if mr.Exists(ctx, "ws:s:working").Val() != 0 {
		t.Fatal("a refused move created the bound working set")
	}
}

func TestBoundRefusalPrintsTheStoredKey(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, args := range [][]string{
		{"create", "machines", "--columns", "ready,working"},
		{"row", "add", "machines", "batman"},
	} {
		if code, _, stderr := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("setup %v: %d %q", args, code, stderr)
		}
	}
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	for _, key := range []string{"bench:batman:cards:ready", "external:" + string(allBytes)} {
		if err := c.HSet(context.Background(), "table:machines:row:batman", "key:ready", key).Err(); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"cell", "add", "machines", "batman", "ready", "m"},
			{"cell", "remove", "machines", "batman", "ready", "m"},
			{"cell", "move", "machines", "batman", "ready", "working", "m"},
			{"clear", "machines"},
		} {
			code, stdout, stderr := runTable(at(addr, args...)...)
			want := "machines.batman.ready is bound to " + oneline.Escape(key) + ", owned elsewhere"
			if code != 1 || stdout != "" || !strings.Contains(stderr, want) || strings.Count(stderr, "\n") != 1 {
				t.Fatalf("%v key=%q: exit=%d stdout=%q stderr=%q", args, key, code, stdout, stderr)
			}
		}
	}
}

func TestStoredViewReadsChangesWithoutRestartOrSummaryReread(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	for _, args := range [][]string{{"create", "live", "--columns", "a,b"}, {"row", "add", "live", "r", "s"}, {"cell", "add", "live", "r", "a", "m1", "m2"}, {"view", "set", "v", "--tables", "live", "--summary", "a", "--title", "before"}} {
		if code, _, stderr := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("%v: %s", args, stderr)
		}
	}
	read := viewReader(c, "v", ntable.RenderOpts{})
	trips := nsstore.New(c).CountTrips()
	// Establish the connection before counting application exchanges.
	if err := c.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	n := trips.N()
	first, err := read(ctx)
	if err != nil || trips.N()-n != 2 || !strings.Contains(first, "before\n\n2/2 100.0% -> ETA") {
		t.Fatalf("first view trips=%d err=%v\n%s", trips.N()-n, err, first)
	}
	if code, _, stderr := runTable(at(addr, "view", "set", "v", "--tables", "live", "--summary", "a", "--title", "after")...); code != 0 {
		t.Fatal(stderr)
	}
	if code, _, stderr := runTable(at(addr, "cell", "move", "live", "r", "a", "b", "m1", "m2")...); code != 0 {
		t.Fatal(stderr)
	}
	n = trips.N()
	second, err := read(ctx)
	if err != nil || trips.N()-n != 2 || !strings.Contains(second, "after\n\n0/2 0.0% -> ETA") {
		t.Fatalf("changed view trips=%d err=%v\n%s", trips.N()-n, err, second)
	}
}
