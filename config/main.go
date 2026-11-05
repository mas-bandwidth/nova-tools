// Command nova-config keeps a fleet's machines and loops as rows in PostgreSQL,
// each write with a history row, and prints them as an Ansible inventory.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
)

//go:embed schema.sql
var schemaSQL string

// deps is what run reaches outside the process through.
type deps struct {
	getenv   func(string) string
	hostname func() (string, error)
	now      func() time.Time
	openPG   func(ctx context.Context, dsn, password string) (Store, error)
}

func main() {
	d := deps{getenv: os.Getenv, hostname: os.Hostname, now: time.Now, openPG: openPG}
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
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] == "help" {
		if len(args) == 2 && wantsHelp(args[1:]) {
			args = []string{"help", "help"}
		}
		if text, ok := helpText(args[1:]); ok {
			fmt.Fprint(stdout, text)
			return 0
		}
		return fail(stderr, "help", unknownVerb(strings.Join(args[1:min(3, len(args))], " ")))
	}
	verb, rest := args[0], args[1:]
	path := []string{verb}
	k, isKind := kinds[verb]
	if isKind && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		path, rest = append(path, rest[0]), rest[1:]
	}
	if text, ok := helpText(path); ok && wantsHelp(rest) {
		fmt.Fprint(stdout, text)
		return 0
	}
	name := strings.Join(path, " ")
	switch {
	case isKind && len(path) == 1:
		return fail(stderr, name, usagef(name, "the verbs are "+strings.Join(k.ops(), ", "), "wants a verb"))
	case isKind && slices.Contains(k.ops(), path[1]):
		if err := runKind(ctx, k, name, rest, d, stdout); err != nil {
			return fail(stderr, name, err)
		}
		return 0
	case isKind:
		return fail(stderr, name, unknownVerb(name))
	case verb == "schema":
		if len(rest) > 0 {
			msg := "takes no arguments"
			if strings.HasPrefix(rest[0], "-") {
				msg = "unknown flag " + rest[0]
			}
			return fail(stderr, verb, usagef(verb, "", "%s", msg))
		}
		fmt.Fprint(stdout, schemaSQL)
		return 0
	case verb == "inventory":
		if err := runInventory(ctx, rest, d, stdout); err != nil {
			return fail(stderr, verb, err)
		}
		return 0
	case verb == "history":
		if err := runHistory(ctx, rest, d, stdout); err != nil {
			return fail(stderr, verb, err)
		}
		return 0
	}
	return fail(stderr, verb, unknownVerb(strings.Join(args[:min(2, len(args))], " ")))
}

func fail(stderr io.Writer, verb string, err error) int {
	var u usageError
	if errors.As(err, &u) {
		fmt.Fprintln(stderr, escape(err.Error()))
		return 2
	}
	fmt.Fprintf(stderr, "nova-config %s: %s\n", verb, escape(err.Error()))
	var r refusal
	if errors.As(err, &r) {
		return 1
	}
	return 2
}

// flags parses a verb's flags around the words before them: the names a verb
// takes. A flag it does not have is a usage error that names the flag and, as
// hint, what the verb does take.
func flags(verb, hint string, args []string, define func(*flag.FlagSet)) (*flag.FlagSet, []string, map[string]bool, error) {
	fs := flag.NewFlagSet("nova-config "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("pg", "", "the PostgreSQL dsn, with no password (env NOVA_PG_DSN)")
	define(fs)
	var words []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		words, args = append(words, args[0]), args[1:]
	}
	if err := fs.Parse(args); err != nil {
		msg := strings.TrimPrefix(err.Error(), "flag provided but not defined: -")
		if msg != err.Error() {
			msg = "unknown flag --" + msg
		} else if m := strings.TrimPrefix(err.Error(), "flag needs an argument: -"); m != err.Error() {
			msg = "--" + m + " wants a value"
		}
		return nil, nil, nil, usagef(verb, hint, "%s", oneLine(msg))
	}
	words = append(words, fs.Args()...)
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return fs, words, set, nil
}

func str(fs *flag.FlagSet, name string) string { return fs.Lookup(name).Value.String() }

// open opens the store the flag or the environment names.
func open(ctx context.Context, fs *flag.FlagSet, d deps) (Store, error) {
	verb := strings.TrimPrefix(fs.Name(), "nova-config ")
	dsn := str(fs, "pg")
	if dsn == "" {
		dsn = d.getenv("NOVA_PG_DSN")
	}
	if dsn == "" {
		return nil, usagef(verb, "", "no store: set NOVA_PG_DSN or --pg")
	}
	if str(fs, "pg") != "" && dsnPassword(dsn) {
		return nil, usagef(verb, "", "--pg carries a password; leave it out and name its variable in NOVA_PG_PASSWORD_ENV")
	}
	pe := d.getenv("NOVA_PG_PASSWORD_ENV")
	if pe == "" {
		pe = "NOVA_PG_PASSWORD"
	}
	return d.openPG(ctx, dsn, d.getenv(pe))
}

func runKind(ctx context.Context, k *kind, verb string, args []string, d deps, stdout io.Writer) error {
	op := verb[len(k.name)+1:]
	write := op == "add" || op == "set" || op == "remove"
	hint := k.fieldsLine()
	fs, words, given, err := flags(verb, hint, args, func(fs *flag.FlagSet) {
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
	name := ""
	switch {
	case k.one && len(words) > 0, len(words) > 1:
		return usagef(verb, hint, "unexpected words %q", words[1:])
	case len(words) == 1:
		name = words[0]
	}
	if k.one {
		name = k.name
	}
	if (op == "list") != (name == "") {
		return usagef(verb, hint, "wants %s", map[bool]string{true: "no name", false: "a name"}[op == "list"])
	}
	actor, reason := "", ""
	if write {
		actor = str(fs, "actor")
		var missing []string
		if op == "add" {
			missing = k.missing(given)
		}
		if strings.TrimSpace(actor) == "" {
			missing = append(missing, "--actor")
		}
		if len(missing) > 0 {
			return usagef(verb, hint, "missing required flag %s", strings.Join(missing, ", "))
		}
		if reason = str(fs, "reason"); strings.ContainsAny(reason, "\r\n") {
			return usagef(verb, hint, "--reason wants one line")
		}
		if op == "set" && len(values(fs, k, given)) == 0 {
			return usagef(verb, hint, "no field given to set")
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
			return refused("%s %s not found; run: nova-config %s list", k.name, name, k.name)
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
		return refused("%s %s exists; use: nova-config %s set", k.name, name, k.name)
	case op != "add" && !found:
		return refused("%s %s not found; run: nova-config %s list", k.name, name, k.name)
	case op == "add":
		before = Row{}
		if after, err = k.newRow(name, values(fs, k, given)); err != nil {
			return refusal{err.Error()}
		}
	case op == "set":
		if after, err = k.merge(cur, values(fs, k, given)); err != nil {
			return refusal{err.Error()}
		}
	}
	if fs.Lookup("dry-run").Value.String() == "true" {
		fmt.Fprintln(stdout, planLines(k, op, name, actor, reason, before.F, after.F))
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
