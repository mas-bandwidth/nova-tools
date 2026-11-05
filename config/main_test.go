package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake is an in-memory store: each kind's rows by name, and the history.
type fake struct {
	rows    map[string]map[string]Row
	history []change
	failure error
}

type change struct {
	kind, name, op, actor string
	before, after         map[string]string
}

func newFake() *fake {
	f := &fake{rows: map[string]map[string]Row{"machine": {}, "loop": {}, "fleet": {}}}
	fleet, err := kinds["fleet"].newRow("fleet", nil)
	if err != nil {
		panic(err)
	}
	fleet.Created, fleet.Updated = "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"
	f.rows["fleet"]["fleet"] = fleet
	return f
}

func (f *fake) List(_ context.Context, k *kind) ([]Row, error) {
	if f.failure != nil {
		return nil, f.failure
	}
	var out []Row
	for _, n := range slices.Sorted(maps.Keys(f.rows[k.name])) {
		out = append(out, f.rows[k.name][n])
	}
	return out, nil
}

func (f *fake) Write(_ context.Context, k *kind, op string, before, after Row, actor, reason string) (int64, error) {
	b, a := before.F, after.F
	if reason != "" && a != nil {
		a = maps.Clone(a)
		a["reason"] = reason
	} else if reason != "" {
		b = maps.Clone(b)
		b["reason"] = reason
	}
	name := after.Name + before.Name
	if op == "remove" {
		delete(f.rows[k.name], before.Name)
	} else {
		name = after.Name
		after.Created, after.Updated = "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z"
		f.rows[k.name][after.Name] = after
	}
	f.history = append(f.history, change{k.name, name, op, actor, b, a})
	return int64(len(f.history)), nil
}

func (f *fake) Close() {}

// harness runs nova-config against one fake store, with NOVA_PG_DSN set.
type harness struct {
	st   *fake
	env  map[string]string
	host string
}

func newHarness() *harness {
	return &harness{st: newFake(), env: map[string]string{"NOVA_PG_DSN": "postgres://u@db/x"}, host: "elsewhere.local"}
}

func (h *harness) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	d := deps{
		getenv:   func(k string) string { return h.env[k] },
		hostname: func() (string, error) { return h.host, nil },
		openPG:   func(context.Context, string, string) (Store, error) { return h.st, nil },
	}
	code := run(context.Background(), args, d, &out, &errs)
	return code, out.String(), errs.String()
}

func (h *harness) ok(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errs := h.run(t, args...)
	require.Equal(t, 0, code, "%v: %s", args, errs)
	return out
}

// fleet is a store with two machines, a loop, and the fleet row.
func fleet(t *testing.T) *harness {
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--roles", "services,bench", "--actor", "t")
	h.ok(t, "machine", "add", "bench-b", "--user", "nova", "--address", "bench-b.tail", "--os", "linux", "--arch", "amd64", "--cores", "12", "--runners", "2", "--actor", "t")
	h.ok(t, "loop", "add", "member-b", "--machine", "bench-b", "--keepalive", "true", "--seat", "s-b", "--keys", "B_KEY,A_KEY",
		"--argv", `["/usr/bin/env","A=b","prog"]`, "--actor", "t")
	h.ok(t, "fleet", "set", "--pg_dsn", "postgres://u@bench-a/x", "--actor", "t")
	return h
}

func TestListAndShowLines(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "set", "bench-b", "--note", strings.Repeat("ab ", 30)+"=", "--cores", "4", "--actor", "t")
	out := h.ok(t, "machine", "list")
	assert.Equal(t, "MACHINE name=bench-a user=nova address=- os=- arch=- cores=- runners=0 roles=bench,services note=-\n"+
		"MACHINE name=bench-b user=nova address=bench-b.tail os=linux arch=amd64 cores=4 runners=2 roles=bench note="+
		strings.ReplaceAll(strings.Repeat("ab ", 20), " ", `\x20`)+"...\n"+
		"CONFIG LIST kind=machine rows=2\n", out)
	out = h.ok(t, "machine", "show", "bench-b")
	assert.Contains(t, out, `\x20\x3d created=2026-01-01T00:00:00Z updated=2026-01-02T00:00:00Z loops=member-b`+"\n", "show does not cut the note")
	out = h.ok(t, "loop", "list")
	assert.Equal(t, `LOOP name=member-b machine=bench-b argv=["/usr/bin/env","A\x3db","prog"] seat=s-b keys=A_KEY,B_KEY every=0 keepalive=true enabled=true`+"\nCONFIG LIST kind=loop rows=1\n", out)
	assert.Equal(t, "FLEET name=fleet pg_dsn=postgres://u@bench-a/x created=2026-01-01T00:00:00Z updated=2026-01-02T00:00:00Z\n", h.ok(t, "fleet", "show"))
}

