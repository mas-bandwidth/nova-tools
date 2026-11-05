package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb path takes -h, --help and help <verb>: its usage, exit 0, nothing on stderr, no store needed.
func TestVerbHelp(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env = map[string]string{}
	paths := [][]string{{"machine"}, {"loop"}, {"history"}, {"inventory"}, {"schema"}, {"help"}}
	for _, k := range []string{"machine", "loop"} {
		for _, op := range kindOps {
			paths = append(paths, []string{k, op})
		}
	}
	for _, p := range paths {
		want := "usage: nova-config " + p[0]
		for _, form := range [][]string{append(append([]string{}, p...), "-h"), append(append([]string{}, p...), "--help"), append([]string{"help"}, p...)} {
			code, out, errs := h.run(t, form...)
			assert.Equal(t, 0, code, "%v", form)
			assert.Empty(t, errs, "%v", form)
			assert.Contains(t, out, want, "%v", form)
		}
	}
	_, out, _ := h.run(t, "machine", "add", "--help")
	assert.Contains(t, out, "machine fields: name, address, user, os, arch, cores, roles, runners, note")
	assert.Contains(t, out, "--user <text>")
	for _, p := range paths[6:] { // the banner names each verb path
		assert.Contains(t, usage, "nova-config "+strings.Join(p, " "))
	}
	code, _, errs := h.run(t, "help", "machine", "frobnicate")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "unknown verb")
}

