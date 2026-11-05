// Command nova-config keeps a fleet's machines and loops as rows in PostgreSQL,
// each write with a history row, and prints them as an Ansible inventory.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

const usage = `nova-config: a fleet's machines and loops, kept as rows in PostgreSQL.

  nova-config machine|loop list [--json]
  nova-config machine|loop show <name> [--json]
  nova-config machine|loop add <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
  nova-config machine|loop set <name> --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
  nova-config machine|loop remove <name> --actor <name> [--reason <line>] [--dry-run]
  nova-config fleet show [--json]
  nova-config fleet set --<field> <value> ... --actor <name> [--reason <line>] [--dry-run]
  nova-config inventory [--list | --host <name>] [--redis <host:port>]
  nova-config version

machine  a machine, named by its tailnet host (ssh <name> reaches it): user, seat, slots
         (required), runners, roles (bench,coordination,runner,services), width (default
         unsets it), gates_only, tla, note
loop     a command a machine runs under launchd or systemd: machine, argv (a JSON array,
         required), seat, keys (secret NAMES, never values), every (seconds) or keepalive
         true, enabled
fleet    the one fleet row: store and coordinator (machine names), redis_port, pg_dsn (no
         password), bus (host:port), loops_dir (where loop logs go)

Every write is one transaction: the row and a history row naming the actor (and the reason).
inventory prints the Ansible dynamic inventory: use it as ansible-playbook -i <script>, where
the script is: #!/bin/sh followed by: exec nova-config inventory "$@". --redis (or
NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR) adds each machine's reported os, arch and cores.

Store: --pg <dsn> or NOVA_PG_DSN, postgres://user@host:port/db with no password; the password
is read from the variable NOVA_PG_PASSWORD_ENV names, else NOVA_PG_PASSWORD. Redis login:
NOVA_SPRINT_REDIS_USER, and the password from the variable NOVA_SPRINT_REDIS_PASSWORD_ENV
names. NOVA_MACHINE names the machine inventory runs on (else the host name's first label).
A fresh store is made with: psql <dsn> -f schema.sql
Exit codes: 0 done, 1 refused, 2 could not run.
`

// deps is what run reaches outside the process through.
type deps struct {
	getenv   func(string) string
	hostname func() (string, error)
	openPG   func(ctx context.Context, dsn, password string) (Store, error)
	beats    func(ctx context.Context, addr, user, password string, machines []string) (map[string]*Beat, error)
}

