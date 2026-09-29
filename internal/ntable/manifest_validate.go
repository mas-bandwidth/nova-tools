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
}

var rootAllowedKeys = map[string]bool{
	"schema":                  true,
	"table":                   true,
	"epoch":                   true,
	"expected_table_revision": true,
	"operation_id":            true,
	"actor":                   true,
	"members":                 true,
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

// ValidateBatchManifestRaw strictly validates raw batch manifest bytes according
// to Nova Table specification invariants before any normalization occurs.
func ValidateBatchManifestRaw(raw []byte) (*BatchManifest, error) {
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
					return nil, fmt.Errorf("duplicate key %q in manifest", key)
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
						return nil, fmt.Errorf("unknown field %q in root manifest", key)
					}
				case "member":
					if !memberAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in member object", key)
					}
				case "expect":
					if !expectAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in expect", key)
					}
				case "place":
					if !placeAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in place", key)
					}
				case "create":
					if !createAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in create", key)
					}
				case "move":
					if !moveAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in move", key)
					}
				case "field_guard":
					if !fieldGuardAllowedKeys[key] {
						return nil, fmt.Errorf("unknown field %q in field guard", key)
					}
				}
				continue
			}

			// Value for top.lastKey in containerObject
			if tok == nil {
				if top.path == "set" {
					return nil, fmt.Errorf("null value not allowed in set for field %q", top.lastKey)
				}
				return nil, fmt.Errorf("null value not allowed for %s", top.lastKey)
			}

			// 1. Keys that MUST be objects:
			if (top.path == "member" && (top.lastKey == "expect" || top.lastKey == "create" || top.lastKey == "move" || top.lastKey == "set")) ||
				(top.path == "expect" && (top.lastKey == "place" || top.lastKey == "fields")) ||
				(top.path == "fields") {
				delim, ok := tok.(json.Delim)
				if !ok || delim != '{' {
					return nil, fmt.Errorf("expected object for %s", top.lastKey)
				}
			}

			// 2. Keys that MUST be arrays:
			if (top.path == "root" && top.lastKey == "members") ||
				(top.path == "member" && top.lastKey == "unset") ||
				(top.path == "field_guard" && top.lastKey == "one_of") {
				delim, ok := tok.(json.Delim)
				if !ok || delim != '[' {
					return nil, fmt.Errorf("expected array for %s", top.lastKey)
				}
			}

			// 3. Keys that MUST be booleans:
			if top.path == "member" && top.lastKey == "remove" {
				b, ok := tok.(bool)
				if !ok || !b {
					return nil, errors.New("remove must be true")
				}
			} else if (top.path == "expect" && top.lastKey == "absent") ||
				(top.path == "field_guard" && top.lastKey == "absent") {
				b, ok := tok.(bool)
				if !ok {
					return nil, fmt.Errorf("expected boolean for %s", top.lastKey)
				}
				if !b {
					return nil, fmt.Errorf("expected boolean true for %s", top.lastKey)
				}
			}

			// 4. Keys that MUST be strings:
			if (top.path == "root" && (top.lastKey == "table" || top.lastKey == "operation_id" || top.lastKey == "actor")) ||
				(top.path == "member" && top.lastKey == "id") ||
				(top.path == "place" && (top.lastKey == "row" || top.lastKey == "col")) ||
				(top.path == "create" && (top.lastKey == "row" || top.lastKey == "col")) ||
				(top.path == "move" && (top.lastKey == "row" || top.lastKey == "col")) ||
				(top.path == "field_guard" && top.lastKey == "equals") ||
				(top.path == "set") {
				if _, ok := tok.(string); !ok {
					return nil, fmt.Errorf("expected string for %s", top.lastKey)
				}
			}

			// 5. Keys that MUST be numbers:
			if (top.path == "root" && (top.lastKey == "schema" || top.lastKey == "epoch" || top.lastKey == "expected_table_revision")) ||
				(top.path == "expect" && top.lastKey == "revision") ||
				(top.path == "create" && top.lastKey == "score") ||
				(top.path == "move" && top.lastKey == "score") {
				if !isNumberToken(tok) {
					return nil, fmt.Errorf("expected number for %s (non-number)", top.lastKey)
				}
			}

			if delim, ok := tok.(json.Delim); ok {
				if delim == '{' {
					childPath := "object"
					var childLower map[string]bool
					switch top.path {
					case "root":
						childPath = "object"
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
					})
					continue
				} else if delim == '[' {
					childPath := "array"
					switch top.path {
					case "root":
						if top.lastKey == "members" {
							childPath = "members"
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
						kind: containerArray,
						path: childPath,
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

			if tok == nil {
				if top.path == "unset" {
					return nil, errors.New("null value not allowed in unset")
				}
				if top.path == "one_of" {
					return nil, errors.New("null value not allowed in one_of")
				}
				if top.path == "members" {
					return nil, errors.New("null value not allowed in members")
				}
				return nil, fmt.Errorf("null value not allowed in %s", top.path)
			}

			if top.path == "unset" {
				if _, ok := tok.(string); !ok {
					return nil, errors.New("field name in unset must be a string")
				}
			} else if top.path == "one_of" {
				if _, ok := tok.(string); !ok {
					return nil, errors.New("item in one_of must be a string")
				}
			} else if top.path == "members" {
				if delim, ok := tok.(json.Delim); !ok || delim != '{' {
					return nil, errors.New("member entry must be an object")
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
					})
				} else if delim == '[' {
					stack = append(stack, containerState{
						kind: containerArray,
						path: "array",
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
			return nil, errors.New("member id cannot be empty")
		}
		if strings.ContainsAny(id, "\x00\r\n\t") {
			return nil, fmt.Errorf("invalid member id %q", id)
		}
		if seenMemberIDs[id] {
			return nil, fmt.Errorf("duplicate member id %q in manifest", id)
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
						return nil, fmt.Errorf("one_of for %q must be nonempty array", fName)
					}
				}
				if conds != 1 {
					return nil, fmt.Errorf("field guard for %q must specify exactly one condition, got %d", fName, conds)
				}
			}
		}
	}

	if err := validateManifestSemantics(&manifest); err != nil {
		return nil, err
	}
	if err := CheckBatchBounds(&manifest); err != nil {
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
		return fmt.Errorf("schema %d is not supported, expected 1", m.Schema)
	}
	if !ValidName(m.Table) {
		return fmt.Errorf("table %q wants letters, digits, _ . and -", m.Table)
	}
	if !word(m.OperationID) {
		return errors.New("operation_id must be a nonempty string without control characters")
	}
	if len(m.Members) == 0 {
		return errors.New("a manifest names at least one member")
	}
	if !uintString(m.Epoch) {
		return errors.New("epoch must be a decimal uint64 string")
	}
	if !uintString(m.ExpectedTableRevision) {
		return errors.New("expected_table_revision must be a decimal uint64 string")
	}
	for _, e := range m.Members {
		if !word(e.ID) {
			return errors.New("member id must be a nonempty string without control characters")
		}
		for f := range e.Set {
			if reservedField(f) {
				return fmt.Errorf("member %q: %w: field %q", e.ID, ErrReservedField, f)
			}
			if !word(f) {
				return fmt.Errorf("member %q: field names are nonempty strings without control characters", e.ID)
			}
		}
		namedUnset := map[string]bool{}
		for _, f := range e.Unset {
			if namedUnset[f] {
				return fmt.Errorf("member %q: unset names field %q twice", e.ID, f)
			}
			namedUnset[f] = true
			if reservedField(f) {
				return fmt.Errorf("member %q: %w: field %q", e.ID, ErrReservedField, f)
			}
			if !word(f) {
				return fmt.Errorf("member %q: field names are nonempty strings without control characters", e.ID)
			}
			if _, both := e.Set[f]; both {
				return fmt.Errorf("member %q: %w: field %q cannot be both set and unset", e.ID, ErrMutation, f)
			}
		}
		if e.Expect == nil {
			return fmt.Errorf("member %q: %w: missing expect", e.ID, ErrMalformedManifest)
		}
		x := e.Expect
		if x.Absent && (x.Revision != "" || x.Place != nil || x.Fields != nil) {
			return fmt.Errorf("member %q: %w: expect absent cannot combine with revision, place or fields", e.ID, ErrMutation)
		}
		if x.Revision != "" && !uintString(x.Revision) {
			return fmt.Errorf("member %q: expect revision must be a decimal string", e.ID)
		}
		for name, g := range x.Fields {
			if !word(name) {
				return fmt.Errorf("member %q: field guard names are nonempty strings without control characters", e.ID)
			}
			seen := map[string]bool{}
			for _, o := range g.OneOf {
				if seen[o] {
					return fmt.Errorf("member %q: field %q: one_of names an option twice", e.ID, name)
				}
				seen[o] = true
			}
		}
		if e.Create != nil {
			if e.Move != nil || e.Remove {
				return fmt.Errorf("member %q: %w: create cannot combine with move or remove", e.ID, ErrMutation)
			}
			if !x.Absent {
				return fmt.Errorf("member %q: %w: create requires expect absent", e.ID, ErrMutation)
			}
			if math.IsNaN(e.Create.Score) || math.IsInf(e.Create.Score, 0) {
				return fmt.Errorf("member %q: %w: expected a finite JSON number", e.ID, ErrInvalidScore)
			}
		}
		if e.Move != nil {
			if e.Remove {
				return fmt.Errorf("member %q: %w: move cannot combine with remove", e.ID, ErrMutation)
			}
			if e.Move.Score != nil && (math.IsNaN(*e.Move.Score) || math.IsInf(*e.Move.Score, 0)) {
				return fmt.Errorf("member %q: %w: expected a finite JSON number", e.ID, ErrInvalidScore)
			}
		}
	}
	return nil
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
