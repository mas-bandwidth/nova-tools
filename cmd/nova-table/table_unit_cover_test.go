package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// novaTableCoverApp is the application the cover tests step the verbs on: the
// environment reads empty (no seat, no address) and no redis-server is on PATH,
// so the tests never touch the process's own environment and a verb that
// refuses before dialling never reaches redisconn.
func novaTableCoverApp() *application {
	return &application{
		getenv:   func(string) string { return "" },
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

// novaTableCoverRun dispatches one command line in process and returns its
// exit, stdout and stderr; the address given holds no store, so a verb that
// dialled would be answered unreachable instead.
func novaTableCoverRun(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := novaTableCoverApp().dispatch(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestNovaTableTableCoverCreateRefusals pins every argument create refuses
// before it dials: a missing name, a missing or malformed --columns, a column
// list past the table bound (the store's own no, exit 1), a malformed --width,
// a width naming an undeclared column, and a name the store's grammar refuses.
func TestNovaTableTableCoverCreateRefusals(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	over := make([]string, ntable.LimitColumns+1)
	for i := range over {
		over[i] = "c" + strconv.Itoa(i)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
		code int
	}{
		{
			name: "no table name",
			args: []string{"create", "--columns", "a"},
			want: "CREATE REFUSED: wants one table name:",
			code: 2,
		},
		{
			name: "no columns",
			args: []string{"create", "demo"},
			want: "CREATE REFUSED: --columns wants the columns",
			code: 2,
		},
		{
			name: "a malformed columns spec",
			args: []string{"create", "demo", "--columns", "a:bogus"},
			want: "CREATE REFUSED: --columns: column a wants a projection",
			code: 2,
		},
		{
			name: "more columns than the table bound",
			args: []string{"create", "demo", "--columns", strings.Join(over, ",")},
			want: "CREATE REFUSED: --columns: limit exceeded: columns per table: bound 1000, observed 1001",
			code: 1,
		},
		{
			name: "a malformed width",
			args: []string{"create", "demo", "--columns", "a", "--width", "a=x"},
			want: `CREATE REFUSED: --width: width "a=x" wants col=n`,
			code: 2,
		},
		{
			name: "a width for a column the columns do not name",
			args: []string{"create", "demo", "--columns", "a", "--width", "b=5"},
			want: "CREATE REFUSED: --width names column b, which --columns does not declare",
			code: 2,
		},
		{
			name: "a table name the grammar refuses",
			args: []string{"create", "bad name", "--columns", "a"},
			want: `CREATE REFUSED: the table name wants letters, digits, _ . and -, got "bad name"`,
			code: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := novaTableCoverRun(append(append([]string{}, tc.args...), "--redis", nowhere)...)
			assert.EqualValues(t, tc.code, code, "%s: exit", tc.name)
			assert.Empty(t, stdout, tc.name)
			assert.True(t, strings.HasPrefix(stderr, tc.want), "%s: stderr %q, want it to open %q", tc.name, stderr, tc.want)
			assert.NotContains(t, stderr, "unreachable", "%s: the refusal comes before any dial: %q", tc.name, stderr)
		})
	}
}

// TestNovaTableTableCoverSetRefusals pins the two argument faults set refuses
// before it dials: more than one table name, and a malformed --columns.
func TestNovaTableTableCoverSetRefusals(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "two table names",
			args: []string{"set", "a", "b"},
			want: "SET REFUSED: wants one table name:",
		},
		{
			name: "a malformed columns spec",
			args: []string{"set", "a", "--columns", "a:bogus"},
			want: "SET REFUSED: --columns: column a wants a projection",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := novaTableCoverRun(append(append([]string{}, tc.args...), "--redis", nowhere)...)
			assert.EqualValues(t, 2, code, "%s: exit", tc.name)
			assert.Empty(t, stdout, tc.name)
			assert.True(t, strings.HasPrefix(stderr, tc.want), "%s: stderr %q, want it to open %q", tc.name, stderr, tc.want)
			assert.NotContains(t, stderr, "unreachable", "%s: %q", tc.name, stderr)
		})
	}
}

