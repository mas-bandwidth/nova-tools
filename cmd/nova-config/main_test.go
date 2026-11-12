package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	config "github.com/mas-bandwidth/nova-tools/v2/modules/config"
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
	return &fake{rows: map[string]map[string]Row{"machine": {}, "service": {}, "friend": {}}}
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

func (f *fake) Write(_ context.Context, k *kind, op string, before, after Row, actor, reason string, dry bool) (int64, error) {
	if _, taken := f.rows[k.name][after.Name]; op == "add" && taken { // the primary key, as PostgreSQL refuses it
		return 0, diagnose(pe("23505", "duplicate key value violates unique constraint"), k, op, after.Name, after, nil, nil)
	}
	if dry {
		return 0, nil
	}
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

// History is the writes newest first; the n-th write happened n seconds past 2026-01-02T00:00:00Z.
func (f *fake) History(_ context.Context, q HistoryQuery) ([]HistoryEntry, error) {
	var out []HistoryEntry
	for i := len(f.history) - 1; i >= 0; i-- {
		c := f.history[i]
		at := fmt.Sprintf("2026-01-02T00:00:%02dZ", i+1)
		t, _ := time.Parse(time.RFC3339, at)
		if (q.Kind != "" && q.Kind != c.kind) || (q.Name != "" && q.Name != c.name) || t.Before(q.Since) {
			continue
		}
		out = append(out, HistoryEntry{ID: int64(i + 1), At: at, Actor: c.actor, Kind: c.kind, Name: c.name, Op: c.op, Before: c.before, After: c.after})
		if q.Limit > 0 && len(out) == q.Limit {
			break
		}
	}
	return out, nil
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
		now:      func() time.Time { return time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC) },
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

// fleet is a store with two machines, a service, and the fleet row.
func fleet(t *testing.T) *harness {
	h := newHarness()
	h.ok(t, "machine", "add", "bench-a", "--user", "nova", "--roles", "services,bench", "--actor", "t")
	h.ok(t, "machine", "add", "bench-b", "--user", "nova", "--address", "bench-b.tail", "--os", "linux", "--arch", "amd64", "--cores", "12", "--runners", "2", "--actor", "t")
	h.ok(t, "service", "add", "member-b", "--machine", "bench-b", "--keepalive", "true", "--seat", "s-b", "--keys", "B_KEY,A_KEY",
		"--argv", `["/usr/bin/env","A=b","prog"]`, "--actor", "t")
	return h
}

func TestListAndShowLines(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "set", "bench-b", "--note", strings.Repeat("ab ", 30)+"=", "--cores", "4", "--actor", "t")
	out := h.ok(t, "machine", "list")
	assert.Equal(t, "MACHINE name=bench-a address=- public=- user=nova os=- arch=- cores=- roles=bench,services runners=0 runner_scopes=- windows_runners=- windows_admin_runners=- runner_env={} note=-\n"+
		"MACHINE name=bench-b address=bench-b.tail public=- user=nova os=linux arch=amd64 cores=4 roles=bench runners=2 runner_scopes=- windows_runners=- windows_admin_runners=- runner_env={} note="+
		`"`+strings.Repeat("ab ", 20)+`..."`+"\n"+
		"CONFIG LIST kind=machine rows=2\n", out)
	out = h.ok(t, "machine", "show", "bench-b")
	assert.Contains(t, out, ` ="`+" created=2026-01-01T00:00:00Z updated=2026-01-02T00:00:00Z services=member-b\n", "show does not cut the note")
	out = h.ok(t, "service", "list")
	assert.Equal(t, `SERVICE name=member-b machine=bench-b argv=["/usr/bin/env","A=b","prog"] seat=s-b keys=A_KEY,B_KEY every=0 keepalive=true enabled=true env={} log=-`+"\nCONFIG LIST kind=service rows=1\n", out)
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
	assert.Equal(t, []any{"member-b"}, got.Items[0].Fields["services"])
	assert.Equal(t, float64(12), got.Items[0].Fields["cores"])
	assert.NotContains(t, got.Items[0].Fields, "seat", "the old columns are not fields")
	out := h.ok(t, "service", "list", "--json")
	assert.True(t, strings.HasPrefix(out, `{"result":{"verb":"service list","status":"ok","exit":0},"facts":{"kind":"service","rows":1},"items":[{"kind":"service","fields":{"name":"member-b","machine":"bench-b","argv":["/usr/bin/env","A=b","prog"],"seat":"s-b","keys":"A_KEY,B_KEY","every":0,"keepalive":true`), out)
	assert.NotContains(t, out, "created", "a list item carries no stamps")
}

