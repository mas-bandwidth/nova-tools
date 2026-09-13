package tokens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// antigravityJSONRow selects exact supported member spellings before decoding
// into a Go struct. Missing/null native identity must never become the default
// integer zero, and duplicate supported members must never select a last value.
// Unknown source material is discarded, not retained or echoed in diagnostics.
func antigravityJSONRow(raw []byte, identity string, dst any, supported ...string) error {
	refuse := func() error {
		// identity is a fixed caller-supplied schema member, never source text.
		return fmt.Errorf("antigravity: malformed or ambiguous JSON row; an explicit integer %s and unique supported members are required", identity)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	start, err := dec.Token()
	if err != nil || start != json.Delim('{') {
		return refuse()
	}
	fields := make(map[string]json.RawMessage, len(supported))
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return refuse()
		}
		key, ok := token.(string)
		if !ok {
			return refuse()
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return refuse()
		}
		if !slices.Contains(supported, key) {
			continue
		}
		if _, duplicate := fields[key]; duplicate {
			return refuse()
		}
		fields[key] = value
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return refuse()
	}
	if _, err := dec.Token(); err != io.EOF {
		return refuse()
	}
	id, present := fields[identity]
	var native int
	if !present || bytes.Equal(bytes.TrimSpace(id), []byte("null")) || json.Unmarshal(id, &native) != nil {
		return refuse()
	}
	// Re-encode only the selected spellings: encoding/json's case-insensitive
	// struct matching must not give an unknown alias control over a known field.
	selected, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(selected, dst) != nil {
		return refuse()
	}
	return nil
}
