package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Output grammar. A line is TOKEN then key=value fields. A value is bare when
// it holds nothing a reader could split on, and a JSON string otherwise; an
// argv or env is its JSON; free text (a note, a reason) is always a JSON
// string. "-" is empty. Nothing is escaped any other way, so what is printed
// can be pasted back.

// odd says a rune that could hide or reorder text on a terminal.
func odd(r rune) bool {
	return r == 0x2028 || r == 0x2029 || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

var oddEscapes = func() *strings.Replacer {
	var pairs []string
	for _, r := range "\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069" {
		pairs = append(pairs, string(r), fmt.Sprintf(`\u%04x`, r))
	}
	return strings.NewReplacer(pairs...)
}()

// jsonString is s as a JSON string; the bidi controls are \u escapes too.
func jsonString(s string) string { return oddEscapes.Replace(marshal(s)) }

// bare says s can be printed as it is: one word of printable characters.
func bare(s string) bool {
	if s == "" || s == "-" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == '"' || odd(r) || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// value is one key=value token: "-" when empty, bare when it can be, else a JSON string.
func value(s string) string {
	switch {
	case s == "":
		return "-"
	case bare(s):
		return s
	}
	return jsonString(s)
}

// fieldValue is a field's value as a line prints it.
func fieldValue(f field, v string) string {
	switch {
	case v == "":
		return "-"
	case f.typ == "argv" || f.typ == "env":
		return jsonText(f, v)
	case f.free:
		return jsonString(v)
	}
	return value(v)
}

// jsonText is a stored argv or env as valid JSON, re-encoded so a stray control
// character in an old row cannot reach the terminal; text that is not JSON is a JSON string.
func jsonText(f field, v string) string {
	if f.typ == "argv" {
		var a []string
		if json.Unmarshal([]byte(v), &a) == nil {
			return oddEscapes.Replace(marshal(a))
		}
	} else {
		var m map[string]string
		if json.Unmarshal([]byte(v), &m) == nil {
			return oddEscapes.Replace(marshal(m))
		}
	}
	return jsonString(v)
}

// jsonValue is a field's value as JSON, typed: a number, true or false, null
// for an unset int, the argv or env as it is, else a string.
func jsonValue(f field, v string) string {
	switch {
	case f.typ == "int" && v == "":
		return "null"
	case f.typ == "int" || f.typ == "bool":
		return v
	case f.typ == "argv" || f.typ == "env":
		return jsonText(f, v)
	}
	return jsonString(v)
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

// kindNamed is the kind of a name, or an empty one for a kind this tool no
// longer has, so its old history still prints.
func kindNamed(name string) *kind {
	if k, ok := kinds[name]; ok {
		return k
	}
	return &kind{name: name}
}

// escape keeps a message on one line: control characters, the Unicode line
// separators and the bidi controls become \xNN or \uNNNN.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x80 && unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case odd(r) || unicode.IsControl(r):
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
		line += " " + f.name + "=" + fieldValue(f, v)
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
		facts = fmt.Sprintf(`{"kind":%s,"rows":%d}`, jsonString(k.name), len(rows))
	}
	items := make([]string, len(rows))
	for i, r := range rows {
		pairs := []string{`"name":` + jsonString(r.Name)}
		for _, f := range k.fields {
			pairs = append(pairs, jsonString(f.name)+":"+jsonString(r.F[f.name]))
		}
		if !list {
			pairs = append(pairs, `"created":`+jsonString(r.Created), `"updated":`+jsonString(r.Updated))
			if k.name == "machine" {
				names, _ := json.Marshal(append([]string{}, loops...))
				pairs = append(pairs, `"loops":`+string(names))
			}
		}
		items[i] = `{"kind":` + jsonString(k.name) + `,"fields":{` + strings.Join(pairs, ",") + `}}`
	}
	_, err := fmt.Fprintf(w, `{"result":{"verb":%s,"status":"ok","exit":0},"facts":%s,"items":[%s]}`+"\n", jsonString(verb), facts, strings.Join(items, ","))
	return err
}

// changedNames are the fields a write put in the row, in the kind's order and
// then the columns it does not know: for a set, those that differ. "reason"
// is not one.
func changedNames(k *kind, op string, before, after map[string]string) []string {
	side := after
	if op == "remove" {
		side = before
	}
	all := []string{}
	for _, f := range k.fields {
		all = append(all, f.name)
	}
	seen := []map[string]string{side}
	if op == "set" {
		seen = []map[string]string{before, after}
	}
	for _, m := range seen {
		for _, n := range slices.Sorted(maps.Keys(m)) {
			if !slices.Contains(all, n) && n != "reason" {
				all = append(all, n)
			}
		}
	}
	var out []string
	for _, n := range all {
		old, had := before[n]
		cur, has := after[n]
		_, inSide := side[n]
		if op == "set" && had == has && old == cur || op != "set" && !inSide {
			continue
		}
		out = append(out, n)
	}
	return out
}

// changeLines are the fields of a write, one line each, indented: `name: value`
// for an add or a remove, `name: before -> after` for the fields a set changes,
// values as JSON. A "reason" rides in the written side and is the last line.
func changeLines(k *kind, op string, before, after map[string]string) []string {
	side := after
	if op == "remove" {
		side = before
	}
	var out []string
	for _, n := range changedNames(k, op, before, after) {
		f := k.findField(n)
		if op == "set" {
			old, had := before[n]
			cur, has := after[n]
			out = append(out, "  "+n+": "+jsonOrNull(f, old, had)+" -> "+jsonOrNull(f, cur, has))
		} else {
			out = append(out, "  "+n+": "+jsonValue(f, side[n]))
		}
	}
	if r := side["reason"]; r != "" {
		out = append(out, "  reason: "+jsonString(r))
	}
	return out
}

func jsonOrNull(f field, v string, present bool) string {
	if !present {
		return "null"
	}
	return jsonValue(f, v)
}

// planLines is a dry run: the event line, then what the write would change.
func planLines(k *kind, op, name, actor, reason string, before, after map[string]string) string {
	if reason != "" {
		if op == "remove" {
			before = maps.Clone(before)
			before["reason"] = reason
		} else {
			after = maps.Clone(after)
			after["reason"] = reason
		}
	}
	head := "CONFIG DRY-RUN op=" + op + " kind=" + k.name + " name=" + value(name) + " actor=" + value(actor) + " wrote=nothing"
	return strings.Join(append([]string{head}, changeLines(k, op, before, after)...), "\n")
}
