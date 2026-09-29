package ntable

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
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
	"receipt":                 true,
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

// ValidateBatchManifestRaw strictly validates raw batch manifest bytes according
// to Nova Table specification invariants before any normalization occurs.
func ValidateBatchManifestRaw(raw []byte) (*BatchManifest, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty manifest")
	}

	// 1. Strict exact-case, path-aware tokenization pass: duplicate keys and type checks.
	decToken := json.NewDecoder(bytes.NewReader(raw))
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
			if top.path == "member" && top.lastKey == "remove" {
				b, ok := tok.(bool)
				if !ok || !b {
					return nil, errors.New("remove must be true")
				}
			}

			if top.path == "set" {
				if tok == nil {
					return nil, fmt.Errorf("null value not allowed in set for field %q", top.lastKey)
				}
				if delim, ok := tok.(json.Delim); ok {
					return nil, fmt.Errorf("value for field %q in set must be a string, got delimiter %c", top.lastKey, delim)
				}
				if _, ok := tok.(string); !ok {
					return nil, fmt.Errorf("value for field %q in set must be a string", top.lastKey)
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

			if top.path == "unset" {
				if _, ok := tok.(string); !ok {
					return nil, errors.New("field name in unset must be a string")
				}
			} else if top.path == "one_of" {
				if _, ok := tok.(string); !ok {
					return nil, errors.New("item in one_of must be a string")
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

	// 4. Basic member entry sanity.
	seenMemberIDs := make(map[string]bool, len(manifest.Members))
	for _, m := range manifest.Members {
		id := m.ID
		if id == "" {
			return nil, errors.New("member id cannot be empty")
		}
		if len(id) > 256 {
			return nil, fmt.Errorf("member id %q exceeds 256 bytes", id)
		}
		if strings.ContainsAny(id, "\x00\r\n\t") {
			return nil, fmt.Errorf("invalid member id %q", id)
		}
		if seenMemberIDs[id] {
			return nil, fmt.Errorf("duplicate member id %q in manifest", id)
		}
		seenMemberIDs[id] = true
	}

	return &manifest, nil
}
