package cardhdr

import (
	"fmt"
	"regexp"
	"strings"
)

// A card is a spec (nova-tools#4313): a work card defines a bounded, verifiable change
// specifying EVIDENCE (proof of bug/need), PATHS (with file:line pointers),
// SEAMS (what to mock), RULES (standard test rules), RECEIPTS (typed lines
// change must print), KEEP (what not to touch and why), and DONE-WHEN.
const (
	KeyEvidence = "EVIDENCE"
	KeyPaths    = "PATHS"
	KeySeams    = "SEAMS"
	KeyRules    = "RULES"
	KeyReceipts = "RECEIPTS"
	KeyKeep     = "KEEP"
)

// StandardRules is the default testing rules for a work card spec (#4313).
const StandardRules = "t.Parallel, no sleeps, no network, no child process; touched packages only"

// pointerRE matches a file:line pointer at the end of a path token.
// Matches forms like "file.go:42", "file.go:42-55", "file.go:42:10", "file.go:L42-L55".
var pointerRE = regexp.MustCompile(`^(.*?):((?:L|#L)?[0-9]+(?:[-:](?:L|#L)?[0-9]+)?)$`)

// SplitPathPointer splits a path with an optional line pointer (e.g.
// "file.go:42", "file.go:42-55", "file.go:42:10") into the path and the line
// pointer. If no line pointer is present, line is empty.
func SplitPathPointer(p string) (path, line string) {
	if m := pointerRE.FindStringSubmatch(p); m != nil {
		return m[1], m[2]
	}
	return p, ""
}

// ParseEvidence reads an EVIDENCE value: evidence of the bug or need.
func ParseEvidence(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" || v == "-" {
		return "", fmt.Errorf("EVIDENCE: is empty; name the proof of need, bug or repro")
	}
	return v, nil
}

// ParseReceipts reads a RECEIPTS value: the typed lines or command receipts
// the change must produce.
func ParseReceipts(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" || v == "-" {
		return "", fmt.Errorf("RECEIPTS: is empty; name the typed lines change must print")
	}
	return v, nil
}

// ParseSeams reads a SEAMS value: what to mock / test boundaries.
func ParseSeams(value string) (string, error) {
	return strings.TrimSpace(value), nil
}

// ParseRules reads a RULES value: standard test rules or card-specific ones.
// An empty value defaults to StandardRules.
func ParseRules(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" || v == "-" {
		return StandardRules, nil
	}
	return v, nil
}

// ParseKeep reads a KEEP value: what not to touch and why.
func ParseKeep(value string) (string, error) {
	return strings.TrimSpace(value), nil
}
