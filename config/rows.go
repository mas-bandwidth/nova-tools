package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	config "github.com/mas-bandwidth/nova-tools/v2/config/lib"
)

// field is one column of a kind's table. typ is text, int, bool, list (a
// sorted comma list of words matching word), argv (a JSON array of strings),
// env (a JSON object of NAME to value), path (an absolute or ~/ path), ref
// (the name of a machine row, NULL when empty) or public (a public IP literal
// or a DNS name).
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
	fields      []field
	check       func(Row) error
}

// Row is one row: its name, its fields as text, and its stamps (RFC 3339).
type Row = config.Row

var (
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	roleWord    = regexp.MustCompile(`^(bench|runner|services)$`)
	keyWord     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envName     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	tierWord    = regexp.MustCompile(`^(flash|pro|heavy|frontier)$`)
)

var kinds = map[string]*kind{
	"machine": {name: "machine", table: "machines",
		fields: []field{
			{name: "address", typ: "text"},
			{name: "public", typ: "public", null: true},
			{name: "user", typ: "text", required: true},
			{name: "os", typ: "text"},
			{name: "arch", typ: "text"},
			{name: "cores", typ: "int", null: true},
			{name: "roles", typ: "list", def: "bench", word: roleWord},
			{name: "runners", typ: "int"},
			{name: "note", typ: "text", cut: true, free: true},
		}},
	"service": {name: "service", table: "services", check: checkService,
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
	"friend": {name: "friend", table: "friends", check: checkFriend,
		fields: []field{
			{name: "harness", typ: "text", required: true},
			{name: "model", typ: "text"},
			{name: "width", typ: "int", def: "1"},
			{name: "tiers", typ: "list", def: "flash,frontier,heavy,pro", word: tierWord},
			{name: "pronouns", typ: "text", null: true},
			{name: "note", typ: "text", cut: true, free: true},
			{name: "enabled", typ: "bool", def: "true"},
		}},
}

// fieldsLine names a kind's fields on one line, for a usage error and for help.
func (k *kind) fieldsLine() string {
	names := []string{"name"}
	for _, f := range k.fields {
		names = append(names, f.name)
	}
	return k.name + " fields: " + strings.Join(names, ", ")
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
	case "public":
		if raw != "" {
			return canonicalPublic(f, raw)
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

// dnsLabel is one lower-case label of a DNS name (RFC 1123).
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// notPublic are the ranges a public address is never in (they are disjoint), each with its name.
var notPublic = map[string]string{"100.64.0.0/10": "the tailnet's CGNAT range", "10.0.0.0/8": "a private range",
	"172.16.0.0/12": "a private range", "192.168.0.0/16": "a private range", "fc00::/7": "a private range (unique local)",
	"127.0.0.0/8": "the loopback range", "::1/128": "the loopback address", "169.254.0.0/16": "the link-local range",
	"fe80::/10": "the link-local range", "0.0.0.0/8": "the unspecified range", "::/128": "the unspecified address"}

// canonicalPublic is a public address as it is stored: an IP literal in its
// canonical form, or a DNS name of lower-case labels (RFC 1123). An address in
// the tailnet's CGNAT range or a private range is refused as not public.
func canonicalPublic(f field, raw string) (string, error) {
	if a, err := netip.ParseAddr(raw); err == nil {
		if a.Zone() != "" {
			return "", fmt.Errorf("--%s %q: want an address without a zone", f.name, raw)
		}
		for p, what := range notPublic {
			if netip.MustParsePrefix(p).Contains(a.Unmap()) {
				return "", fmt.Errorf("--%s %q: not public: in %s, %s; want the address the internet reaches the machine at", f.name, raw, p, what)
			}
		}
		return a.String(), nil
	}
	labels := strings.Split(raw, ".")
	ok := len(raw) <= 253 && strings.Trim(labels[len(labels)-1], "0123456789") != ""
	for _, l := range labels {
		ok = ok && dnsLabel.MatchString(l)
	}
	if !ok {
		return "", fmt.Errorf("--%s %q: want an IP address or a DNS name of lower-case labels (RFC 1123)", f.name, raw)
	}
	return raw, nil
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

// valueError is a flag value the row refuses to take: a malformed invocation.
type valueError struct{ msg string }

func (v valueError) Error() string { return v.msg }

// newRow is the row add writes: the given fields, the rest at their zero.
func (k *kind) newRow(name string, raw map[string]string) (Row, error) {
	empty := Row{Name: name, F: map[string]string{}}
	for _, f := range k.fields {
		empty.F[f.name] = f.zero()
	}
	r, err := k.merge(empty, raw)
	if len(name) > 128 || !namePattern.MatchString(name) {
		return Row{}, valueError{strings.TrimSuffix(fmt.Sprintf("name %q: want lower-case letters, digits and dashes, starting with a letter or digit; %v", name, err), "; <nil>")}
	}
	return r, err
}

// merge is the row a set leaves: cur with the given fields, checked whole.
func (k *kind) merge(cur Row, raw map[string]string) (Row, error) {
	next := Row{Name: cur.Name, F: maps.Clone(cur.F)}
	var problems []string // of the values given
	for _, f := range k.fields {
		if v, ok := raw[f.name]; ok {
			if c, err := f.canonical(v); err != nil {
				problems = append(problems, err.Error())
			} else {
				next.F[f.name] = c
			}
		}
	}
	if k.check != nil {
		if err := k.check(next); err != nil {
			if len(problems) == 0 {
				return Row{}, err
			}
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return Row{}, valueError{strings.Join(problems, "; ")}
	}
	return next, nil
}

func checkService(r Row) error {
	var p []string
	if r.F["every"] != "0" && r.F["keepalive"] == "true" {
		p = append(p, fmt.Sprintf("service %s has --every %s and --keepalive true; a service runs every n seconds or is kept alive, so set one: --every 0 or --keepalive false", r.Name, r.F["every"]))
	} else if r.F["every"] == "0" && r.F["keepalive"] != "true" {
		p = append(p, fmt.Sprintf("service %s has neither --every nor --keepalive; want --every <seconds> or --keepalive true", r.Name))
	}
	if r.F["keys"] != "" && r.F["seat"] == "" {
		p = append(p, fmt.Sprintf("service %s names secrets (--keys %s) and no --seat to open them from", r.Name, r.F["keys"]))
	}
	if len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "; "))
	}
	return nil
}

func checkFriend(r Row) error {
	var p []string
	if r.F["width"] == "0" {
		p = append(p, fmt.Sprintf("friend %s has --width 0; a friend works at least one card at once: --width 1 or more", r.Name))
	}
	if r.F["tiers"] == "" {
		p = append(p, fmt.Sprintf("friend %s has no --tiers; want a comma list of flash, pro, heavy, frontier", r.Name))
	}
	if len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "; "))
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
