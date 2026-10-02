//go:build functional

package main

import (
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/redis/go-redis/v9"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{[]string{"create", "demo", "--columns", "a:rows", "--redis", addr}, `column a wants a projection of count, members, first, last, text, pct(<count column>), pct(<count column>/<a>+<b>) or sum(<a>+<b>), not "rows"`},
		{[]string{"create", "demo", "--columns", "a", "--width", "b=3", "--redis", addr}, "--width names column b, which --columns does not declare"},
		{[]string{"create", "bad name", "--columns", "a", "--redis", addr}, "the table name wants letters, digits, _ . and -"},
		{[]string{"row", "--redis", addr}, "wants add, set, hide, show, del, move, order, sort"},
		{[]string{"row", "add", "demo", "--redis", addr}, "wants a table and a row: row add <table> <row>"},
		{[]string{"row", "add", "demo", "r", "ready=", "--redis", addr}, "a binding wants <col>=<key>"},
		{[]string{"row", "add", "demo", "r", "ready=ws:s:ready", "--redis", addr}, "a row that binds a set wants --owner <verb>"},
		{[]string{"row", "move", "demo", "r", "--redis", addr}, "wants a table, a row and a place: row move <table> <row> --first | --last | --before <name> | --after <name>"},
		{[]string{"row", "move", "demo", "r", "--first", "--after", "s", "--redis", addr}, "wants one place, not 2"},
		{[]string{"row", "order", "demo", "--redis", addr}, "wants a table and the rows that go first, in order"},
		{[]string{"row", "sort", "--redis", addr}, "wants one table: row sort <table>"},
		{[]string{"row", "sort", "demo", "--manual", "--keep", "--redis", addr}, "--manual ends a standing sort and takes no --keep or --desc"},
		{[]string{"col", "--redis", addr}, "wants add, del, move"},
		{[]string{"col", "add", "demo", "--redis", addr}, "wants a table and one column: col add <table>"},
		{[]string{"col", "add", "demo", "a:rows", "--redis", addr}, `column a wants a projection of count`},
		{[]string{"col", "del", "demo", "--redis", addr}, "wants a table and a column: col del <table> <col>"},
		{[]string{"col", "move", "demo", "a", "--redis", addr}, "wants a table, a column and a place: col move <table> <col>"},
		{[]string{"cell", "--redis", addr}, "wants add, remove, move, members"},
		{[]string{"cell", "add", "demo", "r", "c", "--redis", addr}, "wants a table, a row, a column and one or more members"},
		{[]string{"cell", "add", "demo", "r", "c", "m", "--score", "x", "--redis", addr}, `--score wants a number, got "x"`},
		{[]string{"cell", "move", "demo", "r", "c", "--redis", addr}, "wants a table, a row, the column left, the column joined and one or more members"},
		{[]string{"render", "--redis", addr}, "wants one table name or --view <name>: render <table>"},
		{[]string{"render", "demo", "--width", "a=x", "--redis", addr}, "--width: width \"a=x\" wants col=n"},
		{[]string{"watch", "--redis", addr}, "wants the tables to watch, comma-separated"},
		{[]string{"watch", "demo", "--every", "0s", "--redis", addr}, "--every wants a duration between 1ms and 1h"},
		{[]string{"list", "demo", "--redis", addr}, "takes no table name: list"},
		{[]string{"show", "--bogus", "--redis", addr}, "unknown flag --bogus; show flags: --at-epoch, --redis"},
	} {
		code, stdout, stderr := runTable(c.args...)
		assert.EqualValues(t, 2, code, "%v: exit %d stdout %q, want 2 and nothing", c.args, code, stdout)
		assert.Empty(t, stdout, "%v: exit %d stdout %q, want 2 and nothing", c.args, code, stdout)
		assert.Contains(t, stderr, c.want, "%v: stderr %q, want one line holding %q and the door", c.args, stderr, c.want)
		assert.Contains(t, stderr, "; run: nova-table help", "%v: stderr %q, want one line holding %q and the door", c.args, stderr, c.want)
		assert.EqualValues(t, 1, strings.Count(stderr, "\n"), "%v: stderr %q, want one line holding %q and the door", c.args, stderr, c.want)
	}
	code, _, stderr := runTable("create", "demo", "--columns", "a")
	require.EqualValues(t, 2, code, "no address: exit %d stderr %q", code, stderr)
	require.Contains(t, stderr, "--redis <addr> is required", "no address: exit %d stderr %q", code, stderr)
	code, stdout, stderr := runTable(at(addr, "render", "nope")...)
	require.EqualValues(t, 1, code, "render of no table: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Empty(t, stdout, "render of no table: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Contains(t, stderr, `table "nope": no such table; run: nova-table create`, "render of no table: exit %d stdout %q stderr %q", code, stdout, stderr)
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
		{
			code, stdout, stderr := runTable(args...)
			require.EqualValues(t, 0, code, "%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
			require.Equal(t, "TABLE CREATE table=demo columns=2 trips=1\n", stdout, "%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
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
		{[]string{"render", "jobs", "--label-width", "8"}, "jobs     | ready | working | who\n" +
			"---------+-------+---------+-------\n" +
			"build    |     1 |       0 | ann\n" +
			"tests    |     0 |       0 | bo\n" +
			"---------+-------+---------+-------\n" +
			"all      |     1 |       0 | ann,bo\n"}, // an all-zero row shows
		{[]string{"list"}, "TABLE LIST tables=1 trips=1\nTABLE table=jobs columns=3 rows=2\n"},
		// a definition changed in place (set: footer, columns, rename; row set: a text cell), rows kept
		{[]string{"set", "jobs", "--footer", "sum"}, "TABLE SET table=jobs footer=\"sum\" trips=1\n"},
		{[]string{"set", "jobs", "--columns", "ready,working,who:members:union,pct:pct(ready):pooled:ready%,note:text:none"}, "TABLE SET table=jobs columns=5 trips=1\n"},
		{[]string{"row", "set", "jobs", "build", "note=green"}, "TABLE ROW SET table=jobs row=build cols=1 trips=1\n"},
		{[]string{"render", "jobs"}, "jobs  | ready | working | who    | ready% | note\n" +
			"------+-------+---------+--------+--------+------\n" +
			"build |     1 |       0 | ann    | 100.0% | green\n" +
			"tests |     0 |       0 | bo     | 0.0%   |\n" +
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
		require.EqualValues(t, 0, code, "%v: exit %d stderr %q\nstdout:\n%s\nwant:\n%s", s.args, code, stderr, stdout, s.want)
		require.Empty(t, stderr, "%v: exit %d stderr %q\nstdout:\n%s\nwant:\n%s", s.args, code, stderr, stdout, s.want)
		require.Equal(t, s.want, stdout, "%v: exit %d stderr %q\nstdout:\n%s\nwant:\n%s", s.args, code, stderr, stdout, s.want)
	}
	// a second create with another definition is the store's no
	{
		code, _, stderr := runTable(at(addr, "create", "jobs", "--columns", "a")...)
		require.EqualValues(t, 0, code, "create after drop: exit %d stderr %q", code, stderr)
	}
	code, _, stderr := runTable(at(addr, "create", "jobs", "--columns", "a,b")...)
	require.EqualValues(t, 1, code, "create with another definition: exit %d stderr %q", code, stderr)
	require.Contains(t, stderr, `table "jobs": exists with another definition; run: nova-table set`, "create with another definition: exit %d stderr %q", code, stderr)
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
	{
		_, err := mr.ZAdd(ctx, "ws:s:ready", redis.Z{Score: 1, Member: "c1"}).Result()
		require.NoError(t, err, "%v", err)
	}
	{
		_, err := mr.ZAdd(ctx, "ws:s:ready", redis.Z{Score: 2, Member: "s:sentinel"}).Result()
		require.NoError(t, err, "%v", err)
	}
	for _, args := range [][]string{
		{"create", "views", "--columns", "ready,working"},
		{"row", "add", "views", "s", "ready=ws:s:ready", "working=ws:s:working", "--owner", "nova-sprint task move", "--exclude", "s:sentinel"},
	} {
		{
			code, _, stderr := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "%v: exit %d stderr %q", args, code, stderr)
		}
	}
	code, stdout, stderr := runTable(at(addr, "render", "views")...)
	want := "views | ready | working\n" +
		"------+-------+--------\n" +
		"s     |     1 |       0\n" +
		"------+-------+--------\n" +
		"      |     1 |       0\n"
	require.EqualValues(t, 0, code, "render of a bound row: exit %d stderr %q\n%s", code, stderr, stdout)
	require.Equal(t, want, stdout, "render of a bound row: exit %d stderr %q\n%s", code, stderr, stdout)
	{
		code, stdout, _ := runTable(at(addr, "cell", "members", "views", "s", "ready")...)
		require.EqualValues(t, 0, code, "cell members of a bound cell: exit %d\n%s", code, stdout)
		require.Equal(t, "TABLE CELL table=views row=s col=ready n=1 trips=1\nTABLE MEMBER table=views row=s col=ready member=c1 score=1\n", stdout, "cell members of a bound cell: exit %d\n%s", code, stdout)
	}
	for _, args := range [][]string{
		{"cell", "add", "views", "s", "ready", "c2"},
		{"cell", "remove", "views", "s", "ready", "c1"},
		{"cell", "move", "views", "s", "ready", "working", "c1"},
	} {
		code, stdout, stderr := runTable(at(addr, args...)...)
		verb := strings.Join(args[:2], " ")
		require.EqualValues(t, 1, code, "%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		require.Empty(t, stdout, "%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		require.False(t, (!strings.HasPrefix(stderr, "nova-table "+verb+": ") || !strings.Contains(stderr, "views.s.ready is bound to ws:s:ready, owned elsewhere; run: nova-sprint task move")), "%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
	}
	{
		n, err := mr.ZScore(ctx, "ws:s:ready", "c1").Result()
		require.NoError(t, err, "the bound set was written: %v %v", n, err)
		require.EqualValues(t, 1, n, "the bound set was written: %v %v", n, err)
	}
	require.EqualValues(t, 0, mr.Exists(ctx, "ws:s:working").Val(), "%v", "a refused move created the bound working set")
}

func TestBoundRefusalPrintsTheStoredKey(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		assert.NoError(t, c.Close())
	})
	for _, args := range [][]string{
		{"create", "machines", "--columns", "ready,working"},
		{"row", "add", "machines", "batman"},
	} {
		{
			code, _, stderr := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "setup %v: %d %q", args, code, stderr)
		}
	}
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	for _, key := range []string{"bench:batman:cards:ready", "external:" + string(allBytes)} {
		require.NoError(t, c.HSet(context.Background(), "table:machines:row:batman", "key:ready", key).Err())
		for _, args := range [][]string{
			{"cell", "add", "machines", "batman", "ready", "m"},
			{"cell", "remove", "machines", "batman", "ready", "m"},
			{"cell", "move", "machines", "batman", "ready", "working", "m"},
			{"clear", "machines"},
		} {
			code, stdout, stderr := runTable(at(addr, args...)...)
			want := "machines.batman.ready is bound to " + oneline.Escape(key) + ", owned elsewhere"
			require.EqualValues(t, 1, code, "%v key=%q: exit=%d stdout=%q stderr=%q", args, key, code, stdout, stderr)
			require.Empty(t, stdout, "%v key=%q: exit=%d stdout=%q stderr=%q", args, key, code, stdout, stderr)
			require.Contains(t, stderr, want, "%v key=%q: exit=%d stdout=%q stderr=%q", args, key, code, stdout, stderr)
			require.EqualValues(t, 1, strings.Count(stderr, "\n"), "%v key=%q: exit=%d stdout=%q stderr=%q", args, key, code, stdout, stderr)
		}
	}
}

func TestStoredViewReadsChangesWithoutRestartOrSummaryReread(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		assert.NoError(t, c.Close())
	})
	ctx := context.Background()
	for _, args := range [][]string{{"create", "live", "--columns", "a,b"}, {"row", "add", "live", "r", "s"}, {"cell", "add", "live", "r", "a", "m1", "m2"}, {"view", "set", "v", "--tables", "live", "--summary", "a", "--title", "before"}} {
		{
			code, _, stderr := runTable(at(addr, args...)...)
			require.EqualValues(t, 0, code, "%v: %s", args, stderr)
		}
	}
	read := viewReader(c, "v", ntable.RenderOpts{}, false)
	trips := redisconn.CountTrips(c)
	// Establish the connection before counting application exchanges.
	require.NoError(t, c.Ping(ctx).Err())
	n := trips.N()
	first, err := read(ctx)
	require.NoError(t, err, "first view trips=%d err=%v\n%s", trips.N()-n, err, first)
	require.EqualValues(t, 2, trips.N()-n, "first view trips=%d err=%v\n%s", trips.N()-n, err, first)
	require.Contains(t, first, "before\n\n2/2 100.0% -> ETA", "first view trips=%d err=%v\n%s", trips.N()-n, err, first)
	{
		code, _, stderr := runTable(at(addr, "view", "set", "v", "--tables", "live", "--summary", "a", "--title", "after")...)
		require.EqualValues(t, 0, code, "%v", stderr)
	}
	{
		code, _, stderr := runTable(at(addr, "cell", "move", "live", "r", "a", "b", "m1", "m2")...)
		require.EqualValues(t, 0, code, "%v", stderr)
	}
	n = trips.N()
	second, err := read(ctx)
	require.NoError(t, err, "changed view trips=%d err=%v\n%s", trips.N()-n, err, second)
	require.EqualValues(t, 2, trips.N()-n, "changed view trips=%d err=%v\n%s", trips.N()-n, err, second)
	require.Contains(t, second, "after\n\n0/2 0.0% -> ETA", "changed view trips=%d err=%v\n%s", trips.N()-n, err, second)
}

