package ntable

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type containerType int

const (
	containerObject containerType = iota
	containerArray
)

type containerState struct {
	kind          containerType
	path          string
	seenKeys      map[string]bool
	seenLowerKeys map[string]bool
	expectKey     bool
	lastKey       string
	where         string // the manifest path of this container: members[0].create
	count         int    // elements seen, for an array
}

// joinPath appends an object key to a manifest path.
func joinPath(where, key string) string {
	if where == "" {
		return key
	}
	return where + "." + key
}

// valueWhere is the manifest path of the value about to be read in c.
func (c *containerState) valueWhere() string {
	if c.kind == containerArray {
		return fmt.Sprintf("%s[%d]", c.where, c.count)
	}
	return joinPath(c.where, c.lastKey)
}

// describe names a JSON token's type in the manifest's language.
func describe(tok any) string {
	switch v := tok.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case json.Delim:
		if v == '{' {
			return "an object"
		}
		return "an array"
	}
	return "something else"
}

// wrongType is the refusal of a value of the wrong type, at its place.
func wrongType(where, want string, tok any) error {
	name := where
	if i := strings.LastIndexAny(where, ".]"); i >= 0 && where[len(where)-1] != ']' {
		name = where[i+1:]
	}
	return &ManifestError{Where: where, Msg: fmt.Sprintf("%s must be %s, found %s", name, want, describe(tok))}
}

var rootAllowedKeys = map[string]bool{
	"schema":                  true,
	"table":                   true,
	"epoch":                   true,
	"expected_table_revision": true,
	"operation_id":            true,
	"actor":                   true,
	"members":                 true,
	"props":                   true,
	"prop_expect":             true,
	"prop_absent":             true,
}

var memberAllowedKeys = map[string]bool{
	"id":     true,
	"remove": true,
	"expect": true,
	"create": true,
	"move":   true,
	"set":    true,
	"unset":  true,
}

var expectAllowedKeys = map[string]bool{
	"absent":   true,
	"revision": true,
	"place":    true,
	"fields":   true,
}

var placeAllowedKeys = map[string]bool{
	"row": true,
	"col": true,
}

var createAllowedKeys = map[string]bool{
	"row":   true,
	"col":   true,
	"score": true,
}

var moveAllowedKeys = map[string]bool{
	"row":   true,
	"col":   true,
	"score": true,
}

var fieldGuardAllowedKeys = map[string]bool{
	"equals": true,
	"absent": true,
	"one_of": true,
}

func isNumberToken(tok any) bool {
	switch v := tok.(type) {
	case json.Number:
		_, err := v.Float64()
		return err == nil
	case float64:
		return true
	case int, int64, uint64:
		return true
	case string:
		if len(v) == 0 {
			return false
		}
		if (v[0] < '0' || v[0] > '9') && v[0] != '-' {
			return false
		}
		_, err := strconv.ParseFloat(v, 64)
		return err == nil
	default:
		return false
	}
}

// ValidateBatchManifestRaw reads raw manifest bytes as a batch manifest and
// applies every rule the server applies that needs no store: the manifest's
// size, UTF-8, exact-case keys, no key twice, each value's type, the bounds, and
// the combinations of changes. Nothing is coerced. The errors are of two kinds:
// a *ManifestError, a manifest that cannot be read as one (it names the place,
// members[0].create.score, in the manifest's words), and a *RuleError or a
// *LimitError, a manifest that reads as one and that a rule refuses; each is
// what the store would answer, with the same code.
func ValidateBatchManifestRaw(raw []byte) (*BatchManifest, error) {
	m, err := validateBatchManifest(raw)
	if err != nil {
		var (
			me *ManifestError
			re *RuleError
			le *LimitError
		)
		if !errors.As(err, &me) && !errors.As(err, &re) && !errors.As(err, &le) {
			err = &ManifestError{Msg: err.Error()}
		}
		return nil, err
	}
	return m, nil
}

