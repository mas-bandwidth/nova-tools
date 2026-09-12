package records

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// The parsed shape. encoding/json's map[string]any cannot be used for this work: it
// silently keeps the LAST of two duplicate keys, and the format requires duplicate keys to
// be refused rather than resolved. Key order is kept too, so a diagnostic can name the
// second occurrence and so the canonicaliser sorts deliberately rather than inheriting a
// map's order.
type (
	// Value is nil, bool, string, json.Number, *Object or []Value.
	Value any

	// Object is a JSON object with its keys in source order.
	Object struct {
		keys []string
		vals map[string]Value
	}
)

// Keys returns the member names in source order.
func (o *Object) Keys() []string { return o.keys }

// Get returns a member and whether it was present. Present-with-null and absent are
// different answers, which is the whole point of the presence rules further down.
func (o *Object) Get(k string) (Value, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *Object) set(k string, v Value) {
	if o.vals == nil {
		o.vals = map[string]Value{}
	}
	o.keys = append(o.keys, k)
	o.vals[k] = v
}

// parseStrict parses one JSON document. Rules enforced here rather than in the structural
// pass are the ones that are properties of the BYTES and are gone once a value exists:
// invalid UTF-8, a lone surrogate escape, a duplicate key, and a raw JSON number.
func parseStrict(raw []byte, skip map[string]bool, field string) (Value, error) {
	if !skip[RuleInvalidUTF8] && !utf8.Valid(raw) {
		return nil, refuse(RuleInvalidUTF8, field, "document is not valid UTF-8")
	}
	if !skip[RuleLoneSurrogate] {
		if err := scanEscapes(raw, field); err != nil {
			return nil, err
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := parseValue(dec, field, skip)
	if err != nil {
		return nil, err
	}
	// Trailing content is not a second document; it is a malformed one.
	if _, err := dec.Token(); err != io.EOF {
		return nil, refuse(RuleNotJSON, field, "trailing content after the document")
	}
	return v, nil
}

func parseValue(dec *json.Decoder, field string, skip map[string]bool) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, refuse(RuleNotJSON, field, "the document does not parse")
	}
	return parseFrom(dec, tok, field, skip)
}

func parseFrom(dec *json.Decoder, tok json.Token, field string, skip map[string]bool) (Value, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &Object{}
			for {
				kt, err := dec.Token()
				if err != nil {
					return nil, refuse(RuleNotJSON, field, "the document does not parse")
				}
				if d, ok := kt.(json.Delim); ok && d == '}' {
					return o, nil
				}
				key, ok := kt.(string)
				if !ok {
					return nil, refuse(RuleNotJSON, field, "a member name is not a string")
				}
				child := field + "." + key
				if _, dup := o.vals[key]; dup && !skip[RuleDuplicateKey] {
					return nil, refuse(RuleDuplicateKey, child, "the member name occurs twice")
				}
				val, err := parseValue(dec, child, skip)
				if err != nil {
					return nil, err
				}
				if _, dup := o.vals[key]; dup {
					// Only reachable with the rule skipped: last wins, the way a lenient
					// reader would resolve it. The fixture test uses this to show the rule
					// is load-bearing.
					o.vals[key] = val
					continue
				}
				o.set(key, val)
			}
		case '[':
			arr := []Value{}
			for i := 0; ; i++ {
				et, err := dec.Token()
				if err != nil {
					return nil, refuse(RuleNotJSON, field, "the document does not parse")
				}
				if d, ok := et.(json.Delim); ok && d == ']' {
					return arr, nil
				}
				val, err := parseFrom(dec, et, indexPath(field, i), skip)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
		}
		return nil, refuse(RuleNotJSON, field, "the document does not parse")
	case json.Number:
		// "No raw JSON numbers occur in these bodies." A number here is not a value to
		// coerce; it is a body written against a different contract.
		if !skip[RuleRawJSONNumber] {
			return nil, refuse(RuleRawJSONNumber, field, "usage and identity values are JSON strings, never numbers")
		}
		return t, nil
	default:
		return tok, nil
	}
}

func indexPath(field string, i int) string {
	return field + "[" + itoa(i) + "]"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// scanEscapes walks the raw bytes and refuses a \u escape that names half a surrogate
// pair. encoding/json turns one of those into U+FFFD without complaint, which would make
// two different documents hash the same and would silently rewrite a label.
func scanEscapes(raw []byte, field string) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(raw) {
				return refuse(RuleNotJSON, field, "the document ends inside an escape")
			}
			if raw[i+1] != 'u' {
				i++
				continue
			}
			hi, ok := hex4(raw, i+2)
			if !ok {
				return refuse(RuleNotJSON, field, "a \\u escape is not four hex digits")
			}
			i += 5
			if hi >= 0xDC00 && hi <= 0xDFFF {
				return refuse(RuleLoneSurrogate, field, "a low surrogate escape with no high surrogate")
			}
			if hi >= 0xD800 && hi <= 0xDBFF {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return refuse(RuleLoneSurrogate, field, "a high surrogate escape with no low surrogate")
				}
				lo, ok := hex4(raw, i+3)
				if !ok || lo < 0xDC00 || lo > 0xDFFF {
					return refuse(RuleLoneSurrogate, field, "a high surrogate escape with no low surrogate")
				}
				i += 6
			}
		}
	}
	return nil
}

func hex4(raw []byte, at int) (int, bool) {
	if at+4 > len(raw) {
		return 0, false
	}
	v := 0
	for _, c := range raw[at : at+4] {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= int(c - '0')
		case c >= 'a' && c <= 'f':
			v |= int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return v, true
}
