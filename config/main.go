// Command nova-config keeps a fleet's machines and services, and the friends
// that work on it, as rows in PostgreSQL, each write with a history row, and
// prints the machines and services as an Ansible inventory. Other programs
// read the same rows through package github.com/mas-bandwidth/nova-tools/config/lib.
package main

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	config "github.com/mas-bandwidth/nova-tools/config/lib"
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
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" && len(args) == 1 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] == "help" {
		args = args[1:]
		if len(args) == 1 && wantsHelp(args) {
			args = []string{"help"}
		}
		if text, ok := helpText(args); ok {
			fmt.Fprint(stdout, text)
			return 0
		}
		return fail(stderr, "help", unknownVerb(strings.Join(args[:min(2, len(args))], " ")))
	}
	path, rest := args[:1], args[1:]
	k, isKind := kinds[path[0]]
	if isKind && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		path, rest = args[:2], args[2:]
	}
	name := strings.Join(path, " ")
	if text, ok := helpText(path); ok && wantsHelp(rest) {
		fmt.Fprint(stdout, text)
		return 0
	}
	var err error
	switch {
	case isKind && len(path) == 1:
		err = usagef(name, "the verbs are "+strings.Join(kindOps, ", "), "wants a verb")
	case isKind && slices.Contains(kindOps, path[1]):
		err = runKind(ctx, k, name, rest, d, stdout)
	case path[0] == "schema" && len(rest) > 0:
		err = usagef(name, "", "%s", map[bool]string{true: "unknown flag " + rest[0], false: "takes no arguments"}[strings.HasPrefix(rest[0], "-")])
	case path[0] == "schema":
		fmt.Fprint(stdout, schemaSQL)
		fmt.Fprintln(stderr, "CONFIG SCHEMA OK next="+value(`nova-config schema | PGPASSWORD="$NOVA_PG_PASSWORD" psql "$NOVA_PG_DSN"`))
	case path[0] == "inventory":
		err = runInventory(ctx, rest, d, stdout)
	case path[0] == "history":
		err = runHistory(ctx, rest, d, stdout)
	default:
		err = unknownVerb(strings.Join(args[:min(2, len(args))], " "))
	}
	return fail(stderr, name, err)
}

// fail prints an error as its one line and is the exit code: 0 for none, 1 for a
// refusal, 2 for a usage error or a run that could not happen.
func fail(stderr io.Writer, verb string, err error) int {
	var u usageError
	var r refusal
	switch {
	case err == nil:
		return 0
	case errors.As(err, &u):
		fmt.Fprintln(stderr, escape(err.Error()))
		return 2
	}
	fmt.Fprintf(stderr, "nova-config %s: %s\n", verb, escape(err.Error()))
	if errors.As(err, &r) {
		return 1
	}
	return 2
}

var invalidValue = regexp.MustCompile(`^(?:invalid|parse) .*?(?:value )?("[^"]*") for flag -(\S+):`)

// flags parses a verb's flags around the words before them. A flag it does not
// have is a usage error that names the flag and, as hint, what the verb takes.
func flags(verb, hint string, args []string, define func(*flag.FlagSet)) (*flag.FlagSet, []string, error) {
	fs := flag.NewFlagSet("nova-config "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("pg", "", "the PostgreSQL dsn, with no password (env NOVA_PG_DSN)")
	define(fs)
	var words []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		words, args = append(words, args[0]), args[1:]
	}
	if err := fs.Parse(args); err != nil {
		msg := err.Error()
		if m, ok := strings.CutPrefix(msg, "flag provided but not defined: -"); ok {
			msg = "unknown flag --" + m
		} else if m, ok := strings.CutPrefix(msg, "flag needs an argument: -"); ok {
			msg = "--" + m + " wants a value"
		} else if m := invalidValue.FindStringSubmatch(msg); m != nil {
			msg = fmt.Sprintf("--%s %s: not a valid value", m[2], m[1])
		}
		return nil, nil, usagef(verb, hint, "%s", oneLine(msg))
	}
	return fs, append(words, fs.Args()...), nil
}