func TestJSONShapes(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	var got struct {
		Result struct {
			Verb, Status string
			Exit         int
		}
		Facts map[string]any
		Items []struct {
			Kind   string
			Fields map[string]any
		}
	}
	require.NoError(t, json.Unmarshal([]byte(h.ok(t, "machine", "show", "bench-b", "--json")), &got))
	assert.Equal(t, "machine show", got.Result.Verb)
	assert.Equal(t, "ok", got.Result.Status)
	assert.Empty(t, got.Facts)
	require.Len(t, got.Items, 1)
	assert.Equal(t, []any{"member-b"}, got.Items[0].Fields["loops"])
	assert.Equal(t, "12", got.Items[0].Fields["cores"])
	assert.NotContains(t, got.Items[0].Fields, "seat", "the old columns are not fields")
	out := h.ok(t, "loop", "list", "--json")
	assert.True(t, strings.HasPrefix(out, `{"result":{"verb":"loop list","status":"ok","exit":0},"facts":{"kind":"loop","rows":1},"items":[{"kind":"loop","fields":{"name":"member-b","machine":"bench-b","argv":"[\"/usr/bin/env\"`), out)
	assert.NotContains(t, out, "created", "a list item carries no stamps")
}

func TestWritesRecordHistory(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	n := len(h.st.history)
	assert.Equal(t, "CONFIG SET kind=loop name=member-b rev=5 changed=enabled\n", h.ok(t, "loop", "set", "member-b", "--enabled", "false", "--actor", "a1", "--reason", "rest it"))
	c := h.st.history[n]
	assert.Equal(t, change{"loop", "member-b", "set", "a1", c.before, c.after}, c)
	assert.Equal(t, "true", c.before["enabled"])
	assert.Equal(t, "false", c.after["enabled"])
	assert.Equal(t, "rest it", c.after["reason"])
	assert.NotContains(t, c.before, "reason")
	assert.Equal(t, "CONFIG REMOVE kind=loop name=member-b rev=6\n", h.ok(t, "loop", "remove", "member-b", "--actor", "a1", "--reason", "gone"))
	c = h.st.history[n+1]
	assert.Nil(t, c.after)
	assert.Equal(t, "gone", c.before["reason"])
	assert.Equal(t, "CONFIG ADD kind=machine name=m3 rev=7\n", h.ok(t, "machine", "add", "m3", "--user", "u", "--actor", "a1"))
	assert.Nil(t, h.st.history[n+2].before)
	assert.Equal(t, "bench", h.st.history[n+2].after["roles"], "an add writes every field, the defaults too")
}

func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	n := len(h.st.history)
	assert.Equal(t, "CONFIG DRY-RUN op=set kind=loop name=member-b actor=a1 wrote=nothing reason=a\\x20b every=0>5 keepalive=true>false\n",
		h.ok(t, "loop", "set", "member-b", "--every", "5", "--keepalive", "false", "--actor", "a1", "--reason", "a b", "--dry-run"))
	out := h.ok(t, "machine", "remove", "bench-b", "--actor", "a1", "--dry-run")
	assert.Equal(t, "CONFIG DRY-RUN op=remove kind=machine name=bench-b actor=a1 wrote=nothing address=bench-b.tail arch=amd64 cores=12 note=- os=linux roles=bench runners=2 user=nova\n", out)
	assert.Len(t, h.st.history, n)
}

func TestRefusals(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	for _, c := range []struct {
		code int
		args []string
		want string
	}{
		{1, []string{"loop", "set", "member-b", "--every", "5", "--actor", "a"}, "has --every 5 and --keepalive true"},
		{1, []string{"loop", "set", "nope", "--every", "5", "--actor", "a"}, "loop nope not found"},
		{1, []string{"machine", "add", "bench-a", "--user", "u", "--actor", "a"}, "machine bench-a exists"},
		{1, []string{"machine", "add", "Bad_Name", "--user", "u", "--actor", "a"}, "want lower-case letters"},
		{1, []string{"machine", "add", "m9", "--actor", "a"}, "--user is required"},
		{1, []string{"machine", "set", "bench-a", "--roles", "coordination", "--actor", "a"}, `--roles "coordination"`},
		{2, []string{"machine", "set", "bench-a", "--slots", "4", "--actor", "a"}, "flag provided but not defined"},
		{2, []string{"version"}, `unknown verb "version"`},
		{1, []string{"machine", "set", "bench-a", "--roles", "bench,chef", "--actor", "a"}, `--roles "chef"`},
		{1, []string{"loop", "set", "member-b", "--argv", "prog", "--actor", "a"}, "want the command as a JSON array"},
		{1, []string{"loop", "set", "member-b", "--seat", "", "--actor", "a"}, "names secrets (--keys A_KEY,B_KEY) and no --seat"},
		{1, []string{"fleet", "set", "--pg_dsn", "postgres://u:hunter2@h/x", "--actor", "a"}, "carries a password"},
		{1, []string{"machine", "show", "nope"}, "machine nope not found"},
		{2, []string{"loop", "set", "member-b", "--every", "5"}, "--actor is required"},
		{2, []string{"loop", "set", "member-b", "--actor", "a"}, "set names no field"},
		{2, []string{"machine", "set", "bench-a", "--flavour", "x", "--actor", "a"}, "flag provided but not defined"},
		{2, []string{"machine", "history", "bench-a"}, `unknown verb "machine history"`},
		{2, []string{"fleet", "list"}, `unknown verb "fleet list"`},
		{2, []string{"friend", "list"}, `unknown verb "friend list"`},
		{2, []string{"machine", "list", "--pg", "postgres://u:hunter2@h/x"}, "--pg carries a password"},
		{2, []string{"machine", "show"}, "machine show wants a name"},
		{2, []string{"machine", "show", "a", "b"}, "unexpected words"},
	} {
		code, out, errs := h.run(t, c.args...)
		assert.Equal(t, c.code, code, "%v: %s", c.args, errs)
		assert.Empty(t, out, "%v", c.args)
		assert.Contains(t, errs, c.want, "%v", c.args)
		assert.NotContains(t, errs, "hunter2", "%v: a refusal never echoes a password", c.args)
	}
	h.env = map[string]string{}
	code, _, errs := h.run(t, "machine", "list")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "no store: give --pg")
	h = newHarness()
	h.st.failure = errors.New("postgres: down")
	code, _, errs = h.run(t, "loop", "list")
	assert.Equal(t, 2, code, "a store that does not answer is could-not-run")
	assert.Contains(t, errs, "postgres: down")
}

