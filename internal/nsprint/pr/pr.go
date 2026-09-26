// Package pr reads the reap fields of one #3139 unit record, s:<S>:u:<unit>
// (nova-tools#3091 rev 5). The name is kept because #3156 imports pr.Inputs;
// the package reads a unit hash, never a retired PR key.
//
// Seven fields feed #3156's reap rules. land.lua writes every one of them,
// each from exactly one function:
//
//	head          ns_unit_head (#3139; this package only reads it)
//	paths         ns_unit_head, write-once, canonical JSON from CanonPaths
//	card_type     ns_unit_head, write-once, from the card hash (TYPE: line)
//	cut_at        ns_unit_head, write-once, from the card hash (Redis TIME at ns_card_push)
//	last_read_at  present-empty at create; ns_read on a counted read, monotonic
//	approve_head  present-empty at create; ns_read on a counted APPROVE
//	merged_at     present-empty at create; ns_land, once, with state=landed
//
// A present-empty last_read_at, approve_head or merged_at is a valid "not
// yet". An absent field is MISSING: the unit is never reap-eligible.
package pr

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

// ReapFields are the seven unit fields #3156's rules read, in the order
// Inputs checks them.
var ReapFields = []string{"head", "paths", "card_type", "cut_at", "last_read_at", "approve_head", "merged_at"}

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
