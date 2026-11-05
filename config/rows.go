package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// field is one column of a kind's table. typ is text, int, bool, list (a
// sorted comma list of words matching word), argv (a JSON array of strings),
// env (a JSON object of NAME to value), path (an absolute or ~/ path) or ref
// (the name of a machine row, NULL when empty).
type field struct {
	name, typ string
	required  bool
	def       string
	null      bool // an int that may be unset: the value "default" unsets it
	cut       bool // cut to 60 characters in list lines
	free      bool // free text: always printed as a JSON string
	word      *regexp.Regexp
}

type kind struct {
	name, table string
	about       string // what the kind is, for its help
	one         bool   // the fleet: one row, named after the kind, only show and set
	fields      []field
	check       func(Row) error
}

// Row is one row: its name, its fields as text, and its stamps (RFC 3339).
type Row struct {
	Name             string
	F                map[string]string
	Created, Updated string
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	roleWord    = regexp.MustCompile(`^(bench|runner|services)$`)
	keyWord     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envName     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

var kinds = map[string]*kind{
	"fleet": {name: "fleet", table: "fleet", one: true, check: checkFleet,
		about: "the one fleet row: pg_dsn (no password), the store's address; the inventory emits it as nova_pg_dsn",
		fields: []field{
			{name: "pg_dsn", typ: "text"},
		}},
	"machine": {name: "machine", table: "machines",
		about: "a machine, named by its tailnet host: user (required), address (what ssh dials, else the name), os, arch,\ncores (default unsets it), roles (bench,runner,services), runners (CI runners on it), note",
		fields: []field{
			{name: "address", typ: "text"},
			{name: "user", typ: "text", required: true},
			{name: "os", typ: "text"},
			{name: "arch", typ: "text"},
			{name: "cores", typ: "int", null: true},
			{name: "roles", typ: "list", def: "bench", word: roleWord},
			{name: "runners", typ: "int"},
			{name: "note", typ: "text", cut: true, free: true},
		}},
	"loop": {name: "loop", table: "loops", check: checkLoop,
		about: "a command a machine runs under launchd or systemd: machine and argv (a JSON array, the program first) are\nrequired; seat; keys (secret NAMES, never values); every (seconds) or keepalive true; enabled; env (a JSON\nobject of NAME to value, names [A-Z_][A-Z0-9_]*); log (a path, default " + loopLogDir + "/<name>.log)",
		fields: []field{
			{name: "machine", typ: "ref", required: true},
			{name: "argv", typ: "argv", required: true},
			{name: "seat", typ: "text"},
			{name: "keys", typ: "list", word: keyWord},
			{name: "every", typ: "int"},
			{name: "keepalive", typ: "bool"},
			{name: "enabled", typ: "bool", def: "true"},
			{name: "env", typ: "env", def: "{}"},
			{name: "log", typ: "path"},
		}},
}

// ops are the verbs of a kind: the fleet has one row, so only show and set.
func (k *kind) ops() []string {
	if k.one {
		return []string{"show", "set"}
	}
	return []string{"list", "show", "add", "set", "remove"}
}

// fieldsLine names a kind's fields on one line, for a usage error and for help.
func (k *kind) fieldsLine() string {
	names := []string{"name"}
	for _, f := range k.fields {
		names = append(names, f.name)
	}
	return k.name + " fields: " + strings.Join(names, ", ")
}

// missing is the required fields a add does not give.
func (k *kind) missing(given map[string]bool) []string {
	var out []string
	for _, f := range k.fields {
		if f.required && !given[f.name] {
			out = append(out, "--"+f.name)
		}
	}
	return out
}

func (f field) canonical(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch f.typ {
	case "int":
		if f.null && raw == "default" {
			return "", nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return "", fmt.Errorf("--%s %q: want a non-negative integer", f.name, raw)
		}
		return strconv.Itoa(n), nil
	case "bool":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return "", fmt.Errorf("--%s %q: want true or false", f.name, raw)
		}
		return strconv.FormatBool(b), nil
	case "list":
		words := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
		sort.Strings(words)
		words = slices.Compact(words)
		for _, w := range words {
			if !f.word.MatchString(w) {
				return "", fmt.Errorf("--%s %q: want a comma list of words matching %s", f.name, w, f.word)
			}
		}
		return strings.Join(words, ","), nil
	case "argv":
		var words []string
		dec := json.NewDecoder(strings.NewReader(raw))
		if !strings.HasPrefix(raw, "[") || dec.Decode(&words) != nil || dec.More() || len(words) == 0 || words[0] == "" {
			return "", fmt.Errorf(`--%s: want the command as a JSON array of strings, the program first: '["/path/prog","--flag","v"]'`, f.name)
		}
		return marshal(words), nil
	case "env":
		return canonicalEnv(f, raw)
	case "path":
		if raw != "" && !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "~/") {
			return "", fmt.Errorf("--%s %q: want an absolute path or one starting ~/", f.name, raw)
		}
	case "ref":
		if raw != "" && !namePattern.MatchString(raw) {
			return "", fmt.Errorf("--%s %q: want the name of a machine row", f.name, raw)
		}
		if raw == "" && f.required {
			return "", fmt.Errorf("--%s: want the name of a machine row", f.name)
		}
	}
	if strings.ContainsAny(raw, "\r\n") {
		return "", fmt.Errorf("--%s: want one line", f.name)
	}
	if raw == "" && f.required {
		return "", fmt.Errorf("--%s: want a value", f.name)
	}
	return raw, nil
}

