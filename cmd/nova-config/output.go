package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode"
)

// Output grammar. A line is TOKEN then key=value fields. A value is bare when it
// holds nothing a reader could split on, and a JSON string otherwise; an argv or
// env is its JSON; free text (a note, a reason) is always a JSON string; "-" is
// empty. Nothing is escaped any other way, so what is printed can be pasted back.

// oddRunes could hide or reorder text on a terminal: they print as \u escapes.
const oddRunes = "\u2028\u2029\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069"

var oddEscapes = func() *strings.Replacer {
	var pairs []string
	for _, r := range oddRunes {
		pairs = append(pairs, string(r), fmt.Sprintf(`\u%04x`, r))
	}
	return strings.NewReplacer(pairs...)
}()

func jsonString(s string) string { return oddEscapes.Replace(marshal(s)) }

// escape keeps an error message on one line, with nothing that moves the terminal.
func escape(s string) string {
	return oddEscapes.Replace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
}

// value is one key=value token: "-" when empty, bare when it can be, else a JSON string.
func value(s string) string {
	switch {
	case s == "":
		return "-"
	case s != "-" && strings.IndexFunc(s, func(r rune) bool {
		return r == '"' || r == unicode.ReplacementChar || unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(oddRunes, r)
	}) < 0:
		return s
	}
	return jsonString(s)
}

// jsonValue is a field's value as typed JSON: a number, true or false, null for
// an unset int, an argv or env as JSON (re-encoded, so a stray control character
// in an old row cannot reach the terminal), else a string.
func jsonValue(f field, v string) string {
	switch f.typ {
	case "int":
		return cmp.Or(v, "null")
	case "bool":
		return v
	case "argv":
		var a []string
		if json.Unmarshal([]byte(v), &a) == nil {
			return oddEscapes.Replace(marshal(a))
		}
	case "env":
		var m map[string]string
		if json.Unmarshal([]byte(v), &m) == nil {
			return oddEscapes.Replace(marshal(m))
		}
	}
	return jsonString(v)
}

// fieldValue is a field's value as a line prints it.
func fieldValue(f field, v string) string {
	if v != "" && (f.free || f.typ == "argv" || f.typ == "env") {
		return jsonValue(f, v)
	}
	return value(v)
}

// findField is a kind's field by name; a column this tool does not know is text.
func (k *kind) findField(name string) field {
	for _, f := range k.fields {
		if f.name == name {
			return f
		}
	}
	return field{name: name, typ: "text"}
}

func rowLine(k *kind, r Row, cut bool) string {
	line := strings.ToUpper(k.name) + " name=" + value(r.Name)
	for _, f := range k.fields {
		v := r.F[f.name]
		if rs := []rune(v); cut && f.cut && len(rs) > 60 {
			v = string(rs[:60]) + "..."
		}
		line += " " + f.name + "=" + fieldValue(f, v)
	}
	return line
}

// printRows prints list (services nil) or show lines, or one JSON object with typed values.
func printRows(w io.Writer, k *kind, verb string, rows []Row, services []string, asJSON bool) {
	list := strings.HasSuffix(verb, " list")
	var items []string
	for _, r := range rows {
		if !asJSON {
			line := rowLine(k, r, list)
			if !list {
				line += " created=" + value(r.Created) + " updated=" + value(r.Updated)
				if k.name == "machine" {
					line += " services=" + value(strings.Join(services, ","))
				}
			}
			fmt.Fprintln(w, line)
			continue
		}
		pairs := []string{`"name":` + jsonString(r.Name)}
		for _, f := range k.fields {
			pairs = append(pairs, jsonString(f.name)+":"+jsonValue(f, r.F[f.name]))
		}
		if !list {
			pairs = append(pairs, `"created":`+jsonString(r.Created), `"updated":`+jsonString(r.Updated))
			if k.name == "machine" {
				pairs = append(pairs, `"services":`+marshal(append([]string{}, services...)))
			}
		}
		items = append(items, `{"kind":`+jsonString(k.name)+`,"fields":{`+strings.Join(pairs, ",")+`}}`)
	}
	facts := "{}"
	if list {
		facts = fmt.Sprintf(`{"kind":%s,"rows":%d}`, jsonString(k.name), len(rows))
	}
	switch {
	case asJSON:
		fmt.Fprintf(w, `{"result":{"verb":%s,"status":"ok","exit":0},"facts":%s,"items":[%s]}`+"\n", jsonString(verb), facts, strings.Join(items, ","))
	case list:
		fmt.Fprintf(w, "CONFIG LIST kind=%s rows=%d\n", k.name, len(rows))
	}
}