func main() {
	d := deps{getenv: os.Getenv, hostname: os.Hostname, openPG: openPG, beats: redisBeats}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	code := run(ctx, os.Args[1:], d, os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

// refusal is a refusal of the store or of the row: exit 1, never 2.
type refusal struct{ msg string }

func (r refusal) Error() string { return r.msg }

func refused(format string, a ...any) error { return refusal{fmt.Sprintf(format, a...)} }

func run(ctx context.Context, args []string, d deps, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	verb, rest := args[0], args[1:]
	if k, ok := kinds[verb]; ok && len(rest) > 0 {
		verb, rest = verb+" "+rest[0], rest[1:]
		if err := runKind(ctx, k, verb, rest, d, stdout, stderr); err != nil {
			return fail(stderr, verb, err)
		}
		return 0
	}
	switch verb {
	case "version":
		v := "(devel)"
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
			v = bi.Main.Version
		}
		fmt.Fprintf(stdout, "nova-config %s %s/%s %s\n", v, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return 0
	case "inventory":
		if err := runInventory(ctx, rest, d, stdout, stderr); err != nil {
			return fail(stderr, verb, err)
		}
		return 0
	}
	return fail(stderr, verb, fmt.Errorf("unknown verb %q; run: nova-config help", strings.Join(args[:min(2, len(args))], " ")))
}

func fail(stderr io.Writer, verb string, err error) int {
	fmt.Fprintf(stderr, "nova-config %s REFUSED: %s\n", verb, escape(err.Error()))
	var r refusal
	if errors.As(err, &r) {
		return 1
	}
	return 2
}

// flags parses a verb's flags around one optional leading name.
func flags(verb string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (*flag.FlagSet, string, map[string]bool, error) {
	fs := flag.NewFlagSet("nova-config "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintf(stderr, "usage: nova-config %s; the flags:\n", verb); fs.PrintDefaults() }
	fs.String("pg", "", "the PostgreSQL `dsn`, with no password (env NOVA_PG_DSN)")
	define(fs)
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, "", nil, err
	}
	rest := fs.Args()
	if name == "" && len(rest) > 0 {
		name, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 {
		return nil, "", nil, fmt.Errorf("unexpected words %q", rest)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return fs, name, set, nil
}

func str(fs *flag.FlagSet, name string) string { return fs.Lookup(name).Value.String() }

// open opens the store the flag or the environment names.
func open(ctx context.Context, fs *flag.FlagSet, d deps) (Store, error) {
	dsn := str(fs, "pg")
	if dsn == "" {
		dsn = d.getenv("NOVA_PG_DSN")
	}
	if dsn == "" {
		return nil, errors.New("no store: give --pg postgres://user@host:5432/db or set NOVA_PG_DSN")
	}
	if str(fs, "pg") != "" && dsnPassword(dsn) {
		return nil, errors.New("--pg carries a password; leave it out and name its variable in NOVA_PG_PASSWORD_ENV")
	}
	pe := d.getenv("NOVA_PG_PASSWORD_ENV")
	if pe == "" {
		pe = "NOVA_PG_PASSWORD"
	}
	return d.openPG(ctx, dsn, d.getenv(pe))
}

func runKind(ctx context.Context, k *kind, verb string, args []string, d deps, stdout, stderr io.Writer) error {
	op := verb[len(k.name)+1:]
	write := op == "add" || op == "set" || op == "remove"
	if !slices.Contains([]string{"list", "show", "add", "set", "remove"}, op) || (k.one && op != "show" && op != "set") {
		return fmt.Errorf("unknown verb %q; run: nova-config help", verb)
	}
	fs, name, given, err := flags(verb, args, stderr, func(fs *flag.FlagSet) {
		fs.Bool("json", false, "print one JSON object instead of the lines")
		if !write {
			return
		}
		fs.String("actor", "", "the `name` the write is recorded under (required)")
		fs.String("reason", "", "why: one line, recorded in the history row")
		fs.Bool("dry-run", false, "print the change and write nothing")
		if op != "remove" {
			for _, f := range k.fields {
				fs.String(f.name, "", f.typ+" field")
			}
		}
	})
	if err != nil {
		return err
	}
	if k.one {
		name = k.name
	}
	if (op == "list") != (name == "") {
		return fmt.Errorf("%s wants %s", verb, map[bool]string{true: "no name", false: "a name"}[op == "list"])
	}
	actor, reason := "", ""
	if write {
		actor = str(fs, "actor")
		if reason = str(fs, "reason"); strings.ContainsAny(reason, "\r\n") {
			return errors.New("--reason wants one line")
		}
		if strings.TrimSpace(actor) == "" {
			return errors.New("--actor is required: the name the write is recorded under")
		}
	}
	st, err := open(ctx, fs, d)
	if err != nil {
		return err
	}
	defer st.Close()
	rows, err := st.List(ctx, k)
	if err != nil {
		return err
	}
	cur, found := find(rows, name)
	asJSON := fs.Lookup("json").Value.String() == "true"
	switch op {
	case "list":
		return printRows(stdout, k, verb, rows, nil, asJSON)
	case "show":
		if !found {
			return refused("%s %s not found", k.name, name)
		}
		var loops []string
		if k.name == "machine" {
			if loops, err = loopsOn(ctx, st, name); err != nil {
				return err
			}
		}
		return printRows(stdout, k, verb, []Row{cur}, loops, asJSON)
	}
	before, after := cur, Row{}
	switch {
	case op == "add" && found:
		return refused("%s %s exists", k.name, name)
	case op != "add" && !found:
		return refused("%s %s not found", k.name, name)
	case op == "add":
		before = Row{}
		if after, err = k.newRow(name, values(fs, k, given)); err != nil {
			return refusal{err.Error()}
		}
	case op == "set":
		changes := values(fs, k, given)
		if len(changes) == 0 {
			return errors.New("set names no field")
		}
		if after, err = k.merge(cur, changes); err != nil {
			return refusal{err.Error()}
		}
	}
	if fs.Lookup("dry-run").Value.String() == "true" {
		fmt.Fprintln(stdout, planLine(k.name, op, name, actor, reason, before.F, after.F))
		return nil
	}
	rev, err := st.Write(ctx, k, op, before, after, actor, reason)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("CONFIG %s kind=%s name=%s rev=%d", strings.ToUpper(op), k.name, value(name), rev)
	if op == "set" {
		changes := values(fs, k, given)
		line += " changed=" + value(strings.Join(slices.Sorted(maps.Keys(changes)), ","))
	}
	fmt.Fprintln(stdout, line)
	return nil
}

// values is the field flags given on the line.
func values(fs *flag.FlagSet, k *kind, given map[string]bool) map[string]string {
	out := map[string]string{}
	for _, f := range k.fields {
		if given[f.name] {
			out[f.name] = str(fs, f.name)
		}
	}
	return out
}

func find(rows []Row, name string) (Row, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return Row{}, false
}

func loopsOn(ctx context.Context, st Store, machine string) ([]string, error) {
	rows, err := st.List(ctx, kinds["loop"])
	var out []string
	for _, r := range rows {
		if r.F["machine"] == machine {
			out = append(out, r.Name)
		}
	}
	return out, err
}
