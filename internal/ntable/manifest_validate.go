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
	kind      containerType
	seenKeys  map[string]bool
	expectKey bool
	lastKey   string
}

// ValidateBatchManifestRaw strictly validates raw batch manifest bytes according
// to Nova Table specification invariants before any normalization occurs.
func ValidateBatchManifestRaw(raw []byte) (*BatchManifest, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("empty manifest")
	}

	// 1. Strict tokenization pass: duplicate keys and remove validation.
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
				kind:      containerObject,
				seenKeys:  make(map[string]bool),
				expectKey: true,
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
				if top.seenKeys[key] {
					return nil, fmt.Errorf("duplicate key %q in manifest", key)
				}
				top.seenKeys[key] = true
				top.lastKey = key
				top.expectKey = false
				continue
			}

			// Value for top.lastKey
			if top.lastKey == "remove" {
				b, ok := tok.(bool)
				if !ok || !b {
					return nil, errors.New("remove must be true")
				}
			}

			if delim, ok := tok.(json.Delim); ok {
				if delim == '{' {
					stack = append(stack, containerState{
						kind:      containerObject,
						seenKeys:  make(map[string]bool),
						expectKey: true,
					})
					continue
				} else if delim == '[' {
					stack = append(stack, containerState{
						kind: containerArray,
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

			if delim, ok := tok.(json.Delim); ok {
				if delim == '{' {
					stack = append(stack, containerState{
						kind:      containerObject,
						seenKeys:  make(map[string]bool),
						expectKey: true,
					})
				} else if delim == '[' {
					stack = append(stack, containerState{
						kind: containerArray,
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