// changeLines are the fields of a write, one line each, indented: `name: value`
// for an add or a remove, `name: before -> after` for those a set changes, values
// as JSON; a "reason" (it rides in the written side) is the last line.
func changeLines(k *kind, op string, before, after map[string]string) []string {
	names := k.names()
	for _, m := range []map[string]string{before, after} { // then the columns the kind does not know
		for _, n := range slices.Sorted(maps.Keys(m)) {
			if !slices.Contains(names, n) && n != "reason" {
				names = append(names, n)
			}
		}
	}
	val := func(f field, v string, ok bool) string {
		if !ok {
			return "null"
		}
		return jsonValue(f, v)
	}
	var out []string
	for _, n := range names {
		f := k.findField(n)
		old, had := before[n]
		cur, has := after[n]
		switch {
		case op == "set" && had == has && old == cur:
		case op == "set":
			out = append(out, "  "+n+": "+val(f, old, had)+" -> "+val(f, cur, has))
		case op == "add" && has:
			out = append(out, "  "+n+": "+val(f, cur, true))
		case op == "remove" && had:
			out = append(out, "  "+n+": "+val(f, old, true))
		}
	}
	if r := before["reason"] + after["reason"]; r != "" {
		out = append(out, "  reason: "+jsonString(r))
	}
	return out
}

// withReason puts a write's reason in its after side, or in its before side
// when there is no after (a remove); with no reason both are as given.
func withReason(before, after map[string]string, reason string) (map[string]string, map[string]string) {
	switch {
	case reason == "":
	case after != nil:
		after = maps.Clone(after)
		after["reason"] = reason
	default:
		before = maps.Clone(before)
		before["reason"] = reason
	}
	return before, after
}

// planLines is a dry run: the event line, then what the write would change.
func planLines(k *kind, op, name, actor, reason string, before, after map[string]string) string {
	before, after = withReason(before, after, reason)
	head := "CONFIG DRY-RUN op=" + op + " kind=" + k.name + " name=" + value(name) + " actor=" + value(actor) + " wrote=nothing"
	return strings.Join(append([]string{head}, changeLines(k, op, before, after)...), "\n")
}

var kindOps = []string{"list", "show", "add", "set", "remove"}

// kindOrder is the kinds in the order help names them.
var kindOrder = []string{"machine", "service", "friend", "harness", "provider", "model", "rented"}

// verbHelp is the help of each verb that is no kind's own, in the order help names them: its path, then its usage
// line and what it does. The root help prints the usage lines and an unknown verb names the root verbs.
var verbHelp = [][2]string{
	{"friend watch", "nova-config friend watch [--output <path>]\nPrints the friend directory as one JSON line now and after each committed friend or harness write, until the store fails; --output replaces that file atomically instead.\n"},
	{"history", "nova-config history [<kind> [<name>]] [--since <duration|RFC3339>] [--max <n>] [--json]\nPrints the writes, newest first, --max of them (default 20, 0 for all; more=true says rows were left out); --since is a duration back from now (90m, 24h, 7d) or an RFC 3339 time.\n"},
	{"inventory", "nova-config inventory [--list | --host <name>]\nPrints Ansible's dynamic inventory as JSON; the groups are all, bench, runner and services.\n"},
	{"schema", "nova-config schema\nPrints schema.sql: apply it with: nova-config schema | PGPASSWORD=\"$NOVA_PG_PASSWORD\" psql \"$NOVA_PG_DSN\"\n"},
	{"help", "nova-config help [<verb>]\n"},
}

// verbs are the root verbs, for an unknown-verb refusal.
var verbs = strings.Join(append(slices.Clone(kindOrder), "history", "inventory", "schema", "help"), ", ")