// canonicalEnv is an env object as it is stored: keys sorted, one line; the
// names are checked against envName, and every value must be a string.
func canonicalEnv(f field, raw string) (string, error) {
	if raw == "" {
		return "{}", nil
	}
	var m map[string]string
	dec := json.NewDecoder(strings.NewReader(raw))
	if !strings.HasPrefix(raw, "{") || dec.Decode(&m) != nil || dec.More() || m == nil {
		return "", fmt.Errorf(`--%s: want a JSON object of NAME to value strings: '{"NAME":"value"}'`, f.name)
	}
	for _, n := range slices.Sorted(maps.Keys(m)) {
		if !envName.MatchString(n) {
			return "", fmt.Errorf("--%s name %q: want upper-case letters, digits and underscores, not starting with a digit (%s)", f.name, n, envName)
		}
	}
	return marshal(m), nil
}

// marshal is v as compact JSON, with < > & left alone.
func marshal(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // ignored: a []string or map of strings always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

func (f field) zero() string {
	switch {
	case f.null:
		return ""
	case f.def != "":
		return f.def
	case f.typ == "int":
		return "0"
	case f.typ == "bool":
		return "false"
	}
	return ""
}

// newRow is the row add writes: the given fields, the rest at their zero.
func (k *kind) newRow(name string, raw map[string]string) (Row, error) {
	if len(name) > 128 || !namePattern.MatchString(name) {
		return Row{}, fmt.Errorf("name %q: want lower-case letters, digits and dashes, starting with a letter or digit", name)
	}
	empty := Row{Name: name, F: map[string]string{}}
	for _, f := range k.fields {
		empty.F[f.name] = f.zero()
	}
	return k.merge(empty, raw)
}

// merge is the row a set leaves: cur with the given fields, checked whole.
func (k *kind) merge(cur Row, raw map[string]string) (Row, error) {
	next := Row{Name: cur.Name, F: maps.Clone(cur.F)}
	var problems []string
	for _, f := range k.fields {
		if v, ok := raw[f.name]; ok {
			c, err := f.canonical(v)
			if err != nil {
				problems = append(problems, err.Error())
			}
			next.F[f.name] = c
		}
	}
	if len(problems) == 0 && k.check != nil {
		if err := k.check(next); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return Row{}, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return next, nil
}

func checkLoop(r Row) error {
	periodic, kept := r.F["every"] != "0", r.F["keepalive"] == "true"
	switch {
	case periodic && kept:
		return fmt.Errorf("loop %s has --every %s and --keepalive true; a loop runs every n seconds or is kept alive, so set one: --every 0 or --keepalive false", r.Name, r.F["every"])
	case !periodic && !kept:
		return fmt.Errorf("loop %s has neither --every nor --keepalive; want --every <seconds> or --keepalive true", r.Name)
	case r.F["keys"] != "" && r.F["seat"] == "":
		return fmt.Errorf("loop %s names secrets (--keys %s) and no --seat to open them from", r.Name, r.F["keys"])
	}
	return nil
}

func checkFleet(r Row) error {
	if dsnPassword(r.F["pg_dsn"]) {
		return fmt.Errorf("--pg_dsn carries a password; leave it out")
	}
	return nil
}

// dsnPassword says a postgres URI carries a password, in its user part or its query.
func dsnPassword(dsn string) bool {
	u, err := url.Parse(dsn)
	if err != nil {
		return strings.Contains(strings.ToLower(dsn), "password")
	}
	_, has := u.User.Password()
	q, _ := url.ParseQuery(u.RawQuery)
	for k := range q {
		has = has || strings.EqualFold(k, "password")
	}
	return has
}