func TestWritesRecordHistory(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	n := len(h.st.history)
	assert.Equal(t, "CONFIG SET kind=service name=member-b rev=4 changed=enabled\nnext: nova-fleet services --check\n", h.ok(t, "service", "set", "member-b", "--enabled", "false", "--actor", "a1", "--reason", "rest it"))
	c := h.st.history[n]
	assert.Equal(t, change{"service", "member-b", "set", "a1", c.before, c.after}, c)
	assert.Equal(t, "true", c.before["enabled"])
	assert.Equal(t, "false", c.after["enabled"])
	assert.Equal(t, "rest it", c.after["reason"])
	assert.NotContains(t, c.before, "reason")
	assert.Equal(t, "CONFIG REMOVE kind=service name=member-b rev=5\n", h.ok(t, "service", "remove", "member-b", "--actor", "a1", "--reason", "gone"))
	c = h.st.history[n+1]
	assert.Nil(t, c.after)
	assert.Equal(t, "gone", c.before["reason"])
	assert.Equal(t, "CONFIG ADD kind=machine name=m3 rev=6\nnext: nova-fleet join m3\n", h.ok(t, "machine", "add", "m3", "--user", "u", "--actor", "a1"))
	assert.Nil(t, h.st.history[n+2].before)
	assert.Equal(t, "bench", h.st.history[n+2].after["roles"], "an add writes every field, the defaults too")
}

func TestDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	n := len(h.st.history)
	assert.Equal(t, "CONFIG DRY-RUN op=set kind=service name=member-b actor=a1 wrote=nothing\n  every: 0 -> 5\n  keepalive: true -> false\n  reason: \"a b\"\n",
		h.ok(t, "service", "set", "member-b", "--every", "5", "--keepalive", "false", "--actor", "a1", "--reason", "a b", "--dry-run"))
	out := h.ok(t, "machine", "remove", "bench-a", "--actor", "a1", "--dry-run") // bench-b runs member-b: its dry-run is refused (TestFunctionalDryRunReal)
	assert.Equal(t, "CONFIG DRY-RUN op=remove kind=machine name=bench-a actor=a1 wrote=nothing\n  address: \"\"\n  public: \"\"\n  user: \"nova\"\n  os: \"\"\n  arch: \"\"\n  cores: null\n  roles: \"bench,services\"\n  runners: 0\n  runner_scopes: \"\"\n  windows_runners: \"\"\n  windows_admin_runners: \"\"\n  runner_env: {}\n  note: \"\"\n", out)
	assert.Equal(t, "CONFIG DRY-RUN op=set kind=machine name=bench-a actor=a1 wrote=nothing\n  note: \"\" -> \"the first\"\n",
		h.ok(t, "machine", "set", "bench-a", "--note", "the first", "--actor", "a1", "--dry-run"))
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
		{1, []string{"service", "set", "member-b", "--every", "5", "--actor", "a"}, "has --every 5 and --keepalive true"},
		{1, []string{"service", "set", "nope", "--every", "5", "--actor", "a"}, "service nope not found"},
		{1, []string{"machine", "add", "bench-a", "--user", "u", "--actor", "a"}, "machine bench-a exists"},
		{2, []string{"machine", "add", "Bad_Name", "--user", "u", "--actor", "a"}, "want lower-case letters"},
		{2, []string{"machine", "add", "m9", "--actor", "a"}, "missing required flag --user; machine fields: name, address, public, user, os, arch, cores, roles, runners, runner_scopes, windows_runners, windows_admin_runners, runner_env, note; run: nova-config machine add -h"},
		{2, []string{"service", "add", "l9"}, "missing required flag --machine, --argv, --actor"},
		{2, []string{"machine", "set", "bench-a", "--roles", "coordination", "--actor", "a"}, `--roles "coordination"`},
		{2, []string{"machine", "set", "bench-a", "--slots", "4", "--actor", "a"}, "unknown flag --slots; machine fields: name, address, public, user, os, arch, cores, roles, runners, runner_scopes, windows_runners, windows_admin_runners, runner_env, note; run: nova-config machine set -h"},
		{2, []string{"version"}, `unknown verb "version"`},
		{2, []string{"machine", "set", "bench-a", "--roles", "bench,chef", "--actor", "a"}, `--roles "chef"`},
		{2, []string{"service", "set", "member-b", "--argv", "prog", "--actor", "a"}, "want the command as a JSON array"},
		{1, []string{"service", "set", "member-b", "--seat", "", "--actor", "a"}, "names secrets (--keys A_KEY,B_KEY) and no --seat"},
		{1, []string{"machine", "show", "nope"}, "machine nope not found"},
		{2, []string{"service", "set", "member-b", "--every", "5"}, "missing required flag --actor"},
		{2, []string{"service", "set", "member-b", "--actor", "a"}, "no field given to set"},
		{2, []string{"machine", "set", "bench-a", "--flavour", "x", "--actor", "a"}, "unknown flag --flavour"},
		{2, []string{"service", "set", "member-b", "--env", "{\"bad-name\":\"1\"}", "--actor", "a"}, `--env name "bad-name"`},
		{2, []string{"machine", "history", "bench-a"}, `unknown verb "machine history"`},
		{2, []string{"fleet", "show"}, `unknown verb "fleet show"`},
		{2, []string{"friend", "fly"}, `unknown verb "friend fly"`},
		{2, []string{"machine", "list", "--pg", "postgres://u:hunter2@h/x"}, "--pg carries a password"},
		{2, []string{"machine", "show"}, "nova-config machine show: wants a name"},
		{2, []string{"machine", "show", "a", "b"}, "unexpected words"},
		// D8 (2026-11-07): a fetcher on a loop or a timer is refused, whatever the words around it.
		{2, []string{"service", "add", "mirror", "--machine", "bench-a", "--argv", `["mirror-refresh","--loop","60"]`, "--keepalive", "true", "--actor", "a"},
			"service mirror fetches on a clock (mirror-refresh with --loop); fetch on an event (a push) or once per job, never on a clock"},
		{2, []string{"service", "add", "sync", "--machine", "bench-a", "--argv", `["/usr/bin/rsync","-a","h:/src","/dst"]`, "--every", "300", "--actor", "a"},
			"service sync fetches on a clock (rsync with --every 300); fetch on an event"},
		{2, []string{"service", "add", "shloop", "--machine", "bench-a", "--argv", `["sh","-c","while true; do git -C /r fetch --all; sleep 60; done"]`, "--keepalive", "true", "--actor", "a"},
			"service shloop fetches on a clock (git fetch with while)"},
		// The D8 shape with the shell's punctuation against the words: ; & | ( ) split a word as white space does.
		{2, []string{"service", "add", "pull-loop", "--machine", "bench-a", "--argv", `["sh","-c","while true; do git pull; sleep 60; done"]`, "--keepalive", "true", "--actor", "a"},
			"service pull-loop fetches on a clock (git pull with while)"},
		{2, []string{"service", "add", "amp", "--machine", "bench-a", "--argv", `["sh","-c","until false; do git fetch&&sleep 60; done"]`, "--keepalive", "true", "--actor", "a"},
			"service amp fetches on a clock (git fetch with until)"},
		{1, []string{"service", "set", "member-b", "--argv", `["(curl","https://x/y)|tee","/l","--interval","60"]`, "--actor", "a"}, "(curl with --interval)"},
		{1, []string{"service", "set", "member-b", "--argv", `["git","pull","--loop=60"]`, "--actor", "a"}, "service member-b fetches on a clock (git pull with --loop)"},
		{1, []string{"service", "set", "member-b", "--every", "60", "--keepalive", "false", "--argv", `["curl","-fsSL","https://x/y"]`, "--actor", "a"}, "(curl with --every 60)"},
		{1, []string{"service", "set", "member-b", "--argv", `["watch","-n","60","wget","https://x/y"]`, "--actor", "a"}, "(wget with watch)"},
	} {
		code, out, errs := h.run(t, c.args...)
		assert.Equal(t, c.code, code, "%v: %s", c.args, errs)
		assert.Empty(t, out, "%v", c.args)
		assert.Contains(t, errs, c.want, "%v", c.args)
		assert.NotContains(t, errs, "hunter2", "%v: a refusal never echoes a password", c.args)
	}
	// A fetch once, at the start of a kept-alive program, is not on a clock; nor is a program that only sounds like one.
	h.ok(t, "service", "set", "member-b", "--argv", `["sh","-c","git pull --ff-only && exec /srv/prog --loops 3"]`, "--actor", "a")
	h.ok(t, "service", "set", "member-b", "--argv", `["/srv/prog","--every","60","--wget-compatible"]`, "--actor", "a")
	h.env = map[string]string{}
	code, _, errs := h.run(t, "machine", "list")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "no store: set NOVA_PG_DSN or --pg (postgres://USER@HOST:PORT/DB?sslmode=disable); run: nova-config machine list -h")
	for _, extra := range [][]string{{}, {"--every", "5", "--keepalive", "true"}, {"--every", "5", "--keys", "K1"}} {
		code, _, errs = h.run(t, append([]string{"service", "add", "s1", "--machine", "m1", "--argv", `["p"]`, "--actor", "a"}, extra...)...)
		assert.Equal(t, 2, code, "%v: %s", extra, errs)
	}
	code, _, errs = h.run(t, "service", "add", "Bad_Name", "--argv", "bad", "--keys", "K1")
	assert.Equal(t, 2, code)
	for _, want := range []string{"missing required flag --machine, --actor", `name "Bad_Name"`, "--argv", "neither --every nor --keepalive", "--keys K1", "no store"} {
		assert.Contains(t, errs, want, "every fault named in one refusal")
	}
	h.env = map[string]string{"NOVA_PG_DSN": "postgres://u@db/x", "USER": "login-u"}
	h.ok(t, "machine", "add", "m-env", "--user", "u")
	assert.Equal(t, "login-u", h.st.history[len(h.st.history)-1].actor)
	h.env["NOVA_ACTOR"] = "nova-a"
	h.ok(t, "machine", "set", "m-env", "--note", "x")
	assert.Equal(t, "nova-a", h.st.history[len(h.st.history)-1].actor)
	assert.Contains(t, h.ok(t, "history", "--max", "1", "--json"), `"actor":"nova-a","kind":"machine","name":"m-env","op":"set"`)
	h = newHarness()
	h.st.failure = errors.New("postgres: down")
	code, _, errs = h.run(t, "service", "list")
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