func TestUsageErrorsAreOneLine(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	for _, args := range [][]string{{"machine", "list", "--nope"}, {"inventory", "--nope"}, {"history", "--nope"}, {"schema", "--nope"}, {"machine", "list", "--json", "--max", "3"}, {"history", "--since"}, {"history", "--max", "abc"},
		{"machine", "add", "x", "--actor", "a"}, {"machine"}, {"loop", "wat"}, {"nonsense"}} {
		code, out, errs := h.run(t, args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Empty(t, out, "%v", args)
		assert.Equal(t, 1, strings.Count(errs, "\n"), "%v: %q", args, errs)
		assert.NotContains(t, errs, "Usage of")
		assert.NotContains(t, errs, "flag provided")
		assert.Contains(t, errs, "; run: nova-config", "%v", args)
	}
	_, _, errs := h.run(t, "nonsense")
	assert.Contains(t, errs, "machine, loop, history, inventory, schema, help; run: nova-config help")
}

// What is printed can be pasted back: argv is JSON, = is never escaped, free text is a JSON string.
func TestArgvAndValuesRoundTrip(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	argv := []string{"/bin/sh", "-c", "A=b echo \"x y\" <z> ‮ & \t"}
	raw, err := json.Marshal(argv)
	require.NoError(t, err)
	h.ok(t, "loop", "add", "rt", "--machine", "bench-a", "--argv", string(raw), "--every", "5", "--actor", "t")
	out := h.ok(t, "loop", "show", "rt")
	line := out[strings.Index(out, "argv=")+5:]
	line = line[:strings.Index(line, " seat=")]
	var back []string
	require.NoError(t, json.Unmarshal([]byte(line), &back), line)
	assert.Equal(t, argv, back)
	assert.NotContains(t, out, `\x3d`)
	assert.NotContains(t, out, "‮", "bidi controls are escaped")
	assert.Equal(t, `"" -> "the first"`, strings.TrimPrefix(strings.TrimSpace(strings.Split(h.ok(t, "machine", "set", "bench-a", "--note", "the first", "--actor", "t", "--dry-run"), "\n")[1]), "note: "))
	h.ok(t, "machine", "set", "bench-a", "--note", "k=v \"q\"", "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "list"), `note="k=v \"q\""`)
	assert.Equal(t, "-", value(""))
	assert.Equal(t, `"-"`, value("-"))
	assert.Equal(t, "a=b", value("a=b"))
	assert.Equal(t, `"a b"`, value("a b"))
}

func TestEnvAndLog(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "loop", "add", "e1", "--machine", "bench-a", "--argv", `["p"]`, "--every", "5", "--env", `{"B_VAR":"2","A_VAR":"x=y z"}`, "--log", "/var/log/e1.log", "--actor", "t")
	assert.Equal(t, `{"A_VAR":"x=y z","B_VAR":"2"}`, h.st.rows["loop"]["e1"].F["env"], "keys sorted, canonical")
	_, out, _ := h.run(t, "inventory", "--host", "bench-a")
	assert.JSONEq(t, `{"ansible_host":"bench-a","ansible_user":"nova","runners":0,"nova_loops":[{"name":"e1","argv":["p"],"seat":"","keys":[],"every":5,"keepalive":false,"enabled":true,
		"env":{"A_VAR":"x=y z","B_VAR":"2"},"log":"/var/log/e1.log"}]}`, out)
	h.ok(t, "loop", "set", "e1", "--env", "{}", "--log", "", "--actor", "t")
	_, out, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.Contains(t, out, `"log": "~/.local/state/nova/loops/e1.log"`)
	for _, bad := range []string{`{"lower":"1"}`, `{"1A":"1"}`, `{"A-B":"1"}`, `{"A":1}`, `["A"]`, `A=1`, `{"A":"1"} x`} {
		code, _, errs := h.run(t, "loop", "set", "e1", "--env", bad, "--actor", "t")
		assert.Equal(t, 2, code, bad)
		assert.Contains(t, errs, "--env", bad)
	}
	code, _, errs := h.run(t, "loop", "set", "e1", "--log", "relative.log", "--actor", "t")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--log")
	assert.NoError(t, func() error { _, err := kinds["loop"].fields[7].canonical(`{"_X9":"v"}`); return err }())
}

func TestHistory(t *testing.T) {
	t.Parallel()
	h := fleet(t) // writes 1..3: add bench-a, add bench-b, add loop member-b
	h.ok(t, "loop", "set", "member-b", "--enabled", "false", "--actor", "a1", "--reason", "rest it")
	h.ok(t, "machine", "remove", "bench-a", "--actor", "a2")
	out := h.ok(t, "history", "loop")
	assert.Equal(t, "HISTORY at=2026-01-02T00:00:04Z actor=a1 kind=loop name=member-b op=set rev=4\n  enabled: true -> false\n  reason: \"rest it\"\n"+
		"HISTORY at=2026-01-02T00:00:03Z actor=t kind=loop name=member-b op=add rev=3\n"+
		"  machine: \"bench-b\"\n  argv: [\"/usr/bin/env\",\"A=b\",\"prog\"]\n  seat: \"s-b\"\n  keys: \"A_KEY,B_KEY\"\n  every: 0\n  keepalive: true\n  enabled: true\n  env: {}\n  log: \"\"\n"+
		"CONFIG HISTORY rows=2\n", out)
	out = h.ok(t, "history")
	assert.True(t, strings.HasPrefix(out, "HISTORY at=2026-01-02T00:00:05Z actor=a2 kind=machine name=bench-a op=remove rev=5\n  address: \"\"\n  user: \"nova\"\n"), out)
	assert.Equal(t, 5, strings.Count(out, "HISTORY at="))
	assert.Equal(t, 1, strings.Count(h.ok(t, "history", "machine", "bench-b"), "HISTORY at="))
	assert.Equal(t, 2, strings.Count(h.ok(t, "history", "--since", "2026-01-02T00:00:04Z"), "HISTORY at="), "RFC 3339 since")
	assert.Equal(t, 5, strings.Count(h.ok(t, "history", "--since", "24h"), "HISTORY at="), "the harness's now is 2026-01-03T00:00:00Z")
	assert.Equal(t, 0, strings.Count(h.ok(t, "history", "--since", "1m"), "HISTORY at="))
	for _, args := range [][]string{{"history", "--since", "yesterday"}, {"history", "friend"}, {"history", "loop", "a", "b"}} {
		code, _, errs := h.run(t, args...)
		assert.Equal(t, 2, code, "%v", args)
		assert.Contains(t, errs, "nova-config history: ")
	}
}

func TestParseSince(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"90m": now.Add(-90 * time.Minute), "24h": now.Add(-24 * time.Hour), "7d": now.Add(-7 * 24 * time.Hour),
		"2026-03-01T00:00:00Z": time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	} {
		got, err := parseSince(in, now)
		require.NoError(t, err, in)
		assert.True(t, want.Equal(got), in)
	}
	for _, bad := range []string{"", "0h", "-5m", "xd", "2026-03-01", "soon"} {
		_, err := parseSince(bad, now)
		assert.Error(t, err, bad)
	}
}

// pe is a constructed pgconn.PgError, the way the driver returns one.
func pe(code, msg string) *pgconn.PgError {
	return &pgconn.PgError{Severity: "ERROR", Code: code, Message: msg}
}

func TestStoreErrors(t *testing.T) {
	t.Parallel()
	for code, want := range map[string]string{
		"42P01": schemaMissing, "42703": schemaMissing, "3F000": schemaMissing,
		"28P01": "postgres refused the login for nova: check NOVA_PG_PASSWORD", "28000": "postgres refused the login for nova: check NOVA_PG_PASSWORD",
		"42501": "nova may not add machines",
		"57014": "store error (SQLSTATE 57014): canceling statement due to timeout",
	} {
		err := storeError(fmt.Errorf("list machine: %w", pe(code, "permission denied for table machines\nLINE 1: SELECT")), "nova", "add")
		if code == "57014" {
			err = storeError(pe(code, "canceling statement due to timeout. Try again.\nmore"), "nova", "read")
		}
		assert.EqualError(t, err, want, code)
		var r refusal
		assert.False(t, errors.As(err, &r))
	}
	assert.EqualError(t, storeError(errors.New("line one\n\tline two"), "u", "read"), "postgres: line one line two")
	assert.NoError(t, storeError(nil, "u", "read"))
}

func TestConnectErrors(t *testing.T) {
	t.Parallel()
	cfg, err := pgx.ParseConfig("postgres://nova@db.example:5432/mydb")
	require.NoError(t, err)
	assert.EqualError(t, connectError(cfg, pe("3D000", "database \"mydb\" does not exist")), "database mydb does not exist on db.example")
	assert.EqualError(t, connectError(cfg, pe("28P01", "password authentication failed")), "postgres refused the login for nova: check NOVA_PG_PASSWORD")
	assert.EqualError(t, connectError(cfg, fmt.Errorf("dial: %w", syscall.ECONNREFUSED)), "cannot reach postgres at db.example:5432 (NOVA_PG_DSN): connection refused")
	for dsn, want := range map[string]string{"postgres://u@h:99999/x": "port 99999 out of range", "mysql://u@h/x": "not a postgres:// URL", "h=1": "not a postgres:// URL", "postgres://u@h:5432/x": ""} {
		assert.Equal(t, want, dsnProblem(dsn), dsn)
	}
	_, err = openPG(t.Context(), "postgres://u:secret@h:99999/x", "")
	assert.EqualError(t, err, "the postgres dsn is wrong: port 99999 out of range")
}

func TestDiagnose(t *testing.T) {
	t.Parallel()
	loop := kinds["loop"]
	machine := kinds["machine"]
	after := Row{Name: "l1", F: map[string]string{"machine": "ghost"}}
	err := diagnose(pe("23503", "insert violates foreign key"), loop, "add", "l1", after, []string{"a", "b", "c"}, nil)
	assert.EqualError(t, err, "no machine ghost; the machines are a,b,c")
	assert.EqualError(t, diagnose(pe("23503", "x"), loop, "set", "l1", after, nil, nil), "no machine ghost; the machines are (none)")
	err = diagnose(pe("23503", "delete violates foreign key"), machine, "remove", "m1", Row{}, nil, []string{"l1", "l2"})
	assert.EqualError(t, err, "machine m1 still runs loops l1,l2; remove them first: nova-config loop remove l1 --actor <you>")
	err = diagnose(pe("23505", "duplicate key value violates unique constraint"), machine, "add", "m1", Row{}, nil, nil)
	assert.EqualError(t, err, "machine m1 exists; use: nova-config machine set")
	err = diagnose(pe("23514", "new row violates\ncheck constraint \"x\""), machine, "add", "m1", Row{}, nil, nil)
	assert.EqualError(t, err, `machine m1: new row violates check constraint "x"`)
	for _, e := range []error{err, diagnose(pe("23503", "x"), machine, "remove", "m1", Row{}, nil, []string{"l1"})} {
		var r refusal
		assert.True(t, errors.As(e, &r), "a constraint is a refusal, exit 1")
	}
}

func TestConnectionErrorIsOneLine(t *testing.T) {
	t.Parallel()
	cause := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "db.invalid"}}
	err := fmt.Errorf("failed to connect to `user=u database=x`:\n\tdb.invalid:5432 (db.invalid): dial error: %w", cause)
	assert.Equal(t, "lookup db.invalid: no such host", oneLine(rootCause(err).Error()))
	assert.Equal(t, "connection refused", rootCause(fmt.Errorf("dial: %w", syscall.ECONNREFUSED)).Error())
	assert.Equal(t, "a b c", oneLine("a\n  b\tc\n"))
}

func TestOpenPGConnectFailure(t *testing.T) {
	t.Parallel()
	l, err := net.Listen("tcp", "127.0.0.1:0") // a port nothing answers on, once closed
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	_, err = openPG(t.Context(), "postgres://u@"+addr+"/x?sslmode=disable&connect_timeout=2", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot reach postgres at "+addr+" (NOVA_PG_DSN): ")
	assert.NotContains(t, err.Error(), "\n")
}
