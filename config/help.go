package main

import (
	"fmt"
	"slices"
	"strings"
)

// verbs are the root verbs, for an unknown-verb refusal.
const verbs = "machine, loop, fleet, history, inventory, schema, help"

const trailer = `
Store: --pg <dsn> or NOVA_PG_DSN, postgres://user@host:port/db with no password. Exit codes: 0 done, 1 refused,
2 could not run (a usage error, or no store).
`

const writeLine = "--actor <name> [--reason <line>] [--dry-run]"

// usage is the root help: every verb path by name, each kind and the store.
var usage = func() string {
	var b strings.Builder
	b.WriteString("nova-config: a fleet's machines and loops, kept as rows in PostgreSQL.\n\n")
	for _, n := range kindOrder {
		for _, op := range kinds[n].ops() {
			b.WriteString("  " + kinds[n].opLine(op) + "\n")
		}
	}
	b.WriteString(`  nova-config history [<kind> [<name>]] [--since <duration|RFC3339>] [--max <n>] [--json]
  nova-config inventory [--list | --host <name>]
  nova-config schema
  nova-config help [<verb>]

`)
	for _, n := range kindOrder {
		b.WriteString(n + strings.Repeat(" ", 9-len(n)) + strings.ReplaceAll(kinds[n].about, "\n", "\n         ") + "\n")
	}
	b.WriteString(`
Every verb takes -h or --help and prints its own usage. Every write is one transaction: the row and a history
row naming the actor (and the reason). A printed value is bare, or a JSON string when it holds a space; argv, env
and a note are always JSON. history prints the writes, newest first.
inventory prints the Ansible dynamic inventory, with the groups all, bench, runner and services (each group is
named as its role): use it as ansible-playbook -i <script>, where the script is: #!/bin/sh followed by:
exec nova-config inventory "$@".

A fresh or older store is made current with: nova-config schema | psql "$NOVA_PG_DSN"
Apply it before running this binary against an older store; nothing migrates on start.
NOVA_MACHINE names the machine inventory runs on (else the host name's first label). The password is read from
the variable NOVA_PG_PASSWORD_ENV names, else NOVA_PG_PASSWORD.
` + trailer)
	return b.String()
}()

var kindOrder = []string{"machine", "loop", "fleet"}

func placeholder(f field) string {
	switch f.typ {
	case "int":
		return "<n>"
	case "bool":
		return "<true|false>"
	case "list":
		return "<a,b>"
	case "argv":
		return "<json-array>"
	case "env":
		return "<json-object>"
	case "path":
		return "<path>"
	case "ref":
		return "<machine>"
	}
	return "<text>"
}

// opLine is the usage line of a kind's verb.
func (k *kind) opLine(op string) string {
	head := "nova-config " + k.name + " " + op
	nm := " <name>"
	if k.one {
		nm = ""
	}
	switch op {
	case "list":
		return head + " [--json] [--pg <dsn>]"
	case "show":
		return head + nm + " [--json] [--pg <dsn>]"
	case "remove":
		return head + nm + " " + writeLine
	}
	var fl []string
	for _, f := range k.fields {
		s := "--" + f.name + " " + placeholder(f)
		if !f.required || op == "set" {
			s = "[" + s + "]"
		}
		fl = append(fl, s)
	}
	return head + nm + " " + strings.Join(fl, " ") + " " + writeLine
}

var opAbout = map[string]string{
	"list":   "Prints every row, one line each, then CONFIG LIST. --json prints one JSON object instead.",
	"show":   "Prints one row with its stamps, and a machine's loops. --json prints one JSON object instead.",
	"add":    "Adds a row; a field not given is at its default. The row and its history row are one transaction.",
	"set":    "Changes the fields given (at least one); the others keep their values. --dry-run prints each change as field: old -> new.",
	"remove": "Removes a row; a machine that still runs loops is refused. --dry-run prints the row and removes nothing.",
}

func kindHelp(k *kind) string {
	var b strings.Builder
	b.WriteString("usage: nova-config " + k.name + " " + strings.Join(k.ops(), "|") + " ...\n\n")
	for _, op := range k.ops() {
		b.WriteString("  " + k.opLine(op) + "\n")
	}
	return b.String() + "\n" + k.name + "  " + strings.ReplaceAll(k.about, "\n", "\n"+strings.Repeat(" ", len(k.name)+2)) + "\n" + k.fieldsLine() + "\n" + trailer
}

func opHelp(k *kind, op string) string {
	return "usage: " + k.opLine(op) + "\n\n" + opAbout[op] + "\n" + k.fieldsLine() + "\n" + trailer
}

const historyHelp = `usage: nova-config history [<kind> [<name>]] [--since <duration|RFC3339>] [--max <n>] [--json] [--pg <dsn>]

Prints the writes recorded in the history table, newest first: when, who, kind, name, op, rev (the number a write
printed), then the fields that changed, one per line, as field: old -> new (an add or a remove prints the fields
as field: value) and the reason. kind is machine or loop (fleet, a retired kind, still reads).
--since is a duration (90m, 24h, 7d) back from now, or an RFC 3339 time. --max is the rows shown, default 20,
0 for all; more=true on the closing CONFIG HISTORY line says rows were left out. --json prints one JSON object.
` + trailer

const inventoryHelp = `usage: nova-config inventory [--list | --host <name>] [--pg <dsn>]

Prints Ansible's dynamic inventory as JSON: --list (the default) the whole inventory, --host one machine's
variables. The groups are all, bench, runner and services: each group is named exactly as its role. A machine is
in runner too when its runners is above 0. Each machine has ansible_host, ansible_user, runners, nova_os, nova_arch,
cores (each left out when unset) and nova_loops: its loops, each with name, argv, seat, keys, every, keepalive,
enabled, env (an object) and log (the row's log, else ~/.local/state/nova/loops/<name>.log).
` + trailer

const schemaHelp = `usage: nova-config schema

Prints the schema (schema.sql) and nothing else, and needs no store. Apply it to a fresh or older store with:
nova-config schema | psql "$NOVA_PG_DSN"
It is idempotent, adds what an older store lacks and drops nothing.
`

const helpHelp = `usage: nova-config help [<verb>]

Prints the usage of nova-config, or of one verb: nova-config help machine add. Each verb also takes -h or --help.
`

// helpText is the help of a verb path; ok is false for a path that names no verb.
func helpText(path []string) (string, bool) {
	if len(path) == 0 {
		return usage, true
	}
	if k, ok := kinds[path[0]]; ok {
		switch {
		case len(path) == 1:
			return kindHelp(k), true
		case len(path) == 2 && slices.Contains(k.ops(), path[1]):
			return opHelp(k, path[1]), true
		}
		return "", false
	}
	if len(path) == 1 {
		switch path[0] {
		case "history":
			return historyHelp, true
		case "inventory":
			return inventoryHelp, true
		case "schema":
			return schemaHelp, true
		case "help":
			return helpHelp, true
		}
	}
	return "", false
}

// usageError is a malformed invocation: exit 2, one line naming what is
// wrong, what the verb takes, and where its help is.
type usageError struct{ verb, msg, hint string }

func (u usageError) Error() string {
	line := "nova-config"
	run := "nova-config help"
	if u.verb != "" {
		line += " " + u.verb
		run = "nova-config " + u.verb + " -h"
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

// unknownVerb is the usage error of a verb that does not exist: it is named,
// the verbs are listed, and the root help is pointed at.
func unknownVerb(text string) error {
	return usageError{"", fmt.Sprintf("unknown verb %q", text), "the verbs are " + verbs}
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" || a == "-help" {
			return true
		}
	}
	return false
}