func validateBatchManifest(raw []byte) (*BatchManifest, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty manifest")
	}
	if err := over(limitNameManifest, LimitManifestBytes, len(raw), ""); err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("manifest is not valid UTF-8")
	}
	if err := checkEscapes(raw); err != nil {
		return nil, err
	}

	// 1. Strict exact-case, path-aware tokenization pass: duplicate keys, null checks, and type checks.
	decToken := json.NewDecoder(bytes.NewReader(raw))
	decToken.UseNumber()
	var stack []containerState
	sawRoot := false

	for {
		tok, err := decToken.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if sawRoot && len(stack) == 0 {
				return nil, errors.New("unexpected trailing content after JSON manifest")
			}
			return nil, err
		}

		if len(stack) == 0 {
			if sawRoot {
				return nil, errors.New("unexpected trailing content after JSON manifest")
			}
			delim, ok := tok.(json.Delim)
			if !ok || delim != '{' {
				return nil, errors.New("manifest must be a JSON object")
			}
			sawRoot = true
			stack = append(stack, containerState{
				kind:          containerObject,
				path:          "root",
				seenKeys:      make(map[string]bool),
				seenLowerKeys: make(map[string]bool),
				expectKey:     true,
			})
			continue
		}

		top := &stack[len(stack)-1]

		if top.kind == containerObject {
			if delim, ok := tok.(json.Delim); ok && delim == '}' {
				if top.path == "place" && (!top.seenKeys["row"] || !top.seenKeys["col"]) {
					return nil, errors.New("place requires row and col")
				}
				if top.path == "move" && (!top.seenKeys["row"] || !top.seenKeys["col"]) {
					return nil, errors.New("move requires row and col")
				}
				if top.path == "member" && (!top.seenKeys["id"] || !top.seenKeys["expect"]) {
					if !top.seenKeys["id"] {
						return nil, errors.New("member entry has no id")
					}
					return nil, errors.New("member entry has no expect record")
				}
				if top.path == "create" {
					if !top.seenKeys["score"] {
						return nil, errors.New("create requires score")
					}
					if !top.seenKeys["row"] || !top.seenKeys["col"] {
						return nil, errors.New("create requires row and col")
					}
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					parent := &stack[len(stack)-1]
					if parent.kind == containerObject {
						parent.expectKey = true
						parent.lastKey = ""
					}
				}
				continue
			}

			if top.expectKey {
				key, ok := tok.(string)
				if !ok {
					return nil, errors.New("expected string key in object")
				}
				if top.seenKeys[key] || (top.seenLowerKeys != nil && top.seenLowerKeys[strings.ToLower(key)]) {
					return nil, fmt.Errorf("duplicate key %q in manifest", bounded(key))
				}
				top.seenKeys[key] = true
				if top.seenLowerKeys != nil {
					top.seenLowerKeys[strings.ToLower(key)] = true
				}
				top.lastKey = key
				top.expectKey = false

				// Path-aware allowed key validation (exact case)
				switch top.path {
				case "root":
					if !rootAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in root manifest", bounded(key))
					}
				case "member":
					if !memberAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in member object", bounded(key))
					}
				case "expect":
					if !expectAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in expect", bounded(key))
					}
				case "place":
					if !placeAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in place", bounded(key))
					}
				case "create":
					if !createAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in create", bounded(key))
					}
				case "move":
					if !moveAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in move", bounded(key))
					}
				case "field_guard":
					if !fieldGuardAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in field guard", bounded(key))
					}
				}
				continue
			}

			// Value for top.lastKey in containerObject
			where := top.valueWhere()
			if tok == nil {
				return nil, &ManifestError{Where: where, Msg: fmt.Sprintf("%s must not be null", top.lastKey)}
			}
			isObject := func() bool { d, ok := tok.(json.Delim); return ok && d == '{' }
			isArray := func() bool { d, ok := tok.(json.Delim); return ok && d == '[' }
			_, isString := tok.(string)
			switch {
			case top.path == "root" && (top.lastKey == "props" || top.lastKey == "prop_expect"),
				top.path == "member" && (top.lastKey == "expect" || top.lastKey == "create" || top.lastKey == "move" || top.lastKey == "set"),
				top.path == "expect" && (top.lastKey == "place" || top.lastKey == "fields"),
				top.path == "fields":
				if !isObject() {
					return nil, wrongType(where, "an object", tok)
				}
			case top.path == "root" && (top.lastKey == "members" || top.lastKey == "prop_absent"),
				top.path == "member" && top.lastKey == "unset",
				top.path == "field_guard" && top.lastKey == "one_of":
				if !isArray() {
					return nil, wrongType(where, "an array", tok)
				}
			case top.path == "member" && top.lastKey == "remove",
				top.path == "expect" && top.lastKey == "absent",
				top.path == "field_guard" && top.lastKey == "absent":
				if b, ok := tok.(bool); !ok || !b {
					return nil, wrongType(where, "true", tok)
				}
			case top.path == "root" && (top.lastKey == "table" || top.lastKey == "operation_id" || top.lastKey == "actor"),
				top.path == "member" && top.lastKey == "id",
				(top.path == "place" || top.path == "create" || top.path == "move") && (top.lastKey == "row" || top.lastKey == "col"),
				top.path == "field_guard" && top.lastKey == "equals",
				top.path == "set", top.path == "props":
				if !isString {
					return nil, wrongType(where, "a string", tok)
				}
			case top.path == "root" && (top.lastKey == "epoch" || top.lastKey == "expected_table_revision"),
				top.path == "expect" && top.lastKey == "revision":
				if !isString {
					return nil, wrongType(where, "a decimal string", tok)
				}
				if v := tok.(string); !uintString(v) {
					return nil, &ManifestError{Where: where, Msg: fmt.Sprintf("%s must be a decimal number in a string (0, 1, 2 ...), found %q", top.lastKey, bounded(v))}
				}
			case top.path == "root" && top.lastKey == "schema":
				if _, ok := tok.(json.Number); !ok {
					return nil, wrongType(where, "the number 1", tok)
				}
			case (top.path == "create" || top.path == "move") && top.lastKey == "score":
				if !isNumberToken(tok) {
					return nil, wrongType(where, "a JSON number", tok)
				}
				if _, ok := tok.(json.Number); !ok {
					return nil, wrongType(where, "a JSON number", tok)
				}
			}

			if delim, ok := tok.(json.Delim); ok {
				if delim == '{' {
					childPath := "object"
					var childLower map[string]bool
					switch top.path {
					case "root":
						childPath = "object"
						if top.lastKey == "props" || top.lastKey == "prop_expect" {
							childPath = "props"
						}
					case "members":
						childPath = "member"
						childLower = make(map[string]bool)
					case "member":
						switch top.lastKey {
						case "expect":
							childPath = "expect"
							childLower = make(map[string]bool)
						case "create":
							childPath = "create"
							childLower = make(map[string]bool)
						case "move":
							childPath = "move"
							childLower = make(map[string]bool)
						case "set":
							childPath = "set"
						}
					case "expect":
						switch top.lastKey {
						case "place":
							childPath = "place"
							childLower = make(map[string]bool)
						case "fields":
							childPath = "fields"
						}
					case "fields":
						childPath = "field_guard"
						childLower = make(map[string]bool)
					}

					stack = append(stack, containerState{
						kind:          containerObject,
						path:          childPath,
						seenKeys:      make(map[string]bool),
						seenLowerKeys: childLower,
						expectKey:     true,
						where:         where,
					})
					continue
				} else if delim == '[' {
					childPath := "array"
					switch top.path {
					case "root":
						if top.lastKey == "members" {
							childPath = "members"
						}
						if top.lastKey == "prop_absent" {
							childPath = "prop_absent"
						}
					case "member":
						if top.lastKey == "unset" {
							childPath = "unset"
						}
					case "field_guard":
						if top.lastKey == "one_of" {
							childPath = "one_of"
						}
					}

					stack = append(stack, containerState{
						kind:  containerArray,
						path:  childPath,
						where: where,
					})
					continue
				}
			}

			top.expectKey = true
			top.lastKey = ""
		} else { // containerArray
			if delim, ok := tok.(json.Delim); ok && delim == ']' {
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					parent := &stack[len(stack)-1]
					if parent.kind == containerObject {
						parent.expectKey = true
						parent.lastKey = ""
					}
				}
				continue
			}

			where := top.valueWhere()
			top.count++
			if tok == nil {
				return nil, &ManifestError{Where: where, Msg: "an element must not be null"}
			}
			_, isString := tok.(string)
			switch top.path {
			case "unset":
				if !isString {
					return nil, wrongType(where, "a field name (a string)", tok)
				}
			case "prop_absent":
				if !isString {
					return nil, wrongType(where, "a property name (a string)", tok)
				}
			case "one_of":
				if !isString {
					return nil, wrongType(where, "an option (a string)", tok)
				}
			case "members":
				if delim, ok := tok.(json.Delim); !ok || delim != '{' {
					return nil, wrongType(where, "a member entry (an object)", tok)
				}
			}

			if delim, ok := tok.(json.Delim); ok {
				if delim == '{' {
					childPath := "object"
					var childLower map[string]bool
					if top.path == "members" {
						childPath = "member"
						childLower = make(map[string]bool)
					}
					stack = append(stack, containerState{
						kind:          containerObject,
						path:          childPath,
						seenKeys:      make(map[string]bool),
						seenLowerKeys: childLower,
						expectKey:     true,
						where:         where,
					})
				} else if delim == '[' {
					stack = append(stack, containerState{
						kind:  containerArray,
						path:  "array",
						where: where,
					})
				}
			}
		}
	}

	if len(stack) != 0 {
		return nil, errors.New("unclosed JSON object or array")
	}

	// 2. Strict unknown fields & unmarshaling pass.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var manifest BatchManifest
	if err := dec.Decode(&manifest); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return nil, &ManifestError{Where: typeErr.Field, Msg: fmt.Sprintf("a value of the wrong type (found %s)", typeErr.Value)}
		}
		return nil, err
	}

	// 3. Strict EOF check: no trailing non-whitespace.
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, errors.New("unexpected trailing content after JSON manifest")
	}

	// 4. Basic member entry sanity and field guard validation.
	seenMemberIDs := make(map[string]bool, len(manifest.Members))
	for _, m := range manifest.Members {
		id := m.ID
		if id == "" {
			return nil, rule("MEMBER", "", nil, "a member id cannot be empty")
		}
		if strings.ContainsAny(id, "\x00\r\n\t") {
			return nil, rule("MEMBER", "", nil, "invalid member id %q", bounded(id))
		}
		if seenMemberIDs[id] {
			return nil, rule("TWICE", id, ErrDuplicateMember, "the id appears more than once in the manifest")
		}
		seenMemberIDs[id] = true
		if m.Expect != nil && m.Expect.Fields != nil {
			for fName, fg := range m.Expect.Fields {
				conds := 0
				if fg.Equals != nil {
					conds++
				}
				if fg.Absent != nil {
					conds++
				}
				if fg.OneOf != nil {
					conds++
					if len(fg.OneOf) == 0 {
						return nil, rule("FIELDGUARD", id, ErrFieldGuard, "one_of for %q must be a nonempty array", fName)
					}
				}
				if conds != 1 {
					return nil, rule("FIELDGUARD", id, ErrFieldGuard, "field guard for %q must specify exactly one condition, got %d", fName, conds)
				}
			}
		}
	}

	if err := CheckBatchBounds(&manifest); err != nil {
		return nil, err
	}
	if err := validateManifestSemantics(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// word is the server's T.word: a nonempty string without control characters.
func word(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}

// uintString is a canonical decimal uint64.
func uintString(s string) bool {
	v, err := strconv.ParseUint(s, 10, 64)
	return err == nil && strconv.FormatUint(v, 10) == s
}

func reservedField(f string) bool {
	return f == "epoch" || f == "revision" || strings.HasPrefix(f, "place:")
}

// validateManifestSemantics holds every check of the server's T.static_entries
// and of ns_table_apply's manifest header that needs no store, so that a
// manifest the server would refuse for its shape is refused here first.
func validateManifestSemantics(m *BatchManifest) error {
	if m.Schema != 1 {
		return rule("SCHEMA", "", nil, "schema %d is not supported, expected 1", m.Schema)
	}
	if !ValidName(m.Table) {
		return rule("NAME", "", nil, "table %q wants letters, digits, _ . and -", bounded(m.Table))
	}
	if !word(m.OperationID) {
		return rule("OPERATION", "", nil, "operation_id must be a nonempty string without control characters")
	}
	if len(m.Members) == 0 && len(m.Props)+len(m.PropExpect)+len(m.PropAbsent) == 0 {
		return rule("MANIFEST", "", ErrMalformedManifest, "a manifest names at least one member or table property")
	}
	if err := validateProps(m); err != nil {
		return err
	}
	if !uintString(m.Epoch) {
		return rule("EPOCH", "", nil, "epoch must be a decimal uint64 string")
	}
	if !uintString(m.ExpectedTableRevision) {
		return rule("REVISION", "", nil, "expected_table_revision must be a decimal uint64 string")
	}
	for _, e := range m.Members {
		if !word(e.ID) {
			return rule("MEMBER", "", nil, "member id must be a nonempty string without control characters")
		}
		for f := range e.Set {
			if reservedField(f) {
				return rule("RESERVEDFIELD", e.ID, ErrReservedField, "field %q", bounded(f))
			}
			if !word(f) {
				return rule("ARGS", e.ID, nil, "field names are nonempty strings without control characters")
			}
		}
		namedUnset := map[string]bool{}
		for _, f := range e.Unset {
			if namedUnset[f] {
				return rule("ARGS", e.ID, nil, "unset names field %q twice", bounded(f))
			}
			namedUnset[f] = true
			if reservedField(f) {
				return rule("RESERVEDFIELD", e.ID, ErrReservedField, "field %q", bounded(f))
			}
			if !word(f) {
				return rule("ARGS", e.ID, nil, "field names are nonempty strings without control characters")
			}
			if _, both := e.Set[f]; both {
				return rule("MUTATION", e.ID, ErrMutation, "field %q cannot be both set and unset", bounded(f))
			}
		}
		if e.Expect == nil {
			return rule("MANIFEST", e.ID, ErrMalformedManifest, "missing expect")
		}
		x := e.Expect
		if x.Absent && (x.Revision != "" || x.Place != nil || x.Fields != nil) {
			return rule("MUTATION", e.ID, ErrMutation, "expect absent cannot combine with revision, place or fields")
		}
		if x.Revision != "" && !uintString(x.Revision) {
			return rule("ARGS", e.ID, nil, "expect revision must be a decimal string")
		}
		for name, g := range x.Fields {
			if !word(name) {
				return rule("ARGS", e.ID, nil, "field guard names are nonempty strings without control characters")
			}
			seen := map[string]bool{}
			for _, o := range g.OneOf {
				if seen[o] {
					return rule("FIELDGUARD", e.ID, ErrFieldGuard, "field %q: one_of names an option twice", bounded(name))
				}
				seen[o] = true
			}
		}
		if e.Create != nil {
			if e.Move != nil || e.Remove {
				return rule("MUTATION", e.ID, ErrMutation, "create cannot combine with move or remove")
			}
			if !x.Absent {
				return rule("MUTATION", e.ID, ErrMutation, "create requires expect absent")
			}
			if math.IsNaN(e.Create.Score) || math.IsInf(e.Create.Score, 0) {
				return rule("SCORE", e.ID, ErrInvalidScore, "expected a finite JSON number")
			}
		}
		if e.Move != nil {
			if e.Remove {
				return rule("MUTATION", e.ID, ErrMutation, "move cannot combine with remove")
			}
			if e.Move.Score != nil && (math.IsNaN(*e.Move.Score) || math.IsInf(*e.Move.Score, 0)) {
				return rule("SCORE", e.ID, ErrInvalidScore, "expected a finite JSON number")
			}
		}
	}
	return nil
}

// validateProps is T.static_props: the table properties a manifest sets and
// expects are identifiers with string values, at most LimitManifestProps of
// each, and no property is expected both present and absent (L1 contract
// amendment, table properties, section 4).
func validateProps(m *BatchManifest) error {
	for _, set := range []map[string]string{m.Props, m.PropExpect} {
		if err := over(limitNameManifestProp, LimitManifestProps, len(set), ""); err != nil {
			return err
		}
		for name, v := range set {
			if !ValidName(name) {
				return rule("ARGS", "", nil, "property %q wants letters, digits, _ . and -", bounded(name))
			}
			if err := over(limitNameFieldValue, LimitFieldValueBytes, len(v), ""); err != nil {
				return err
			}
		}
	}
	if err := over(limitNameManifestProp, LimitManifestProps, len(m.PropAbsent), ""); err != nil {
		return err
	}
	named := map[string]bool{}
	for _, name := range m.PropAbsent {
		if !ValidName(name) {
			return rule("ARGS", "", nil, "property %q wants letters, digits, _ . and -", bounded(name))
		}
		if _, both := m.PropExpect[name]; both || named[name] {
			return rule("ARGS", "", nil, "prop_absent names property %q twice or one prop_expect names", bounded(name))
		}
		named[name] = true
	}
	return nil
}

// RuleError is a manifest that reads as a manifest and that a rule refuses:
// a repeated member, a set and an unset of one field, a reserved field, a create
// with a move, an empty members array. It is a refusal, as the store's are, and
// says what the store would: a code and a sentence.
type RuleError struct {
	Code   string // the store's refusal code for the same rule
	Member string // the member at fault, if one is
	Msg    string // the sentence, naming the member
	Detail string // the sentence without the member
	is     error
}

func (e *RuleError) Error() string { return e.Msg }
func (e *RuleError) Unwrap() error { return e.is }

// rule builds a RuleError. When a sentinel names the rule, the sentence starts
// with the sentinel's words, then the detail.
func rule(code, member string, is error, format string, a ...any) error {
	detail := fmt.Sprintf(format, a...)
	msg := detail
	if is != nil && is != ErrMalformedManifest {
		msg = is.Error() + ": " + detail
	}
	if member != "" {
		msg = fmt.Sprintf("member %q: %s", member, msg)
	}
	if is != nil && is != ErrMalformedManifest {
		detail = is.Error() + ": " + detail
	}
	return &RuleError{Code: code, Member: member, Msg: msg, Detail: detail, is: is}
}

// checkEscapes refuses a \u escape the JSON decoder would silently replace with
// U+FFFD: a high surrogate not followed by a low one, or a low one alone. The
// server refuses the same text; nothing is coerced.
func checkEscapes(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		switch {
		case !inString:
			if raw[i] == '"' {
				inString = true
			}
		case raw[i] == '"':
			inString = false
		case raw[i] == '\\' && i+1 < len(raw):
			if raw[i+1] != 'u' {
				i++
				continue
			}
			unit, ok := hex4(raw, i+2)
			if !ok {
				return nil // malformed escapes are the decoder's to refuse, with its own words
			}
			i += 5
			switch {
			case unit >= 0xd800 && unit < 0xdc00:
				low, ok := uint16(0), false
				if i+2 < len(raw) && raw[i+1] == '\\' && raw[i+2] == 'u' {
					var v int
					if v, ok = hex4(raw, i+3); ok {
						low = uint16(v)
					}
				}
				if !ok || low < 0xdc00 || low > 0xdfff {
					return errors.New("a high surrogate escape is not followed by a low surrogate escape")
				}
				i += 6
			case unit >= 0xdc00 && unit <= 0xdfff:
				return errors.New("a low surrogate escape has no high surrogate before it")
			}
		}
	}
	return nil
}

func hex4(raw []byte, at int) (int, bool) {
	if at+4 > len(raw) {
		return 0, false
	}
	v, err := strconv.ParseUint(string(raw[at:at+4]), 16, 16)
	return int(v), err == nil
}

// ManifestError is a manifest that cannot be read as a manifest: it is not JSON,
// names a key it may not, or holds a value of the wrong type. It says where, in
// the manifest's own words (members[0].create.score), and never in the parser's.
type ManifestError struct {
	Where, Msg string
}

func (e *ManifestError) Error() string {
	if e.Where == "" {
		return e.Msg
	}
	return e.Msg + " at " + e.Where
}

func (e *ManifestError) Is(target error) bool { return target == ErrMalformedManifest }

// bounded is s as a refusal may carry it: a value over 64 bytes is its first 32
// and its length, so no refusal echoes a long input.
func bounded(s string) string {
	if len(s) <= 64 {
		return s
	}
	cut := 32
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s...(%d bytes)", s[:cut], len(s))
}
