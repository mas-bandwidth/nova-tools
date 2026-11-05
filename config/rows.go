package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// field is one column of a kind's table. typ is text, int, bool, list (a
// sorted comma list of words matching word), argv (a JSON array of strings)
// or ref (the name of a machine row, NULL when empty).
type field struct {
	name, typ string
	required  bool
	def       string
	null      bool // an int that may be unset: the value "default" unsets it
	cut       bool // cut to 60 characters in list lines
	word      *regexp.Regexp
}

type kind struct {
	name, table string
	one         bool // the fleet: one row, named after the kind, only show and set
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
)

var kinds = map[string]*kind{
	"machine": {name: "machine", table: "machines", fields: []field{
		{name: "user", typ: "text", required: true},
		{name: "address", typ: "text"},
		{name: "os", typ: "text"},
		{name: "arch", typ: "text"},
		{name: "cores", typ: "int", null: true},
		{name: "runners", typ: "int"},
		{name: "roles", typ: "list", def: "bench", word: roleWord},
		{name: "note", typ: "text", cut: true},
	}},
	"fleet": {name: "fleet", table: "fleet", one: true, check: checkFleet, fields: []field{
		{name: "pg_dsn", typ: "text"},
	}},
	"loop": {name: "loop", table: "loops", check: checkLoop, fields: []field{
		{name: "machine", typ: "ref", required: true},
		{name: "argv", typ: "argv", required: true},
		{name: "seat", typ: "text"},
		{name: "keys", typ: "list", word: keyWord},
		{name: "every", typ: "int"},
		{name: "keepalive", typ: "bool"},
		{name: "enabled", typ: "bool", def: "true"},
	}},
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
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(words) // ignored: a []string always encodes
		return strings.TrimSuffix(b.String(), "\n"), nil
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
	return raw, nil
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
		if _, ok := raw[f.name]; !ok && f.required {
			return Row{}, fmt.Errorf("--%s is required", f.name)
		}
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

// --- output -----------------------------------------------------------------

// escape keeps a value on one line: control characters, the Unicode line
// separators and the bidi controls become \xNN or \uNNNN.
func escape(s string) string { return escaped(s, false) }

// value is one key=value token: escape, and every space and = as well; "-" when empty.
func value(s string) string {
	if s == "" {
		return "-"
	}
	return escaped(s, true)
}

func escaped(s string, token bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		odd := r == 0x2028 || r == 0x2029 || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x80 && (unicode.IsControl(r) || token && (r == '=' || unicode.IsSpace(r))):
			fmt.Fprintf(&b, `\x%02x`, r)
		case odd || unicode.IsControl(r) || token && unicode.IsSpace(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func rowLine(k *kind, r Row, cut bool) string {
	line := strings.ToUpper(k.name) + " name=" + value(r.Name)
	for _, f := range k.fields {
		v := r.F[f.name]
		if rs := []rune(v); cut && f.cut && len(rs) > 60 {
			v = string(rs[:60]) + "..."
		}
		line += " " + f.name + "=" + value(v)
	}
	return line
}

// printRows prints list (loops nil) or show lines, or one JSON object.
func printRows(w io.Writer, k *kind, verb string, rows []Row, loops []string, asJSON bool) error {
	list := strings.HasSuffix(verb, " list")
	if !asJSON {
		for _, r := range rows {
			if list {
				fmt.Fprintln(w, rowLine(k, r, true))
				continue
			}
			line := rowLine(k, r, false) + " created=" + value(r.Created) + " updated=" + value(r.Updated)
			if k.name == "machine" {
				line += " loops=" + value(strings.Join(loops, ","))
			}
			fmt.Fprintln(w, line)
		}
		if list {
			fmt.Fprintf(w, "CONFIG LIST kind=%s rows=%d\n", k.name, len(rows))
		}
		return nil
	}
	facts := "{}"
	if list {
		facts = fmt.Sprintf(`{"kind":%s,"rows":%d}`, quote(k.name), len(rows))
	}
	items := make([]string, len(rows))
	for i, r := range rows {
		pairs := []string{`"name":` + quote(r.Name)}
		for _, f := range k.fields {
			pairs = append(pairs, quote(f.name)+":"+quote(r.F[f.name]))
		}
		if !list {
			pairs = append(pairs, `"created":`+quote(r.Created), `"updated":`+quote(r.Updated))
			if k.name == "machine" {
				names, _ := json.Marshal(append([]string{}, loops...))
				pairs = append(pairs, `"loops":`+string(names))
			}
		}
		items[i] = `{"kind":` + quote(k.name) + `,"fields":{` + strings.Join(pairs, ",") + `}}`
	}
	_, err := fmt.Fprintf(w, `{"result":{"verb":%s,"status":"ok","exit":0},"facts":%s,"items":[%s]}`+"\n", quote(verb), facts, strings.Join(items, ","))
	return err
}

func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // ignored: a string always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

// planLine is a dry run's line: the fields an add or a remove carries, or
// those a set changes as before>after.
func planLine(kind, op, name, actor, reason string, before, after map[string]string) string {
	line := "CONFIG DRY-RUN op=" + op + " kind=" + kind + " name=" + value(name) + " actor=" + value(actor) + " wrote=nothing"
	if reason != "" {
		line += " reason=" + value(reason)
	}
	side := after
	if op == "remove" {
		side = before
	}
	for _, f := range slices.Sorted(maps.Keys(side)) {
		switch {
		case op != "set":
			line += " " + f + "=" + value(side[f])
		case before[f] != after[f]:
			line += " " + f + "=" + value(before[f]) + ">" + value(after[f])
		}
	}
	return line
}
