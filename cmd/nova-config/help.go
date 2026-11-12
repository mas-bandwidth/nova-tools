package main

import (
	"fmt"
	"slices"
	"strings"
)

var kindOps = []string{"list", "show", "add", "set", "remove"}

// verbs are the root verbs, for an unknown-verb refusal.
const verbs = "machine, service, friend, history, inventory, schema, help"

const writeLine = "[--actor <name>] [--reason <line>] [--dry-run]"

const historyLine = "nova-config history [<kind> [<name>]] [--since <duration|RFC3339>] [--max <n>] [--json]"

const trailer = `Store: --pg <dsn> or NOVA_PG_DSN (postgres://user@host:port/db, no password; the password is read from
NOVA_PG_PASSWORD). Exit codes: 0 done, 1 refused, 2 could not run.
`

// usage is the root help: every verb path by name, from the kind tables.
var usage = func() string {
	var b strings.Builder
	b.WriteString("nova-config: a fleet's machines, services and friends, kept as rows in PostgreSQL.\n\n")
	for _, n := range []string{"machine", "service", "friend"} {
		for _, op := range kindOps {
			b.WriteString("  " + kinds[n].opLine(op) + "\n")
		}
	}
	return b.String() + "  " + historyLine + "\n  nova-config inventory [--list | --host <name>]\n  nova-config schema\n  nova-config help [<verb>]\n\n" +
		kinds["machine"].fieldsLine() + "\n" + kinds["service"].fieldsLine() + "\n" + kinds["friend"].fieldsLine() + "\n" +
		`Every verb takes -h or --help. Use inventory as an Ansible script: #!/bin/sh, then: exec nova-config inventory "$@".
Make a store current with: nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"
` + trailer
}()

var placeholders = map[string]string{"int": "<n>", "bool": "<true|false>", "list": "<a,b>", "argv": "<json-array>",
	"env": "<json-object>", "path": "<path>", "ref": "<machine>", "public": "<addr>",
	"scopes": "<url=n[:label+label],...>", "admins": "<url=account:label[+label],...>"}

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
	for _, f := range k.fields {
		p := placeholders[f.typ]
		if p == "" {
			p = "<text>"
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
	var lines []string
	if k, ok := kinds[path[0]]; ok {
		switch {
		case len(path) == 1:
			for _, op := range kindOps {
				lines = append(lines, k.opLine(op))
			}
		case len(path) == 2 && slices.Contains(kindOps, path[1]):
			lines = []string{k.opLine(path[1])}
		default:
			return "", false
		}
		return "usage: " + strings.Join(lines, "\n       ") + "\n" + k.fieldsLine() + "\n", true
	}
	if len(path) != 1 {
		return "", false
	}
	switch path[0] {
	case "history":
		return "usage: " + historyLine + "\nPrints the writes, newest first, --max of them (default 20, 0 for all; more=true says rows were left out); --since is a duration back from now (90m, 24h, 7d) or an RFC 3339 time.\n", true
	case "inventory":
		return "usage: nova-config inventory [--list | --host <name>]\nPrints Ansible's dynamic inventory as JSON; the groups are all, bench, runner and services.\n", true
	case "schema":
		return "usage: nova-config schema\nPrints schema.sql: apply it with: nova-config schema | PGPASSWORD=\"$NOVA_PG_PASSWORD\" psql \"$NOVA_PG_DSN\"\n", true
	case "help":
		return "usage: nova-config help [<verb>]\n", true
	}
	return "", false
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