// TestNovaTableTableCoverDropClearRefusals pins that drop and clear insist on
// exactly one table name, before either dials.
func TestNovaTableTableCoverDropClearRefusals(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "drop with no name", args: []string{"drop"}, want: "DROP REFUSED: wants one table name: drop <table>"},
		{name: "clear with no name", args: []string{"clear"}, want: "CLEAR REFUSED: wants one table name: clear <table>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := novaTableCoverRun(append(append([]string{}, tc.args...), "--redis", nowhere)...)
			assert.EqualValues(t, 2, code, "%s: exit", tc.name)
			assert.Empty(t, stdout, tc.name)
			assert.True(t, strings.HasPrefix(stderr, tc.want), "%s: stderr %q, want it to open %q", tc.name, stderr, tc.want)
			assert.NotContains(t, stderr, "unreachable", "%s: %q", tc.name, stderr)
		})
	}
}

// TestNovaTableTableCoverListRefusals pins that list takes no table name,
// before it dials.
func TestNovaTableTableCoverListRefusals(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	code, stdout, stderr := novaTableCoverRun("list", "demo", "--redis", nowhere)
	assert.EqualValues(t, 2, code)
	assert.Empty(t, stdout)
	assert.True(t, strings.HasPrefix(stderr, "LIST REFUSED: takes no table name: list"), "%q", stderr)
	assert.NotContains(t, stderr, "unreachable", "%q", stderr)
}

// TestNovaTableTableCoverShowRefusals pins the two argument faults show
// refuses before it dials: a missing table name and a non-numeric --at-epoch.
func TestNovaTableTableCoverShowRefusals(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no table name", args: []string{"show"}, want: "SHOW REFUSED: wants one table name: show <table>"},
		{name: "an at-epoch that is not a number", args: []string{"show", "demo", "--at-epoch", "abc"}, want: "SHOW REFUSED: --at-epoch wants an unsigned integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := novaTableCoverRun(append(append([]string{}, tc.args...), "--redis", nowhere)...)
			assert.EqualValues(t, 2, code, "%s: exit", tc.name)
			assert.Empty(t, stdout, tc.name)
			assert.True(t, strings.HasPrefix(stderr, tc.want), "%s: stderr %q, want it to open %q", tc.name, stderr, tc.want)
			assert.NotContains(t, stderr, "unreachable", "%s: %q", tc.name, stderr)
		})
	}
}

// TestNovaTableTableCoverRenderRefusals pins every argument fault render
// refuses before it dials: no target, a table and --view together, --view and
// --at-epoch together, a negative --label-width and a non-numeric --at-epoch.
func TestNovaTableTableCoverRenderRefusals(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no target",
			args: []string{"render"},
			want: "RENDER REFUSED: wants one table name or --view <name>",
		},
		{
			name: "a table and a view together",
			args: []string{"render", "demo", "--view", "v"},
			want: "RENDER REFUSED: wants one table name or --view <name>",
		},
		{
			name: "a view with an at-epoch",
			args: []string{"render", "--view", "v", "--at-epoch", "1"},
			want: "RENDER REFUSED: --at-epoch applies to a table",
		},
		{
			name: "a negative label width",
			args: []string{"render", "demo", "--label-width", "-1"},
			want: "RENDER REFUSED: --label-width: -1 is negative",
		},
		{
			name: "an at-epoch that is not a number",
			args: []string{"render", "demo", "--at-epoch", "abc"},
			want: "RENDER REFUSED: --at-epoch wants an unsigned integer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := novaTableCoverRun(append(append([]string{}, tc.args...), "--redis", nowhere)...)
			assert.EqualValues(t, 2, code, "%s: exit", tc.name)
			assert.Empty(t, stdout, tc.name)
			assert.True(t, strings.HasPrefix(stderr, tc.want), "%s: stderr %q, want it to open %q", tc.name, stderr, tc.want)
			assert.NotContains(t, stderr, "unreachable", "%s: %q", tc.name, stderr)
		})
	}
}

