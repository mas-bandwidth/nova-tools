package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

// runTable invokes the binary's own entry point.
func runTable(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// throwaway is an in-process store for the verbs; every command of a test
// names it with --redis, never the environment (t.Setenv would serialise
// the package).
func throwaway(t *testing.T) string {
	t.Helper()
	return miniredis.RunT(t).Addr()
}

// at appends --redis <addr> to a command line.
func at(addr string, args ...string) []string { return append(args, "--redis", addr) }

func TestBareCommandNamesTheDoor(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := runTable()
	if code != 2 || stdout != "" || stderr != "nova-table: no verb; create, row, cell, clear, show, render or watch a table; run: nova-table help\n" {
		t.Fatalf("bare: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, _, stderr = runTable("bogus")
	if code != 2 || !strings.HasPrefix(stderr, "nova-table: unknown verb bogus;") || !strings.HasSuffix(stderr, "; run: nova-table help\n") {
		t.Fatalf("unknown verb: exit %d stderr %q", code, stderr)
	}
	code, stdout, _ = runTable("help")
	if code != 0 || !strings.HasPrefix(stdout, "nova-table: ") || !strings.Contains(stdout, "\nexample:\n") {
		t.Fatalf("help: exit %d\n%s", code, stdout)
	}
	code, stdout, _ = runTable("version")
	if code != 0 || !strings.HasPrefix(stdout, "nova-table ") {
		t.Fatalf("version: exit %d %q", code, stdout)
	}
}

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
		{[]string{"create", "demo", "--columns", "a:rows", "--redis", addr}, `column a wants a projection of count, members, first, last or text, not "rows"`},
		{[]string{"create", "demo", "--columns", "a", "--width", "b=3", "--redis", addr}, "--width names column b, which --columns does not declare"},
		{[]string{"create", "bad name", "--columns", "a", "--redis", addr}, "the table name wants letters, digits, _ . and -"},
		{[]string{"row", "--redis", addr}, "wants add or del"},
		{[]string{"row", "add", "demo", "--redis", addr}, "wants a table and a row: row add <table> <row>"},
		{[]string{"row", "add", "demo", "r", "ready", "--redis", addr}, "a binding wants <col>=<key>"},
		{[]string{"row", "add", "demo", "r", "ready=ws:s:ready", "--redis", addr}, "a row that binds a set wants --owner <verb>"},
		{[]string{"cell", "--redis", addr}, "wants add <table> <row> <col> <member>"},
		{[]string{"cell", "add", "demo", "r", "c", "--redis", addr}, "wants a table, a row, a column and a member"},
		{[]string{"cell", "add", "demo", "r", "c", "m", "--score", "x", "--redis", addr}, `--score wants a number, got "x"`},
		{[]string{"cell", "move", "demo", "r", "c", "--redis", addr}, "wants a table, a row, the column left, the column joined and a member"},
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
	if code != 1 || stdout != "" || stderr != "nova-table render: table nope: no such table; run: nova-table help\n" {
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
		if code, stdout, stderr := runTable(args...); code != 0 || stdout != "TABLE CREATE table=demo columns=2\n" {
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
		{[]string{"create", "jobs", "--columns", "job:text:none,ready,working,who:members:union", "--footer", "all", "--width", "job=6"}, "TABLE CREATE table=jobs columns=4\n"},
		{[]string{"create", "jobs", "--columns", "job:text:none,ready,working,who:members:union", "--footer", "all", "--width", "job=6"}, "TABLE CREATE table=jobs columns=4\n"},
		{[]string{"row", "add", "jobs", "build"}, "TABLE ROW ADD table=jobs row=build cols=4 bound=0\n"},
		{[]string{"row", "add", "jobs", "the tests", "--label", "tests"}, "TABLE ROW ADD table=jobs row=\"the tests\" cols=4 bound=0\n"},
		{[]string{"cell", "add", "jobs", "build", "ready", "b1", "--score", "1"}, "TABLE CELL table=jobs row=build col=ready n=1\n"},
		{[]string{"cell", "add", "jobs", "build", "ready", "b2", "--score", "2"}, "TABLE CELL table=jobs row=build col=ready n=2\n"},
		{[]string{"cell", "add", "jobs", "build", "who", "ann", "--score", "1"}, "TABLE CELL table=jobs row=build col=who n=1\n"},
		{[]string{"cell", "add", "jobs", "the tests", "who", "bo", "--score", "1"}, "TABLE CELL table=jobs row=\"the tests\" col=who n=1\n"},
		{[]string{"cell", "remove", "jobs", "build", "ready", "b2"}, "TABLE CELL table=jobs row=build col=ready n=1\n"},
		{[]string{"cell", "members", "jobs", "build", "ready"}, "TABLE CELL table=jobs row=build col=ready n=1\nTABLE MEMBER table=jobs row=build col=ready member=b1 score=1\n"},
		{[]string{"show", "jobs"}, "TABLE table=jobs columns=4 rows=2\nTABLE ROW table=jobs row=build ready=1 working=0 who=1\nTABLE ROW table=jobs row=\"the tests\" ready=0 working=0 who=1\n"},
		{[]string{"render", "jobs"}, "job    | ready | working | who\n" +
			"-------+-------+---------+-------\n" +
			"build  |     1 |       0 | ann\n" +
			"tests  |     0 |       0 | bo\n" +
			"-------+-------+---------+-------\n" +
			"all    |     1 |       0 | ann,bo\n"},
		{[]string{"render", "jobs", "--hide-zero-rows", "--width", "job=8"}, "job      | ready | working | who\n" +
			"---------+-------+---------+-------\n" +
			"build    |     1 |       0 | ann\n" +
			"---------+-------+---------+-------\n" +
			"all      |     1 |       0 | ann,bo\n"}, // the fold is the column's, hidden rows included
		{[]string{"list"}, "TABLE LIST tables=1\nTABLE table=jobs columns=4 rows=2\n"},
		{[]string{"row", "del", "jobs", "the tests"}, "TABLE ROW DEL table=jobs row=\"the tests\" existed=1\n"},
		{[]string{"row", "del", "jobs", "the tests"}, "TABLE ROW DEL table=jobs row=\"the tests\" existed=0\n"},
		{[]string{"drop", "jobs"}, "TABLE DROP table=jobs rows=1\n"},
		{[]string{"list"}, "TABLE LIST tables=0\n"},
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
	if code != 1 || stderr != "nova-table create: table jobs: exists with another definition; run: nova-table help\n" {
		t.Fatalf("create with another definition: exit %d stderr %q", code, stderr)
	}
}

// TestABoundCellIsAViewTheWritesRefuse: a row bound to a set another tool
// owns reads and renders freely (render, show, cell members), and cell
// add, cell remove and cell move refuse it naming the owner verb, exit 1,
// writing nothing.
func TestABoundCellIsAViewTheWritesRefuse(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	addr := mr.Addr()
	if _, err := mr.ZAdd("ws:s:ready", 1, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := mr.ZAdd("ws:s:ready", 2, "s:sentinel"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"create", "views", "--columns", "stream:text:none,ready,working"},
		{"row", "add", "views", "s", "ready=ws:s:ready", "working=ws:s:working", "--owner", "nova-sprint task move", "--exclude", "s:sentinel"},
	} {
		if code, _, stderr := runTable(at(addr, args...)...); code != 0 {
			t.Fatalf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
	code, stdout, stderr := runTable(at(addr, "render", "views")...)
	want := "stream | ready | working\n" +
		"-------+-------+--------\n" +
		"s      |     1 |       0\n" +
		"-------+-------+--------\n" +
		"total  |     1 |       0\n"
	if code != 0 || stdout != want {
		t.Fatalf("render of a bound row: exit %d stderr %q\n%s", code, stderr, stdout)
	}
	if code, stdout, _ := runTable(at(addr, "cell", "members", "views", "s", "ready")...); code != 0 || stdout != "TABLE CELL table=views row=s col=ready n=1\nTABLE MEMBER table=views row=s col=ready member=c1 score=1\n" {
		t.Fatalf("cell members of a bound cell: exit %d\n%s", code, stdout)
	}
	for _, args := range [][]string{
		{"cell", "add", "views", "s", "ready", "c2"},
		{"cell", "remove", "views", "s", "ready", "c1"},
		{"cell", "move", "views", "s", "ready", "working", "c1"},
	} {
		code, stdout, stderr := runTable(at(addr, args...)...)
		verb := strings.Join(args[:2], " ")
		if code != 1 || stdout != "" || stderr != "nova-table "+verb+": views.s.ready is bound to ws:s:ready, owned elsewhere; run: nova-sprint task move; run: nova-table help\n" {
			t.Fatalf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	if n, err := mr.ZScore("ws:s:ready", "c1"); err != nil || n != 1 {
		t.Fatalf("the bound set was written: %v %v", n, err)
	}
	if mr.Exists("ws:s:working") {
		t.Fatal("a refused move created the bound working set")
	}
}
