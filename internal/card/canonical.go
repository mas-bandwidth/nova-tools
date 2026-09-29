package card

import (
	"bytes"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Obj is an object node of a canonical tree.
type Obj map[string]any

// Str sets key to v when v is not empty. An empty optional string and an absent
// one are the same thing in the canonical form: the key is left out.
func (o Obj) Str(key, v string) {
	if v != "" {
		o[key] = v
	}
}

// OptSet sets key to the set when it has items. An empty optional array and an
// absent one are the same thing in the canonical form: the key is left out.
func (o Obj) OptSet(key string, s Set) {
	if len(s.Items) > 0 {
		o[key] = s
	}
}

// Set is an array whose order carries no meaning. The encoder sorts its items:
// objects by the string value of their Key field, strings by themselves, ties
// by the items' own canonical bytes, so a request that lists its entries in
// another order has the same bytes and the same hash. An []any is an array whose
// order does mean something and is written as given.
type Set struct {
	Key   string
	Items []any
}

// Strings is a Set of strings.
func Strings[T ~string](list []T) Set {
	items := make([]any, len(list))
	for i, v := range list {
		items[i] = string(v)
	}
	return Set{Items: items}
}

// Encode writes v in the one canonical form: object keys sorted bytewise, no
// insignificant whitespace, integers only (a value that is not one of the
// supported types is written null; no float is ever written), order-free arrays
// sorted, and strings escaped by the one rule of writeString. Supported values:
// Obj, []any, Set, string, int, uint64, bool and nil.
func Encode(v any) []byte {
	var buf bytes.Buffer
	write(&buf, v)
	return buf.Bytes()
}

func write(buf *bytes.Buffer, v any) {
	switch t := v.(type) {
	case Obj:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			write(buf, t[k])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			write(buf, e)
		}
		buf.WriteByte(']')
	case Set:
		type item struct {
			key string
			enc []byte
		}
		items := make([]item, len(t.Items))
		for i, e := range t.Items {
			it := item{enc: Encode(e)}
			switch x := e.(type) {
			case string:
				it.key = x
			case Obj:
				if s, ok := x[t.Key].(string); ok {
					it.key = s
				}
			}
			items[i] = it
		}
		sort.SliceStable(items, func(a, b int) bool {
			if items[a].key != items[b].key {
				return items[a].key < items[b].key
			}
			return bytes.Compare(items[a].enc, items[b].enc) < 0
		})
		buf.WriteByte('[')
		for i, it := range items {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(it.enc)
		}
		buf.WriteByte(']')
	case string:
		writeString(buf, t)
	case int:
		buf.WriteString(strconv.Itoa(t))
	case uint64:
		buf.WriteString(strconv.FormatUint(t, 10))
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	default:
		buf.WriteString("null")
	}
}

// writeString writes s as a JSON string by one rule: the quote and the backslash
// are escaped with a backslash; every control character (below 0x20 and 0x7f),
// the Unicode line and paragraph separators and any byte that is not valid UTF-8
// are written as a six-character \u escape in lower-case hexadecimal (an invalid
// byte as \ufffd); everything else is its own UTF-8. There is no short escape
// (\n, \t) and no HTML escape (<, > and & are themselves), so one string has one
// encoding in every package that uses this encoder.
func writeString(buf *bytes.Buffer, s string) {
	const hexd = "0123456789abcdef"
	buf.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			buf.WriteString(`\ufffd`)
		case r == '"':
			buf.WriteString(`\"`)
		case r == '\\':
			buf.WriteString(`\\`)
		case r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029:
			buf.WriteString(`\u`)
			buf.WriteByte(hexd[r>>12&0xf])
			buf.WriteByte(hexd[r>>8&0xf])
			buf.WriteByte(hexd[r>>4&0xf])
			buf.WriteByte(hexd[r&0xf])
		default:
			buf.WriteString(s[i : i+size])
		}
		i += size
	}
	buf.WriteByte('"')
}