func TestCoresDefaultUnsets(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "set", "bench-a", "--cores", "3", "--actor", "a")
	assert.Equal(t, "3", h.st.rows["machine"]["bench-a"].F["cores"])
	h.ok(t, "machine", "set", "bench-a", "--cores", "default", "--actor", "a")
	assert.Equal(t, "", h.st.rows["machine"]["bench-a"].F["cores"])
}

func TestHelp(t *testing.T) {
	t.Parallel()
	h := newHarness()
	assert.Equal(t, usage, h.ok(t, "help"))
	assert.Equal(t, usage, h.ok(t))
	code, _, errs := h.run(t, "apply", "--kind", "loop")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, `unknown verb "apply --kind"`)
}

// TestSchemaHoldsEveryField keeps schema.sql and the kinds in step: every
// field is a column of its kind's table.
func TestSchemaHoldsEveryField(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	schema := string(raw)
	for _, k := range kinds {
		at := strings.Index(schema, "CREATE TABLE IF NOT EXISTS config."+k.table+" (")
		require.GreaterOrEqual(t, at, 0, k.table)
		body := schema[at : at+strings.Index(schema[at:], ");")]
		for _, f := range k.fields {
			assert.Regexp(t, `\n    "?`+f.name+`"? +`, body, "%s.%s", k.table, f.name)
		}
	}
}

// TestSQLNamesItsColumns holds the SQL to the fields it knows: no *, none of
// the old columns, so a store that still has them is read and written as is.
func TestSQLNamesItsColumns(t *testing.T) {
	t.Parallel()
	assert.Equal(t, `SELECT name, coalesce("pg_dsn"::text, ''), to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM config.fleet ORDER BY name`, listSQL(kinds["fleet"]))
	for _, k := range kinds {
		q := listSQL(k)
		assert.NotContains(t, q, "*")
		for _, old := range []string{"seat", "slots", "width", "gates_only", "tla", "redis_port", "bus", "store", "coordinator", "loops_dir"} {
			if old == "seat" && k.name == "loop" {
				continue // a loop still carries its seat
			}
			assert.NotRegexp(t, `"`+old+`"`, q, "%s reads %s", k.name, old)
		}
	}
	row := Row{Name: "m1", F: map[string]string{"user": "u", "address": "", "os": "linux", "arch": "", "cores": "", "runners": "2", "roles": "bench", "note": ""}}
	q, args := writeSQL(kinds["machine"], "add", "m1", row)
	assert.Equal(t, `INSERT INTO config.machines (name, "user", "address", "os", "arch", "cores", "runners", "roles", "note") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, q)
	assert.Equal(t, []any{"m1", "u", "", "linux", "", nil, int64(2), "bench", ""}, args)
	q, args = writeSQL(kinds["machine"], "set", "m1", row)
	assert.True(t, strings.HasPrefix(q, `UPDATE config.machines SET "user" = $2, `) && strings.HasSuffix(q, `, updated_at = now() WHERE name = $1`), q)
	assert.Len(t, args, 9)
	q, args = writeSQL(kinds["machine"], "remove", "m1", Row{})
	assert.Equal(t, "DELETE FROM config.machines WHERE name = $1", q)
	assert.Equal(t, []any{"m1"}, args)
}

// TestSchemaIsAdditive: schema.sql never drops a column, a table or a row.
func TestSchemaIsAdditive(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	assert.NotRegexp(t, `(?i)\bdrop\b|\bdelete\b|\btruncate\b`, string(raw))
}