const writeLine = "[--actor <name>] [--reason <line>] [--dry-run]"

const trailer = `Store: --pg <dsn> or NOVA_PG_DSN (postgres://user@host:port/db, no password; the password is read from
NOVA_PG_PASSWORD). Exit codes: 0 done, 1 refused, 2 could not run.
`

// usage is the root help: every verb path by name, from the kind tables and verbHelp.
var usage = func() string {
	var b strings.Builder
	b.WriteString("nova-config: a fleet's machines, services, friends, model providers, models and rented boxes, kept as rows in PostgreSQL.\n\n")
	for _, n := range kindOrder {
		for _, op := range kindOps {
			b.WriteString("  " + kinds[n].opLine(op) + "\n")
		}
	}
	for _, h := range verbHelp {
		b.WriteString("  " + strings.Split(h[1], "\n")[0] + "\n")
	}
	b.WriteString("\n")
	for _, n := range kindOrder {
		b.WriteString(kinds[n].fieldsLine() + "\n")
	}
	return b.String() + `A model's harness speaks its provider's api (claude: anthropic; opencode, codex: openai).
Every verb takes -h or --help. Use inventory as an Ansible script: #!/bin/sh, then: exec nova-config inventory "$@".
Make a store current with: nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"
` + trailer
}()

var placeholders = map[string]string{"int": "<n>", "bool": "<true|false>", "list": "<a,b>", "argv": "<json-array>",
	"env": "<json-object>", "path": "<path>", "ref": "<machine>", "public": "<addr>",
	"scopes": "<url=n[:label+label],...>", "admins": "<url=account:label[+label]@workflow,...>",
	"price": "<usd-per-hour>", "time": "<rfc3339>", "url": "<url>", "secret": "<secret-name>"}

// opLine is the usage line of a kind's verb, from its field table.
func (k *kind) opLine(op string) string {
	head := "nova-config " + k.name + " " + op
	switch op {
	case "list":
		return head + " [--json]"
	case "show":
		return head + " <name> [--json]"
	case "remove":
		return head + " <name> " + writeLine
	}
	fl := ""
	for _, f := range append(slices.Clip(k.fields), k.extra...) {
		p := cmp.Or(f.hint, placeholders[f.typ], "<text>")
		if f.typ == "word" {
			p = "<" + strings.Trim(f.word.String(), "^()$") + ">"
		}
		s := "--" + f.name + " " + p
		if !f.required || op == "set" {
			s = "[" + s + "]"
		}
		fl += " " + s
	}
	return head + " <name>" + fl + " " + writeLine
}

// helpText is the help of a verb path: its usage line and fields; ok is false for a path that names no verb.
func helpText(path []string) (string, bool) {
	for _, h := range verbHelp {
		if h[0] == strings.Join(path, " ") {
			return "usage: " + h[1], true
		}
	}
	var lines []string
	k, ok := kinds[path[0]]
	switch {
	case ok && len(path) == 1:
		for _, op := range kindOps {
			lines = append(lines, k.opLine(op))
		}
	case ok && len(path) == 2 && slices.Contains(kindOps, path[1]):
		lines = []string{k.opLine(path[1])}
	default:
		return "", false
	}
	return "usage: " + strings.Join(lines, "\n       ") + "\n" + k.fieldsLine() + "\n" + k.note, true
}

// usageError is a malformed invocation: exit 2, one line naming what is
// wrong, what the verb takes, and where its help is.
type usageError struct{ verb, msg, hint string }

func (u usageError) Error() string {
	line, run := "nova-config", "nova-config help"
	if u.verb != "" {
		line, run = line+" "+u.verb, "nova-config "+u.verb+" -h"
	}
	line += ": " + u.msg
	if u.hint != "" {
		line += "; " + u.hint
	}
	return line + "; run: " + run
}

func usagef(verb, hint, format string, a ...any) error {
	return usageError{verb, fmt.Sprintf(format, a...), hint}
}

// unknownVerb is the usage error of a verb that does not exist.
func unknownVerb(text string) error {
	return usageError{"", fmt.Sprintf("unknown verb %q", text), "the verbs are " + verbs}
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}
