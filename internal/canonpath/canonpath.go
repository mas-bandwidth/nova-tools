// Package canonpath canonicalizes and encodes file path declarations from card
// and PR PATHS lines.
//
// A PATHS line is a JSON array of strings, or whitespace- or comma-separated
// tokens; a path holding a space or a comma can only be written in the JSON
// form. Each entry uses / separators and is path.Clean'ed (the leading ./ and
// a trailing / go). An entry that is absolute, empty or has a .. component is
// refused with "REFUSED paths <absolute|empty|dotdot|json> <entry>". The
// result is sorted and deduplicated.
package canonpath

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"
)

// ErrRefused is wrapped by every CanonPaths refusal.
var ErrRefused = errors.New("REFUSED paths")

type refusedError struct{ reason, entry string }

func (e *refusedError) Error() string { return "REFUSED paths " + e.reason + " " + e.entry }
func (e *refusedError) Unwrap() error { return ErrRefused }

func refuse(reason, entry string) error {
	if entry == "" {
		entry = `""`
	}
	return &refusedError{reason: reason, entry: entry}
}

// CanonPaths canonicalizes a card's PATHS line. The line is a JSON array of
// strings, or whitespace- or comma-separated tokens; a path holding a space
// or a comma can only be written in the JSON form. Each entry uses /
// separators and is path.Clean'ed (the leading ./ and a trailing / go). An
// entry that is absolute, empty or has a .. component is refused with
// "REFUSED paths <absolute|empty|dotdot|json> <entry>". The result is sorted
// and deduplicated.
func CanonPaths(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	var raw []string
	if strings.HasPrefix(line, "[") {
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil, refuse("json", line)
		}
	} else {
		raw = strings.FieldsFunc(line, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
	}
	if len(raw) == 0 {
		return nil, refuse("empty", line)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		e := strings.ReplaceAll(strings.TrimSpace(entry), `\`, "/")
		switch {
		case e == "":
			return nil, refuse("empty", entry)
		case strings.HasPrefix(e, "/"):
			return nil, refuse("absolute", entry)
		}
		for _, part := range strings.Split(e, "/") {
			if part == ".." {
				return nil, refuse("dotdot", entry)
			}
		}
		c := strings.TrimPrefix(path.Clean(e), "./")
		if c == "." || c == "" {
			return nil, refuse("empty", entry)
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out, nil
}

// EncodePaths is the stored form of a canonical path list: compact JSON,
// e.g. ["a b/c","internal/nsprint/pr"].
func EncodePaths(paths []string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(paths) // a []string always encodes
	return strings.TrimSuffix(b.String(), "\n")
}

// CanonJSON is CanonPaths followed by EncodePaths: the value ns_unit_head stores.
func CanonJSON(line string) (string, error) {
	paths, err := CanonPaths(line)
	if err != nil {
		return "", err
	}
	return EncodePaths(paths), nil
}