// TestPublicField: --public takes a public IP literal or a DNS name, shows as "-"
// when unset, is recorded in the history like any field, and "" unsets it.
func TestPublicField(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	// A slice, not a map: the last value set is the "before" asserted below.
	for _, c := range [][2]string{{"203.0.113.7", "203.0.113.7"}, {"2001:db8::0:1", "2001:db8::1"}, {"edge.example.com", "edge.example.com"}, {"a-1.b2.io", "a-1.b2.io"}} {
		h.ok(t, "machine", "set", "bench-a", "--public", c[0], "--actor", "a")
		assert.Equal(t, c[1], h.st.rows["machine"]["bench-a"].F["public"], c[0])
	}
	n := len(h.st.history)
	assert.Equal(t, "CONFIG SET kind=machine name=bench-a rev="+fmt.Sprint(n+1)+" changed=public\n", h.ok(t, "machine", "set", "bench-a", "--public", "dash.example.com", "--actor", "a1"))
	c := h.st.history[n]
	assert.Equal(t, "a-1.b2.io", c.before["public"])
	assert.Equal(t, "dash.example.com", c.after["public"])
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a"), " address=- public=dash.example.com user=nova ")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a", "--json"), `"address":"","public":"dash.example.com","user":"nova"`)
	assert.Contains(t, h.ok(t, "machine", "list", "--json"), `"public":""`, "unset is empty, like the other text fields")
	assert.Contains(t, h.ok(t, "machine", "list"), "MACHINE name=bench-b address=bench-b.tail public=- user=nova")
	h.ok(t, "machine", "set", "bench-a", "--public", "", "--actor", "a")
	assert.Equal(t, "", h.st.rows["machine"]["bench-a"].F["public"], "an empty value unsets it")
	h.ok(t, "machine", "add", "edge", "--user", "u", "--public", "198.51.100.2", "--actor", "a")
	assert.Equal(t, "198.51.100.2", h.st.rows["machine"]["edge"].F["public"])
	assert.Contains(t, h.ok(t, "machine", "add", "-h"), "[--public <addr>]")
	for in, want := range map[string]string{
		"100.101.102.103":                `--public "100.101.102.103": not public: in 100.64.0.0/10, the tailnet's CGNAT range`,
		"10.1.2.3":                       `--public "10.1.2.3": not public: in 10.0.0.0/8, a private range`,
		"172.20.0.1":                     `--public "172.20.0.1": not public: in 172.16.0.0/12, a private range`,
		"192.168.1.1":                    `--public "192.168.1.1": not public: in 192.168.0.0/16, a private range`,
		"::ffff:192.168.1.1":             `not public: in 192.168.0.0/16, a private range`,
		"fd7a:115c:a1e0::1":              `--public "fd7a:115c:a1e0::1": not public: in fc00::/7, a private range (unique local)`,
		"127.0.0.1":                      `not public: in 127.0.0.0/8, the loopback range`,
		"fe80::1%en0":                    `--public "fe80::1%en0": want an address without a zone`,
		"Edge.Example.com":               `--public "Edge.Example.com": want an IP address or a DNS name of lower-case labels (RFC 1123)`,
		"edge_1.example.com":             `--public "edge_1.example.com": want an IP address`,
		"-edge.example.com":              `--public "-edge.example.com": want an IP address`,
		"edge..example.com":              `--public "edge..example.com": want an IP address`,
		"edge.example.com.":              `--public "edge.example.com.": want an IP address`,
		"1.2.3":                          `--public "1.2.3": want an IP address`,
		"256.1.1.1":                      `--public "256.1.1.1": want an IP address`,
		"https://example.com":            `--public "https://example.com": want an IP address`,
		strings.Repeat("a", 64) + ".com": `want an IP address`,
	} {
		code, _, errs := h.run(t, "machine", "set", "bench-a", "--public", in, "--actor", "a")
		assert.Equal(t, 2, code, in)
		assert.Contains(t, errs, want, in)
	}
	assert.Equal(t, "", h.st.rows["machine"]["bench-a"].F["public"], "a refused value writes nothing")
}