// TestOrderVerbsThroughTheCommand: the rows and columns of a table moved by
// the verbs, one exchange each, and the render follows.
func TestOrderVerbsThroughTheCommand(t *testing.T) {
	t.Parallel()

	addr := throwaway(t)
	for _, step := range []struct {
		args []string
		want string
	}{
		{[]string{"create", "crew", "--columns", "busy,idle,note:text:none"}, "TABLE CREATE table=crew columns=3 trips=1\n"},
		{[]string{"row", "add", "crew", "studio", "hetzner", "stella", "rowan"}, "TABLE ROWS ADD table=crew rows=4 trips=1\n"},
		{[]string{"row", "order", "crew", "rowan", "stella"}, "TABLE ROW ORDER table=crew first=rowan,stella trips=1\n"},
		{[]string{"row", "move", "crew", "hetzner", "--before", "studio"}, "TABLE ROW MOVE table=crew row=hetzner place=before of=studio trips=1\n"},
		{[]string{"col", "move", "crew", "note", "--first"}, "TABLE COL MOVE table=crew col=note place=first trips=1\n"},
		{[]string{"col", "add", "crew", "share:pct(busy)", "--after", "busy"}, "TABLE COL ADD table=crew col=share place=after of=busy trips=1\n"},
		{[]string{"col", "del", "crew", "idle"}, "TABLE COL DEL table=crew col=idle trips=1\n"},
		{[]string{"render", "crew"}, "crew    | note | busy | share\n--------+------+------+------\nrowan   |      |    0 | 0.0%\nstella  |      |    0 | 0.0%\nhetzner |      |    0 | 0.0%\nstudio  |      |    0 | 0.0%\n--------+------+------+------\n        |      |    0 | 0.0%\n"},
		{[]string{"row", "sort", "crew", "--keep"}, "TABLE ROW SORT table=crew by=name desc=false keep=true trips=1\n"},
		{[]string{"row", "add", "crew", "alex"}, "TABLE ROW ADD table=crew row=alex cols=3 bound=0 trips=1\n"},
	} {
		code, stdout, stderr := runTable(at(addr, step.args...)...)
		require.EqualValues(t, 0, code, "%v: exit %d\nstdout %q\nwant   %q\nstderr %q", step.args, code, stdout, step.want, stderr)
		require.Equal(t, step.want, stdout, "%v: exit %d\nstdout %q\nwant   %q\nstderr %q", step.args, code, stdout, step.want, stderr)
	}
	code, stdout, stderr := runTable(at(addr, "row", "move", "crew", "alex", "--last")...)
	require.EqualValues(t, 1, code, "a move under a standing sort: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Empty(t, stdout, "a move under a standing sort: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.Contains(t, stderr, "the rows are kept sorted by name", "a move under a standing sort: exit %d stdout %q stderr %q", code, stdout, stderr)
	require.True(t, strings.HasSuffix(stderr, "; run: nova-table row sort 'crew' --manual\n"), "a move under a standing sort: exit %d stdout %q stderr %q", code, stdout, stderr)
	code, stdout, _ = runTable(at(addr, "show", "crew")...)
	require.EqualValues(t, 0, code, "the standing sort places the new row: %q", stdout)
	require.Contains(t, stdout, " sort=name\n", "the standing sort places the new row: %q", stdout)
	require.Contains(t, stdout, "row=alex", "the standing sort places the new row: %q", stdout)
	require.LessOrEqual(t, strings.Index(stdout, "row=alex"), strings.Index(stdout, "row=hetzner"), "the standing sort places the new row: %q", stdout)
}