// TestNovaTableTableCoverViewRefusals pins every argument fault view refuses
// before it dials: no subverb and an unknown one (stepped directly, because
// dispatch answers those two before cmdView), list with a name, state with no
// name or no text, state with a text ValidViewState refuses (a newline, or over
// the bound), set with no --tables (or one of only commas and spaces) and del
// with no name.
func TestNovaTableTableCoverViewRefusals(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "list with a name",
			args: []string{"view", "list", "work"},
			want: "VIEW-LIST REFUSED: takes no view name",
		},
		{
			name: "state clear with no name",
			args: []string{"view", "state", "--clear"},
			want: "VIEW-STATE REFUSED: wants one view name with --clear",
		},
		{
			name: "state with no text",
			args: []string{"view", "state", "work"},
			want: "VIEW-STATE REFUSED: wants a view name and its state text",
		},
		{
			name: "state with a newline",
			args: []string{"view", "state", "work", "a\nb"},
			want: "VIEW-STATE REFUSED: a state is one line of at most 64 bytes",
		},
		{
			name: "state past the byte bound",
			args: []string{"view", "state", "work", strings.Repeat("x", ntable.MaxViewState+1)},
			want: "VIEW-STATE REFUSED: a state is one line of at most 64 bytes",
		},
		{
			name: "set with no tables",
			args: []string{"view", "set", "work"},
			want: "VIEW-SET REFUSED: wants --tables <a,b,...>",
		},
		{
			name: "set with tables of only commas and spaces",
			args: []string{"view", "set", "work", "--tables", ", ,"},
			want: "VIEW-SET REFUSED: wants --tables <a,b,...>",
		},
		{
			name: "del with no name",
			args: []string{"view", "del"},
			want: "VIEW-DEL REFUSED: wants one view name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := novaTableCoverRun(append(append([]string{}, tc.args...), "--redis", nowhere)...)
			assert.EqualValues(t, 2, code, "%s: exit", tc.name)
			assert.Empty(t, stdout, tc.name)
			assert.True(t, strings.HasPrefix(stderr, tc.want), "%s: stderr %q, want it to open %q", tc.name, stderr, tc.want)
			assert.NotContains(t, stderr, "unreachable", "%s: %q", tc.name, stderr)
		})
	}
	t.Run("no subverb", func(t *testing.T) {
		t.Parallel()
		app := novaTableCoverApp()
		var stdout, stderr bytes.Buffer
		code := app.cmdView(nil, &stdout, &stderr)
		assert.EqualValues(t, 2, code)
		assert.Empty(t, stdout.String())
		assert.Equal(t, "VIEW REFUSED: wants set, state, show, list or del; run: nova-table help view\n", stderr.String())
	})
	t.Run("an unknown subverb", func(t *testing.T) {
		t.Parallel()
		app := novaTableCoverApp()
		var stdout, stderr bytes.Buffer
		code := app.cmdView([]string{"frobnicate"}, &stdout, &stderr)
		assert.EqualValues(t, 2, code)
		assert.Empty(t, stdout.String())
		assert.Equal(t, "VIEW REFUSED: unknown subverb frobnicate; wants set, state, show, list or del; run: nova-table help view\n", stderr.String())
	})
}

// TestNovaTableTableCoverDryRuns pins that the write verbs plan a --dry-run
// without dialling: the exit is 0, stderr is empty, and stdout is the one
// TABLE DRY-RUN line saying dialled=0 written=0, for create with a --width, set
// with every presentation flag, drop --definition, clear, view set, view state
// --clear and view del.
func TestNovaTableTableCoverDryRuns(t *testing.T) {
	t.Parallel()
	nowhere := filepath.Join(t.TempDir(), "no-store.sock")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "create with a width", args: []string{"create", "demo", "--columns", "a,b", "--width", "a=5"}},
		{name: "set with every presentation flag", args: []string{"set", "demo", "--footer", "done", "--hide", "a", "--show", "b", "--hidden", "--visible"}},
		{name: "drop the definition", args: []string{"drop", "demo", "--definition"}},
		{name: "clear", args: []string{"clear", "demo"}},
		{name: "view set", args: []string{"view", "set", "work", "--tables", "demo"}},
		{name: "view state clear", args: []string{"view", "state", "work", "--clear"}},
		{name: "view del", args: []string{"view", "del", "work"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := append(append([]string{}, tc.args...), "--redis", nowhere, "--dry-run")
			code, stdout, stderr := novaTableCoverRun(args...)
			require.EqualValues(t, 0, code, "%s: %q", tc.name, stderr)
			assert.Empty(t, stderr, tc.name)
			assert.Equal(t, 1, strings.Count(stdout, "\n"), "%s: one line: %q", tc.name, stdout)
			assert.True(t, strings.HasPrefix(stdout, "TABLE DRY-RUN verb="), "%s: %q", tc.name, stdout)
			assert.Contains(t, stdout, "dialled=0 written=0", "%s: %q", tc.name, stdout)
		})
	}
}