func TestHelp(t *testing.T) {
	t.Parallel()
	h := newHarness()
	assert.Equal(t, usage, h.ok(t, "help"))
	assert.Equal(t, usage, h.ok(t))
	code, _, errs := h.run(t, "apply", "--kind", "service")
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

// TestKindsAreTheLibraryColumns keeps the kinds and the config package's row
// types in step: a list verb reads exactly the columns Machines, Friends and
// Services read, in the same order, so the two print the same fields.
func TestKindsAreTheLibraryColumns(t *testing.T) {
	t.Parallel()
	for name, cols := range map[string][]string{"machine": config.Columns[config.Machine](), "friend": config.Columns[config.Friend](), "service": config.Columns[config.Service]()} {
		var fields []string
		for _, f := range kinds[name].fields {
			fields = append(fields, f.name)
		}
		assert.Equal(t, cols, fields, name)
	}
	assert.Len(t, kinds, 3)
}

// TestSQLNamesItsColumns holds the writes to the fields they know, so a store
// that still has old columns is written as is.
func TestSQLNamesItsColumns(t *testing.T) {
	t.Parallel()
	row := Row{Name: "m1", F: map[string]string{"user": "u", "address": "", "public": "", "os": "linux", "arch": "", "cores": "", "runners": "2", "roles": "bench", "runner_scopes": "", "windows_runners": "", "windows_admin_runners": "", "runner_env": "{}", "note": ""}}
	q, args := writeSQL(kinds["machine"], "add", "m1", Row{}, row)
	assert.Equal(t, `INSERT INTO config.machines (name, "address", "public", "user", "os", "arch", "cores", "roles", "runners", "runner_scopes", "windows_runners", "windows_admin_runners", "runner_env", "note") VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`, q)
	assert.Equal(t, []any{"m1", "", nil, "u", "linux", "", nil, "bench", int64(2), "", "", "", "{}", ""}, args)
	old := Row{Name: "m1", F: map[string]string{"user": "u", "address": "", "public": "", "os": "", "arch": "", "cores": "4", "runners": "2", "roles": "bench", "runner_scopes": "", "windows_runners": "", "windows_admin_runners": "", "runner_env": "{}", "note": ""}}
	same := ` AND jsonb_build_object('address', coalesce("address"::text, ''), 'public', coalesce("public"::text, ''), 'user', coalesce("user"::text, ''), 'os', coalesce("os"::text, ''), 'arch', coalesce("arch"::text, ''), 'cores', coalesce("cores"::text, ''), 'roles', coalesce("roles"::text, ''), 'runners', coalesce("runners"::text, ''), 'runner_scopes', coalesce("runner_scopes"::text, ''), 'windows_runners', coalesce("windows_runners"::text, ''), 'windows_admin_runners', coalesce("windows_admin_runners"::text, ''), 'runner_env', coalesce("runner_env"::text, ''), 'note', coalesce("note"::text, '')) = `
	oldJSON := `{"address":"","arch":"","cores":"4","note":"","os":"","public":"","roles":"bench","runner_env":"{}","runner_scopes":"","runners":"2","user":"u","windows_admin_runners":"","windows_runners":""}`
	q, args = writeSQL(kinds["machine"], "set", "m1", old, row)
	assert.True(t, strings.HasPrefix(q, `UPDATE config.machines SET "address" = $2, `) && strings.HasSuffix(q, `, updated_at = now() WHERE name = $1`+same+`$15::jsonb`), q)
	assert.Len(t, args, 15)
	assert.Equal(t, oldJSON, args[14], "a set matches the row only as it was read")
	q, args = writeSQL(kinds["machine"], "remove", "m1", old, Row{})
	assert.Equal(t, "DELETE FROM config.machines WHERE name = $1"+same+"$2::jsonb", q)
	assert.Equal(t, []any{"m1", oldJSON}, args, "a remove matches the row only as it was read")
}

// TestSchemaIsAdditive: schema.sql never drops a column, a table or a row.
func TestSchemaIsAdditive(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	code := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(string(raw), "")
	assert.NotRegexp(t, `(?i)\bdrop\s+(column|table|schema|index)\b|\bdelete\b|\btruncate\b`, code)
}

func TestSchemaVerbPrintsTheSchema(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("schema.sql")
	require.NoError(t, err)
	h := newHarness()
	h.env = map[string]string{} // no store needed
	assert.Equal(t, string(raw), h.ok(t, "schema"))
}

// An old row's role the tool no longer knows is read, listed and put in no group; it never fails a read.
func TestUnknownOldRoleIsRead(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	r := h.st.rows["machine"]["bench-a"]
	r.F["roles"] = "bench,coordination"
	h.st.rows["machine"]["bench-a"] = r
	assert.Contains(t, h.ok(t, "machine", "list"), "roles=bench,coordination")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a"), "roles=bench,coordination")
	assert.Contains(t, h.ok(t, "inventory"), `"bench": {`+"\n"+`    "hosts": [`+"\n"+`      "bench-a"`)
	h.ok(t, "machine", "set", "bench-a", "--note", "n", "--actor", "a")
	assert.Equal(t, "bench,coordination", h.st.rows["machine"]["bench-a"].F["roles"], "a set that leaves roles alone keeps them")
}