// str is a flag's value, empty when the verb has no such flag.
func str(fs *flag.FlagSet, name string) string {
	if f := fs.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

func storeDSN(fs *flag.FlagSet, d deps) (string, string) {
	dsn := cmp.Or(str(fs, "pg"), d.getenv("NOVA_PG_DSN"))
	switch {
	case dsn == "":
		return "", "no store: set NOVA_PG_DSN or --pg (postgres://USER@HOST:PORT/DB?sslmode=disable)"
	case str(fs, "pg") != "" && dsnPassword(dsn):
		return "", "--pg carries a password; leave it out and name its variable in NOVA_PG_PASSWORD_ENV"
	case dsnPassword(dsn):
		return "", "NOVA_PG_DSN carries a password; remove it and set NOVA_PG_PASSWORD"
	case config.DSNProblem(dsn) != "":
		return "", "the postgres dsn is wrong: " + config.DSNProblem(dsn)
	}
	return dsn, ""
}

// open opens the store the flag or the environment names.
func open(ctx context.Context, fs *flag.FlagSet, d deps) (Store, error) {
	dsn, prob := storeDSN(fs, d)
	if prob != "" {
		return nil, usagef(strings.TrimPrefix(fs.Name(), "nova-config "), "", "%s", prob)
	}
	return d.openPG(ctx, dsn, d.getenv(cmp.Or(d.getenv("NOVA_PG_PASSWORD_ENV"), "NOVA_PG_PASSWORD")))
}

func runKind(ctx context.Context, k *kind, verb string, args []string, d deps, stdout io.Writer) error {
	op := verb[len(k.name)+1:]
	write := op == "add" || op == "set" || op == "remove"
	hint := k.fieldsLine()
	fs, words, err := flags(verb, hint, args, func(fs *flag.FlagSet) {
		fs.Bool("json", false, "print one JSON object instead of the lines")
		if !write {
			return
		}
		fs.String("actor", cmp.Or(d.getenv("NOVA_ACTOR"), d.getenv("USER"), d.getenv("LOGNAME")), "the name the write is recorded under")
		fs.String("reason", "", "why: one line, recorded in the history row")
		fs.Bool("dry-run", false, "print the change and write nothing")
		for _, f := range k.fields {
			if op != "remove" {
				fs.String(f.name, "", f.typ)
			}
		}
	})
	if err != nil {
		return err
	}
	given := map[string]string{} // the field flags on the line
	fs.Visit(func(f *flag.Flag) {
		if slices.ContainsFunc(k.fields, func(x field) bool { return x.name == f.Name }) {
			given[f.Name] = f.Value.String()
		}
	})
	if len(words) > 1 {
		return usagef(verb, hint, "unexpected words %q", words[1:])
	}
	name := strings.Join(words, "")
	if (op == "list") != (name == "") {
		return usagef(verb, hint, "wants %s", map[bool]string{true: "no name", false: "a name"}[op == "list"])
	}
	actor, reason := str(fs, "actor"), str(fs, "reason")
	var after Row
	var missing, problems []string
	if write {
		for _, f := range k.fields {
			if _, ok := given[f.name]; op == "add" && f.required && !ok {
				missing = append(missing, "--"+f.name)
			}
		}
		if strings.TrimSpace(actor) == "" {
			missing = append(missing, "--actor")
		}
		if len(missing) > 0 {
			problems = append(problems, "missing required flag "+strings.Join(missing, ", "))
		}
		if strings.ContainsAny(reason, "\r\n") {
			problems = append(problems, "--reason wants one line")
		}
		if op == "set" && len(given) == 0 {
			problems = append(problems, "no field given to set")
		} else if op == "add" {
			after, err = k.newRow(name, given)
		} else if op == "set" {
			_, err = k.merge(Row{Name: name, F: map[string]string{"every": "1"}}, given)
		}
		if err != nil && (op == "add" || errors.As(err, new(valueError))) {
			problems = append(problems, err.Error())
		}
		if _, sp := storeDSN(fs, d); sp != "" {
			problems = append(problems, sp)
		}
		if len(problems) > 0 {
			return usagef(verb, hint, "%s", strings.Join(problems, "; "))
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
	var cur Row
	found := false
	for _, r := range rows {
		if r.Name == name {
			cur, found = r, true
		}
	}
	switch {
	case op == "list":
		printRows(stdout, k, verb, rows, nil, str(fs, "json") == "true")
		return nil
	case op == "add" && found:
		return refused("%s %s exists; use: nova-config %s set %s", k.name, name, k.name, name)
	case op != "add" && !found:
		return refused("%s %s not found; run: nova-config %s list", k.name, name, k.name)
	case op == "show":
		var services []string
		if k.name == "machine" {
			ls, err := st.List(ctx, kinds["service"])
			for _, l := range ls {
				if l.F["machine"] == name {
					services = append(services, l.Name)
				}
			}
			if err != nil {
				return err
			}
		}
		printRows(stdout, k, verb, []Row{cur}, services, str(fs, "json") == "true")
		return nil
	}
	before := cur
	if op == "add" {
		before = Row{}
	} else if op == "set" {
		if after, err = k.merge(cur, given); err != nil {
			return refusal{err.Error()}
		}
	}
	if str(fs, "dry-run") == "true" {
		fmt.Fprintln(stdout, planLines(k, op, name, actor, reason, before.F, after.F))
		return nil
	}
	var changed []string // the fields a set really changes
	for _, n := range slices.Sorted(maps.Keys(given)) {
		if after.F[n] != cur.F[n] {
			changed = append(changed, n)
		}
	}
	if op == "set" && len(changed) == 0 { // nothing differs: no write, no history row
		fmt.Fprintf(stdout, "CONFIG SET kind=%s name=%s changed=none rev=unchanged\n", k.name, value(name))
		return nil
	}
	rev, err := st.Write(ctx, k, op, before, after, actor, reason)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("CONFIG %s kind=%s name=%s rev=%d", strings.ToUpper(op), k.name, value(name), rev)
	if op == "set" {
		line += " changed=" + value(strings.Join(changed, ","))
	}
	if k.name == "machine" && op == "add" {
		line += " next=" + value("nova-fleet join "+name)
	} else if k.name == "service" && op != "remove" {
		line += " next=" + value("nova-fleet services --check")
	}
	fmt.Fprintln(stdout, line)
	return nil
}
